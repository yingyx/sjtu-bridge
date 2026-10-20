package qrlogin

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestOptionalTrustRemovalKeepsValidJA(t *testing.T) {
	for _, directive := range []struct {
		name, cookie string
	}{
		{"delete", "JATrustCookie=REMOVED_SYNTHETIC_TRUST; Max-Age=0; Path=/jaccount"},
		{"expired", "JATrustCookie=REMOVED_SYNTHETIC_TRUST; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/jaccount"},
		{"empty", "JATrustCookie=; Path=/jaccount"},
	} {
		for _, issueNewJA := range []bool{false, true} {
			name := directive.name + "/existing JA"
			if issueNewJA {
				name = directive.name + "/new JA"
			}
			t.Run(name, func(t *testing.T) {
				credential := auth.CampusCredential{Version: 1, JAAuthCookie: "EXISTING_SYNTHETIC_JA", JATrustCookie: "OLD_SYNTHETIC_TRUST", UserAgent: userAgent}
				h := http.Header{"Set-Cookie": {directive.cookie}}
				wantJA := credential.JAAuthCookie
				if issueNewJA {
					h.Add("Set-Cookie", "JAAuthCookie=NEW_SYNTHETIC_JA; Path=/jaccount; Secure")
					wantJA = "NEW_SYNTHETIC_JA"
				}
				if err := collectRoots(&credential, h); err != nil {
					t.Fatalf("optional trust removal invalidated valid JA: %v", err)
				}
				if credential.JAAuthCookie != wantJA || credential.JATrustCookie != "" || credential.UserAgent != userAgent {
					t.Fatal("trust removal lost required context or retained removed trust")
				}
			})
		}
	}
}

func TestTrustRemovalSurvivesTheCompleteSSOExchange(t *testing.T) {
	for _, cookie := range []string{
		"JATrustCookie=REMOVED_SYNTHETIC_TRUST; Max-Age=0; Path=/jaccount",
		"JATrustCookie=REMOVED_SYNTHETIC_TRUST; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/jaccount",
		"JATrustCookie=; Path=/jaccount",
	} {
		s := lifecycleService(t, true, cookie)
		jar, _ := cookiejar.New(nil)
		secret, err := s.complete(context.Background(), jar, testID, nil)
		if err != nil {
			t.Fatalf("valid SSO failed after optional trust removal: %v", err)
		}
		credential, parseErr := auth.ParseCampusCredential(secret)
		clear(secret)
		if parseErr != nil || credential.JAAuthCookie != "VALID_SYNTHETIC_JA" || credential.JATrustCookie != "" || credential.UserAgent != userAgent {
			t.Fatal("completed import did not retain valid JA and clear removed trust")
		}
	}
}

func TestJADeletionStillRejectsCompletedSession(t *testing.T) {
	for _, cookie := range []string{
		"JAAuthCookie=REMOVED_SYNTHETIC_JA; Max-Age=0; Path=/jaccount",
		"JAAuthCookie=REMOVED_SYNTHETIC_JA; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/jaccount",
		"JAAuthCookie=; Path=/jaccount",
	} {
		s := lifecycleService(t, true, cookie)
		jar, _ := cookiejar.New(nil)
		secret, err := s.complete(context.Background(), jar, testID, nil)
		defer clear(secret)
		assertError(t, err, "AUTH_REQUIRED")
		if len(secret) != 0 {
			t.Fatal("deleted required JA was returned as a valid session")
		}
	}
}

func TestDuplicateCookiesRemainAmbiguousDuringRemoval(t *testing.T) {
	for _, cookies := range [][]string{
		{"JATrustCookie=; Max-Age=0; Path=/jaccount", "JATrustCookie=NEW_SYNTHETIC_TRUST; Path=/jaccount"},
		{"JATrustCookie=NEW_SYNTHETIC_TRUST; Path=/jaccount", "JATrustCookie=; Path=/jaccount"},
		{"JAAuthCookie=NEW_SYNTHETIC_JA; Path=/jaccount", "JAAuthCookie=; Max-Age=0; Path=/jaccount"},
	} {
		credential := auth.CampusCredential{Version: 1, JAAuthCookie: "VALID_SYNTHETIC_JA", JATrustCookie: "OLD_SYNTHETIC_TRUST", UserAgent: userAgent}
		h := http.Header{"Set-Cookie": cookies}
		assertError(t, collectRoots(&credential, h), "UPSTREAM_CHANGED")
	}
}

func TestCompletionCanObtainRequiredJAOnALaterReviewedHop(t *testing.T) {
	s := lifecycleService(t, false, "JAAuthCookie=VALID_SYNTHETIC_JA; Path=/jaccount; Secure")
	jar, _ := cookiejar.New(nil)
	secret, err := s.complete(context.Background(), jar, testID, nil)
	defer clear(secret)
	if err != nil {
		t.Fatalf("reviewed later hop could not issue required JA: %v", err)
	}
	credential, err := auth.ParseCampusCredential(secret)
	if err != nil || credential.JAAuthCookie != "VALID_SYNTHETIC_JA" || credential.UserAgent != userAgent {
		t.Fatal("later first-party JA was not imported")
	}
}

// lifecycleService completes only the fixed express -> OAuth -> portal chain.
// Removing optional trust must not require an extra scan or a replacement JA.
func lifecycleService(t *testing.T, rootAtExpress bool, oauthCookie string) *Service {
	t.Helper()
	contextURL := "https://" + host + "/oauth2/authorize?context=synthetic-context&jatkt=synthetic-ticket"
	callback := start + "?code=synthetic-code&state=synthetic-state"
	authCalls, portalCalls := 0, 0
	return &Service{
		auth: fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
			authCalls++
			if !allowed(u) || authCalls > 2 || h.Get("User-Agent") != userAgent {
				t.Fatal("unreviewed lifecycle authentication request")
			}
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
				t.Fatal("root sent to OAuth while testing cookie lifecycle")
			}
			if authCalls == 1 {
				if u.Path != "/jaccount/expresslogin" {
					t.Fatal("lifecycle did not start at expresslogin")
				}
				r := redirect(contextURL)
				if rootAtExpress {
					r.Header.Add("Set-Cookie", "JAAuthCookie=VALID_SYNTHETIC_JA; Path=/jaccount; Secure")
					r.Header.Add("Set-Cookie", "JATrustCookie=OLD_SYNTHETIC_TRUST; Path=/jaccount; Secure")
				}
				return r, nil
			}
			if u.String() != contextURL {
				t.Fatal("lifecycle left the reviewed OAuth hop")
			}
			r := redirect(callback)
			r.Header.Add("Set-Cookie", oauthCookie)
			return r, nil
		}),
		portal: fakeGetter(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
			portalCalls++
			if portalCalls != 1 || u.String() != callback || h.Get("User-Agent") != userAgent {
				t.Fatal("unreviewed lifecycle portal callback")
			}
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
				t.Fatal("root sent to portal during lifecycle completion")
			}
			return transport.Reply{Status: 200, Header: http.Header{}, Body: []byte("synthetic read-only landing")}, nil
		}),
	}
}
