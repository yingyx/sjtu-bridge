package engine

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
)

var secondClassRecordRef = regexp.MustCompile(`^dekt:record:[a-f0-9]{64}$`)
var secondClassDuration = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func campusUnavailableModules(id string, modules []string) ([]string, error) {
	if len(modules) == 0 {
		return nil, nil
	}
	if !contracts.IsSecondClassCapability(id) || len(modules) > len(contracts.SecondClassModules(id)) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid unavailable campus modules")
	}
	seen := map[string]bool{}
	for _, module := range modules {
		if !contracts.ValidSecondClassModule(id, module) || seen[module] {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid unavailable second-class module")
		}
		seen[module] = true
	}
	return append([]string(nil), modules...), nil
}

func safeCampusText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func projectActivityRecord(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed activity view")
	}
	row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title}
	if !safeCampusText(r.Title, 2048) || r.Title == "" || (r.ActivityTypeID != "" && !contracts.ValidActivityTypeID(r.ActivityTypeID)) {
		return bad()
	}
	switch id {
	case contracts.ActivityTypesCapability:
		if !contracts.ValidActivityTypeID(r.ActivityTypeID) || r.Ref != "activity:type:"+r.ActivityTypeID || (r.ParentRef != "" && (!strings.HasPrefix(r.ParentRef, "activity:type:") || !contracts.ValidActivityTypeID(strings.TrimPrefix(r.ParentRef, "activity:type:")) || r.ParentRef == r.Ref)) {
			return bad()
		}
		row.ActivityTypeID, row.ParentRef = r.ActivityTypeID, r.ParentRef
		if r.CalendarEnabled != nil {
			enabled := *r.CalendarEnabled
			row.CalendarEnabled = &enabled
		}
	case contracts.ActivityCountsCapability:
		if !contracts.ValidActivityDate(r.Date) || !strings.HasPrefix(r.Date, q.Month+"-") || r.Ref != "activity:date:"+r.Date || r.Count == nil || *r.Count < 0 || *r.Count > 1000000 {
			return bad()
		}
		count := *r.Count
		row.Date, row.Count = r.Date, &count
	case contracts.ActivityCapability, contracts.ActivityCalendarCapability, contracts.ActivityRegistrationsCapability, contracts.ActivityCollectionsCapability:
		if !activityRecordRef.MatchString(r.Ref) || len(r.ActivityTime) != 2 || r.ActivityTime[0] == "" || r.ActivityTime[1] == "" || !safeCampusText(r.Location, 2048) || !safeCampusText(r.Sponsor, 2048) {
			return bad()
		}
		for _, v := range r.ActivityTime {
			if !safeCampusText(v, 2048) {
				return bad()
			}
		}
		row.Location, row.Sponsor, row.ActivityTypeID, row.ActivityTime = r.Location, r.Sponsor, r.ActivityTypeID, append([]string(nil), r.ActivityTime...)
		if id == contracts.ActivityRegistrationsCapability {
			if !contracts.ValidActivityRegistrationState(r.Status) {
				return bad()
			}
			row.Status = r.Status
		}
	default:
		return bad()
	}
	return row, nil
}

func projectSecondClassRecord(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed second-class view")
	}
	if !secondClassRecordRef.MatchString(r.Ref) || !contracts.ValidSecondClassModule(id, r.Module) || r.Title == "" {
		return bad()
	}
	for _, v := range []string{r.Title, r.Organization, r.AwardedAt, r.Level, r.Role, r.StartedAt, r.EndedAt, r.DataSource} {
		if !safeCampusText(v, 2048) {
			return bad()
		}
	}
	row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title, Module: r.Module, StartedAt: r.StartedAt, EndedAt: r.EndedAt, DataSource: r.DataSource}
	if id == contracts.SecondClassHonorsCapability {
		row.Organization, row.AwardedAt, row.Level, row.Role = r.Organization, r.AwardedAt, r.Level, r.Role
	} else {
		if len(r.Duration) > 64 || (r.Duration != "" && !secondClassDuration.MatchString(r.Duration)) || !safeCampusText(r.DurationUnit, 64) || (r.DurationUnit != "" && r.Duration == "") {
			return bad()
		}
		row.Duration, row.DurationUnit = r.Duration, r.DurationUnit
		if id == contracts.SecondClassPracticeCapability {
			row.Role = r.Role
			if r.Module == "social_practice" {
				row.Level = r.Level
			}
		} else if r.Module == "labor" {
			row.Level = r.Level
		}
	}
	return row, nil
}
