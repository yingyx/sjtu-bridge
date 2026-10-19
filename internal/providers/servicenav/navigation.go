// Package servicenav routes tasks to reviewed official destinations without network access,
// credentials, browser automation, or upstream business operations.
package servicenav

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

const revision = "2026-10-19-navigation-v2"

type Provider struct{}

type cursor struct {
	Revision   string `json:"revision"`
	Capability string `json:"capability"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	Expires    int64  `json:"expires"`
}

func (Provider) List(ctx context.Context, capability string, input contracts.ListInput) (contracts.Response, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	items, ok := entries[capability]
	if !ok {
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown navigation capability")
	}
	if input.Limit < 1 || input.Limit > 100 {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Limit must be 1-100")
	}
	c := cursor{Revision: revision, Capability: capability, Limit: input.Limit, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid navigation cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Revision != revision || c.Capability != capability || c.Limit != input.Limit || c.Offset < 1 || c.Offset >= len(items) || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Navigation cursor expired or does not match this query")
		}
	}
	end := min(c.Offset+input.Limit, len(items))
	// Return independent records: API callers cannot mutate the bundled reference.
	page := make([]contracts.Item, 0, end-c.Offset)
	sources := []contracts.Source{}
	seen := map[string]bool{}
	for _, item := range items[c.Offset:end] {
		g := *item.Navigation
		g.CrossListings = append([]contracts.TaskPath{}, g.CrossListings...)
		g.RelatedCapabilities = append([]string{}, g.RelatedCapabilities...)
		g.Sources = append([]contracts.NavigationSource{}, g.Sources...)
		item.Navigation = &g
		page = append(page, item)
		for _, source := range g.Sources {
			if !seen[source.URL] {
				sources = append(sources, contracts.Source{Provider: "servicenav", URL: source.URL, Status: "bundled", Coverage: "curated"})
				seen[source.URL] = true
			}
		}
	}
	result := contracts.Response{Data: contracts.Data{Items: page}, Meta: contracts.Meta{
		Completeness: "partial", Sources: sources, FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai",
	}, Warnings: []contracts.Warning{{Code: "NAVIGATION_ONLY", Message: "官方目的地导航，不含操作教程或个人状态；verified_at 仅表示入口依据核对日期，未在本次调用探测链接。"}}}
	if end < len(items) {
		c.Offset = end
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		result.Meta.NextCursor, result.Meta.HasMore = &next, true
	}
	return result, nil
}
