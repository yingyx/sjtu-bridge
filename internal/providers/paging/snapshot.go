// Package paging paginates bounded upstream snapshots without caching private results.
package paging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// Slice re-fetches on continuation and rejects a changed projected snapshot.
// Callers must bound the upstream response and total item count before calling.
func Slice[T any](rows []T, limit int, cursor string) ([]T, string, error) {
	raw, e := json.Marshal(rows)
	if e != nil || limit < 1 {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid snapshot")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	offset := 0
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || parts[1] != digest {
			return nil, "", contracts.Fail("CURSOR_INVALID", "Snapshot changed; restart the query")
		}
		offset, e = strconv.Atoi(parts[0])
		if e != nil || offset < 1 || offset >= len(rows) || offset%limit != 0 {
			return nil, "", contracts.Fail("CURSOR_INVALID", "Invalid snapshot offset")
		}
	}
	end := min(offset+limit, len(rows))
	result := append([]T{}, rows[offset:end]...)
	next := ""
	if end < len(rows) {
		next = strconv.Itoa(end) + ":" + digest
	}
	return result, next, nil
}
