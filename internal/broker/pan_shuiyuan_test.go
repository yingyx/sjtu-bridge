package broker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func TestPanAndShuiyuanSharedGrantAndFixedProjection(t *testing.T) {
	for _, provider := range []string{"pan", "shuiyuan"} {
		t.Run(provider, func(t *testing.T) {
			h := setup(t)
			id, account, resource, view, coverage, secret := contracts.PanQuotaCapability, "pan:user:1:42", contracts.PanQuotaResource, contracts.PanQuotaView, contracts.PanQuotaCoverage, "SYNTHETIC_JA_COOKIE"
			if provider == "shuiyuan" {
				id, account, resource, view, coverage, secret = contracts.ShuiyuanSearchCapability, "shuiyuan:user:42", "shuiyuan:category:7", contracts.ShuiyuanSearchView, contracts.ShuiyuanSearchCoverage, strings.Repeat("a", 32)
			}
			f := &campusFake{account: account, read: func(q contracts.CampusQuery) domain.CampusPage {
				row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: contracts.PanQuotaResource, Title: "合成配额", Pan: &contracts.PanQuota{QuotaMode: "not_opened"}, Sender: "MUST_NOT_LEAK"}
				if provider == "shuiyuan" {
					row.Ref, row.Title = "shuiyuan:post:8", "合成主题"
					row.Shuiyuan = &contracts.ShuiyuanSearchHit{TopicRef: "shuiyuan:topic:9", PostRef: row.Ref, CategoryRef: q.ResourceRef, URL: "https://shuiyuan.sjtu.edu.cn/t/topic/9/1", Excerpt: "合成摘要", CreatedAt: "2026-10-23T01:00:00Z"}
				}
				return domain.CampusPage{AccountRef: account, Rows: []contracts.CampusRecord{row}, Coverage: coverage}
			}}
			h.b.Engine.CampusProviders = map[string]domain.Campus{provider: f}
			value, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: provider, Secret: []byte(secret), ExpectedAccount: account, Label: "main"})
			if e != nil {
				t.Fatal(e)
			}
			c := value.(storage.Credential)
			q := contracts.CampusQuery{AccountRef: account, ResourceRef: resource, View: view, Limit: 1}
			if provider == "shuiyuan" {
				q.Query = "合成查询"
			}
			if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: c.ID, AccountRef: account, Capability: id, Resources: []string{resource}, View: view, Limit: 1}); e != nil {
				t.Fatal(e)
			}
			raw, e := invokeCampusTest(h.b, h.a, id, q)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(raw), "MUST_NOT_LEAK") {
				t.Fatal("Fixed view leaked unrelated provider fields")
			}
			var response contracts.CampusResponse
			if json.Unmarshal(raw, &response) != nil || len(response.Data.Items) != 1 {
				t.Fatal("Missing fixed query result")
			}
			row := response.Data.Items[0]
			if provider == "pan" {
				if row.Pan == nil || row.Pan.HasPersonalSpace || row.Pan.QuotaMode != "not_opened" || row.Pan.UsedBytes != nil || row.Pan.CapacityBytes != nil || row.Pan.AvailableBytes != nil || response.Meta.NextCursor != nil {
					t.Fatal("Pan singleton did not preserve the unopened state")
				}
			} else if row.Shuiyuan == nil || row.Shuiyuan.CategoryRef != resource || row.Pan != nil {
				t.Fatal("Forum search did not retain only its granted-category projection")
			}
			before := f.calls.Load()
			for _, mutate := range []func(*contracts.CampusQuery){
				func(q *contracts.CampusQuery) { q.Query = "in:personal" },
				func(q *contracts.CampusQuery) { q.ResourceRef = "shuiyuan:category:8" },
			} {
				bad := q
				mutate(&bad)
				if _, e := invokeCampusTest(h.b, h.a, id, bad); e == nil || f.calls.Load() != before {
					t.Fatal("Illegal query or ungranted category reached Provider")
				}
			}
			input, _ := json.Marshal(q)
			var fields map[string]any
			_ = json.Unmarshal(input, &fields)
			fields["category"] = "7"
			input, _ = json.Marshal(fields)
			e = h.b.invoke(context.Background(), h.a, contracts.Invocation{Capability: id, Input: input}, func([]byte) error { t.Fatal("Unexpected output"); return nil })
			if e == nil || f.calls.Load() != before {
				t.Fatal("Unsupported selector reached Provider")
			}
			if provider == "pan" {
				bad := q
				bad.Limit = 2
				if _, e := invokeCampusTest(h.b, h.a, id, bad); e == nil || f.calls.Load() != before {
					t.Fatal("Pan accepted a non-singleton limit")
				}
				f.read = func(q contracts.CampusQuery) domain.CampusPage {
					return domain.CampusPage{AccountRef: account, Coverage: coverage}
				}
				if out, e := invokeCampusTest(h.b, h.a, id, q); e == nil || len(out) != 0 {
					t.Fatal("Missing quota state became a successful empty result")
				}
			}
		})
	}
}

func TestPanAndShuiyuanCredentialKindsCannotCrossAuthenticate(t *testing.T) {
	h := setup(t)
	h.b.Engine.CampusProviders = map[string]domain.Campus{"pan": &campusFake{account: "pan:user:1:42"}, "shuiyuan": &campusFake{account: "shuiyuan:user:42"}}
	importCredential := func(provider, account, secret string) storage.Credential {
		t.Helper()
		value, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: provider, Secret: []byte(secret), ExpectedAccount: account, Label: provider})
		if e != nil {
			t.Fatal(e)
		}
		return value.(storage.Credential)
	}
	pan := importCredential("pan", "pan:user:1:42", "SYNTHETIC_JA_COOKIE")
	forum := importCredential("shuiyuan", "shuiyuan:user:42", strings.Repeat("b", 32))
	if pan.Kind != "JAAuthCookie" || forum.Kind == "JAAuthCookie" {
		t.Fatal("Independent credentials lost their type boundary")
	}
	if e := h.b.Store.CheckSourceCredential(forum); e == nil {
		t.Fatal("Forum API key accepted as JAAuthCookie source")
	}
	for _, q := range []AdminRequest{
		{Action: "verify", Provider: "pan", SourceCredentialID: forum.ID},
		{Action: "verify", Provider: "shuiyuan", SourceCredentialID: pan.ID},
		{Action: "import", Provider: "shuiyuan", Secret: []byte("JAAuthCookie=SYNTHETIC_JA_COOKIE"), ExpectedAccount: "shuiyuan:user:42", Label: "wrong"},
	} {
		if _, e := h.b.Manage(context.Background(), q); e == nil {
			t.Fatal("API key and campus session were cross-used")
		}
	}
}
