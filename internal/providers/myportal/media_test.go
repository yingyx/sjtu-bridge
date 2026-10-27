package myportal

import (
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"net/http"
	"net/url"
	"testing"
)

func TestProductionPlainTextJSONKeepsStrictEnvelope(t *testing.T) {
	for _, media := range []string{"application/json;charset=UTF-8", "text/plain;charset=UTF-8"} {
		r := js(`{"errno":0,"total":1,"entities":[{"account":"synthetic_user"}]}`)
		r.Header.Set("Content-Type", media)
		if _, e := decode(r); e != nil {
			t.Fatal(media, e)
		}
		for _, body := range []string{`{"errno":0,"errno":1,"total":1}`, `<html>login</html>`, `{"errno":0,"total":null}`} {
			r.Body = []byte(body)
			if _, e := decode(r); e == nil {
				t.Fatal("invalid envelope accepted")
			}
		}
	}
	r := js(`{"errno":0,"total":0}`)
	r.Header.Set("Content-Type", "text/html")
	if _, e := decode(r); e == nil {
		t.Fatal("HTML MIME accepted")
	}
}

func TestTaskBootstrapRejectsInvalidPageBeforeBusinessRead(t *testing.T) {
	for _, r := range []transport.Reply{
		{Status: 302, Header: http.Header{"Location": {callback}}},
		js(`{"errno":0,"entities":[]}`),
		{Status: 200, Header: http.Header{"Content-Type": {"text/html;charset=utf-8"}}, Body: []byte{0xff}},
	} {
		f := &fakeHTTP{t: t, bootstrapReply: &r}
		f.get = func(u *url.URL) (transport.Reply, error) {
			if u.Path != "/api/account" {
				t.Fatal("task business read after invalid bootstrap")
			}
			return js(`{"errno":0,"total":0,"entities":[{"account":"synthetic_user"}]}`), nil
		}
		s := &Service{f, f}
		q := contracts.CampusQuery{AccountRef: "myportal:account:synthetic_user", ResourceRef: "myportal:tasks:pending", View: "tasks_v1", Limit: 1}
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); e == nil {
			t.Fatal("invalid task bootstrap accepted")
		}
		if f.bootstrapCalls != 1 || f.apiCalls != 1 {
			t.Fatal("bootstrap did not stop the business read")
		}
	}
}
func TestAccountUsesSingleEntityRatherThanPaginationTotal(t *testing.T) {
	for _, entities := range []string{`[{"account":"synthetic_user"}]`, `[]`, `[{"account":"synthetic_user"},{"account":"other_user"}]`} {
		f := &fakeHTTP{t: t}
		f.get = func(*url.URL) (transport.Reply, error) {
			return js(`{"errno":0,"total":0,"entities":` + entities + `}`), nil
		}
		s := &Service{f, f}
		account, e := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE"))
		if entities == `[{"account":"synthetic_user"}]` {
			if e != nil || account != "myportal:account:synthetic_user" {
				t.Fatal(e)
			}
		} else if e == nil {
			t.Fatal("ambiguous or missing account accepted")
		}
	}
}

func TestNonzeroErrnoCannotBecomeSuccessfulEmptyOrPartialResult(t *testing.T) {
	for _, body := range []string{
		`{"errno":9999,"total":0,"entities":[],"error":"SYNTHETIC_ERROR"}`,
		`{"errno":9999,"total":1,"entities":[{"id":"step1","name":"synthetic","assignTime":1760000000,"process":{"id":"process1","name":"synthetic","status":"doing"}}],"error":"SYNTHETIC_ERROR"}`,
	} {
		if _, err := decode(js(body)); err == nil {
			t.Fatal("upstream error became successful data")
		}
	}
}
