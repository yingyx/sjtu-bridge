package contracts

import (
	"reflect"
	"regexp"
)

const (
	PanQuotaCapability = "digital.pan.quota"
	PanQuotaView       = "quota_v1"
	PanQuotaResource   = "pan:space:self"
	PanQuotaCoverage   = "personal_space_quota_only"
)

// PanQuota contains only the authenticated user's quota. All amounts are exact
// decimal byte strings. Null capacity/available means shared organization quota;
// an unopened space may omit every amount. Neither case is a zero-byte quota.
type PanQuota struct {
	HasPersonalSpace bool    `json:"has_personal_space"`
	UsedBytes        *string `json:"used_bytes"`
	CapacityBytes    *string `json:"capacity_bytes"`
	AvailableBytes   *string `json:"available_bytes"`
	QuotaMode        string  `json:"quota_mode"`
}

var panBytes = regexp.MustCompile(`^(0|[1-9][0-9]{0,29})$`)
var panAccount = regexp.MustCompile(`^pan:user:1:[1-9][0-9]{0,19}$`)

func ValidPanAccount(account string) bool { return panAccount.MatchString(account) }

func ValidPanQuota(q *PanQuota) bool {
	if q == nil {
		return false
	}
	for _, amount := range []*string{q.UsedBytes, q.CapacityBytes, q.AvailableBytes} {
		if amount != nil && !panBytes.MatchString(*amount) {
			return false
		}
	}
	if !q.HasPersonalSpace {
		return q.QuotaMode == "not_opened"
	}
	if q.UsedBytes == nil {
		return false
	}
	switch q.QuotaMode {
	case "allocated":
		return q.CapacityBytes != nil && q.AvailableBytes != nil
	case "shared":
		return q.CapacityBytes == nil && q.AvailableBytes == nil
	}
	return false
}

// ValidatePanQuery rejects every filter outside the fixed singleton view.
func ValidatePanQuery(q CampusQuery) error {
	fixed := CampusQuery{AccountRef: q.AccountRef, ResourceRef: PanQuotaResource, View: PanQuotaView, Limit: 1}
	if !ValidPanAccount(q.AccountRef) || !reflect.DeepEqual(q, fixed) {
		return Fail("INVALID_ARGUMENT", "Expected the authenticated Pan account, pan:space:self, quota_v1 and limit 1 without filters or cursor")
	}
	return nil
}

// PanQuotaSchema is embedded as the required CampusRecord.pan fixed projection.
func PanQuotaSchema() map[string]any {
	amount := func() map[string]any {
		return map[string]any{"type": []string{"string", "null"}, "pattern": panBytes.String(), "maxLength": 30, "description": "精确Byte整数字符串；null不表示0"}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"has_personal_space": map[string]any{"type": "boolean"},
			"used_bytes":         amount(), "capacity_bytes": amount(), "available_bytes": amount(),
			"quota_mode": map[string]any{"type": "string", "enum": []string{"allocated", "shared", "not_opened"}, "description": "shared时capacity/available为null，表示共享组织剩余未分配额度；not_opened不初始化空间"},
		},
		"required": []string{"has_personal_space", "used_bytes", "capacity_bytes", "available_bytes", "quota_mode"},
	}
}
