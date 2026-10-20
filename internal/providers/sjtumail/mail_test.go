package sjtumail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/campussession"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const headers = `{"more":false,"offset":0,"m":[{"id":"7","l":"2","su":"合成通知","d":1700000000000,"e":[{"t":"f","a":"synthetic@example.invalid"}],"fr":"SECRET_SNIPPET","mp":[{"content":"SECRET_BODY"}]}]}`

type fake struct {
	t            *testing.T
	search       map[string]any
	posts        int
	landingReply *transport.Reply
	soapReply    *transport.Reply
	csrf         string
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if u.String() == landing {
		if f.landingReply != nil {
			return *f.landingReply, nil
		}
		return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(`<script>var csrfToken = "SYNTHETIC_CSRF_CONTEXT";</script>`)}, nil
	}
	return transport.Reply{Status: 200, Header: http.Header{"Set-Cookie": {"ZM_AUTH_TOKEN=SYNTHETIC_SESSION; Secure; Path=/"}}}, nil
}
func (f *fake) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	f.posts++
	if u.String() != endpoint || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("mail endpoint or secret scope")
	}
	csrf := f.csrf
	if csrf == "" {
		csrf = "SYNTHETIC_CSRF_CONTEXT"
	}
	if h.Get("X-Zimbra-Csrf-Token") != csrf {
		f.t.Fatal("missing CSRF context")
	}
	var v struct {
		Header struct {
			Context struct {
				CSRFToken string
				AuthToken struct {
					Content string `json:"_content"`
				}
			}
		}
		Body map[string]map[string]any
	}
	if json.Unmarshal(b, &v) != nil || len(v.Body) != 1 {
		f.t.Fatal("bad request")
	}
	if v.Header.Context.CSRFToken != csrf || v.Header.Context.AuthToken.Content != "SYNTHETIC_SESSION" {
		f.t.Fatal("SOAP session context missing")
	}
	body := ""
	if _, ok := v.Body["GetInfoRequest"]; ok {
		body = `{"GetInfoResponse":{"id":"synthetic-account-01","name":"MUST_NOT_RETURN"}}`
	} else if p, ok := v.Body["SearchRequest"]; ok {
		f.search = p
		if p["fetch"] != "none" || p["read"] != float64(0) {
			f.t.Fatal("mailbox request may have effects")
		}
		body = `{"SearchResponse":` + headers + `}`
	} else {
		f.t.Fatal("non-read mailbox operation")
	}
	if f.soapReply != nil {
		return *f.soapReply, nil
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/soap+json"}}, Body: []byte(`{"Body":` + body + `}`)}, nil
}

func fakeService(f *fake) Service {
	return Service{Session: campussession.Service{Host: Host, Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: f, Auth: f}}
}

func TestMailboxClassicAndModernCSRF(t *testing.T) {
	for _, tc := range []struct{ name, html, csrf string }{
		{"classic", `<script>var csrfToken = "SYNTHETIC_CSRF_CONTEXT";</script>`, "SYNTHETIC_CSRF_CONTEXT"},
		{"classic_quoted_key", `<script>var config = {'csrfToken': 'SYNTHETIC_CSRF_CONTEXT'};</script>`, "SYNTHETIC_CSRF_CONTEXT"},
		{"modern", `<script>localStorage.setItem("csrfToken" , "SYNTHETIC_CSRF_CONTEXT"); window.location.replace('/modern/');</script>`, "SYNTHETIC_CSRF_CONTEXT"},
		{"modern_single_quotes", `<script>localStorage.setItem('csrfToken', 'SYNTHETIC_CSRF_CONTEXT');</script>`, "SYNTHETIC_CSRF_CONTEXT"},
		{"minimum_length", `<script>var csrfToken = "` + strings.Repeat("a", 16) + `";</script>`, strings.Repeat("a", 16)},
		{"maximum_length", `<script>localStorage.setItem("csrfToken", "` + strings.Repeat("a", 4096) + `");</script>`, strings.Repeat("a", 4096)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{t: t, csrf: tc.csrf, landingReply: &transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=UTF-8"}}, Body: []byte(tc.html)}}
			s := fakeService(f)
			account, err := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if err != nil || account != "sjtumail:account:synthetic-account-01" || f.posts != 1 {
				t.Fatal("CSRF context did not establish a verified account", err)
			}
		})
	}
}

func TestMailboxRejectsUnboundedOrAmbiguousCSRFBeforeSOAP(t *testing.T) {
	classic := `var csrfToken = "SYNTHETIC_CSRF_CONTEXT";`
	modern := `localStorage.setItem("csrfToken", "SYNTHETIC_CSRF_CONTEXT");`
	for _, tc := range []struct{ name, html, media string }{
		{"missing", `<script>window.location='/modern/';</script>`, "text/html"},
		{"login_cookie_is_not_csrf", `<script>var ZM_LOGIN_CSRF = "SYNTHETIC_CSRF_CONTEXT";</script>`, "text/html"},
		{"empty", `var csrfToken = "";`, "text/html"},
		{"short", `var csrfToken = "too_short";`, "text/html"},
		{"overlong", `var csrfToken = "` + strings.Repeat("a", 4097) + `";`, "text/html"},
		{"invalid_space", `var csrfToken = "SYNTHETIC CSRF CONTEXT";`, "text/html"},
		{"invalid_control", "var csrfToken = \"SYNTHETIC_CSRF_\x00_CONTEXT\";", "text/html"},
		{"invalid_escape", `localStorage.setItem("csrfToken", "SYNTHETIC\x5fCSRF_CONTEXT");`, "text/html"},
		{"mismatched_assignment_quotes", `var csrfToken = 'SYNTHETIC_CSRF_CONTEXT";`, "text/html"},
		{"mismatched_modern_quotes", `localStorage.setItem("csrfToken", 'SYNTHETIC_CSRF_CONTEXT");`, "text/html"},
		{"mismatched_modern_key", `localStorage.setItem('csrfToken", "SYNTHETIC_CSRF_CONTEXT");`, "text/html"},
		{"duplicate_classic", classic + classic, "text/html"},
		{"duplicate_modern", modern + modern, "text/html"},
		{"duplicate_mixed", classic + modern, "text/html"},
		{"wrong_media", classic, "application/json"},
		{"invalid_media", classic, "text/html; charset=\""},
		{"oversized_page", classic + strings.Repeat(" ", 2<<20), "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{t: t, landingReply: &transport.Reply{Status: 200, Header: http.Header{"Content-Type": {tc.media}}, Body: []byte(tc.html)}}
			s := fakeService(f)
			account, err := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if e, ok := err.(*contracts.Error); !ok || e.Code != "AUTH_REQUIRED" || account != "" || f.posts != 0 {
				t.Fatal("invalid CSRF context was not rejected before SOAP", err)
			}
		})
	}
}

func TestMailboxJavaScriptMIMERequiresBoundedJSON(t *testing.T) {
	valid := `{"Body":{"GetInfoResponse":{"id":"synthetic-account-01"}}}`
	for _, media := range []string{"application/json", "application/soap+json", "text/javascript; charset=utf-8", "application/javascript"} {
		t.Run(media, func(t *testing.T) {
			f := &fake{t: t, soapReply: &transport.Reply{Status: 200, Header: http.Header{"Content-Type": {media}}, Body: []byte(valid)}}
			s := fakeService(f)
			if _, err := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE")); err != nil {
				t.Fatal("strict JSON with reviewed media rejected", err)
			}
		})
	}
	for _, tc := range []struct{ name, body, media string }{
		{"jsonp", "callback(" + valid + ");", "text/javascript"},
		{"assignment", "window.response=" + valid, "text/javascript"},
		{"trailing_script", valid + ";alert(1)", "text/javascript"},
		{"trailing_json", valid + valid, "text/javascript"},
		{"single_quotes", `{'Body':{'GetInfoResponse':{'id':'synthetic-account-01'}}}`, "text/javascript"},
		{"html", `<html>synthetic login</html>`, "text/javascript"},
		{"invalid_utf8", strings.Replace(valid, "synthetic", "synthetic\xff", 1), "text/javascript"},
		{"oversized", valid + strings.Repeat(" ", 2<<20), "text/javascript"},
		{"wrong_media", valid, "text/html"},
		{"invalid_media", valid, "application/json; charset=\""},
		{"missing_body", `{}`, "text/javascript"},
		{"null_body", `{"Body":null}`, "text/javascript"},
		{"missing_response", `{"Body":{}}`, "text/javascript"},
		{"null_response", `{"Body":{"GetInfoResponse":null}}`, "text/javascript"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{t: t, soapReply: &transport.Reply{Status: 200, Header: http.Header{"Content-Type": {tc.media}}, Body: []byte(tc.body)}}
			s := fakeService(f)
			account, err := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if e, ok := err.(*contracts.Error); !ok || e.Code != "UPSTREAM_CHANGED" || account != "" {
				t.Fatal("non-JSON or unbounded SOAP accepted", err)
			}
		})
	}
}
func TestMailboxHeadersOnlyAndLiteralSearch(t *testing.T) {
	f := &fake{t: t}
	s := Service{Session: campussession.Service{Host: Host, Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: f, Auth: f}}
	q := contracts.CampusQuery{AccountRef: "sjtumail:account:synthetic-account-01", ResourceRef: "mail:folder:inbox", View: "headers_v1", Limit: 10, Query: `合成 " OR in:sent`}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(page.Rows) != 1 {
		t.Fatal(page, e)
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "MUST_NOT_RETURN") || strings.Contains(string(b), "SYNTHETIC_SESSION") {
		t.Fatal("private fields or token leaked")
	}
	query := f.search["query"].(string)
	if !strings.HasPrefix(query, "in:inbox subject:") || !strings.Contains(query, `subject:"in:sent"`) {
		t.Fatal("raw syntax inserted", query)
	}
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), "sjtumail:account:wrong", q, ""); e == nil {
		t.Fatal("wrong mailbox accepted")
	}
}
func TestMailboxScopeAndEnvelopeFailures(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "mail:folder:inbox", View: "headers_v1", Limit: 10}
	for _, raw := range []string{`{}`, `{"more":true,"offset":0}`, strings.Replace(headers, `"l":"2"`, `"l":"5"`, 1), strings.Replace(headers, `"id":"7"`, `"id":"../7"`, 1), strings.Replace(headers, `"offset":0`, `"offset":1`, 1)} {
		if _, e := parse([]byte(raw), "account", q, 0); e == nil {
			t.Fatal("invalid mailbox response accepted")
		}
	}
	page, e := parse([]byte(`{"more":false,"offset":0}`), "account", q, 0)
	if e != nil || page.Rows == nil || len(page.Rows) != 0 {
		t.Fatal(page, e)
	}
}
