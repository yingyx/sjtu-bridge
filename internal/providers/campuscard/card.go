// Package campuscard reads the owner's card through the reviewed school HTTPS
// ecard pages. It never reads bank account numbers or executes card actions.
package campuscard

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "weixin.sjtu.edu.cn"
const balancePath = "/xxzx/sjtu-net/ecard/ecardbalance.php"
const historyPath = "/xxzx/sjtu-net/ecard/ecardbill.php"
const historyResultPath = "/xxzx/sjtu-net/ecard/ecardbillback.php"
const origin = "https://" + Host
const cardScope = "profile connect_wechat card_info card_transactions write_card_info"

type Service struct{ Portal, Auth sso.OAuthHTTP }

func Allowed(u *url.URL) bool {
	return allowedPage(u, balancePath) || allowedPage(u, historyPath) || allowedHistoryResult(u)
}
func allowedHistoryResult(u *url.URL) bool {
	return allowedPage(u, historyResultPath) && len(u.Query()) == 2
}
func allowedPage(u *url.URL, path string) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 2)
	if !ok {
		return false
	}
	return u.Path == path && (len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") == "4"))
}
func initialAuthorize(u *url.URL, path string) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == "janicweixin20150709" && q.Get("response_type") == "code" && q.Get("scope") == cardScope && q.Get("redirect_uri") == "http://"+Host+path && q.Get("state") == "4"
}
func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u, balancePath) || initialAuthorize(u, historyPath) || initialAuthorize(u, historyResultPath) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
}
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}
func (s *Service) page(ctx context.Context, secret []byte, path string) ([]byte, error) {
	if path != balancePath && path != historyPath {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed campus card page")
	}
	portal := &cardCallbackHTTP{OAuthHTTP: s.Portal, path: path}
	cfg := sso.OAuthConfig{Host: Host, Start: origin + path, CallbackPath: path, Portal: portal, Auth: s.Auth,
		PortalAllowed: func(u *url.URL) bool { return allowedPage(u, path) },
		AuthAllowed:   func(u *url.URL) bool { return initialAuthorize(u, path) || sso.OAuthLogin(u) || sso.TicketAuthorize(u) }}
	if path == historyPath {
		cfg.Continuation = &sso.OAuthContinuation{CallbackPath: historyResultPath, PortalAllowed: allowedHistoryResult, AuthAllowed: func(u *url.URL) bool {
			return initialAuthorize(u, historyResultPath) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
		}}
	}
	session, e := sso.OpenOAuth(ctx, secret, cfg)
	if e != nil {
		clear(portal.callback.Body)
		return nil, e
	}
	if portal.hasCallback {
		body, e := htmlReply(portal.callback)
		if e != nil {
			clear(portal.callback.Body)
		}
		if e == nil && path == historyPath {
			defer clear(body)
			return s.queryHistory(ctx, session, portal, secret, body)
		}
		return body, e
	}
	body, e := readHTML(ctx, session, origin+path)
	if e == nil && path == historyPath {
		defer clear(body)
		return s.queryHistory(ctx, session, portal, secret, body)
	}
	return body, e
}
func (s *Service) login(ctx context.Context, secret []byte) (*contracts.StudentRecord, string, error) {
	body, e := s.page(ctx, secret, balancePath)
	if e != nil {
		return nil, "", e
	}
	defer clear(body)
	view, cardID, e := parseBalance(body)
	if e != nil {
		return nil, "", e
	}
	return view, "card:account:" + cardID, nil
}

// The card PHP endpoint may render its authenticated result directly at the
// one-time OAuth callback without establishing a cookie session. OpenOAuth
// checks the callback's client, destination and state before requesting it;
// consume this result only after that complete flow has succeeded. The reply
// lives only for the current login and is cleared once the projected card data
// has been parsed; no callback URL or private HTML is persisted.
type cardCallbackHTTP struct {
	sso.OAuthHTTP
	path        string
	callback    transport.Reply
	hasCallback bool
}

func (c *cardCallbackHTTP) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	r, e := c.OAuthHTTP.Get(ctx, u, h)
	if e == nil && allowedPage(u, c.path) && u.Query().Get("code") != "" && r.Status == http.StatusOK {
		c.callback, c.hasCallback = r, true
	}
	return r, e
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, account, e := s.login(ctx, secret)
	return account, e
}
func readHTML(ctx context.Context, session *sso.OAuthSession, raw string) ([]byte, error) {
	r, e := session.Get(ctx, raw)
	if e != nil {
		return nil, e
	}
	return htmlReply(r)
}
func htmlReply(r transport.Reply) ([]byte, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "text/html" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid campus card HTML")
	}
	return r.Body, nil
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := contracts.CardBalanceCapability
	if q.View == "card_transactions_v1" {
		id = contracts.CardTransactionsCapability
	}
	rules, _ := contracts.StudentRecordRules(id)
	if q.AccountRef != account || !rules.ValidQuery(q) || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected an own-card fixed-view query")
	}
	balance, actual, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Campus card identity changed")
	}
	rows := []contracts.CampusRecord{{ResourceRef: q.ResourceRef, Ref: "card:balance:self", Title: "校园卡余额", Student: balance}}
	if id == contracts.CardTransactionsCapability {
		// History is a separately authenticated PHP callback, not a cookie
		// continuation of the balance page. Verify the card owner first, then
		// open only the independently pinned history OAuth flow.
		body, e := s.page(ctx, secret, historyPath)
		if e != nil {
			return domain.CampusPage{}, e
		}
		defer clear(body)
		rows, e = parseHistory(body, q.ResourceRef)
		if e != nil {
			return domain.CampusPage{}, e
		}
	}
	selected, continuation, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	return domain.CampusPage{AccountRef: actual, Rows: selected, Next: continuation, Coverage: rules.Coverage}, nil
}
