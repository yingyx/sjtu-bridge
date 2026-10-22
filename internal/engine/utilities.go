package engine

import (
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func projectUtility(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title}
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed utility view")
	}
	if r.Title == "" {
		return bad()
	}
	switch id {
	case contracts.ElectricityHistoryCapability:
		if !contracts.ValidDateRange(r.Date, r.Date, 1) || r.Date < q.Begin || r.Date > q.End || r.Ref != "electric:usage:"+r.Date || r.Available == nil || *r.Available && (len(r.UsedKWh) > 64 || !electricAmount.MatchString(r.UsedKWh)) || !*r.Available && r.UsedKWh != "" {
			return bad()
		}
		row.Date, row.UsedKWh, row.Available = r.Date, r.UsedKWh, r.Available
	case contracts.BathBindingCapability:
		if r.Ref != "bath:binding:self" || r.Bound == nil || *r.Bound && (!strings.HasPrefix(r.BuildingRef, "bath:building:") || r.Location == "") || !*r.Bound && (r.BuildingRef != "" || r.Location != "") {
			return bad()
		}
		row.Bound, row.BuildingRef, row.Location = r.Bound, r.BuildingRef, r.Location
	case contracts.BathDevicesCapability:
		if !strings.HasPrefix(r.Ref, "bath:floor:") || r.Location == "" || r.AvailableCount == nil || r.InUseCount == nil || *r.AvailableCount < 0 || *r.InUseCount < 0 {
			return bad()
		}
		row.Location, row.AvailableCount, row.InUseCount = r.Location, r.AvailableCount, r.InUseCount
	case contracts.BathUsageCapability:
		if !strings.HasPrefix(r.Ref, "bath:usage:") || len(r.Date) < 10 || r.Date[:10] < q.Begin || r.Date[:10] > q.End || !contracts.ValidDateRange(r.Date[:10], r.Date[:10], 1) || !electricAmount.MatchString(r.AmountCNY) || !electricAmount.MatchString(r.Duration) || r.DurationUnit != "seconds" {
			return bad()
		}
		row.Date, row.AmountCNY, row.Duration, row.DurationUnit = r.Date, r.AmountCNY, r.Duration, r.DurationUnit
	default:
		return bad()
	}
	return row, nil
}
