package qrlogin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestFailureDiagnosticPreservesCodeWithoutPrivateContext(t *testing.T) {
	ws := newSocket(`{"type":"LOGIN"}`)
	s, _ := newService(t, ws, `uuid="`+testID+`"`, rootReply("JAAuthCookie="+syntheticSecret+"; Max-Age=0"))
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	assertError(t, err, "AUTH_REQUIRED")
	d := Diagnose(err)
	if d.Stage != "completion_cookie" || d.Endpoint != "express" || d.Status != 200 || d.Hop != 1 {
		t.Fatalf("wrong static diagnostic: %+v", d)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), testID) || strings.Contains(string(b), syntheticSecret) || strings.Contains(string(b), "https:") {
		t.Fatal("diagnostic included private context")
	}
	if Diagnose(errors.New(syntheticSecret)).Stage != "unknown" {
		t.Fatal("unknown errors exposed their text")
	}
}

func TestQRServerRejectionDoesNotCompleteLogin(t *testing.T) {
	ws := newSocket(`{"error":1,"type":"LOGIN","message":"` + syntheticSecret + `"}`)
	s, completed := newService(t, ws, `uuid="`+testID+`"`, rootReply("JAAuthCookie=SHOULD_NOT_IMPORT"))
	secret, err := s.Login(context.Background(), func(string) error { return nil })
	defer clear(secret)
	assertError(t, err, "AUTH_REQUIRED")
	if Diagnose(err).Stage != "qr_message" || completed.Load() != 0 || len(secret) != 0 {
		t.Fatal("server rejection attempted completion or lost its stage")
	}
	assertStopped(t, ws)
}

func TestRootMayBeIssuedLaterInReviewedCompletion(t *testing.T) {
	contextURL := "https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket"
	calls := 0
	get := fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		calls++
		switch calls {
		case 1:
			if u.Path != "/jaccount/expresslogin" {
				t.Fatal("expected express login first")
			}
			return redirect(contextURL), nil
		case 2:
			if u.String() != contextURL {
				t.Fatal("expected reviewed continuation")
			}
			r := rootReply("JAAuthCookie=NEW_SYNTHETIC_ROOT; Path=/jaccount")
			r.Status = 302
			r.Header.Set("Location", start+"?code=synthetic-code&state=synthetic-state")
			return r, nil
		case 3:
			if u.Host != "my.sjtu.edu.cn" || h.Get("Cookie") != "" {
				t.Fatal("root leaked into portal callback")
			}
			return rootReply(), nil
		default:
			t.Fatal("completion exceeded expected steps")
			return transport.Reply{}, contracts.Fail("UPSTREAM_CHANGED", "unexpected step")
		}
	})
	s := &Service{auth: get, portal: get}
	jar, _ := cookiejar.New(nil)
	secret, err := s.complete(context.Background(), jar, testID, nil)
	defer clear(secret)
	if err != nil || len(secret) == 0 || calls != 3 {
		t.Fatalf("later root was not accepted after completion: %v", err)
	}
}

func TestRejectedRedirectDiagnosticsNeverEchoDestinations(t *testing.T) {
	for _, tc := range []struct{ raw, target, shape string }{
		{"https://evil.example/" + syntheticSecret + "?private=" + syntheticSecret, "unreviewed", "unreviewed_url"},
		{"http://my.sjtu.edu.cn/ui/appmyinfo?code=" + syntheticSecret, "my_callback", "non_https"},
		{start + "?code=" + syntheticSecret, "my_callback", "code_only"},
		{start + "?code=" + syntheticSecret + "&state=x&state=y", "my_callback", "invalid_query"},
		{start + "?unknown=" + syntheticSecret, "my_callback", "other_query"},
		{pageURL(), "sign_in", "other_query"},
	} {
		t.Run(tc.target+"_"+tc.shape, func(t *testing.T) {
			r := rootReply("JAAuthCookie=" + syntheticSecret)
			r.Status = 302
			r.Header.Set("Location", tc.raw)
			s := &Service{auth: fakeGetter(func(context.Context, *url.URL, http.Header) (transport.Reply, error) { return r, nil })}
			jar, _ := cookiejar.New(nil)
			secret, err := s.complete(context.Background(), jar, testID, nil)
			defer clear(secret)
			assertError(t, err, "AUTH_REQUIRED")
			d := Diagnose(err)
			if d.Stage != "completion_redirect" || d.RedirectTarget != tc.target || d.RedirectShape != tc.shape {
				t.Fatalf("wrong static redirect diagnostic: %+v", d)
			}
			b, _ := json.Marshal(d)
			if strings.Contains(string(b), syntheticSecret) || strings.Contains(string(b), testID) || strings.Contains(string(b), "https:") {
				t.Fatal("diagnostic exposed a destination or challenge")
			}
		})
	}
}
