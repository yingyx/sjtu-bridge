package campusai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type fakeHTTP struct {
	body   string
	status int
	calls  int
}

func (f *fakeHTTP) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.calls++
	if !Allowed(u) || len(h) != 1 || h.Get("Authorization") != "" {
		panic("unreviewed AI request")
	}
	return transport.Reply{Status: f.status, Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: []byte(f.body)}, nil
}

const usageFixture = `{"code":0,"data":{"model_count":2,"model":[{"cname":"Model A","ename":"a","mount":100,"percent":0.25,"reason":"EXCLUDED"},{"cname":"Model B","ename":"b","mount":300,"percent":0.75}],"user":[{"cname":"PRIVATE_NAME","college":"PRIVATE_COLLEGE"}],"org":[{"reason":"PRIVATE_PURPOSE"}]}}`

func TestUsageFixedProjectionAndContinuation(t *testing.T) {
	f := &fakeHTTP{body: usageFixture, status: 200}
	p := &Provider{Client: f}
	q := contracts.ListInput{Limit: 1, Kind: "week"}
	r, e := p.List(context.Background(), contracts.AIUsageCapability, q)
	if e != nil || len(r.Data.Items) != 1 || !r.Meta.HasMore || !r.Meta.Stale || r.Meta.NextCursor == nil {
		t.Fatal(r, e)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "EXCLUDED") {
		t.Fatal("private ranking leaked")
	}
	q.Cursor = *r.Meta.NextCursor
	r, e = p.List(context.Background(), contracts.AIUsageCapability, q)
	if e != nil || len(r.Data.Items) != 1 || r.Data.Items[0].SourceID != "b" || r.Meta.HasMore {
		t.Fatal(r, e)
	}
	q.Kind = "month"
	if _, e = p.List(context.Background(), contracts.AIUsageCapability, q); e == nil {
		t.Fatal("cursor crossed windows")
	}
	q.Kind = "week"
	f.body = strings.ReplaceAll(usageFixture, `"mount":300`, `"mount":301`)
	if _, e = p.List(context.Background(), contracts.AIUsageCapability, q); e == nil {
		t.Fatal("changed snapshot continued")
	}
}

func TestStatusSourceTimeAndZeroAvailability(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.In(time.FixedZone("Shanghai", 8*3600)).Format("2006-01-02 15:04")
	f := &fakeHTTP{status: 200, body: `{"code":0,"data":{"timestamp":"ignored","model_service":{"timestamp":"` + stamp + `","models":[{"name":"Model","status":"offline","availability_by_window":{"1d":0,"30d":1,"365d":2}}]},"top":{"user":["PRIVATE"]}}}`}
	p := &Provider{Client: f}
	r, e := p.List(context.Background(), contracts.AIStatusCapability, contracts.ListInput{Limit: 10})
	if e != nil || r.Meta.Stale || len(r.Data.Items) != 1 {
		t.Fatal(r, e)
	}
	if r.Data.Items[0].Fields[2].Value != "0" {
		t.Fatal("zero availability lost")
	}
	validBody := f.body
	f.body = strings.Replace(validBody, `"1d":0`, `"1d":null`, 1)
	if _, e = p.List(context.Background(), contracts.AIStatusCapability, contracts.ListInput{Limit: 10}); e == nil {
		t.Fatal("null availability became zero")
	}
	f.body = validBody
	f.body = strings.Replace(f.body, stamp, "2030-01-01 00:00", 1)
	if _, e = p.List(context.Background(), contracts.AIStatusCapability, contracts.ListInput{Limit: 10}); e == nil {
		t.Fatal("future timestamp accepted")
	}
}

func TestInvalidAndMissingStatisticsFail(t *testing.T) {
	for _, body := range []string{`null`, `<html>login</html>`, `{"code":0,"data":null}`, strings.Replace(usageFixture, `"model_count":2`, `"model_count":3`, 1), strings.Replace(usageFixture, `"mount":100`, `"mount":null`, 1), strings.Replace(usageFixture, `"percent":0.25`, `"percent":2`, 1), strings.Replace(usageFixture, `"mount":100`, `"mount":1.5`, 1), `{"code":0,"data":{"model_count":0}}`} {
		p := &Provider{Client: &fakeHTTP{status: 200, body: body}}
		if _, e := p.List(context.Background(), contracts.AIUsageCapability, contracts.ListInput{Limit: 10}); e == nil {
			t.Fatal("invalid statistics accepted")
		}
	}
	p := &Provider{Client: &fakeHTTP{status: 401, body: usageFixture}}
	if _, e := p.List(context.Background(), contracts.AIUsageCapability, contracts.ListInput{Limit: 10}); e == nil {
		t.Fatal("unauthorized response accepted")
	}
	f := &fakeHTTP{status: 200, body: usageFixture}
	p.Client = f
	if _, e := p.List(context.Background(), contracts.AIUsageCapability, contracts.ListInput{Limit: 10, Query: "x"}); e == nil || f.calls != 0 {
		t.Fatal("unsupported query fetched")
	}
	for _, raw := range []string{"https://aic.sjtu.edu.cn/top/api/stats/daily?type=day", "https://aic.sjtu.edu.cn/top/api/user/stats", "https://aic.sjtu.edu.cn/top/api/stats/realtime?key=x", "http://aic.sjtu.edu.cn/top/api/stats/realtime"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed endpoint", raw)
		}
	}
}
