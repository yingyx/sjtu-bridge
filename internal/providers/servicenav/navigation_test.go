package servicenav

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestNavigationRoutesWithoutCopyingRules(t *testing.T) {
	known := map[string]bool{}
	for _, c := range contracts.PublicCapabilities() {
		known[c.ID] = true
	}
	for id := range entries {
		first, err := (Provider{}).List(context.Background(), id, contracts.ListInput{Limit: 1})
		if err != nil || first.Meta.HasMore != (len(entries[id]) > 1) || first.Meta.Completeness != "partial" || first.Meta.Sources[0].Status != "bundled" || first.Warnings[0].Code != "NAVIGATION_ONLY" {
			t.Fatal(id, first, err)
		}
		items, page := append([]contracts.Item{}, first.Data.Items...), first
		for page.Meta.HasMore {
			if page.Meta.NextCursor == nil || len(items) >= len(entries[id]) {
				t.Fatal("unbounded or missing continuation", id)
			}
			page, err = (Provider{}).List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: *page.Meta.NextCursor})
			if err != nil {
				t.Fatal(id, err)
			}
			items = append(items, page.Data.Items...)
		}
		if len(items) != len(entries[id]) {
			t.Fatal("missing navigation destination", id)
		}
		for _, i := range items {
			n := i.Navigation
			if n == nil || n.Purpose == "" || n.Primary.Category == "" || n.DestinationKind == "" || len(n.Sources) == 0 || n.VerifiedAt == "" || n.LinkCheckedThisCall || n.Upstream.Status != "link_only" || n.Upstream.ReasonCode == "" || n.Upstream.Authentication == "" {
				t.Fatal(id, i)
			}
			if n.Upstream.ExecutionRule != contracts.ManualExecution || len(n.Upstream.Effects) == 0 {
				t.Fatal("upstream action must remain manual and explicit", id, n.Upstream)
			}
			switch n.Upstream.Operation {
			case contracts.Read:
				if n.Upstream.Reversibility != contracts.NotApplicable || len(n.Upstream.Effects) != 1 || n.Upstream.Effects[0] != contracts.NoEffect {
					t.Fatal("read navigation has business side effects", id, n.Upstream)
				}
			case contracts.Write:
				if n.Upstream.Reversibility != contracts.UnknownReversibility {
					t.Fatal("upstream recoverability was not verified", id, n.Upstream)
				}
			default:
				t.Fatal("unknown upstream operation", id, n.Upstream)
			}
			for _, related := range n.RelatedCapabilities {
				// Static links may point to separately granted personal queries;
				// knowing their ID never makes them callable from public mode.
				_, personal := contracts.PersonalMetadata(related)
				if !known[related] && !personal {
					t.Fatal("dead capability link", related)
				}
			}
		}
		raw, _ := json.Marshal(first)
		if strings.Contains(string(raw), "steps") || strings.Contains(string(raw), "eligibility") {
			t.Fatal("copied workflow", string(raw))
		}
		n := first.Data.Items[0].Navigation
		n.Sources[0].URL = "changed"
		n.Upstream.Effects[0] = contracts.Effect("changed")
		if len(first.Data.Items[0].Fields) > 0 {
			first.Data.Items[0].Fields[0].Value = "changed"
		}
		if len(n.CrossListings) > 0 {
			n.CrossListings[0].Category = "changed"
		}
		if len(n.RelatedCapabilities) > 0 {
			n.RelatedCapabilities[0] = "changed"
		}
		again, _ := (Provider{}).List(context.Background(), id, contracts.ListInput{Limit: 1})
		raw, _ = json.Marshal(again)
		if strings.Contains(string(raw), "changed") {
			t.Fatal("shared navigation mutated")
		}
	}
}
func TestNavigationCursorRejectsCrossCapabilityAndStaleRevision(t *testing.T) {
	const id = "learning.practice.navigate"
	base := cursor{Revision: revision, Capability: id, Limit: 1, Offset: 1, Expires: time.Now().Add(time.Minute).Unix()}
	for _, mutate := range []func(*cursor){func(c *cursor) { c.Capability = "development.activities.navigate" }, func(c *cursor) { c.Limit = 2 }, func(c *cursor) { c.Offset = -1 }, func(c *cursor) { c.Offset = 2 }, func(c *cursor) { c.Revision = "old" }, func(c *cursor) { c.Expires = 1 }} {
		c := base
		mutate(&c)
		raw, _ := json.Marshal(c)
		if _, err := (Provider{}).List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString(raw)}); err == nil {
			t.Fatal(c)
		}
	}
}
