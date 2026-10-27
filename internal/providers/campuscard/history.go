package campuscard

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

func queryForm(body []byte, begin, end string) ([]byte, error) {
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, changed()
	}
	forms := elements(root, "form")
	if len(forms) != 1 {
		return nil, changed()
	}
	f := forms[0]
	base, _ := url.Parse(origin + historyPath)
	action, actionErr := base.Parse(attr(f, "action"))
	if !strings.EqualFold(attr(f, "method"), "post") || attr(f, "action") == "" || actionErr != nil || action.String() != origin+historyPath || action.RawQuery != "" || action.ForceQuery || action.Fragment != "" || action.User != nil || len(elements(f, "select")) != 0 || len(elements(f, "textarea")) != 0 {
		return nil, changed()
	}
	inputs, buttons := elements(f, "input"), elements(f, "button")
	if len(inputs) != 2 || len(buttons) != 1 {
		return nil, changed()
	}
	seen := map[string]bool{}
	for _, input := range inputs {
		name := attr(input, "name")
		if name != "bDate" && name != "eDate" || seen[name] || !strings.EqualFold(attr(input, "type"), "date") || attr(input, "form") != "" {
			return nil, changed()
		}
		seen[name] = true
	}
	button := buttons[0]
	buttonType := attr(button, "type")
	value := attr(button, "value")
	if attr(button, "name") != "searchbill" || buttonType != "" && !strings.EqualFold(buttonType, "submit") || attr(button, "formaction") != "" || attr(button, "formmethod") != "" || attr(button, "form") != "" || len(value) > 64 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return nil, changed()
	}
	return []byte(url.Values{"bDate": {begin}, "eDate": {end}, "searchbill": {value}}.Encode()), nil
}

// The first authenticated history page is only a date-query form. Submitting
// these three pinned fields performs a read; it does not modify the card. The
// provider chooses the last 30 local calendar dates and never accepts a card
// identifier, URL, date range or additional action from the calling app.
func (s *Service) queryHistory(ctx context.Context, session *sso.OAuthSession, portal *cardCallbackHTTP, secret, form []byte) ([]byte, error) {
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	begin, end := today.AddDate(0, 0, -29).Format("2006-01-02"), today.Format("2006-01-02")
	payload, e := queryForm(form, begin, end)
	if e != nil {
		return nil, e
	}
	defer clear(payload)
	u, _ := url.Parse(origin + historyPath)
	h := session.Header(u)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("Origin", origin)
	h.Set("Referer", origin+historyPath)
	r, e := s.Portal.Post(ctx, u, h, payload)
	h.Del("Cookie")
	if e != nil {
		clear(r.Body)
		return nil, e
	}
	if r.Status == http.StatusFound {
		defer clear(r.Body)
		portal.hasCallback = false
		portal.callback = transport.Reply{}
		portal.path = historyResultPath
		if e = session.ContinueOAuth(ctx, secret, u, r); e != nil {
			clear(portal.callback.Body)
			return nil, e
		}
		if !portal.hasCallback {
			return nil, changed()
		}
		body, e := htmlReply(portal.callback)
		if e != nil {
			clear(portal.callback.Body)
		}
		if e == nil {
			e = validateHistoryDates(body, begin, end)
			if e != nil {
				clear(body)
			}
		}
		return body, e
	}
	body, e := htmlReply(r)
	if e != nil {
		clear(r.Body)
	}
	if e == nil {
		e = validateHistoryDates(body, begin, end)
		if e != nil {
			clear(body)
		}
	}
	return body, e
}
