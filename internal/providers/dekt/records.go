package dekt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

const maxRecords = 10000

type module struct {
	name, key, title, organization, awardedAt, level, role, startedAt, endedAt, duration, unit string
}

var honorCounters = map[string]string{
	"scholarship": "totalScholarshipNo", "innovation": "totalKechuangAwardNo",
	"volunteering_award": "totalVolunteeringNo", "social_practice_award": "totalSocialPracticeNo",
	"arts_award": "totalStylisticArtsNo", "student_leader": "totalStudentLeaderNo",
}

var honors = []module{
	{name: "scholarship", key: "studentScholarshipVos", title: "applyType", organization: "grantUnit", awardedAt: "schoolYear", level: "level"},
	{name: "innovation", key: "studentKechuangAwardVos", title: "matchName", organization: "awardOrg", awardedAt: "awardTime", level: "awardType"},
	{name: "volunteering_award", key: "personalVolunteeringVos", title: "awardName", organization: "dept", awardedAt: "awardTime"},
	{name: "social_practice_award", key: "personalSocialPracticeVos", title: "awardName", organization: "dept", awardedAt: "awardTime"},
	{name: "arts_award", key: "stylisticArtsVos", title: "awardName", organization: "dept", awardedAt: "awardTime"},
	{name: "outstanding_graduate", key: "outstandingGraduatesVos", title: "awardName"},
	{name: "student_leader", key: "studentLeaderVos", title: "organizationName", role: "title", startedAt: "startTime", endedAt: "endTime"},
}
var practice = []module{
	{name: "volunteering", key: "studentVoluntaryPublicWelfareVos", title: "projectName", startedAt: "begintime", endedAt: "endtime", duration: "duration"},
	{name: "social_practice", key: "studentSocialPracticeVos", title: "practiceName", level: "practiceType", role: "role", startedAt: "startTime", endedAt: "endTime", duration: "duration"},
}
var labor = []module{
	{name: "labor", key: "studentLaborEducationDtos", title: "activityName", level: "laborTypeDetail", startedAt: "activeStartTime", endedAt: "activeEndTime", duration: "duration", unit: "学时"},
}
var education = []module{
	{name: "lecture", key: "lectureDtos", title: "activityName", startedAt: "activeStartTime", endedAt: "activeEndTime"},
	{name: "cultural_activity", key: "culturalActivityDtos", title: "activityName", startedAt: "activeStartTime", endedAt: "activeEndTime"},
}

var recordID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var durationValue = regexp.MustCompile(`^([0-9]{1,12}(?:\.[0-9]{1,9})?)(?:\s*([^0-9\s]{1,16}))?$`)

func memberText(row map[string]json.RawMessage, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	raw, exists := row[key]
	if !exists || string(raw) == "null" {
		return "", nil
	}
	value := scalar(raw)
	if value == "" && string(raw) != `""` || !safeText(value) {
		return "", changed()
	}
	return value, nil
}

func project(raw json.RawMessage, modules []module, account, resource string) ([]contracts.CampusRecord, error) {
	var entity map[string]json.RawMessage
	if json.Unmarshal(raw, &entity) != nil || entity == nil {
		return nil, changed()
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, m := range modules {
		body, ok := entity[m.key]
		if !ok || string(body) == "null" {
			return nil, changed()
		}
		var records []map[string]json.RawMessage
		if json.Unmarshal(body, &records) != nil || records == nil || len(records) > maxRecords-len(rows) {
			return nil, changed()
		}
		for _, r := range records {
			id := scalar(r["id"])
			if value, exists := r["id"]; exists && string(value) != "null" && (!recordID.MatchString(id) || id == "null") {
				return nil, changed()
			}
			// Owner fields, when provided, corroborate the session. Never return
			// them, and never accept a source row for a different user.
			for _, key := range []string{"userid", "userId"} {
				if owner, exists := r[key]; exists && string(owner) != "null" && scalar(owner) != strings.TrimPrefix(account, "dekt:user:") {
					return nil, contracts.Fail("PERMISSION_DENIED", "Second-classroom record owner changed")
				}
			}
			record := contracts.CampusRecord{ResourceRef: resource, Module: m.name}
			for _, f := range []struct {
				key    string
				target *string
			}{{m.title, &record.Title}, {m.organization, &record.Organization}, {m.awardedAt, &record.AwardedAt}, {m.level, &record.Level}, {m.role, &record.Role}, {m.startedAt, &record.StartedAt}, {m.endedAt, &record.EndedAt}, {m.duration, &record.Duration}, {"dataSource", &record.DataSource}} {
				value, err := memberText(r, f.key)
				if err != nil {
					return nil, err
				}
				*f.target = value
			}
			if record.Title == "" {
				return nil, changed()
			}
			if record.Duration != "" {
				match := durationValue.FindStringSubmatch(strings.TrimSpace(record.Duration))
				if match == nil {
					return nil, changed()
				}
				record.Duration, record.DurationUnit = match[1], match[2]
				if record.DurationUnit == "" {
					record.DurationUnit = m.unit
				}
			}
			fingerprint, _ := json.Marshal(struct {
				ID     string
				Record contracts.CampusRecord
			}{id, record})
			sum := sha256.Sum256(fingerprint)
			record.Ref = "dekt:record:" + hex.EncodeToString(sum[:])
			if seen[record.Ref] {
				return nil, changed()
			}
			seen[record.Ref] = true
			rows = append(rows, record)
		}
	}
	return rows, nil
}

// honorAvailability distinguishes a source's explicit zero counter from a
// module it did not provide. It never treats a missing nonzero list as empty.
func honorAvailability(raw json.RawMessage) (json.RawMessage, []string, error) {
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return nil, nil, changed()
	}
	unavailable := []string{}
	for _, m := range honors {
		if value, exists := v[m.key]; exists && string(value) != "null" {
			continue // Counters are not authoritative list lengths.
		}
		if m.name == "outstanding_graduate" {
			unavailable = append(unavailable, m.name)
		} else {
			counter, exists := v[honorCounters[m.name]]
			if !exists || scalar(counter) != "0" {
				return nil, nil, changed()
			}
		}
		v[m.key] = json.RawMessage(`[]`)
	}
	normalized, err := json.Marshal(v)
	if err != nil {
		return nil, nil, changed()
	}
	return normalized, unavailable, nil
}

func (x *session) records(ctx context.Context, account, resource string) ([]contracts.CampusRecord, []string, error) {
	type endpoint struct {
		path    string
		modules []module
	}
	var endpoints []endpoint
	switch resource {
	case "dekt:honors:self":
		endpoints = []endpoint{{"/personalHonor/getHonor", honors}}
	case "dekt:practice:self":
		endpoints = []endpoint{{"/voluntaryPracticeInfo/getInfo", practice}}
	case "dekt:education:self":
		endpoints = []endpoint{{"/student/education/labor", labor}, {"/student/education/noLabor", education}}
	default:
		return nil, nil, contracts.Fail("INVALID_ARGUMENT", "Invalid second-classroom resource")
	}
	rows := []contracts.CampusRecord{}
	unavailable := []string{}
	used := 0
	for _, endpoint := range endpoints {
		r, err := x.get(ctx, endpoint.path)
		if err != nil {
			return nil, nil, err
		}
		used += len(r.Body)
		if used > 2<<20 {
			return nil, nil, contracts.Fail("RESPONSE_TOO_LARGE", "Second-classroom records exceed response budget")
		}
		raw, err := decode(r)
		if err != nil {
			return nil, nil, err
		}
		if resource == "dekt:honors:self" {
			raw, unavailable, err = honorAvailability(raw)
			if err != nil {
				return nil, nil, err
			}
		}
		part, err := project(raw, endpoint.modules, account, resource)
		if err != nil {
			return nil, nil, err
		}
		if len(part) > maxRecords-len(rows) {
			return nil, nil, contracts.Fail("RESPONSE_TOO_LARGE", "Second-classroom records exceed capacity")
		}
		rows = append(rows, part...)
	}
	return rows, unavailable, nil
}

func scope(q contracts.CampusQuery) (string, bool) {
	capabilities := map[string]string{"dekt:honors:self": contracts.SecondClassHonorsCapability, "dekt:practice:self": contracts.SecondClassPracticeCapability, "dekt:education:self": contracts.SecondClassEducationCapability}
	rules, ok := contracts.CampusRules(capabilities[q.ResourceRef])
	return rules.Coverage, ok && rules.ValidQuery(q)
}

// Read has no upstream student selector or write action. Each call establishes
// an independent session, checks the bound account before and after the read,
// and creates a continuation tied to all projected records and query scope.
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	coverage, ok := scope(q)
	if !ok || account != q.AccountRef || !strings.HasPrefix(account, "dekt:user:") || !identity.MatchString(strings.TrimPrefix(account, "dekt:user:")) || len(next) > 72 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected a fixed-view own second-classroom query")
	}
	offset, expected := 0, ""
	if next != "" {
		parts := strings.Split(next, ":")
		if len(parts) != 2 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid second-classroom continuation")
		}
		var err error
		offset, err = strconv.Atoi(parts[0])
		if err != nil || offset < q.Limit || offset >= maxRecords || offset%q.Limit != 0 || len(parts[1]) != 64 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid second-classroom continuation")
		}
		if _, err := hex.DecodeString(parts[1]); err != nil {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid second-classroom continuation")
		}
		expected = parts[1]
	}
	x, err := s.login(ctx, secret)
	if err != nil {
		return domain.CampusPage{}, err
	}
	before, err := x.identity(ctx)
	if err != nil {
		return domain.CampusPage{}, err
	}
	if before != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Second-classroom account changed")
	}
	rows, unavailable, err := x.records(ctx, account, q.ResourceRef)
	if err != nil {
		return domain.CampusPage{}, err
	}
	after, err := x.identity(ctx)
	if err != nil {
		return domain.CampusPage{}, err
	}
	if after != before {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Second-classroom account changed during the read")
	}
	query := q
	query.Cursor = ""
	raw, _ := json.Marshal(struct {
		Query       contracts.CampusQuery
		Rows        []contracts.CampusRecord
		Unavailable []string
	}{query, rows, unavailable})
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if next != "" && (expected != digest || offset >= len(rows)) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Second-classroom records changed; restart the query")
	}
	end := min(offset+q.Limit, len(rows))
	page := domain.CampusPage{AccountRef: account, Rows: rows[offset:end], Coverage: coverage, UnavailableModules: unavailable}
	if end < len(rows) {
		page.Next = strconv.Itoa(end) + ":" + digest
	}
	return page, nil
}
