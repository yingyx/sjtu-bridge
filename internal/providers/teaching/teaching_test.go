package teaching

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

const fixture = `{"xsxx":{"XH":"SYNTHETIC01","XNM":"2026","XQM":"3","XM":"DO_NOT_RETURN"},"kbList":[{"kcmc":"合成课程","kch":"TEST1","cdmc":"合成教室","xqmc":"合成校区","xqj":"1","jcs":"1-2","zcd":"1-16周(单)"}],"sjkList":[{"private":"DO_NOT_RETURN"}]}`

type fake struct {
	t     *testing.T
	body  string
	posts int
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("bad login destination or root cookie leak")
	}
	return transport.Reply{Status: 200, Header: http.Header{}}, nil
}
func (f *fake) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	f.posts++
	q, e := url.ParseQuery(string(b))
	if e != nil || u.String() != endpoint || len(q) != 4 || q.Get("xnm") != "2026" || q.Get("xqm") != "3" || q.Get("kzlx") != "ck" || q.Get("xsdm") != "" {
		f.t.Fatal("unexpected teaching body or destination")
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(f.body)}, nil
}
func TestTimetableIdentityAndProjection(t *testing.T) {
	f := &fake{t: t, body: fixture}
	s := Service{Session: campussession.Service{Host: Host, Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: f, Auth: f}}
	q := contracts.CampusQuery{AccountRef: "teaching:student:SYNTHETIC01", ResourceRef: "teaching:term:2026:1", View: "schedule_v1", Limit: 10}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(page.Rows) != 1 || page.Rows[0].Weeks != "1-16周(单)" {
		t.Fatal(page, e)
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), "DO_NOT_RETURN") {
		t.Fatal("unexpected fields leaked")
	}
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), "teaching:student:OTHER", q, ""); e == nil {
		t.Fatal("account mismatch accepted")
	}
}
func TestTimetableStructureAndScopeFailures(t *testing.T) {
	for _, body := range []string{`{"kbList":[]}`, strings.Replace(fixture, `"XH":"SYNTHETIC01"`, `"XH":""`, 1), strings.Replace(fixture, `"XQM":"3"`, `"XQM":"12"`, 1), strings.Replace(fixture, `"xqj":"1"`, `"xqj":"9"`, 1), strings.Replace(fixture, `"kbList":`, `"other":`, 1), `<html>login</html>`} {
		if _, _, e := parse([]byte(body), "teaching:term:2026:1", "2026", "3"); e == nil {
			t.Fatal("invalid response accepted", body)
		}
	}
	rows, account, e := parse([]byte(`{"xsxx":{"XH":"SYNTHETIC01","XNM":"2026","XQM":"3"},"kbList":[]}`), "teaching:term:2026:1", "2026", "3")
	if e != nil || rows == nil || account == "" {
		t.Fatal("valid empty timetable rejected", e)
	}
	for _, raw := range []string{endpoint + "&xh=OTHER", "https://i.sjtu.edu.cn/cjcx/cjcx_cxXsKccjList.html?gnmkdm=N305007"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed endpoint allowed")
		}
	}
}
