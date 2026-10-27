package sjtumail

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type attachmentHTTP struct {
	*messageHTTP
	size             int64
	reply            transport.Reply
	fetches          int
	wantFrom         int64
	wantLen          int
	changeAfterFetch bool
	moveAfterFetch   bool
}

func (f *attachmentHTTP) Post(ctx context.Context, u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
	r, e := f.messageHTTP.Post(ctx, u, h, body)
	if f.size != 0 {
		r.Body = []byte(strings.ReplaceAll(string(r.Body), `"s":5`, fmt.Sprintf(`"s":%d`, f.size)))
	}
	if f.fetches != 0 && f.changeAfterFetch {
		r.Body = []byte(strings.ReplaceAll(string(r.Body), `"ms":19`, `"ms":20`))
	}
	if f.fetches != 0 && f.moveAfterFetch {
		r.Body = []byte(strings.ReplaceAll(string(r.Body), `"l":"2"`, `"l":"3"`))
	}
	return r, e
}

func TestAttachmentChangedDuringTransferDoesNotReturnBytes(t *testing.T) {
	for _, moved := range []bool{false, true} {
		reply := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: []byte("abcde")}
		s, f, scope, object := attachmentService(t, reply)
		info, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object)
		if e != nil {
			t.Fatal(e)
		}
		f.changeAfterFetch, f.moveAfterFetch = !moved, moved
		chunk, e := s.ReadContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object, info.Version, 1, 2)
		want := "CONTENT_CHANGED"
		if moved {
			want = "PERMISSION_DENIED"
		}
		if e == nil || e.(*contracts.Error).Code != want || len(chunk.Data) != 0 || f.fetches != 1 {
			t.Fatal("attachment changed during transfer was published")
		}
		for _, b := range reply.Body {
			if b != 0 {
				t.Fatal("unpublished attachment bytes were retained")
			}
		}
	}
}

func (f *attachmentHTTP) GetBounded(_ context.Context, u *url.URL, h http.Header, max int) (transport.Reply, error) {
	f.fetches++
	if !attachmentURL(u) || u.Query().Get("id") != "7" || u.Query().Get("part") != "2" || max != 5 || h.Get("Range") != fmt.Sprintf("bytes=%d-%d", f.wantFrom, f.wantFrom+int64(f.wantLen)-1) || h.Get("Accept-Encoding") != "identity" || !strings.Contains(h.Get("Cookie"), "ZM_AUTH_TOKEN=SYNTHETIC_SESSION") || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("attachment request crossed its exact object, session or byte budget")
	}
	return f.reply, nil
}

func attachmentService(t *testing.T, reply transport.Reply) (Service, *attachmentHTTP, contracts.ReadScope, string) {
	f := &attachmentHTTP{messageHTTP: &messageHTTP{fake: &fake{t: t}, folder: "2"}, reply: reply, wantFrom: 1, wantLen: 2}
	s := fakeService(f.fake)
	s.Session.Portal, s.Session.Auth = f, f
	scope := contracts.ReadScope{Capability: contracts.MailAttachmentCapability, AccountRef: "sjtumail:account:synthetic-account-01", ResourceRef: "mail:folder:2"}
	return s, f, scope, `{"id":"7","part":"2"}`
}

func TestSmallAttachmentFullResponseAndExactRange(t *testing.T) {
	for _, ranged := range []bool{false, true} {
		reply := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: []byte("abcde")}
		if ranged {
			reply.Status, reply.Body = 206, []byte("bc")
			reply.Header.Set("Content-Range", "bytes 1-2/5")
		}
		s, f, scope, object := attachmentService(t, reply)
		info, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object)
		if e != nil {
			t.Fatal(e)
		}
		chunk, e := s.ReadContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object, info.Version, 1, 2)
		if e != nil || string(chunk.Data) != "bc" || chunk.Offset != 1 || chunk.Size != 5 || f.fetches != 1 {
			t.Fatalf("missing bounded attachment slice: %v", e)
		}
		for _, b := range reply.Body {
			if b != 0 {
				t.Fatal("full upstream attachment buffer was retained")
			}
		}
	}
}

func TestAttachmentContentRejectsChangedOrUnboundedResults(t *testing.T) {
	for _, reply := range []transport.Reply{
		{Status: 200, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: []byte("abcd")},
		{Status: 200, Header: http.Header{"Content-Type": {"application/pdf"}}, Body: []byte("abcdef")},
		{Status: 206, Header: http.Header{"Content-Type": {"application/pdf"}, "Content-Range": {"bytes 0-1/5"}}, Body: []byte("ab")},
		{Status: 206, Header: http.Header{"Content-Type": {"application/pdf"}, "Content-Range": {"bytes 1-2/5"}}, Body: []byte("b")},
		{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("abcde")},
		{Status: 200, Header: http.Header{"Content-Type": {"application/pdf"}, "Content-Encoding": {"gzip"}}, Body: []byte("abcde")},
	} {
		s, _, scope, object := attachmentService(t, reply)
		info, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.ReadContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object, info.Version, 1, 2); e == nil {
			t.Fatal("invalid attachment bytes accepted")
		}
	}
}

func TestAttachmentSizeVersionAndFolderFailBeforeDownload(t *testing.T) {
	for _, mode := range []string{"large", "changed", "folder"} {
		s, f, scope, object := attachmentService(t, transport.Reply{})
		info, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object)
		if e != nil {
			t.Fatal(e)
		}
		switch mode {
		case "large":
			f.size = contracts.ContentChunkSize + 1
			if _, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object); e == nil || e.(*contracts.Error).Code != "UNSUPPORTED" {
				t.Fatal("unreadable large attachment descriptor issued")
			}
		case "changed":
			info.Version = strings.Repeat("0", 64)
		case "folder":
			f.folder = "3"
		}
		if _, e = s.ReadContent(context.Background(), []byte("SYNTHETIC_COOKIE"), scope, object, info.Version, 1, 2); e == nil || f.fetches != 0 {
			t.Fatal("changed, large or unauthorized attachment reached download")
		}
	}
}
