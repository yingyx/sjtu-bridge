package bath

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type fake struct {
	t                       *testing.T
	foreign, bindingChanged bool
	bindings                int
	unbound                 bool
	calls                   int
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls++
	r := transport.Reply{Status: 302, Header: http.Header{}}
	if u.Host == Host {
		if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
			f.t.Fatal("portal destination or root isolation")
		}
		if u.Path == "/" {
			if u.RawQuery == "" {
				if h.Get("X-Requested-With") != "" {
					f.t.Fatal("homepage must be HTML")
				}
				r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id="+clientID+"&scope=basic+essential&response_type=code&redirect_uri=http%3A%2F%2Fbath.sjtu.edu.cn%2F&state=SYN_STATE")
				r.Header.Add("Set-Cookie", "PORTAL=SYN_SESSION; Path=/; Secure")
			} else {
				if u.Scheme != "https" {
					f.t.Fatal("authorization code delivered over HTTP")
				}
				r.Header.Set("Location", "/ui/index")
			}
			return r, nil
		}
		if !strings.Contains(h.Get("Cookie"), "PORTAL=SYN_SESSION") {
			f.t.Fatal("session cookie missing")
		}
		r.Status = 200
		r.Header.Set("Content-Type", "application/json")
		body := ""
		switch u.Path {
		case "/api/me/info":
			body = `{"errno":0,"total":0,"entities":[{"account":"synthetic_user","workNo":"SYN7","name":"MUST_NOT_LEAK"}]}`
		case "/api/water/mybuilding":
			f.bindings++
			dm := "SYN_BUILDING"
			if f.bindingChanged && f.bindings > 1 {
				dm = "OTHER"
			}
			entity := `{"dm":"` + dm + `","mc":"合成楼栋"}`
			if f.unbound {
				entity = "null"
			}
			body = `{"errno":0,"total":0,"entities":[` + entity + `]}`
		case "/api/water/fee":
			if u.Query().Get("code") != "SYN7" {
				f.t.Fatal("client identity used for fee")
			}
			code := "SYN7"
			if f.foreign {
				code = "OTHER"
			}
			body = `{"errno":0,"total":2,"entities":[{"code":"` + code + `","fee":"1.20","consTime":"120","date":"2026-10-20 20:00:00","name":"MUST_NOT_LEAK","eqcode":"MUST_NOT_LEAK"},{"code":"SYN7","fee":"0","consTime":"0","date":"2026-10-21 00:00:00"}]}`
		case "/api/water/equipments":
			if u.Query().Get("code") != "SYN7" || u.Query().Get("buildingcode") != "SYN_BUILDING" {
				f.t.Fatal("unverified building selected")
			}
			body = `{"errno":0,"total":0,"entities":[{"equipment":[{"code":"SYN7","buildingcode":"SYN_BUILDING","total":2,"idle":1,"name":"MUST_NOT_LEAK","idll":[{"level":"一楼","inuser":0,"eqcode":"MUST_NOT_LEAK"},{"level":"一楼","inuser":1,"name":"MUST_NOT_LEAK"}]}]}]}`
		default:
			f.t.Fatal("unexpected endpoint")
		}
		r.Body = []byte(body)
		return r, nil
	}
	if !AuthAllowed(u) {
		f.t.Fatal("unreviewed authentication")
	}
	switch u.Path {
	case "/jaccount/jalogin":
		if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYN_ROOT") {
			f.t.Fatal("missing root credential")
		}
		r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYN_CONTEXT&jatkt=SYN_TICKET")
	case "/oauth2/authorize":
		if strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
			f.t.Fatal("root cookie outside login")
		}
		if u.Query().Get("context") != "" {
			r.Header.Set("Location", "http://bath.sjtu.edu.cn/?code=SYN_CODE&state=SYN_STATE")
		} else {
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYN_CLIENT&returl=SYN_RETURN&se=SYN_SE")
		}
	}
	return r, nil
}
func TestShowerReadsAndCallbackUpgrade(t *testing.T) {
	for _, view := range []string{contracts.BathBindingView, contracts.BathDevicesView, contracts.BathUsageView} {
		t.Run(view, func(t *testing.T) {
			f := &fake{t: t}
			s := Service{f, f}
			q := contracts.CampusQuery{AccountRef: "bath:user:synthetic_user", ResourceRef: "bath:self", View: view, Limit: 1}
			if view == contracts.BathUsageView {
				q.Begin, q.End = "2026-10-01", "2026-10-21"
			}
			page, e := s.Read(context.Background(), []byte("SYN_ROOT"), q.AccountRef, q, "")
			if e != nil || len(page.Rows) != 1 {
				t.Fatal("shower read", e)
			}
			raw, _ := json.Marshal(page.Rows)
			if strings.Contains(string(raw), "MUST_NOT_LEAK") {
				t.Fatal("unapproved fields escaped")
			}
			if view == contracts.BathUsageView {
				if page.Next == "" {
					t.Fatal("missing continuation")
				}
				last, e := s.Read(context.Background(), []byte("SYN_ROOT"), q.AccountRef, q, page.Next)
				if e != nil || len(last.Rows) != 1 || last.Next != "" {
					t.Fatal("last page", e)
				}
			}
		})
	}
}
func TestShowerRejectsForeignAccountAndBindingChanges(t *testing.T) {
	for _, view := range []string{contracts.BathUsageView, contracts.BathDevicesView} {
		f := &fake{t: t, foreign: true, bindingChanged: true}
		s := Service{f, f}
		q := contracts.CampusQuery{AccountRef: "bath:user:synthetic_user", ResourceRef: "bath:self", View: view, Limit: 1}
		if view == contracts.BathUsageView {
			q.Begin, q.End = "2026-10-01", "2026-10-21"
		}
		if page, e := s.Read(context.Background(), []byte("SYN_ROOT"), q.AccountRef, q, ""); e == nil || len(page.Rows) > 0 {
			t.Fatal("foreign result returned")
		}
	}
}
func TestUnboundIsExplicitAndSensitiveEndpointsDenied(t *testing.T) {
	f := &fake{t: t, unbound: true}
	s := Service{f, f}
	q := contracts.CampusQuery{AccountRef: "bath:user:synthetic_user", ResourceRef: "bath:self", View: contracts.BathBindingView, Limit: 1}
	p, e := s.Read(context.Background(), []byte("SYN_ROOT"), q.AccountRef, q, "")
	if e != nil || p.Rows[0].Bound == nil || *p.Rows[0].Bound {
		t.Fatal("unbound result", e)
	}
	q.View = contracts.BathDevicesView
	if _, e := s.Read(context.Background(), []byte("SYN_ROOT"), q.AccountRef, q, ""); e == nil {
		t.Fatal("unbound devices empty success")
	}
	for _, path := range []string{"/api/water/pwd", "/api/water/building", "/api/water/fee?code=OTHER", "/api/water/equipments?code=SYN7&buildingcode=SYN_BUILDING&extra=1", "/api/me/info?code=OTHER"} {
		u, _ := url.Parse(rootURL + strings.TrimPrefix(path, "/"))
		if Allowed(u) {
			t.Fatal("unreviewed endpoint", path)
		}
	}
}
