package publicinfo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// Synthetic HTML: no real notices, identities, credentials or upstream bodies.
const page = `<html><div class="Newslist"><ul>
<li><div class="sj"><h2>26</h2><p>2026.09</p></div><div class="wz"><a href="../info/1252/100.htm"><h2>示例校历 &amp; 测试</h2></a><p>Do not return this body</p></div></li>
<li><div class="sj"><h2>25</h2><p>2026.09</p></div><div class="wz"><a href="../content.jsp?urltype=news.NewsContentUrl&amp;wbtreeid=1252&amp;wbnewsid=101"><h2>第二条</h2></a></div></li>
</ul><span class="p_pages"><span class="p_next"><a href="lnxl/1.htm">下页</a></span></span></div></html>`

type fakeFetcher struct {
	body  string
	calls []string
	err   error
}

func (f *fakeFetcher) Get(_ context.Context, u string) ([]byte, error) {
	f.calls = append(f.calls, u)
	return []byte(f.body), f.err
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("error=%v want %s", err, want)
	}
}

func TestParsingAndPagination(t *testing.T) {
	f := &fakeFetcher{body: page}
	p := New(f)
	first, err := p.List(context.Background(), "calendars", contracts.ListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.Data.Items[0].Title != "示例校历 & 测试" || *first.Data.Items[0].PublishedDate != "2026-09-26" || first.Meta.NextCursor == nil || !first.Meta.HasMore || first.Meta.Sources[0].Coverage != "incomplete" {
		t.Fatalf("bad result: %+v", first)
	}
	b, _ := json.Marshal(first)
	if strings.Contains(string(b), "Do not return") {
		t.Fatal("body leaked")
	}
	second, err := p.List(context.Background(), "calendars", contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if second.Data.Items[0].URL != "https://www.jwc.sjtu.edu.cn/info/1252/101.htm" {
		t.Fatal(second)
	}
	_, err = p.List(context.Background(), "calendars", contracts.ListInput{Limit: 1, Cursor: *second.Meta.NextCursor})
	if err == nil {
		t.Fatal("self-loop on continuation page must fail")
	}
	if f.calls[2] != "https://www.jwc.sjtu.edu.cn/jxxl/lnxl/1.htm" {
		t.Fatal(f.calls)
	}
	f.body = strings.ReplaceAll(page, "第二条", "changed")
	_, err = p.List(context.Background(), "calendars", contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	code(t, err, "UPSTREAM_CHANGED")
}

func TestCursorValidationBeforeNetwork(t *testing.T) {
	for _, c := range []cursor{
		{Version: 1, Kind: "notices", Path: "/xwtg/tztg.htm", Limit: 10},
		{Version: 1, Kind: "calendars", Path: "/private", Limit: 10},
		{Version: 1, Kind: "calendars", Path: "/jxxl/lnxl.htm", Limit: 1},
		{Version: 1, Kind: "calendars", Path: "/jxxl/lnxl.htm", Limit: 10, Offset: -1},
	} {
		c.Expires = time.Now().Add(time.Minute).Unix()
		f := &fakeFetcher{body: page}
		_, err := New(f).List(context.Background(), "calendars", contracts.ListInput{Limit: 10, Cursor: encode(c)})
		code(t, err, "INVALID_ARGUMENT")
		if len(f.calls) != 0 {
			t.Fatal("denied cursor reached network")
		}
	}
	for _, raw := range []string{"bad", base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"token":"fake-secret"}`)), encode(cursor{Version: 1, Kind: "calendars", Path: "/jxxl/lnxl.htm", Limit: 10, Expires: 1})} {
		f := &fakeFetcher{body: page}
		_, err := New(f).List(context.Background(), "calendars", contracts.ListInput{Limit: 10, Cursor: raw})
		code(t, err, "INVALID_ARGUMENT")
		if len(f.calls) != 0 {
			t.Fatal("network called")
		}
	}
}

func TestFailClosedParser(t *testing.T) {
	for name, body := range map[string]string{
		"login": "<html>Login</html>", "empty": `<div class="Newslist"><ul></ul></div>`,
		"malicious article":  strings.Replace(page, "../info/1252/100.htm", "https://evil.example/steal?token=x", 1),
		"malicious next":     strings.Replace(page, "lnxl/1.htm", "https://evil.example/", 1),
		"unknown pagination": strings.Replace(page, "p_next", "new_next", 1),
		"bad date":           strings.Replace(page, "2026.09", "2026.99", 1),
		"encoding":           string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parse([]byte(body), "https://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm", "calendars")
			code(t, err, "UPSTREAM_CHANGED")
		})
	}
	last := strings.Replace(page, `<span class="p_next"><a href="lnxl/1.htm">下页</a></span>`, `<span class="p_next_d">下页</span>`, 1)
	items, next, err := parse([]byte(last), "https://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm", "calendars")
	if err != nil || next != "" || len(items) != 2 {
		t.Fatal(items, next, err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(page)
	f.Add("<html>login</html>")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 16384 {
			t.Skip()
		}
		_, _, _ = parse([]byte(s), "https://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm", "calendars")
	})
}
