package sjtumail

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

type messageHTTP struct {
	*fake
	folder  string
	message map[string]any
}

func (f *messageHTTP) Post(ctx context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	var request struct{ Body map[string]map[string]any }
	if json.Unmarshal(b, &request) != nil {
		f.t.Fatal("invalid SOAP")
	}
	if request.Body["GetInfoRequest"] != nil {
		return f.fake.Post(ctx, u, h, b)
	}
	if !Allowed(u) || h.Get("X-Zimbra-Csrf-Token") == "" || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("invalid session scope")
	}
	var response string
	if p := request.Body["GetMsgRequest"]; p != nil {
		m, ok := p["m"].(map[string]any)
		if !ok || m["read"] != float64(0) || m["wantContent"] != "full" || m["html"] != float64(0) {
			f.t.Fatal("body fetch must be full and must not mark read")
		}
		f.message = m
		response = `{"GetMsgResponse":{"m":[{"id":"7","l":"` + f.folder + `","su":"合成正文","d":1700000000000,"ms":19,"mp":[{"part":"1","ct":"text/plain","content":"合成正文"},{"part":"2","ct":"application/pdf","filename":"demo.pdf","s":5}]}]}}`
	} else if p := request.Body["SearchRequest"]; p != nil {
		if p["read"] != float64(0) || p["fetch"] != "none" || p["query"] != `inid:2 "hello" is:unread after:2026/10/01 before:2026/10/26` {
			f.t.Fatal("unbounded or side-effecting search", p)
		}
		response = `{"SearchResponse":{"m":[{"id":"7","l":"2","su":"合成标题","d":1700000000000,"f":"u","mp":[{"content":"MUST_NOT_RETURN"}]}],"more":false,"offset":0}}`
	} else {
		f.t.Fatal("unreviewed SOAP operation")
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/soap+json"}}, Body: []byte(`{"Body":` + response + `}`)}, nil
}

func TestMessageFullBodyWithoutReadMutationAndFolderCheck(t *testing.T) {
	for _, folder := range []string{"2", "3"} {
		f := &messageHTTP{fake: &fake{t: t}, folder: folder}
		s := fakeService(f.fake)
		s.Session.Portal = f
		s.Session.Auth = f
		q := contracts.CampusQuery{AccountRef: "sjtumail:account:synthetic-account-01", ResourceRef: "mail:folder:2", View: "message_v1", Limit: 1, ItemID: "7"}
		page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
		if folder == "3" {
			if e == nil {
				t.Fatal("message outside granted folder returned")
			}
			continue
		}
		if e != nil || len(page.Rows) != 1 || page.Rows[0].Digital.Mail.Body != "合成正文\n" || len(page.Rows[0].Digital.Mail.Attachments) != 1 {
			t.Fatal("missing explicit body", e)
		}
	}
}
func TestMailboxSearchDoesNotFetchBodies(t *testing.T) {
	f := &messageHTTP{fake: &fake{t: t}, folder: "2"}
	s := fakeService(f.fake)
	s.Session.Portal = f
	s.Session.Auth = f
	q := contracts.CampusQuery{AccountRef: "sjtumail:account:synthetic-account-01", ResourceRef: "mail:folder:2", View: "messages_v1", Limit: 20, Query: "hello", Unread: true, Begin: "2026-10-01", End: "2026-10-26"}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(page.Rows) != 1 {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(page.Rows)
	if strings.Contains(string(raw), "MUST_NOT_RETURN") || strings.Contains(string(raw), "body") {
		t.Fatal("body leaked through search")
	}
}

func TestMailboxMIMETruncationAndHTMLIsolation(t *testing.T) {
	m := messageWire{ID: "7", Folder: "2", Title: "合成", Date: 1700000000000, Parts: []partWire{{Type: "text/html", Content: `<p>正文</p><img src="https://example.invalid"><script>DROP</script>`}, {Type: "text/plain", Content: strings.Repeat("中", 65537), Truncated: json.RawMessage(`1`)}}}
	item, e := projectMessage(m, true)
	if e != nil || !item.Truncated || len([]rune(item.Body)) != 65536 {
		t.Fatal("lost truncation", e)
	}
	m.Parts = m.Parts[:1]
	item, e = projectMessage(m, true)
	if e != nil || !strings.Contains(item.Body, "正文") || strings.Contains(item.Body, "DROP") || strings.Contains(item.Body, "example.invalid") {
		t.Fatal("HTML was not reduced to text", e)
	}
}
