package qrlogin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestExpressConsumesRootAtTheBoundOriginalLoginHandler(t *testing.T) {
	ws := newSocket(`{"type":"LOGIN"}`)
	s, _ := newService(t, ws, "", transport.Reply{})
	signInCalls, expressCalls := 0, 0
	contextURL := "https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket"
	s.auth = fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		switch u.Path {
		case "/jaccount/jalogin":
			signInCalls++
			if signInCalls == 1 {
				return transport.Reply{Status: 200, Header: http.Header{"Set-Cookie": {"JSESSIONID=SYNTHETIC_BOOTSTRAP_SESSION; Path=/jaccount; Secure"}}, Body: []byte(`uuid="` + testID + `"`)}, nil
			}
			if signInCalls != 2 || u.String() != pageURL() || !strings.Contains(h.Get("Cookie"), "JAAuthCookie=FIRST_SYNTHETIC_ROOT") || !strings.Contains(h.Get("Cookie"), "JSESSIONID=SYNTHETIC_BOOTSTRAP_SESSION") {
				t.Fatal("bound login continuation lost its root/session or changed challenge")
			}
			return redirect(contextURL), nil
		case "/jaccount/expresslogin":
			expressCalls++
			r := rootReply("JAAuthCookie=FIRST_SYNTHETIC_ROOT; Path=/jaccount; Secure")
			r.Status = 302
			r.Header.Set("Location", pageURL())
			return r, nil
		case "/oauth2/authorize":
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JSESSIONID") {
				t.Fatal("path-scoped credentials leaked to OAuth")
			}
			return redirect(start + "?code=synthetic-code&state=synthetic-state"), nil
		default:
			t.Fatal("unexpected request")
			return transport.Reply{}, nil
		}
	})
	s.portal = fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if u.RawQuery == "" {
			return redirect(pageURL()), nil
		}
		if h.Get("Cookie") != "" {
			t.Fatal("authentication cookie leaked into portal callback")
		}
		return transport.Reply{Status: 200, Header: http.Header{}}, nil
	})
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	c, parseErr := auth.ParseCampusCredential(secret)
	if err != nil || parseErr != nil || c.JAAuthCookie != "FIRST_SYNTHETIC_ROOT" || signInCalls != 2 || expressCalls != 1 {
		t.Fatalf("bound continuation failed: %v", err)
	}
	assertStopped(t, ws)
}

func TestLoginContinuationCannotChangeChallengeOrLoop(t *testing.T) {
	bound, _ := url.Parse(pageURL())
	for _, key := range []string{"sid", "returl", "se", "client"} {
		next := *bound
		q := next.Query()
		q.Set(key, "ANOTHER_SYNTHETIC_CHALLENGE")
		next.RawQuery = q.Encode()
		if completionNext(&next, bound, true, false) {
			t.Fatalf("changed %s was accepted", key)
		}
	}
	if completionNext(bound, nil, true, false) || completionNext(bound, bound, false, false) || completionNext(bound, bound, true, true) {
		t.Fatal("unbound, rootless or repeated login continuation accepted")
	}
	if !completionNext(bound, bound, true, false) {
		t.Fatal("exact bound continuation rejected")
	}
}

func TestFailedCompletionDoesNotReleaseIssuedCredential(t *testing.T) {
	ws := newSocket(`{"type":"LOGIN"}`)
	r := rootReply("JAAuthCookie=NEW_SYNTHETIC_ROOT; Path=/jaccount")
	r.Status = 302
	r.Header.Set("Location", "https://evil.example/"+syntheticSecret)
	s, _ := newService(t, ws, `uuid="`+testID+`"`, r)
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	assertError(t, err, "AUTH_REQUIRED")
	if len(secret) != 0 {
		t.Fatal("incomplete login released an unverified credential")
	}
	assertStopped(t, ws)
}
