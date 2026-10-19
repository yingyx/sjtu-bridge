package jaccount

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type getterFunc func(context.Context, *url.URL, http.Header) (transport.Reply, error)

func (f getterFunc) Get(c context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f(c, u, h)
}
func redirect(target string) transport.Reply {
	return transport.Reply{Status: 302, Header: http.Header{"Location": {target}}}
}
func current(body string) transport.Reply {
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v want %s", err, want)
	}
}

func TestCookieIsolationAndOutput(t *testing.T) {
	calls := 0
	f := getterFunc(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		calls++
		if !Allowed(u) {
			t.Fatal("unreviewed URL")
		}
		if h.Get("Authorization") != "" {
			t.Fatal("unexpected bearer")
		}
		if u.Path != "/jaccount/jalogin" && strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
			t.Fatal("root cookie leaked outside auth path")
		}
		switch calls {
		case 1:
			r := redirect("/jaccount/jalogin?sid=test&returl=synthetic&se=test")
			r.Header.Add("Set-Cookie", "JSESSIONID=synthetic-session; Path=/profile/; Secure")
			r.Header.Add("Set-Cookie", "JAAuthCookie=attacker; Path=/")
			return r, nil
		case 2:
			if h.Get("Cookie") != "JAAuthCookie=SYNTHETIC_ROOT" {
				t.Fatal("root credential overwritten or jar crossed paths")
			}
			return redirect("/profile/#/"), nil
		case 3:
			return transport.Reply{Status: 200, Header: http.Header{}, Body: []byte("<html>account shell</html>")}, nil
		case 4:
			if u.Path != "/profile/current" || h.Get("Cookie") != "JSESSIONID=synthetic-session" {
				t.Fatal("wrong current session")
			}
			return current(`{"success":true,"errorCode":0,"data":"SYNTHETIC_SESSION_TOKEN"}`), nil
		}
		t.Fatal("too many calls")
		return transport.Reply{}, nil
	})
	r, err := Check(context.Background(), f, []byte("SYNTHETIC_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if !r.Authenticated || r.IdentityVerified || r.Stored || strings.Contains(string(b), "SYNTHETIC") || calls != 4 {
		t.Fatal("invalid output")
	}
}

func TestSSOFailureBoundaries(t *testing.T) {
	for _, target := range []string{"https://evil.example/?ticket=SYNTHETIC_SECRET", "https://jaccount.sjtu.edu.cn/profile/api/account/updatePassword", "http://jaccount.sjtu.edu.cn/profile/", "https://jaccount.sjtu.edu.cn/profile/jaccount?returnUrl=https://evil.example"} {
		calls := 0
		_, err := Check(context.Background(), getterFunc(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
			calls++
			return redirect(target), nil
		}), []byte("SYNTHETIC_ROOT"))
		code(t, err, "AUTH_REQUIRED")
		if calls != 1 || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
			t.Fatal("followed/leaked forbidden redirect")
		}
	}
	calls := 0
	_, err := Check(context.Background(), getterFunc(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
		calls++
		return redirect("/profile/jaccount?returnUrl=%2F%23%2F"), nil
	}), []byte("SYNTHETIC_ROOT"))
	code(t, err, "AUTH_REQUIRED")
	if calls != 8 {
		t.Fatal(calls)
	}
	calls = 0
	_, err = Check(context.Background(), getterFunc(func(context.Context, *url.URL, http.Header) (transport.Reply, error) {
		calls++
		if calls == 1 {
			return redirect("/jaccount/jalogin?sid=x&returl=x&se=x"), nil
		}
		return transport.Reply{Status: 200, Body: []byte("<form>login/captcha</form>")}, nil
	}), []byte("SYNTHETIC_ROOT"))
	code(t, err, "AUTH_EXPIRED")
}

func TestCurrentIsNotJustHTTP200(t *testing.T) {
	for _, body := range []string{`{"success":true,"errorCode":0,"data":null}`, `{"success":false,"errorCode":401,"data":null}`} {
		_, err := parseCurrent(current(body))
		code(t, err, "AUTH_EXPIRED")
	}
	for _, body := range []string{`{}`, `<html>login</html>`, `{"success":true,"errorCode":0,"data":{}}`, `{"success":true,"errorCode":0,"data":""}`} {
		_, err := parseCurrent(current(body))
		code(t, err, "UPSTREAM_CHANGED")
	}
}
