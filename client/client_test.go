package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
)

func TestConnectionChecksAndUnavailable(t *testing.T) {
	d := t.TempDir()
	if e := local.Protect(d); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(d, "connection.json")
	b, _ := json.Marshal(contracts.Connection{Version: 1, Endpoint: "http://arbitrary.invalid", Token: local.Random()})
	if e := local.WriteNew(path, b); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(path); e == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
	if _, e := Open("relative.json"); e == nil {
		t.Fatal("relative file accepted")
	}
	endpoint := filepath.Join(d, "absent.sock")
	if runtime.GOOS == "windows" {
		endpoint = `\\.\pipe\sjtu-bridge-absent-` + local.Random()
	}
	c := connect(endpoint, local.Random())
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, e := c.Assignments(ctx, contracts.AssignmentQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:1", View: "summary_v1", Limit: 1})
	var v *contracts.Error
	if !errors.As(e, &v) || (v.Code != "BROKER_UNAVAILABLE" && v.Code != "CANCELED" && v.Code != "TIMEOUT") {
		t.Fatal("unavailable Broker was not explicit", e)
	}
	for _, run := range []func() error{
		func() error { _, e := c.Capabilities(ctx); return e },
		func() error {
			_, e := c.Invoke(ctx, contracts.Invocation{Capability: "public.services.list"})
			return e
		},
	} {
		if e := run(); e == nil {
			t.Fatal("unavailable connection fell back to public access")
		}
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInvokePreservesHeterogeneousResultsAndAssignmentsDecode(t *testing.T) {
	public := `{"schema_version":"1","data":{"items":[{"provider":"publicinfo","source_id":"canvas","title":"Canvas","url":"https://oc.sjtu.edu.cn/","published_date":null}]},"meta":{"timezone":"Asia/Shanghai"}}`
	assignment := `{"schema_version":"1","data":{"items":[{"course_ref":"canvas:course:1","assignment_ref":"canvas:assignment:1","due_at":null}]},"meta":{"provider":"canvas","coverage":"complete"}}`
	c := &Client{token: strings.Repeat("a", 64), http: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		var inv contracts.Invocation
		if e := json.NewDecoder(r.Body).Decode(&inv); e != nil {
			t.Fatal(e)
		}
		body := public
		if inv.Capability == contracts.AssignmentsCapability {
			body = assignment
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	defer c.Close()
	raw, e := c.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list"})
	if e != nil || string(raw) != public {
		t.Fatal("public response was narrowed to assignment schema", e, string(raw))
	}
	v, e := c.Assignments(context.Background(), contracts.AssignmentQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:1", View: "deadlines_v1", Limit: 1})
	if e != nil || len(v.Data.Items) != 1 || v.Data.Items[0].AssignmentRef != "canvas:assignment:1" || v.Data.Items[0].Title != nil {
		t.Fatal("typed assignment wrapper changed projection", e)
	}
}
