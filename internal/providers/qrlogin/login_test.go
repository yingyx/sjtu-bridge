package qrlogin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const testID = "12345678-1234-1234-1234-123456789abc"
const syntheticSecret = "SYNTHETIC_PRIVATE_QR_VALUE"

type fakeGetter func(context.Context, *url.URL, http.Header) (transport.Reply, error)

func (f fakeGetter) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f(ctx, u, h)
}

type socketEvent struct {
	body []byte
	err  error
}

type fakeSocket struct {
	events     chan socketEvent
	closed     chan struct{}
	once       sync.Once
	updates    atomic.Int32
	reading    atomic.Int32
	updateFail int32
}

func newSocket(frames ...string) *fakeSocket {
	s := &fakeSocket{events: make(chan socketEvent, 128), closed: make(chan struct{})}
	for _, frame := range frames {
		s.events <- socketEvent{body: []byte(frame)}
	}
	return s
}
func (s *fakeSocket) ReadMessage() ([]byte, error) {
	s.reading.Add(1)
	defer s.reading.Add(-1)
	select {
	case <-s.closed:
		return nil, io.EOF
	case event := <-s.events:
		return event.body, event.err
	}
}
func (s *fakeSocket) Update() error {
	n := s.updates.Add(1)
	if s.updateFail != 0 && n >= s.updateFail {
		return errors.New(syntheticSecret)
	}
	return nil
}
func (s *fakeSocket) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

func pageURL() string {
	return "https://" + host + "/jaccount/jalogin?" + url.Values{
		"sid": {"jaoauth220160718"}, "returl": {"synthetic-return"}, "se": {"synthetic-session"},
	}.Encode()
}
func redirect(location string) transport.Reply {
	return transport.Reply{Status: 302, Header: http.Header{"Location": {location}}}
}
func newService(t *testing.T, ws *fakeSocket, page string, completion transport.Reply) (*Service, *atomic.Int32) {
	t.Helper()
	completed := &atomic.Int32{}
	return &Service{
		portal: fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
			if u.String() != start || h.Get("User-Agent") != userAgent {
				t.Error("unexpected portal request or user agent")
			}
			return redirect(pageURL()), nil
		}),
		auth: fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
			if !allowed(u) || h.Get("User-Agent") != userAgent {
				t.Error("unexpected authentication request or user agent")
			}
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
				t.Error("root supplied before express login")
			}
			switch u.Path {
			case "/jaccount/jalogin":
				return transport.Reply{Status: 200, Header: http.Header{}, Body: []byte(page)}, nil
			case "/jaccount/expresslogin":
				completed.Add(1)
				if u.Query().Get("uuid") != testID {
					t.Error("completion UUID mismatch")
				}
				return completion, nil
			default:
				t.Error("followed an unreviewed request")
				return transport.Reply{}, errors.New(syntheticSecret)
			}
		}),
		dial: func(_ context.Context, u *url.URL, h http.Header) (socket, error) {
			if !allowedSocket(u) || u.Path != "/jaccount/sub/"+testID || h.Get("User-Agent") != userAgent {
				t.Error("unexpected socket destination or user agent")
			}
			return ws, nil
		},
		refresh: time.Hour,
	}, completed
}
func rootReply(values ...string) transport.Reply {
	h := http.Header{}
	for _, value := range values {
		h.Add("Set-Cookie", value)
	}
	return transport.Reply{Status: 200, Header: h}
}
func assertError(t *testing.T, err error, code string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	if strings.Contains(err.Error(), syntheticSecret) || strings.Contains(err.Error(), testID) {
		t.Fatal("error exposed a private synthetic value")
	}
}
func assertStopped(t *testing.T, ws *fakeSocket) {
	t.Helper()
	select {
	case <-ws.closed:
	default:
		t.Error("login socket remained open")
	}
	if ws.reading.Load() != 0 {
		t.Error("login returned while a reader was still blocked")
	}
}

func TestExactDestinations(t *testing.T) {
	validHTTP := []string{
		start, pageURL(),
		start + "?code=synthetic-code&state=synthetic-state",
		"https://" + host + "/jaccount/expresslogin?uuid=" + testID,
		"https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket",
		"https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket&state=synthetic-state",
		"https://" + host + "/oauth2/authorize?" + url.Values{
			"client_id": {"etLP8wWejc7xZCkMpgee"}, "redirect_uri": {start}, "response_type": {"code"}, "state": {"synthetic-state"},
		}.Encode(),
	}
	for _, raw := range validHTTP {
		u, _ := url.Parse(raw)
		if !allowed(u) {
			t.Errorf("reviewed destination rejected: %s", raw)
		}
	}
	invalidHTTP := []string{
		"http://my.sjtu.edu.cn/ui/appmyinfo", start + "?x=1", start + "#fragment",
		"https://my.sjtu.edu.cn:443/ui/appmyinfo", "https://user@my.sjtu.edu.cn/ui/appmyinfo",
		"https://my.sjtu.edu.cn/ui/%61ppmyinfo", "https://my.sjtu.edu.cn.evil.example/ui/appmyinfo",
		pageURL() + "&sid=jaoauth220160718", pageURL() + "&unknown=1", pageURL() + "&client=",
		"https://" + host + "/jaccount/jalogin?sid=other&returl=x&se=x",
		"https://" + host + "/jaccount/expresslogin?uuid=" + testID + "&uuid=" + testID,
		"https://" + host + "/jaccount/expresslogin?uuid=invalid",
		"https://" + host + "/oauth2/authorize?client_id=other&redirect_uri=" + url.QueryEscape(start) + "&response_type=code&state=x",
		start + "?code=synthetic-code", start + "?code=one&state=one&state=two",
		"https://" + host + "/oauth2/authorize?context=one&jatkt=two&unknown=three",
		"https://" + host + "/oauth2/authorize?context=one&jatkt=two&state=one&state=two",
	}
	for _, raw := range invalidHTTP {
		u, err := url.Parse(raw)
		if err == nil && allowed(u) {
			t.Errorf("unreviewed destination allowed: %s", raw)
		}
	}
	if allowed(nil) || allowedSocket(nil) {
		t.Fatal("nil destination allowed")
	}
	validWS := "wss://" + host + "/jaccount/sub/" + testID
	u, _ := url.Parse(validWS)
	if !allowedSocket(u) {
		t.Fatal("reviewed socket rejected")
	}
	for _, raw := range []string{
		strings.Replace(validWS, "wss:", "ws:", 1), validWS + "?x=1", validWS + "#fragment", validWS + "/",
		strings.Replace(validWS, host, host+":443", 1), strings.Replace(validWS, host, "user@"+host, 1),
		strings.Replace(validWS, host, host+".evil.example", 1), strings.Replace(validWS, testID, "../"+testID, 1),
	} {
		u, err := url.Parse(raw)
		if err == nil && allowedSocket(u) {
			t.Errorf("unreviewed socket allowed: %s", raw)
		}
	}
}

func TestUUIDMustBeUnambiguousAndFromOfficialLoginPage(t *testing.T) {
	for _, tc := range []struct {
		name, page string
		wantOK     bool
	}{
		{"single", `var uuid = "` + testID + `";`, true},
		{"repeated identical", `uuid="` + testID + `"; other={uuid:'` + testID + `'}`, true},
		{"different identifiers", `uuid="` + testID + `"; uuid="aaaaaaaa-1234-1234-1234-123456789abc"`, false},
		{"missing", syntheticSecret, false},
		{"invalid identifier", `uuid="not-a-valid-identifier"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newService(t, newSocket(), tc.page, transport.Reply{})
			jar, _ := cookiejar.New(nil)
			id, _, err := s.fetchUUID(context.Background(), jar)
			if tc.wantOK {
				if err != nil || id != testID {
					t.Fatalf("valid UUID not accepted: %v", err)
				}
			} else {
				assertError(t, err, "UPSTREAM_CHANGED")
			}
		})
	}
	for _, location := range []string{
		"https://evil.example/" + syntheticSecret,
		"http://" + host + "/jaccount/jalogin?sid=jaoauth220160718&se=x&returl=x",
		"https://" + host + "/jaccount/expresslogin?uuid=" + testID,
	} {
		s, _ := newService(t, newSocket(), "", transport.Reply{})
		s.portal = fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
			return redirect(location), nil
		})
		s.auth = fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
			t.Error("followed malicious redirect")
			return transport.Reply{}, nil
		})
		jar, _ := cookiejar.New(nil)
		_, _, err := s.fetchUUID(context.Background(), jar)
		assertError(t, err, "AUTH_REQUIRED")
	}
}

func TestLoginReturnsOnlyExpressIssuedRootAndAutomaticTrust(t *testing.T) {
	for _, withTrust := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic user agent", true: "automatic trust and user agent"}[withTrust], func(t *testing.T) {
			ws := newSocket(`{"type":"UPDATE_QR_CODE","payload":{"ts":123456,"sig":"`+syntheticSecret+`"}}`, `{"type":"LOGIN"}`)
			reply := rootReply("JAAuthCookie=NEW_SYNTHETIC_ROOT; Path=/jaccount; Secure; HttpOnly")
			if withTrust {
				reply.Header.Add("Set-Cookie", "JATrustCookie=NEW_SYNTHETIC_TRUST; Path=/jaccount; Secure; HttpOnly")
			}
			s, completed := newService(t, ws, `uuid="`+testID+`"`, reply)
			displays := 0
			secret, err := s.Login(context.Background(), func(raw string) error {
				displays++
				u, parseErr := url.Parse(raw)
				if parseErr != nil || u.Scheme != "https" || u.Host != host || u.Path != "/jaccount/confirmscancode" || u.Query().Get("uuid") != testID || u.Query().Get("sig") != syntheticSecret || u.Query().Get("ts") != "123456" || len(u.Query()) != 3 {
					t.Error("incorrect official challenge")
				}
				return nil
			})
			defer clear(secret)
			if err != nil {
				t.Fatal(err)
			}
			credential, err := auth.ParseCampusCredential(secret)
			if err != nil || credential.JAAuthCookie != "NEW_SYNTHETIC_ROOT" {
				t.Fatal("wrong newly issued root")
			}
			if withTrust && credential.JATrustCookie != "NEW_SYNTHETIC_TRUST" {
				t.Fatal("automatic trust context lost")
			}
			if !withTrust && credential.JATrustCookie != "" {
				t.Fatal("unexpected trust context invented")
			}
			if credential.UserAgent == "" || credential.UserAgent != userAgent {
				t.Fatal("automatically selected login user agent was not retained for portal verification")
			}
			if displays != 1 || completed.Load() != 1 {
				t.Fatal("wrong display or completion count")
			}
			assertStopped(t, ws)
		})
	}
}

func TestPreLoginRootsAndCrossDomainCookiesAreNotReused(t *testing.T) {
	ws := newSocket(`{"type":"LOGIN"}`)
	s, completed := newService(t, ws, `uuid="`+testID+`"`, rootReply())
	basePortal, baseAuth := s.portal, s.auth
	s.portal = fakeGetter(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		r, e := basePortal.Get(ctx, u, h)
		r.Header.Add("Set-Cookie", "JAAuthCookie="+syntheticSecret+"; Path=/")
		r.Header.Add("Set-Cookie", "JATrustCookie="+syntheticSecret+"; Path=/")
		r.Header.Add("Set-Cookie", "Injected="+syntheticSecret+"; Domain=jaccount.sjtu.edu.cn; Path=/")
		return r, e
	})
	s.auth = fakeGetter(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if strings.Contains(h.Get("Cookie"), syntheticSecret) {
			t.Error("pre-login or cross-domain cookie leaked into request")
		}
		r, e := baseAuth.Get(ctx, u, h)
		if u.Path == "/jaccount/jalogin" {
			r.Header.Add("Set-Cookie", "JAAuthCookie="+syntheticSecret+"; Path=/jaccount")
		}
		return r, e
	})
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	assertError(t, err, "AUTH_REQUIRED")
	if len(secret) != 0 || completed.Load() != 1 {
		t.Fatal("accepted a pre-login root")
	}
	assertStopped(t, ws)
}

func TestMalformedOversizedAndExcessFramesFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
	}{
		{"malformed JSON", []string{`{"type":` + syntheticSecret}},
		{"oversized frame", []string{strings.Repeat("x", 4097) + syntheticSecret}},
		{"missing signature", []string{`{"type":"UPDATE_QR_CODE","payload":{"ts":1}}`}},
		{"invalid timestamp", []string{`{"type":"UPDATE_QR_CODE","payload":{"ts":0,"sig":"` + syntheticSecret + `"}}`}},
		{"oversized signature", []string{`{"type":"UPDATE_QR_CODE","payload":{"ts":1,"sig":"` + strings.Repeat("x", 513) + `"}}`}},
		{"control in signature", []string{`{"type":"UPDATE_QR_CODE","payload":{"ts":1,"sig":"` + syntheticSecret + `\n"}}`}},
		{"too many keepalives", append(makeKeepalives(65), `{"type":"LOGIN"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := newSocket(tc.frames...)
			s, completed := newService(t, ws, `uuid="`+testID+`"`, rootReply("JAAuthCookie=SHOULD_NOT_IMPORT"))
			secret, err := s.Login(context.Background(), func(string) error {
				t.Error("invalid challenge was displayed")
				return nil
			})
			defer clear(secret)
			assertError(t, err, "UPSTREAM_CHANGED")
			if len(secret) != 0 || completed.Load() != 0 {
				t.Fatal("failed frame reached credential completion")
			}
			assertStopped(t, ws)
		})
	}
}
func makeKeepalives(count int) []string {
	frames := make([]string, count)
	for i := range frames {
		frames[i] = `{"type":"KEEPALIVE","ignored":"` + syntheticSecret + `"}`
	}
	return frames
}

func TestCancellationClosesBlockedReaderBeforeReturning(t *testing.T) {
	ws := newSocket(`{"type":"UPDATE_QR_CODE","payload":{"ts":1,"sig":"` + syntheticSecret + `"}}`)
	s, completed := newService(t, ws, `uuid="`+testID+`"`, transport.Reply{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	displayed := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		secret, err := s.Login(ctx, func(string) error { close(displayed); return nil })
		clear(secret)
		returned <- err
	}()
	select {
	case <-displayed:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not reach display")
	}
	cancel()
	select {
	case err := <-returned:
		assertError(t, err, "AUTH_REQUIRED")
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left the socket reader blocked")
	}
	assertStopped(t, ws)
	if completed.Load() != 0 {
		t.Fatal("canceled login attempted completion")
	}
}

func TestPrivateFailuresAreSanitizedAndCleanedUp(t *testing.T) {
	for _, stage := range []string{"page", "dial", "update", "read", "display", "complete", "refresh"} {
		t.Run(stage, func(t *testing.T) {
			ws := newSocket()
			s, _ := newService(t, ws, `uuid="`+testID+`"`, transport.Reply{})
			display := func(string) error { return nil }
			want := "UPSTREAM_UNAVAILABLE"
			switch stage {
			case "page":
				s.portal = fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
					return transport.Reply{}, errors.New("https://example.invalid/" + syntheticSecret)
				})
			case "dial":
				s.dial = func(context.Context, *url.URL, http.Header) (socket, error) {
					return nil, errors.New(syntheticSecret)
				}
			case "update":
				ws.updateFail = 1
			case "read":
				ws.events <- socketEvent{err: errors.New(syntheticSecret)}
			case "display":
				ws.events <- socketEvent{body: []byte(`{"type":"UPDATE_QR_CODE","payload":{"ts":1,"sig":"sig"}}`)}
				display = func(string) error { return errors.New(syntheticSecret) }
				want = "CANCELED"
			case "complete":
				ws.events <- socketEvent{body: []byte(`{"type":"LOGIN"}`)}
				base := s.auth
				s.auth = fakeGetter(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
					if u.Path == "/jaccount/expresslogin" {
						return transport.Reply{}, errors.New(syntheticSecret)
					}
					return base.Get(ctx, u, h)
				})
			case "refresh":
				s.refresh = time.Millisecond
				ws.updateFail = 2
			}
			secret, err := s.Login(context.Background(), display)
			defer clear(secret)
			assertError(t, err, want)
			if len(secret) != 0 {
				t.Fatal("failed login returned credential bytes")
			}
			if stage != "page" && stage != "dial" {
				assertStopped(t, ws)
			}
		})
	}
}

func TestCompletionRejectsAmbiguousExpiredMissingAndForeignRoots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cookies []string
		status  int
		code    string
	}{
		{"missing root", []string{"Other=" + syntheticSecret}, 200, "AUTH_REQUIRED"},
		{"duplicate root", []string{"JAAuthCookie=one", "JAAuthCookie=" + syntheticSecret}, 200, "UPSTREAM_CHANGED"},
		{"duplicate trust", []string{"JAAuthCookie=one", "JATrustCookie=one", "JATrustCookie=" + syntheticSecret}, 200, "UPSTREAM_CHANGED"},
		{"deleted root", []string{"JAAuthCookie=" + syntheticSecret + "; Max-Age=0"}, 200, "AUTH_REQUIRED"},
		{"expired root", []string{"JAAuthCookie=" + syntheticSecret + "; Expires=Thu, 01 Jan 1970 00:00:00 GMT"}, 200, "AUTH_REQUIRED"},
		{"wrong cookie domain", []string{"JAAuthCookie=" + syntheticSecret + "; Domain=evil.example; Path=/jaccount"}, 200, "UPSTREAM_CHANGED"},
		{"overbroad cookie domain", []string{"JAAuthCookie=" + syntheticSecret + "; Domain=sjtu.edu.cn; Path=/jaccount"}, 200, "UPSTREAM_CHANGED"},
		{"wrong root path", []string{"JAAuthCookie=" + syntheticSecret + "; Path=/unreviewed"}, 200, "UPSTREAM_CHANGED"},
		{"foreign trust", []string{"JAAuthCookie=one; Path=/jaccount", "JATrustCookie=" + syntheticSecret + "; Domain=evil.example; Path=/jaccount"}, 200, "UPSTREAM_CHANGED"},
		{"rejected status", []string{"JAAuthCookie=" + syntheticSecret}, 401, "AUTH_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rootReply(tc.cookies...)
			r.Status = tc.status
			ws := newSocket(`{"type":"LOGIN"}`)
			s, _ := newService(t, ws, `uuid="`+testID+`"`, r)
			secret, err := s.Login(context.Background(), func(string) error { return nil })
			defer clear(secret)
			assertError(t, err, tc.code)
			if len(secret) != 0 {
				t.Fatal("failed completion returned root")
			}
			assertStopped(t, ws)
		})
	}
}

func TestDisplayRequiredBeforeNetworkAndUnknownStatusRemainsBounded(t *testing.T) {
	s := &Service{}
	_, err := s.Login(context.Background(), nil)
	assertError(t, err, "INVALID_ARGUMENT")
	ws := newSocket(append(makeKeepalives(4), `{"type":"LOGIN"}`)...)
	s, completed := newService(t, ws, `uuid="`+testID+`"`, rootReply("JAAuthCookie=NEW_SYNTHETIC_ROOT"))
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	if err != nil || completed.Load() != 1 {
		t.Fatalf("bounded official status frames failed: %v", err)
	}
	assertStopped(t, ws)
}

func TestSyntheticChallengeCanContainURLReservedCharacters(t *testing.T) {
	signature := "synthetic+slash/equals=&percent%quote\""
	frame, _ := json.Marshal(map[string]any{"type": "UPDATE_QR_CODE", "payload": map[string]any{"ts": 1, "sig": signature}})
	ws := newSocket(string(frame), `{"type":"LOGIN"}`)
	s, _ := newService(t, ws, `uuid="`+testID+`"`, rootReply("JAAuthCookie=NEW_SYNTHETIC_ROOT"))
	secret, err := s.Login(context.Background(), func(raw string) error {
		u, e := url.Parse(raw)
		if e != nil || u.Query().Get("sig") != signature || len(u.Query()) != 3 {
			t.Error("signature escaped its query value")
		}
		return nil
	})
	defer clear(secret)
	if err != nil {
		t.Fatal(err)
	}
	assertStopped(t, ws)
}

func TestLoginPageAndCookieBudgets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply transport.Reply
	}{
		{"page too large", transport.Reply{Status: 200, Header: http.Header{}, Body: []byte(strings.Repeat("x", (1<<20)+1))}},
		{"multiple locations", transport.Reply{Status: 302, Header: http.Header{"Location": {pageURL(), pageURL()}}}},
		{"too many cookies", func() transport.Reply {
			r := redirect(pageURL())
			for i := 0; i < 33; i++ {
				r.Header.Add("Set-Cookie", "Session="+syntheticSecret+"; Path=/")
			}
			return r
		}()},
		{"cookie too large", func() transport.Reply {
			r := redirect(pageURL())
			r.Header.Add("Set-Cookie", "Session="+strings.Repeat("x", 8193)+"; Path=/")
			return r
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{portal: fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
				return tc.reply, nil
			})}
			jar, _ := cookiejar.New(nil)
			_, _, err := s.fetchUUID(context.Background(), jar)
			assertError(t, err, "UPSTREAM_CHANGED")
		})
	}
	calls := 0
	cycle := fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
		calls++
		return redirect(pageURL()), nil
	})
	s := &Service{portal: cycle, auth: cycle}
	jar, _ := cookiejar.New(nil)
	_, _, err := s.fetchUUID(context.Background(), jar)
	assertError(t, err, "AUTH_REQUIRED")
	if calls > 6 {
		t.Fatal("redirect cycle exceeded request budget")
	}
}

func TestRefreshCannotContinueIndefinitely(t *testing.T) {
	ws := newSocket()
	s, completed := newService(t, ws, `uuid="`+testID+`"`, transport.Reply{})
	s.refresh = time.Millisecond
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	assertError(t, err, "AUTH_REQUIRED")
	if ws.updates.Load() > 9 || completed.Load() != 0 || len(secret) != 0 {
		t.Fatal("expired flow exceeded refresh budget or completed")
	}
	assertStopped(t, ws)
}

func TestCompletionFollowsReviewedSSOAndKeepsRootsAtJAccount(t *testing.T) {
	const authSession = "SYNTHETIC_AUTH_SESSION"
	const portalSession = "SYNTHETIC_PORTAL_SESSION"
	contextURL := "https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket&state=synthetic-state"
	callback := start + "?code=synthetic-code&state=synthetic-state"
	express := "https://" + host + "/jaccount/expresslogin?uuid=" + testID
	first := rootReply("JAAuthCookie=FIRST_SYNTHETIC_ROOT; Path=/jaccount; Secure", "JATrustCookie=FIRST_SYNTHETIC_TRUST; Path=/jaccount; Secure", "JSESSIONID="+authSession+"; Path=/; Secure")
	first.Status = 302
	first.Header.Set("Location", contextURL)
	rotated := rootReply("JAAuthCookie=ROTATED_SYNTHETIC_ROOT; Path=/jaccount; Secure", "JATrustCookie=ROTATED_SYNTHETIC_TRUST; Path=/jaccount; Secure")
	rotated.Status = 302
	rotated.Header.Set("Location", callback)
	forged := rootReply("JAAuthCookie="+syntheticSecret+"; Domain=jaccount.sjtu.edu.cn; Path=/", "JATrustCookie="+syntheticSecret+"; Path=/", "PortalSession="+portalSession+"; Path=/; Secure")
	forged.Status = 302
	forged.Header.Set("Location", start)
	landing := rootReply("JAAuthCookie=" + syntheticSecret + "; Path=/")
	landing.Body = []byte("synthetic read-only portal landing")
	steps := []struct {
		url   string
		reply transport.Reply
	}{
		{express, first}, {contextURL, rotated}, {callback, forged}, {start, landing},
	}
	calls := 0
	get := fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if calls >= len(steps) {
			t.Fatal("completion exceeded reviewed exchange")
		}
		step := steps[calls]
		calls++
		if u.String() != step.url || !allowed(u) || h.Get("User-Agent") != userAgent {
			t.Error("completion left its exact SSO sequence")
		}
		cookies := h.Get("Cookie")
		if strings.Contains(cookies, "JAAuthCookie") || strings.Contains(cookies, "JATrustCookie") || strings.Contains(cookies, syntheticSecret) {
			t.Error("root cookie sent to OAuth or portal callback")
		}
		if u.Host == host && u.Path == "/oauth2/authorize" && !strings.Contains(cookies, authSession) {
			t.Error("jAccount's isolated ordinary session was lost")
		}
		if u.Host == "my.sjtu.edu.cn" && strings.Contains(cookies, authSession) {
			t.Error("jAccount session leaked to my portal")
		}
		if u.String() == start && !strings.Contains(cookies, portalSession) {
			t.Error("portal's ordinary session did not survive its own callback")
		}
		return step.reply, nil
	})
	s := &Service{portal: get, auth: get}
	jar, _ := cookiejar.New(nil)
	secret, err := s.complete(context.Background(), jar, testID, nil)
	defer clear(secret)
	if err != nil {
		t.Fatal(err)
	}
	c, err := auth.ParseCampusCredential(secret)
	if err != nil || c.JAAuthCookie != "ROTATED_SYNTHETIC_ROOT" || c.JATrustCookie != "ROTATED_SYNTHETIC_TRUST" || c.UserAgent != userAgent || calls != len(steps) {
		t.Fatal("completion did not retain only the final first-party root context")
	}
	if strings.Contains(string(secret), syntheticSecret) || strings.Contains(string(secret), authSession) || strings.Contains(string(secret), portalSession) {
		t.Fatal("portal-forged root or ordinary transient session was imported")
	}
}

func TestCompletionRejectsUnreviewedRedirectEvenWithIssuedRoot(t *testing.T) {
	for _, location := range []string{
		"https://evil.example/" + syntheticSecret,
		"http://my.sjtu.edu.cn/ui/appmyinfo",
		"https://" + host + "/jaccount/expresslogin?uuid=" + testID,
		pageURL(),
		"https://" + host + "/oauth2/authorize?client_id=etLP8wWejc7xZCkMpgee&redirect_uri=" + url.QueryEscape(start) + "&response_type=code&state=x",
		start + "?code=one&state=one&state=two",
	} {
		t.Run(location, func(t *testing.T) {
			calls := 0
			get := fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
				calls++
				if calls > 1 {
					t.Fatal("followed an unreviewed completion redirect")
				}
				r := rootReply("JAAuthCookie=" + syntheticSecret + "; Path=/jaccount")
				r.Status = 302
				r.Header.Set("Location", location)
				return r, nil
			})
			s := &Service{portal: get, auth: get}
			jar, _ := cookiejar.New(nil)
			secret, err := s.complete(context.Background(), jar, testID, nil)
			defer clear(secret)
			assertError(t, err, "AUTH_REQUIRED")
			if len(secret) != 0 || calls != 1 {
				t.Fatal("malicious redirect was reported as completed login")
			}
		})
	}
}

func TestCompletionBudgetsAndInteractivePagesFailClosed(t *testing.T) {
	contextURL := "https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket"
	for _, tc := range []struct {
		name     string
		bodySize int
		terminal bool
		code     string
		maxCalls int
	}{
		{"redirect cycle", 0, false, "AUTH_REQUIRED", 6},
		{"cumulative response budget", 600 << 10, false, "UPSTREAM_CHANGED", 2},
		{"OAuth consent page", 0, true, "AUTH_REQUIRED", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			get := fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
				calls++
				if calls > tc.maxCalls {
					t.Fatal("completion exceeded its request budget")
				}
				if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
					t.Error("root leaked into OAuth completion")
				}
				r := rootReply()
				if calls == 1 {
					r.Header.Add("Set-Cookie", "JAAuthCookie="+syntheticSecret+"; Path=/jaccount")
				}
				r.Body = []byte(strings.Repeat("x", tc.bodySize))
				if tc.terminal && calls > 1 {
					r.Body = []byte("synthetic interactive approval page " + syntheticSecret)
					return r, nil
				}
				r.Status = 302
				r.Header.Set("Location", contextURL)
				return r, nil
			})
			s := &Service{portal: get, auth: get}
			jar, _ := cookiejar.New(nil)
			secret, err := s.complete(context.Background(), jar, testID, nil)
			defer clear(secret)
			assertError(t, err, tc.code)
			if len(secret) != 0 || calls != tc.maxCalls {
				t.Fatal("incomplete exchange was returned as a valid session")
			}
		})
	}
	get := fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
		r := rootReply("JAAuthCookie=" + syntheticSecret + "; Path=/jaccount")
		r.Status = 302
		r.Header["Location"] = []string{contextURL, contextURL}
		return r, nil
	})
	s := &Service{auth: get}
	jar, _ := cookiejar.New(nil)
	secret, err := s.complete(context.Background(), jar, testID, nil)
	defer clear(secret)
	assertError(t, err, "UPSTREAM_CHANGED")
	if len(secret) != 0 {
		t.Fatal("ambiguous completion redirect returned a root")
	}
}
