package canvas

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

type contentGetter struct {
	get     func(*url.URL, http.Header) (transport.Reply, error)
	bounded func(*url.URL, http.Header, int) (transport.Reply, error)
}

func (f contentGetter) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f.get(u, h)
}
func (f contentGetter) GetBounded(_ context.Context, u *url.URL, h http.Header, n int) (transport.Reply, error) {
	return f.bounded(u, h, n)
}

const fileJSON = `{"id":9,"display_name":"合成课件.txt","content-type":"text/plain","size":4,"updated_at":"2030-01-01T00:00:00Z","url":"https://oc.sjtu.edu.cn/files/9/download?verifier=SYNTHETIC_TICKET"}`

func TestCanvasFileContentCourseAttachmentAndBoundedRange(t *testing.T) {
	for _, assignment := range []string{"", "5"} {
		t.Run("assignment="+assignment, func(t *testing.T) {
			q := contracts.CanvasContentQuery{AccountRef: "canvas:user:123", CourseRef: "canvas:course:7", View: "content_v1", Limit: 1, ItemID: "9", AssignmentID: assignment}
			scope := contracts.ReadScope{Capability: contracts.CanvasFileContent, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: 1}
			reads := 0
			g := contentGetter{get: func(u *url.URL, h http.Header) (transport.Reply, error) {
				if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" {
					t.Fatal("unreviewed Canvas request")
				}
				switch u.Path {
				case "/api/v1/users/self/profile":
					return reply(`{"id":123}`), nil
				case "/api/v1/courses/7":
					return reply(`{"id":7,"enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}`), nil
				case "/api/v1/courses/7/assignments/5/submissions/self":
					return reply(`{"assignment_id":5,"user_id":123,"attachments":[{"id":9}]}`), nil
				case "/api/v1/courses/7/files/9", "/api/v1/files/9":
					return reply(fileJSON), nil
				default:
					t.Fatal("unexpected request", u.Path)
					return transport.Reply{}, nil
				}
			}, bounded: func(u *url.URL, h http.Header, n int) (transport.Reply, error) {
				reads++
				if !Allowed(u) || h.Get("Range") != "bytes=1-2" || n != 2 {
					t.Fatal("range is not a hard byte budget")
				}
				return transport.Reply{Status: 206, Header: http.Header{"Content-Range": {"bytes 1-2/4"}, "Content-Type": {"text/plain"}}, Body: []byte("at")}, nil
			}}
			s := Service{Client: g}
			object := CanvasContentObject(q)
			meta, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object)
			if e != nil || meta.Name != "合成课件.txt" || meta.Size != 4 || strings.Contains(meta.Version, "SYNTHETIC") {
				t.Fatal(meta, e)
			}
			chunk, e := s.ReadContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object, meta.Version, 1, 2)
			if e != nil || string(chunk.Data) != "at" || reads != 1 {
				t.Fatal(chunk, e)
			}
			if _, e = s.ReadContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object, "outdated", 1, 2); e == nil || reads != 1 {
				t.Fatal("stale version reached download")
			}
		})
	}
}

func TestCanvasContentRejectsCrossSubmissionAndExternalLocations(t *testing.T) {
	q := contracts.CanvasContentQuery{AccountRef: "canvas:user:123", CourseRef: "canvas:course:7", View: "content_v1", Limit: 1, ItemID: "9", AssignmentID: "5"}
	scope := contracts.ReadScope{Capability: contracts.CanvasFileContent, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: 1}
	g := contentGetter{get: func(u *url.URL, h http.Header) (transport.Reply, error) {
		switch u.Path {
		case "/api/v1/users/self/profile":
			return reply(`{"id":123}`), nil
		case "/api/v1/courses/7":
			return reply(`{"id":7,"enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}`), nil
		case "/api/v1/courses/7/assignments/5/submissions/self":
			return reply(`{"assignment_id":5,"user_id":999,"attachments":[{"id":9}]}`), nil
		default:
			t.Fatal("unverified attachment metadata reached upstream")
			return transport.Reply{}, nil
		}
	}}
	if _, e := (Service{Client: g}).DescribeContent(context.Background(), []byte("SYNTHETIC"), scope, CanvasContentObject(q)); e == nil {
		t.Fatal("foreign submission accepted")
	}
	for _, raw := range []string{"https://evil.test/files/9/download", "https://oc.sjtu.edu.cn/files/9/download?redirect=https://evil.test", "https://oc.sjtu.edu.cn/api/v1/files/9?include=all", "https://oc.sjtu.edu.cn/files/9/download?verifier=a&verifier=b"} {
		u, _ := url.Parse(raw)
		if contentAllowed(u) {
			t.Fatal("unreviewed content URL", raw)
		}
	}
}

func syntheticStorageURL() *url.URL {
	u := &url.URL{Scheme: "https", Host: contentStorageHost, Path: "/canvas/files/9/synthetic.txt"}
	u.RawQuery = url.Values{"X-Amz-Algorithm": {"AWS4-HMAC-SHA256"}, "X-Amz-Credential": {"SYNTHETIC_KEY/20300101/us-east-1/s3/aws4_request"}, "X-Amz-Date": {"20300101T000000Z"}, "X-Amz-Expires": {"3600"}, "X-Amz-SignedHeaders": {"host"}, "X-Amz-Signature": {strings.Repeat("a", 64)}, "response-content-disposition": {`attachment; filename="synthetic.txt"`}}.Encode()
	return u
}

func TestCanvasSignedSchoolStorageRange(t *testing.T) {
	q := contracts.CanvasContentQuery{AccountRef: "canvas:user:123", CourseRef: "canvas:course:7", View: "content_v1", Limit: 1, ItemID: "9"}
	scope := contracts.ReadScope{Capability: contracts.CanvasFileContent, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: 1}
	storageURL := syntheticStorageURL()
	storageReads := 0
	canvas := contentGetter{get: func(u *url.URL, h http.Header) (transport.Reply, error) {
		if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" {
			t.Fatal("unreviewed Canvas metadata request")
		}
		switch u.Path {
		case "/api/v1/users/self/profile":
			return reply(`{"id":123}`), nil
		case "/api/v1/courses/7":
			return reply(`{"id":7,"enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}`), nil
		case "/api/v1/courses/7/files/9":
			return reply(fileJSON), nil
		default:
			t.Fatal("unexpected metadata request")
			return transport.Reply{}, nil
		}
	}, bounded: func(u *url.URL, h http.Header, n int) (transport.Reply, error) {
		if !contentAllowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" || h.Get("Range") != "bytes=1-2" || n != 2 {
			t.Fatal("unreviewed initial download request")
		}
		return transport.Reply{Status: http.StatusFound, Header: http.Header{"Location": {storageURL.String()}}}, nil
	}}
	storageStatus := http.StatusPartialContent
	storage := contentGetter{bounded: func(u *url.URL, h http.Header, n int) (transport.Reply, error) {
		storageReads++
		if u.String() != storageURL.String() || h.Get("Range") != "bytes=1-2" || n != 2 || h.Get("Authorization") != "" || h.Get("Cookie") != "" || h.Get("Jwt") != "" {
			t.Fatal("storage credentials, destination or range escaped review")
		}
		return transport.Reply{Status: storageStatus, Header: http.Header{"Content-Range": {"bytes 1-2/4"}, "Content-Type": {"text/plain"}, "Location": {"https://evil.test/next"}}, Body: []byte("at")}, nil
	}}
	s := Service{Client: canvas, FileClient: storage}
	object := CanvasContentObject(q)
	meta, err := s.DescribeContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := s.ReadContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object, meta.Version, 1, 2)
	if err != nil || string(chunk.Data) != "at" || storageReads != 1 {
		t.Fatal("signed school storage transfer failed", err)
	}
	storageStatus = http.StatusFound
	if _, err = s.ReadContent(context.Background(), []byte("SYNTHETIC_TOKEN"), scope, object, meta.Version, 1, 2); err == nil || storageReads != 2 {
		t.Fatal("storage second redirect accepted")
	}
}

func TestCanvasStorageURLRejectsUnreviewedShape(t *testing.T) {
	if !contentStorageAllowed(syntheticStorageURL()) {
		t.Fatal("synthetic reviewed URL rejected")
	}
	for _, mutate := range []func(*url.URL){
		func(u *url.URL) { u.Host = "evil.test" },
		func(u *url.URL) { u.Scheme = "http" },
		func(u *url.URL) { u.Path = "/canvas/../secret" },
		func(u *url.URL) { u.Path = "/canvas//secret" },
		func(u *url.URL) { u.RawPath = "/canvas/%66iles/9/synthetic.txt" },
		func(u *url.URL) { u.RawQuery += "&redirect=elsewhere" },
		func(u *url.URL) { u.RawQuery += "&X-Amz-Signature=" + strings.Repeat("a", 64) },
		func(u *url.URL) { q := u.Query(); q.Del("X-Amz-Signature"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Set("X-Amz-Credential", "arbitrary"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Set("X-Amz-Expires", "604801"); u.RawQuery = q.Encode() },
		func(u *url.URL) {
			q := u.Query()
			q.Set("X-Amz-SignedHeaders", "host;authorization")
			u.RawQuery = q.Encode()
		},
	} {
		u := syntheticStorageURL()
		mutate(u)
		if contentStorageAllowed(u) {
			t.Fatal("unreviewed signed storage shape accepted")
		}
	}
}

func TestCanvasFeedbackIndependentViewAndNoReadStatusMutation(t *testing.T) {
	q := canvasTestQuery("canvas.submissions.feedback")
	u := learningTarget("canvas.submissions.feedback", q)
	if u.Query().Get("include[]") != "submission_comments" || strings.Contains(u.RawQuery, "read_status") || !Allowed(u) {
		t.Fatal("feedback endpoint has unreviewed effects")
	}
	raw := []byte(`{"assignment_id":9,"user_id":123,"workflow_state":"graded","attempt":1,"late":false,"missing":false,"grade":"A","score":95,"body":"<p>本人提交</p>","submission_type":"online_upload","attachments":[{"id":12,"display_name":"answer.txt","size":4,"content-type":"text/plain","url":"https://secret.test/SYNTHETIC_TICKET"}],"submission_comments":[{"id":3,"author_name":"合成教师","comment":"<p>反馈</p>","created_at":"2030-01-01T00:00:00Z","author":{"private":"SYNTHETIC_PRIVATE"}}]}`)
	row, _, e := learningRecord(raw, "canvas.submissions.feedback", q, "123")
	if e != nil {
		t.Fatal(e)
	}
	if e = submissionFeedback(raw, &row); e != nil {
		t.Fatal(e)
	}
	if row.Score == nil || *row.Score != 95 || len(row.Attachments) != 1 || len(row.Comments) != 1 || row.Body == nil || *row.Body != "本人提交" {
		t.Fatal(row)
	}
	b, _ := json.Marshal(row)
	if strings.Contains(string(b), "SYNTHETIC_TICKET") || strings.Contains(string(b), "SYNTHETIC_PRIVATE") {
		t.Fatal("feedback leaked nonview data")
	}
	status, _, e := learningRecord(raw, "canvas.submissions.get", canvasTestQuery("canvas.submissions.get"), "123")
	if e != nil || status.Score != nil || status.Body != nil || len(status.Attachments) > 0 {
		t.Fatal("existing status view expanded", status, e)
	}
}

func TestCanvasRichContentDiscoversFileReferencesWithoutTickets(t *testing.T) {
	_, links, _ := canvasBody(`<p>课件</p><a href="/files/9/download?verifier=SYNTHETIC_TICKET">附件</a><a href="/api/v1/files/12">资料</a><a href="https://evil.test/files/13">其他</a>`, "canvas:course:7")
	if len(links) != 2 || links[0].URL != "https://oc.sjtu.edu.cn/courses/7/files/9" || links[1].URL != "https://oc.sjtu.edu.cn/courses/7/files/12" {
		t.Fatal(links)
	}
}
