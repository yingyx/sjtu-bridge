package teaching

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// The total grade and component grade SPs are separate: N305007 serves project
// components, not the N305005 total-grade shape. GPA statistics is absent: it writes temporary
// server state before returning a ranking and is not part of this read view.
const gradesPage = "https://i.sjtu.edu.cn/cjcx/cjcx_cxDgXscj.html?gnmkdm=N305005"
const gradesQuery = "https://i.sjtu.edu.cn/cjcx/cjcx_cxXsgrcj.html?doType=query&gnmkdm=N305005"
const examsPage = "https://i.sjtu.edu.cn/kwgl/kscx_cxXsksxxIndex.html?gnmkdm=N358105"
const examsQuery = "https://i.sjtu.edu.cn/kwgl/kscx_cxXsksxxIndex.html?doType=query&gnmkdm=N358105"
const componentsPage = "https://i.sjtu.edu.cn/cjcx/cjcx_cxDgXsxmcj.html?gnmkdm=N305007"

// The SP page is HTML even with doType=query. Its current first-party script
// cxDgXsxmcj.js uses this separate combined course/component jqGrid endpoint.
const componentsQuery = "https://i.sjtu.edu.cn/cjcx/cjcx_cxXsKccjList.html?gnmkdm=N305007"

func recordsAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	switch u.String() {
	case gradesPage, gradesQuery, examsPage, examsQuery, componentsPage, componentsQuery:
		return true
	}
	return false
}

func (s *Service) readRecords(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id, pageURL, queryURL := contracts.GradesCapability, gradesPage, gradesQuery
	if q.View == "exams_v1" {
		id, pageURL, queryURL = contracts.ExamsCapability, examsPage, examsQuery
	}
	if q.View == "grade_components_v1" {
		id, pageURL, queryURL = contracts.GradesComponentsCapability, componentsPage, componentsQuery
	}
	rules, _ := contracts.StudentRecordRules(id)
	if q.AccountRef != account || !rules.ValidQuery(q) || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected an own-term student record query")
	}
	parts := strings.Split(q.ResourceRef, ":")
	year, xqm := parts[2], "3"
	if parts[3] == "2" {
		xqm = "12"
	}
	session, e := s.Session.Login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	// Prove identity using the same authenticated session that reads grades or
	// exams. Empty course lists still carry xsxx and are valid identity proofs.
	_, actual, e := s.fetchSession(ctx, session, "teaching:term:"+year+":"+parts[3], year, xqm)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Teaching account changed")
	}
	r, e := session.Get(ctx, pageURL)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if e = transport.Status(r.Status); e != nil {
		return domain.CampusPage{}, e
	}
	// GET binds this SP to the authenticated Tomcat session; no script runs.
	if len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid teaching SP page")
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	budget := 0
	total := -1
	for page := 1; page <= 10; page++ {
		form := url.Values{"xnm": {year}, "xqm": {xqm}, "_search": {"false"}, "queryModel.showCount": {"100"}, "queryModel.currentPage": {strconv.Itoa(page)}, "queryModel.sortName": {""}, "queryModel.sortOrder": {"asc"}, "time": {"1"}}
		if id == contracts.GradesCapability {
			form.Set("sfzgcj", "")
			form.Set("kcbj", "")
		} else if id == contracts.ExamsCapability {
			for _, key := range []string{"ksmcdmb_id", "kch", "kc", "ksrq", "kkbm_id"} {
				form.Set(key, "")
			}
		}
		r, e = session.Post(ctx, queryURL, "application/x-www-form-urlencoded", []byte(form.Encode()))
		if e != nil {
			return domain.CampusPage{}, e
		}
		budget += len(r.Body)
		if budget > 8<<20 {
			return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Student records exceed the snapshot budget")
		}
		items, count, e := parseRecordPage(r, id, q.ResourceRef, strings.TrimPrefix(account, "teaching:student:"), year, xqm, page)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if total < 0 {
			total = count
		} else if count != total {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Student record count changed during query")
		}
		if total > 1000 {
			return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Term records exceed the reviewed bound")
		}
		for _, row := range items {
			if seen[row.Ref] {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Repeated teaching records")
			}
			seen[row.Ref] = true
			rows = append(rows, row)
		}
		if len(rows) > total {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Teaching record count mismatch")
		}
		if len(rows) == total {
			break
		}
		if len(items) == 0 || page == 10 {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Teaching records were truncated")
		}
	}
	if id == contracts.GradesComponentsCapability {
		selected := []contracts.CampusRecord{}
		for _, r := range rows {
			if r.CourseCode == parts[4] {
				selected = append(selected, r)
			}
		}
		rows = selected
	}
	selected, continuation, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	return domain.CampusPage{AccountRef: actual, Rows: selected, Next: continuation, Coverage: rules.Coverage}, nil
}

func pageNumber(raw json.RawMessage) (int, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return 0, false
		}
		value = n.String()
	}
	i, e := strconv.Atoi(value)
	return i, e == nil && i >= 0 && strconv.Itoa(i) == value
}

func parseRecordPage(reply transport.Reply, id, resource, studentID, year, xqm string, page int) ([]contracts.CampusRecord, int, error) {
	fail := func() ([]contracts.CampusRecord, int, error) {
		return nil, 0, contracts.Fail("UPSTREAM_CHANGED", "Teaching student record response changed")
	}
	if e := transport.Status(reply.Status); e != nil {
		return nil, 0, e
	}
	media, _, e := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(reply.Body) > 2<<20 || !utf8.Valid(reply.Body) {
		return fail()
	}
	var envelope struct {
		Items []map[string]json.RawMessage `json:"items"`
		Total json.RawMessage              `json:"totalResult"`
		Page  json.RawMessage              `json:"currentPage"`
	}
	if strictjson.Decode(reply.Body, &envelope) != nil || envelope.Items == nil || len(envelope.Items) > 100 {
		return fail()
	}
	total, ok := pageNumber(envelope.Total)
	if !ok {
		return fail()
	}
	if len(envelope.Page) > 0 {
		current, ok := pageNumber(envelope.Page)
		if !ok || current != page {
			return fail()
		}
	}
	rows := make([]contracts.CampusRecord, 0, len(envelope.Items))
	for _, item := range envelope.Items {
		text := func(key string) (string, bool) {
			var value string
			raw, exists := item[key]
			if !exists || string(raw) == "null" {
				return "", true
			}
			if json.Unmarshal(raw, &value) != nil {
				return "", false
			}
			return value, safe(value)
		}
		for key, want := range map[string]string{"xh": studentID, "xnm": year, "xqm": xqm} {
			if _, exists := item[key]; exists {
				got, ok := text(key)
				if !ok || got != want {
					return fail()
				}
			}
		}
		title, ok := text("kcmc")
		if !ok || title == "" {
			return fail()
		}
		code, ok := text("kch")
		if !ok {
			return fail()
		}
		detail := &contracts.StudentRecord{}
		row := contracts.CampusRecord{ResourceRef: resource, Title: title, CourseCode: code, Student: detail}
		fields := map[string]*string{}
		prefix := "teaching:grade:"
		if id == contracts.GradesCapability {
			fields = map[string]*string{"cj": &detail.Grade, "xf": &detail.Credits, "jd": &detail.GradePoint, "xfjd": &detail.WeightedGradePoint, "kcxzmc": &detail.CourseNature, "cjbdsj": &detail.RecordedAt}
		} else if id == contracts.GradesComponentsCapability {
			prefix = "teaching:grade-component:"
			fields = map[string]*string{"xmblmc": &detail.ComponentName, "xmcj": &detail.ComponentGrade, "zpcj": &detail.Grade, "xf": &detail.Credits}
		} else {
			prefix = "teaching:exam:"
			fields = map[string]*string{"ksmc": &detail.ExamName, "kssj": &detail.ExamTime, "ksfs": &detail.ExamMethod, "cxbj": &detail.Makeup, "xf": &detail.Credits, "cdmc": &row.Location, "cdxqmc": &row.Campus}
		}
		for key, target := range fields {
			value, ok := text(key)
			if !ok {
				return fail()
			}
			*target = value
		}
		if (id == contracts.GradesCapability && detail.Credits == "") || (id == contracts.ExamsCapability && detail.ExamTime == "") || (id == contracts.GradesComponentsCapability && (detail.ComponentName == "" || code == "")) {
			return fail()
		}
		b, _ := json.Marshal(row)
		digest := sha256.Sum256(b)
		row.Ref = prefix + hex.EncodeToString(digest[:])
		rows = append(rows, row)
	}
	return rows, total, nil
}
