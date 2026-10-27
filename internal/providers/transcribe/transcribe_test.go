package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type fakeHTTP struct {
	t        *testing.T
	get      func(*url.URL) (transport.Reply, error)
	post     func(*url.URL, http.Header, []byte) (transport.Reply, error)
	apiCalls int
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
	case u.Path == "/ai/ui/transcribe":
		if u.Query().Get("code") == "" {
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id="+client+"&redirect_uri="+url.QueryEscape(callback)+"&response_type=code&scope=basic+profile+privacy&state=bound")
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

func taskFake(t *testing.T) *fakeHTTP {
	f := &fakeHTTP{t: t}
	f.get = func(u *url.URL) (transport.Reply, error) {
		switch u.Path {
		case "/ai/api/user/info":
			return js(`{"errno":0,"data":{"account":"synthetic_user","phone":"PRIVATE_PHONE"}}`), nil
		case "/ai/file/audioVideo/list":
			return js(`{"errno":0,"data":[{"id":"task1","dealMark":"2","delMark":"N","fileName":"合成音频.mp3","createdTime":"2026-10-26 09:00:00","originalText":"第一行\n第二行","destText":"优化内容","mediaUrl":"PRIVATE_URL"},{"id":"deleted","delMark":"Y"}]}`), nil
		}
		t.Fatal("unexpected read")
		return transport.Reply{}, nil
	}
	return f
}
func TestMetadataAndExplicitResultScope(t *testing.T) {
	f := taskFake(t)
	s := &Service{f, f}
	q := contracts.CampusQuery{AccountRef: "transcribe:account:synthetic_user", ResourceRef: "transcribe:self", View: "transcriptions_v1", Limit: 2}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || p.Rows[0].Collaboration.OriginalText != "" || p.Rows[0].Collaboration.OptimizedText != "" {
		t.Fatal("metadata leaked text", e)
	}
	q.ResourceRef = "transcribe:task:task1"
	q.View = "transcription_v1"
	q.Limit = 1
	p, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || p.Rows[0].Collaboration.OriginalText != "第一行\n第二行" {
		t.Fatal("result missing", e)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("raw fields leaked")
	}
	q.ResourceRef = "transcribe:task:missing"
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "RESOURCE_UNAVAILABLE") {
		t.Fatal("missing converted to empty success", e)
	}
}
func TestTextBoundsAndWriteFailures(t *testing.T) {
	full := strings.Repeat("交", MaxTextBytes)
	clipped, truncated := clip(full)
	if !truncated || len(clipped) > MaxTextBytes || !text(clipped) {
		t.Fatal("UTF8 byte limit failed")
	}
	for _, raw := range []string{root + "/ai/file/audioVideo/delete?id=task1", root + "/ai/file/audioVideo/list?account=other", root + "/ai/v2/chat-messages?url=https://other.invalid"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("write/unbounded endpoint allowed")
		}
	}
	f := taskFake(t)
	s := &Service{f, f}
	_, e := s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "transcribe:account:synthetic_user", contracts.TranscriptionMutation{Action: "cancel", TaskRef: "transcribe:task:task1"})
	if !strings.Contains(code(e), "INVALID_ARGUMENT") {
		t.Fatal("unsupported cancellation accepted", e)
	}
	_, e = s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "transcribe:account:synthetic_user", contracts.TranscriptionMutation{Action: "optimize", TaskRef: "transcribe:task:task1"})
	if !strings.Contains(code(e), "RESOURCE_UNAVAILABLE") {
		t.Fatal("duplicate optimization accepted", e)
	}
	if validFilename("../a.mp3", "audio/mpeg") || validFilename("a.exe", "audio/mpeg") || !validFilename("a.mp3", "audio/mpeg") {
		t.Fatal("upload filename policy broken")
	}
	r := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: []byte("data: {\"event\":\"message\",\"answer\":\"合成\"}\n\ndata: {\"event\":\"error\"}\n\n")}
	if validStream(r) {
		t.Fatal("partial error stream accepted")
	}
}

func (f *fakeHTTP) PostUpload(ctx context.Context, u *url.URL, h http.Header, b []byte, maxBytes int) (transport.Reply, error) {
	if len(b) > maxBytes || maxBytes > MaxUploadBytes+4096 {
		f.t.Fatal("unbounded upload")
	}
	return f.Post(ctx, u, h, b)
}
func TestOptimizationWriteVerifiedAndNeverRetried(t *testing.T) {
	f := taskFake(t)
	optimized := false
	writes := 0
	fail := false
	original := f.get
	f.get = func(u *url.URL) (transport.Reply, error) {
		if u.Path == "/ai/file/audioVideo/list" {
			dest := ""
			if optimized {
				dest = "合成优化"
			}
			return js(`{"errno":0,"data":[{"id":"task1","dealMark":"2","delMark":"N","fileName":"合成.mp3","createdTime":"2026-10-26 09:00:00","originalText":"合成转写","destText":"` + dest + `"}]}`), nil
		}
		return original(u)
	}
	f.post = func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		writes++
		if u.Path != "/ai/v2/chat-messages" || string(b) != `{"id":"task1","response_mode":"streaming"}` {
			t.Fatal("unreviewed model request")
		}
		if fail {
			return transport.Reply{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "synthetic timeout")
		}
		optimized = true
		return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: []byte("data: {\"event\":\"message\",\"answer\":\"合成优化\"}\n\ndata: {\"event\":\"message_end\"}\n\n")}, nil
	}
	s := &Service{f, f}
	in := contracts.TranscriptionMutation{Action: "optimize", TaskRef: "transcribe:task:task1"}
	result, e := s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "transcribe:account:synthetic_user", in)
	if e != nil || result.TaskRef != in.TaskRef || writes != 1 {
		t.Fatal(result, e)
	}
	optimized = false
	fail = true
	_, e = s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "transcribe:account:synthetic_user", in)
	if !strings.Contains(code(e), "OUTCOME_UNKNOWN") || writes != 2 {
		t.Fatal("uncertain model write retried", e, writes)
	}
}
func TestUploadHasOnePartAndVerifiesNewOwnTask(t *testing.T) {
	f := taskFake(t)
	uploaded := false
	original := f.get
	f.get = func(u *url.URL) (transport.Reply, error) {
		if uploaded && u.Path == "/ai/file/audioVideo/list" {
			return js(`{"errno":0,"data":[{"id":"newtask","dealMark":"0","delMark":"N","fileName":"合成.mp3","createdTime":"2026-10-26 09:00:00"}]}`), nil
		}
		return original(u)
	}
	f.post = func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		media, params, err := mime.ParseMediaType(h.Get("Content-Type"))
		if u.Path != "/ai/file/audioVideo/upload" || err != nil || media != "multipart/form-data" {
			t.Fatal("invalid multipart")
		}
		reader := multipart.NewReader(bytes.NewReader(b), params["boundary"])
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "file" || part.FileName() != "合成.mp3" || part.Header.Get("Content-Type") != "audio/mpeg" {
			t.Fatal("invalid upload part", err)
		}
		data, err := io.ReadAll(part)
		if err != nil || string(data) != "SYNTHETIC_AUDIO_BYTES" {
			t.Fatal("upload content changed", err)
		}
		if _, err = reader.NextPart(); err != io.EOF {
			t.Fatal("unexpected additional upload part", err)
		}
		uploaded = true
		return js(`{"errno":0,"data":"newtask.mp3"}`), nil
	}
	s := &Service{f, f}
	r, e := s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "transcribe:account:synthetic_user", contracts.TranscriptionMutation{Action: "upload", Filename: "合成.mp3", MIME: "audio/mpeg", Data: []byte("SYNTHETIC_AUDIO_BYTES")})
	if e != nil || r.TaskRef != "transcribe:task:newtask" || r.Status != "queued" {
		t.Fatal(r, e)
	}
}
