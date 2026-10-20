package publicinfo

import (
	"context"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDirectoryPaginationAndRevision(t *testing.T) {
	fetch := &fakeFetcher{}
	p := New(fetch)
	seen, next := map[string]bool{}, ""
	for page := 0; page < 100; page++ {
		r, err := p.List(context.Background(), "services", contracts.ListInput{Limit: 2, Cursor: next})
		if err != nil {
			t.Fatal(err)
		}
		if r.Meta.Completeness != "partial" || len(r.Meta.Sources) != len(r.Data.Items) || len(r.Data.Items) == 0 {
			t.Fatal("misleading coverage", r)
		}
		for i, row := range r.Data.Items {
			if seen[row.SourceID] || row.Fields[0].Key != "task_path" || r.Meta.Sources[i].Status != "bundled" {
				t.Fatal("duplicate or invalid metadata", row)
			}
			seen[row.SourceID] = true
		}
		if !r.Meta.HasMore {
			break
		}
		if r.Meta.NextCursor == nil {
			t.Fatal("missing continuation")
		}
		next = *r.Meta.NextCursor
	}
	if !seen["bath"] || !seen["software"] || !seen["ids"] || len(seen) != len(directory()) || len(fetch.calls) != 0 {
		t.Fatal("incomplete directory or unexpected network", seen)
	}
	for _, c := range []cursor{
		{Version: 1, Kind: "services", Path: "bundled:2026-09-26", Limit: 2, Offset: 2},
		{Version: 1, Kind: "services", Path: directoryRevision, Limit: 2, Offset: len(directory())},
		{Version: 1, Kind: "services", Path: directoryRevision, Limit: 3, Offset: 2},
	} {
		c.Expires = time.Now().Add(time.Minute).Unix()
		_, err := p.List(context.Background(), "services", contracts.ListInput{Limit: 2, Cursor: encode(c)})
		code(t, err, "INVALID_ARGUMENT")
	}
}
