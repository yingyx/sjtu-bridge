package myportal

import (
	"context"
	"encoding/json"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type fakeHTTP struct {
	t              *testing.T
	get            func(*url.URL) (transport.Reply, error)
	post           func(*url.URL, http.Header, []byte) (transport.Reply, error)
	apiCalls       int
	bootstrapCalls int
	bootstrapReply *transport.Reply
}

func (f *fakeHTTP) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if h.Get("Authorization") != "" || (u.Path != "/jaccount/jalogin" && strings.Contains(h.Get("Cookie"), "JAAuthCookie")) {
		f.t.Fatal("root credential crossed login boundary")
	}
	r := transport.Reply{Status: 302, Header: http.Header{}}
	switch {
	case u.Host == "jaccount.sjtu.edu.cn":
		if !AuthAllowed(u) {
			f.t.Fatal("unreviewed auth endpoint")
		}
		if u.Path == "/jaccount/jalogin" {
			if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_COOKIE") {
				f.t.Fatal("root missing")
			}
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC")
		} else if u.Query().Get("client_id") != "" {
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC")
		} else {
			r.Header.Set("Location", callback+"?code=SYNTHETIC&state="+"bound")
		}
		return r, nil
	case u.Path == "/ui/app/":
		if u.Query().Get("code") == "" {
			if strings.Contains(h.Get("Cookie"), "portal_session=") {
				f.bootstrapCalls++
				if f.bootstrapReply != nil {
					return *f.bootstrapReply, nil
				}
				return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: []byte("<html>synthetic task context</html>")}, nil
			}
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id="+client+"&redirect_uri="+url.QueryEscape(callback)+"&response_type=code&state=bound")
		} else {
			r.Header.Add("Set-Cookie", "portal_session=SYNTHETIC_SESSION; Path=/; Secure")
			r.Status = 200
		}
		return r, nil
	default:
		if !Allowed(u) {
			f.t.Fatal("unreviewed portal endpoint")
		}
		f.apiCalls++
		return f.get(u)
	}
}
func (f *fakeHTTP) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	if !Allowed(u) {
		f.t.Fatal("unreviewed POST")
	}
	if strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("root crossed POST")
	}
	if f.post == nil {
		f.t.Fatal("unexpected POST")
	}
	return f.post(u, h, b)
}
func js(s string) transport.Reply {
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json;charset=utf-8"}}, Body: []byte(s)}
}
func code(e error) string {
	if e == nil {
		return ""
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func TestGroupsReadProjectionAndPending(t *testing.T) {
	f := &fakeHTTP{t: t}
	f.get = func(u *url.URL) (transport.Reply, error) {
		switch u.Path {
		case "/api/account":
			return js(`{"errno":0,"total":1,"entities":[{"account":"synthetic_user","nationalId":"SHOULD_NOT_LEAK"}]}`), nil
		case "/api/jwbmessage/group":
			return js(`{"errno":0,"total":2,"entities":[{"groupId":"g1","groupName":"校园通知","groupDescription":"通知摘要","unreadNum":1,"rawBody":"PRIVATE_BODY"},{"groupId":"g2","groupName":"","unreadNum":0}]}`), nil
		case "/api/task/me/processes/todo":
			if f.bootstrapCalls == 0 {
				t.Fatal("task API called before portal context bootstrap")
			}
			return js(`{"errno":0,"total":1,"entities":[{"id":"step1","name":"填写","assignTime":1760000000,"process":{"id":"process1","name":"合成申请","status":"doing","owner":{"account":"PRIVATE_OWNER"}}}]}`), nil
		}
		t.Fatal("unexpected business read")
		return transport.Reply{}, nil
	}
	s := &Service{f, f}
	q := contracts.CampusQuery{AccountRef: "myportal:account:synthetic_user", ResourceRef: "myportal:messages:groups", View: "groups_v1", Limit: 1}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || p.Next == "" {
		t.Fatalf("groups: %v", e)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "SHOULD") {
		t.Fatal("raw fields leaked")
	}
	p2, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, p.Next)
	if e != nil || len(p2.Rows) != 1 || p2.Next != "" {
		t.Fatalf("continuation: %v", e)
	}
	q.ResourceRef = "myportal:tasks:pending"
	q.View = "tasks_v1"
	p, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || p.Rows[0].Collaboration.ProcessRef != "myportal:process:process1" {
		t.Fatalf("pending: %v", e)
	}
}
func TestRejectsMessageReadSideEffectsAndAccountChange(t *testing.T) {
	for _, p := range []string{"/api/jwbmessage/messagelist?groupId=g1", "/api/jwbmessage/readall", "/api/jwbmessage/group?key=&key=&page=1&pageSize=100&read=true"} {
		u, _ := url.Parse(root + p)
		if Allowed(u) {
			t.Fatal("side effect or duplicate query allowed")
		}
	}
	f := &fakeHTTP{t: t}
	f.get = func(u *url.URL) (transport.Reply, error) {
		if u.Path != "/api/account" {
			t.Fatal("business read before matching identity")
		}
		return js(`{"errno":0,"total":1,"entities":[{"account":"other_user"}]}`), nil
	}
	s := &Service{f, f}
	q := contracts.CampusQuery{AccountRef: "myportal:account:synthetic_user", ResourceRef: "myportal:messages:unread", View: "unread_v1", Limit: 1}
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "PERMISSION_DENIED") {
		t.Fatal(e)
	}
}
