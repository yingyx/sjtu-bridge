package pan

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

// Identity trusts only the userId returned beside the exchanged token. An owner
// supplied id, nickname, organization id or browser localStorage is never read.
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	x, e := s.login(ctx, secret)
	if e != nil {
		return "", e
	}
	defer x.close()
	return x.account, nil
}

func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.View == "files_v1" || q.View == "file_v1" {
		return s.readFiles(ctx, secret, account, q, next)
	}
	if e := contracts.ValidatePanQuery(q); e != nil {
		return domain.CampusPage{}, e
	}
	if account != q.AccountRef || next != "" {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected the granted Pan account and a singleton quota view")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	x, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	defer x.close()
	if x.account != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Pan login belongs to a different account")
	}
	var raw struct {
		HasPersonalSpace *bool           `json:"hasPersonalSpace"`
		Size             json.RawMessage `json:"size"`
		Capacity         json.RawMessage `json:"capacity"`
		AvailableSpace   json.RawMessage `json:"availableSpace"`
	}
	if e = x.get(ctx, "/user/v1/space/1", &raw); e != nil {
		return domain.CampusPage{}, e
	}
	if raw.HasPersonalSpace == nil {
		return domain.CampusPage{}, changed()
	}
	quota := &contracts.PanQuota{HasPersonalSpace: *raw.HasPersonalSpace, QuotaMode: "not_opened"}
	var valid bool
	quota.UsedBytes, valid = amount(raw.Size, !quota.HasPersonalSpace)
	if !valid {
		return domain.CampusPage{}, changed()
	}
	quota.CapacityBytes, valid = amount(raw.Capacity, !quota.HasPersonalSpace)
	if !valid {
		return domain.CampusPage{}, changed()
	}
	quota.AvailableBytes, valid = amount(raw.AvailableSpace, !quota.HasPersonalSpace)
	if !valid {
		return domain.CampusPage{}, changed()
	}
	if quota.HasPersonalSpace {
		quota.QuotaMode = "allocated"
		if quota.CapacityBytes == nil && quota.AvailableBytes == nil {
			quota.QuotaMode = "shared"
		}
	}
	if !contracts.ValidPanQuota(quota) {
		return domain.CampusPage{}, changed()
	}
	// The same immutable token was bound to userId by the authentication reply;
	// validate its school membership again before releasing this projection.
	if e = x.membership(ctx); e != nil {
		return domain.CampusPage{}, e
	}
	return domain.CampusPage{AccountRef: account, Coverage: contracts.PanQuotaCoverage, Rows: []contracts.CampusRecord{{ResourceRef: contracts.PanQuotaResource, Ref: contracts.PanQuotaResource, Title: "交大云盘个人空间配额", Pan: quota}}}, nil
}

// Missing fields are accepted only for the explicitly unopened state. A null
// in an opened space is meaningful; absent fields are a changed contract.
func amount(raw json.RawMessage, mayOmit bool) (*string, bool) {
	if len(raw) == 0 {
		return nil, mayOmit
	}
	if string(raw) == "null" {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	// The shared contract validates canonical exact byte strings and lengths.
	return &value, true
}

var _ domain.Campus = (*Service)(nil)
