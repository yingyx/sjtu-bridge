package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPanQuotaNullAndModeContract(t *testing.T) {
	p := func(s string) *string { return &s }
	for _, q := range []*PanQuota{
		{HasPersonalSpace: true, UsedBytes: p("9007199254740993"), CapacityBytes: p("10995116277760"), AvailableBytes: p("1"), QuotaMode: "allocated"},
		{HasPersonalSpace: true, UsedBytes: p("12"), QuotaMode: "shared"},
		{QuotaMode: "not_opened"},
	} {
		if !ValidPanQuota(q) {
			t.Fatal("Valid exact-byte/null state rejected")
		}
	}
	for _, q := range []*PanQuota{nil, {}, {HasPersonalSpace: true, QuotaMode: "shared"}, {HasPersonalSpace: true, UsedBytes: p("0"), CapacityBytes: p("1"), QuotaMode: "shared"}, {HasPersonalSpace: true, UsedBytes: p("-1"), QuotaMode: "shared"}, {HasPersonalSpace: true, UsedBytes: p("1e6"), QuotaMode: "shared"}, {HasPersonalSpace: true, UsedBytes: p("01"), QuotaMode: "shared"}, {QuotaMode: "allocated"}} {
		if ValidPanQuota(q) {
			t.Fatal("Invalid quota accepted")
		}
	}
	data, _ := json.Marshal(&PanQuota{QuotaMode: "not_opened"})
	for _, key := range []string{"used_bytes", "capacity_bytes", "available_bytes"} {
		if !strings.Contains(string(data), `"`+key+`":null`) {
			t.Fatal("Unknown/unopened amount did not preserve null")
		}
	}
	if PanQuotaSchema()["additionalProperties"] != false {
		t.Fatal("Pan fixed projection permits unknown fields")
	}
}
