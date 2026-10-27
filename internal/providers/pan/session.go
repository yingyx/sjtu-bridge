// Package pan authenticates an actively imported JAAuthCookie through the
// first-party Pan SSO exchange. Quota, file reads and non-overwriting creations
// remain independently authorized capabilities.
package pan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "pan.sjtu.edu.cn"
const root = "https://" + Host
const clientID = "nMaJq9TstrfJlcve2lee"
const redirectURI = root + "/login"
const discoveryPath = "/user/v1/sign-in/sso-login-redirect/xpw8ou8y"
const exchangePath = "/user/v1/sign-in/verify-account-login/xpw8ou8y"

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}

type Service struct{ Portal, Auth HTTP }

func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}

var noncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var statePattern = regexp.MustCompile(`^oauth_v1_web_[a-f0-9]{32}$`)
var devicePattern = regexp.MustCompile(`^sjtu-bridge-[a-f0-9]{32}$`)

// sso.Values supplies the 4096-byte value bound; Go's regexp repetition bound
// is smaller, so the character predicate deliberately has no numeric repeat.
var opaque = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{32,512}$`)
var userIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// Allowed admits only discovery, the authentication exchange and two exact
// quota prerequisites. Additional reviewed file endpoints are explicit;
// unopened personal spaces are never initialized by a read.
func Allowed(u *url.URL) bool {
	if writeAllowed(u) {
		return true
	}
	if fileAllowed(u) {
		return true
	}
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 3)
	if !ok {
		return false
	}
	switch u.Path {
	case discoveryPath:
		return len(q) == 3 && q.Get("auto_redirect") == "false" && q.Get("from") == "web" && noncePattern.MatchString(q.Get("custom_state"))
	case exchangePath:
		return len(q) == 3 && q.Get("type") == "sso" && opaque.MatchString(q.Get("credential")) && devicePattern.MatchString(q.Get("device_id"))
	case "/user/v1/organization", "/user/v1/space/1", "/user/v1/space/1/personal":
		return len(q) == 1 && tokenPattern.MatchString(q.Get("user_token"))
	}
	return false
}

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 6)
	return ok && len(q) == 5 && q.Get("response_type") == "code" && q.Get("client_id") == clientID && q.Get("redirect_uri") == redirectURI && q.Get("scope") == "openid" && statePattern.MatchString(q.Get("state"))
}

// AuthAllowed does not permit a general OAuth client or generic callback. The
// login state machine additionally binds every transition to its fresh state.
func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
}

func nonce() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", contracts.Fail("UPSTREAM_UNAVAILABLE", "Pan authentication randomness unavailable")
	}
	return hex.EncodeToString(b[:]), nil
}

// requestError never forwards request URLs, HTTP error messages or bodies:
// OAuth codes and Pan tokens must necessarily live in in-memory query strings.
func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var typed *contracts.Error
	if errors.As(err, &typed) && typed.Code == "TIMEOUT" {
		return contracts.Fail("TIMEOUT", "Pan request timed out")
	}
	return contracts.Fail("UPSTREAM_UNAVAILABLE", "Pan request failed")
}

func decode(r transport.Reply, target any) error {
	if e := transport.Status(r.Status); e != nil {
		return e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	if e != nil || media != "application/json" || charset != "" && charset != "utf8" && charset != "utf-8" || len(r.Body) > 1<<20 {
		return changed()
	}
	var envelope struct {
		Status *int            `json:"status"`
		Code   json.RawMessage `json:"code"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(r.Body)), "{") {
		if strictjson.Decode(r.Body, &envelope) != nil {
			return changed()
		}
		if envelope.Status != nil && *envelope.Status != 0 || len(envelope.Code) > 0 && string(envelope.Code) != `""` && string(envelope.Code) != "0" && string(envelope.Code) != "null" {
			return contracts.Fail("UPSTREAM_UNAVAILABLE", "Pan rejected the request")
		}
	}
	if strictjson.Decode(r.Body, target) != nil {
		return changed()
	}
	return nil
}

func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Pan response structure changed") }

type org struct {
	ID      json.RawMessage `json:"id"`
	Expired *bool           `json:"expired"`
	Member  *struct {
		Enabled    *bool `json:"enabled"`
		Deregister *bool `json:"deregister"`
	} `json:"orgUser"`
}

func scalarID(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if userIDPattern.MatchString(str) {
			return str
		}
		return ""
	}
	if userIDPattern.Match(raw) {
		return string(raw)
	}
	return ""
}

func schoolMember(orgs []org) error {
	if len(orgs) == 0 || len(orgs) > 32 {
		return changed()
	}
	found := 0
	for _, o := range orgs {
		if scalarID(o.ID) == "" {
			return changed()
		}
		if scalarID(o.ID) != "1" {
			continue
		}
		found++
		if o.Member == nil || o.Member.Enabled == nil || !*o.Member.Enabled || o.Member.Deregister == nil || *o.Member.Deregister || o.Expired != nil && *o.Expired {
			return contracts.Fail("PERMISSION_DENIED", "Pan school membership is not active")
		}
	}
	if found != 1 {
		return contracts.Fail("PERMISSION_DENIED", "Pan school organization is unavailable or ambiguous")
	}
	return nil
}

type session struct {
	service *Service
	account string
	token   []byte
	ua      string
}

func (x *session) close() { clear(x.token); x.token = nil }

func (s *Service) login(ctx context.Context, secret []byte) (*session, error) {
	credential, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return nil, e
	}
	n, e := nonce()
	if e != nil {
		return nil, e
	}
	state := "oauth_v1_web_" + n
	discovery, _ := url.Parse(root + discoveryPath)
	discovery.RawQuery = url.Values{"auto_redirect": {"false"}, "from": {"web"}, "custom_state": {n}}.Encode()
	h := http.Header{"Accept": {"application/json"}}
	if credential.UserAgent != "" {
		h.Set("User-Agent", credential.UserAgent)
	}
	r, e := s.Portal.Get(ctx, discovery, h)
	if e != nil {
		return nil, requestError(ctx, e)
	}
	var destination struct {
		URL string `json:"url"`
	}
	if e = decode(r, &destination); e != nil {
		return nil, e
	}
	u, e := url.Parse(destination.URL)
	if e != nil || !initialAuthorize(u) || u.Query().Get("state") != state {
		return nil, contracts.Fail("AUTH_REQUIRED", "Pan returned an unreviewed authentication destination")
	}
	jar, _ := cookiejar.New(nil)
	budget := sso.Budget{Limit: 2 << 20}
	phase := "authorize"
	for hop := 0; hop < 8; hop++ {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if !AuthAllowed(u) {
			return nil, contracts.Fail("AUTH_REQUIRED", "Pan requires an unsupported authentication step")
		}
		h = sso.Header(jar, u, credential.UserAgent)
		h.Set("Accept", "text/html,application/json")
		if phase == "login" {
			if e = sso.AddRoots(h, u, credential); e != nil {
				return nil, e
			}
		}
		r, e = s.Auth.Get(ctx, u, h)
		h.Del("Cookie")
		if e != nil {
			return nil, requestError(ctx, e)
		}
		if !budget.Consume(len(r.Body)) {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Pan SSO exceeded response budget")
		}
		if e = sso.Remember(jar, u, r.Header); e != nil {
			return nil, e
		}
		if r.Status < 300 || r.Status >= 400 {
			if e = transport.Status(r.Status); e != nil {
				return nil, e
			}
			return nil, contracts.Fail("AUTH_REQUIRED", "Complete Pan sign-in or additional verification in your browser, then import a fresh JAAuthCookie")
		}
		next, e := sso.Redirect(r, u)
		if e != nil {
			return nil, e
		}
		if code, ok := callbackCode(next, state); ok && (phase == "authorize" || phase == "login" || phase == "ticket") {
			return s.exchange(ctx, code, credential.UserAgent)
		}
		switch {
		case phase == "authorize" && sso.OAuthLogin(next):
			phase = "login"
		case (phase == "login" || phase == "ticket") && sso.TicketAuthorize(next):
			if next.Query().Get("state") != "" && next.Query().Get("state") != state {
				return nil, contracts.Fail("AUTH_REQUIRED", "Pan authentication state changed")
			}
			phase = "ticket"
		default:
			return nil, contracts.Fail("AUTH_REQUIRED", "Pan authentication left the reviewed flow")
		}
		u = next
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "Pan authentication exceeded hop limit")
}

func callbackCode(u *url.URL, state string) (string, bool) {
	if !transport.ExactHTTPS(u, Host) || u.Path != "/login" {
		return "", false
	}
	q, ok := sso.Values(u, 2)
	return q.Get("code"), ok && len(q) == 2 && q.Get("state") == state && opaque.MatchString(q.Get("code"))
}

func (s *Service) exchange(ctx context.Context, code, ua string) (*session, error) {
	device, e := nonce()
	if e != nil {
		return nil, e
	}
	u, _ := url.Parse(root + exchangePath)
	u.RawQuery = url.Values{"type": {"sso"}, "device_id": {"sjtu-bridge-" + device}, "credential": {code}}.Encode()
	h := http.Header{"Accept": {"application/json"}, "Content-Type": {"application/json"}}
	if ua != "" {
		h.Set("User-Agent", ua)
	}
	// This is only the reviewed OAuth-code exchange. No initialization,
	// organization-selection POST, renewal, key management or file API is called.
	r, e := s.Portal.Post(ctx, u, h, []byte(`{}`))
	u.RawQuery = ""
	if e != nil {
		return nil, requestError(ctx, e)
	}
	defer clear(r.Body)
	var login struct {
		UserID        json.RawMessage `json:"userId"`
		UserToken     string          `json:"userToken"`
		ExpiresIn     *int            `json:"expiresIn"`
		Organizations []org           `json:"organizations"`
	}
	if e = decode(r, &login); e != nil {
		return nil, e
	}
	id := scalarID(login.UserID)
	if id == "" || !tokenPattern.MatchString(login.UserToken) || login.ExpiresIn == nil || *login.ExpiresIn < 1 || *login.ExpiresIn > int((366*24*time.Hour)/time.Second) {
		return nil, changed()
	}
	if e = schoolMember(login.Organizations); e != nil {
		return nil, e
	}
	x := &session{service: s, account: "pan:user:1:" + id, token: []byte(login.UserToken), ua: ua}
	login.UserToken = ""
	if e = x.membership(ctx); e != nil {
		x.close()
		return nil, e
	}
	return x, nil
}

func (x *session) get(ctx context.Context, path string, target any) error {
	u, _ := url.Parse(root + path)
	u.RawQuery = url.Values{"user_token": {string(x.token)}}.Encode()
	if !Allowed(u) {
		return contracts.Fail("PERMISSION_DENIED", "Unreviewed Pan data endpoint")
	}
	h := http.Header{"Accept": {"application/json"}}
	if x.ua != "" {
		h.Set("User-Agent", x.ua)
	}
	r, e := x.service.Portal.Get(ctx, u, h)
	u.RawQuery = ""
	if e != nil {
		return requestError(ctx, e)
	}
	defer clear(r.Body)
	return decode(r, target)
}

func (x *session) membership(ctx context.Context) error {
	var organizations []org
	if e := x.get(ctx, "/user/v1/organization", &organizations); e != nil {
		return e
	}
	return schoolMember(organizations)
}
