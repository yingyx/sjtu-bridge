package myportal

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

func TestGroupsUseZeroBasedPagesAndStableTotal(t *testing.T) {
	for _, test := range []string{"single_page", "two_pages", "truncated", "unstable_total", "duplicate"} {
		t.Run(test, func(t *testing.T) {
			pages := []string{}
			f := &fakeHTTP{t: t}
			f.get = func(u *url.URL) (transport.Reply, error) {
				if u.Path == "/api/account" {
					return js(`{"errno":0,"total":0,"entities":[{"account":"synthetic_user"}]}`), nil
				}
				pages = append(pages, u.Query().Get("page"))
				n, total := 24, 24
				if test != "single_page" {
					n, total = 100, 101
				}
				start := 0
				if u.Query().Get("page") == "1" {
					start, n = 100, 1
					switch test {
					case "truncated":
						n = 0
					case "unstable_total":
						total = 102
					case "duplicate":
						start = 0
					}
				}
				rows := []string{}
				for i := start; i < start+n; i++ {
					rows = append(rows, fmt.Sprintf(`{"groupId":"g%d","groupName":"synthetic","unreadNum":0}`, i))
				}
				return js(fmt.Sprintf(`{"errno":0,"total":%d,"entities":[%s]}`, total, strings.Join(rows, ","))), nil
			}
			s := &Service{f, f}
			q := contracts.CampusQuery{AccountRef: "myportal:account:synthetic_user", ResourceRef: "myportal:messages:groups", View: "groups_v1", Limit: 50}
			p, err := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
			if test == "single_page" {
				if err != nil || len(p.Rows) != 24 || strings.Join(pages, ",") != "0" {
					t.Fatalf("zero page not read: %v", err)
				}
			} else if test == "two_pages" {
				if err != nil || len(p.Rows) != 50 || p.Next == "" || strings.Join(pages, ",") != "0,1" {
					t.Fatalf("pagination incomplete: %v", err)
				}
			} else if err == nil {
				t.Fatal("incomplete or unstable snapshot accepted")
			}
		})
	}
	for _, page := range []string{"-1", "5"} {
		u, _ := url.Parse(root + "/api/jwbmessage/group?key=&page=" + page + "&pageSize=100&read=true")
		if Allowed(u) {
			t.Fatal("out of bound page allowed")
		}
	}
}

func TestPortalAJAXHeadersAreFixedAndScoped(t *testing.T) {
	// Capture the actual headers before the synthetic API implementation.
	f := &captureHTTP{}
	p := portalHTTP{f}
	u, _ := url.Parse(root + "/api/task/me/processes/todo?thing=false")
	original := http.Header{"Accept": {"text/html"}}
	_, _ = p.Get(context.Background(), u, original)
	if original.Get("Accept") != "text/html" || f.header.Get("X-Requested-With") != "XMLHttpRequest" || f.header.Get("Referer") != root+"/ui/task/todo" || f.header.Get("Accept") != "application/json, text/plain, */*" {
		t.Fatal("portal AJAX headers are missing or mutated source headers")
	}
}

type captureHTTP struct{ header http.Header }

func (c *captureHTTP) Get(_ context.Context, _ *url.URL, h http.Header) (transport.Reply, error) {
	c.header = h
	return transport.Reply{}, nil
}
func (c *captureHTTP) Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error) {
	return transport.Reply{}, nil
}
