// Package mlearning reads a student's own course attendance. Sign/scan and
// other attendance writes are deliberately not transport destinations.
package mlearning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "mlearning.sjtu.edu.cn"
const origin = "https://" + Host
const callbackPath = "/lms-auth-sjtu/auth/oauth2/mobile/callback"
const selfPath = "/lms-canvas-sjtu/users/self"
const recordPath = "/lms-lti-rollcall-sjtu/sign/records"
const statusPath = "/lms-lti-rollcall-sjtu/rollcall/existentB"
const coursePath = "/lms-canvas-sjtu/course/"
const clientID = "pmZTZ9QZEAwXGLGheU99"

type Service struct{ Portal, Auth sso.OAuthHTTP }

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,15}$`)
var tokenValue = regexp.MustCompile(`^[a-zA-Z0-9._~+/-]+={0,2}$`)

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 4)
	return ok && len(q) == 4 && q.Get("client_id") == clientID && q.Get("scope") == "essential" && q.Get("response_type") == "code" && q.Get("redirect_uri") == origin+callbackPath
}
func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
}
func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 3)
	if !ok {
		return false
	}
	if u.Path == callbackPath {
		return len(q) >= 1 && len(q) <= 2 && q.Get("code") != "" && (len(q) == 1 || q.Get("state") != "")
	}
	if u.Path == selfPath {
		return len(q) == 0
	}
	if strings.HasPrefix(u.Path, coursePath) {
		return len(q) == 0 && numericID.MatchString(strings.TrimPrefix(u.Path, coursePath))
	}
	if u.Path == statusPath {
		return len(q) == 1 && numericID.MatchString(q.Get("courseCode"))
	}
	return u.Path == recordPath && len(q) == 3 && numericID.MatchString(q.Get("courseCode")) && q.Get("pageSize") == "500" && numericID.MatchString(q.Get("pageNum"))
}
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}
func (s *Service) login(ctx context.Context, secret []byte) (*sso.OAuthSession, string, string, error) {
	start := "https://" + sso.Host + "/oauth2/authorize?" + url.Values{"client_id": {clientID}, "scope": {"essential"}, "response_type": {"code"}, "redirect_uri": {origin + callbackPath}}.Encode()
	session, e := sso.OpenOAuth(ctx, secret, sso.OAuthConfig{Host: Host, Start: start, CallbackPath: callbackPath, Portal: s.Portal, Auth: s.Auth, PortalAllowed: Allowed, AuthAllowed: AuthAllowed})
	if e != nil {
		return nil, "", "", e
	}
	token, e := url.QueryUnescape(session.Cookie("token"))
	token = strings.Trim(token, "\"")
	if e != nil || len(token) > 8192 || !tokenValue.MatchString(token) {
		return nil, "", "", contracts.Fail("AUTH_REQUIRED", "Attendance portal did not issue a session token")
	}
	body, e := s.get(ctx, session, token, selfPath)
	if e != nil {
		return nil, "", "", e
	}
	var self struct {
		ID json.RawMessage `json:"id"`
	}
	if strictjson.Decode(body, &self) != nil {
		return nil, "", "", contracts.Fail("UPSTREAM_CHANGED", "Attendance identity structure changed")
	}
	id, ok := scalar(self.ID)
	if !ok {
		return nil, "", "", contracts.Fail("UPSTREAM_CHANGED", "Attendance stable identity missing")
	}
	return session, token, "mlearning:user:" + id, nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, _, account, e := s.login(ctx, secret)
	return account, e
}
func (s *Service) get(ctx context.Context, session *sso.OAuthSession, token, path string) ([]byte, error) {
	u, e := url.Parse(origin + path)
	if e != nil || !Allowed(u) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed attendance endpoint")
	}
	h := session.Header(u)
	h.Set("Authorization", token)
	h.Set("Accept", "application/json")
	r, e := s.Portal.Get(ctx, u, h)
	if e != nil {
		return nil, e
	}
	if e = transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid attendance JSON")
	}
	var v struct {
		Code json.RawMessage `json:"resultCode"`
		Body json.RawMessage `json:"body"`
	}
	if strictjson.Decode(r.Body, &v) != nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Attendance response structure changed")
	}
	code, ok := scalar(v.Code)
	if !ok {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Missing attendance response status")
	}
	if code == "10001" || code == "10005" {
		return nil, contracts.Fail("AUTH_REQUIRED", "Attendance session expired")
	}
	if code != "200" {
		return nil, contracts.Fail("PERMISSION_DENIED", "Attendance query was rejected")
	}
	if len(v.Body) == 0 || string(v.Body) == "null" {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Missing attendance body")
	}
	return v.Body, nil
}
func scalar(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return "", false
		}
		value = n.String()
	}
	return value, numericID.MatchString(value)
}

func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := contracts.AttendanceRecordsCapability
	if q.View == "attendance_status_v1" {
		id = contracts.AttendanceStatusCapability
	}
	rules, _ := contracts.StudentRecordRules(id)
	if q.AccountRef != account || !rules.ValidQuery(q) || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected an own-course attendance query")
	}
	session, token, actual, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Attendance account changed")
	}
	courseID := strings.TrimPrefix(q.ResourceRef, "mlearning:course:")
	body, e := s.get(ctx, session, token, coursePath+courseID)
	if e != nil {
		return domain.CampusPage{}, e
	}
	var course struct {
		ID      json.RawMessage `json:"courseCode"`
		Name    string          `json:"courseName"`
		Section struct {
			Role string `json:"roleCode"`
		} `json:"section"`
	}
	if strictjson.Decode(body, &course) != nil {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Attendance course structure changed")
	}
	actualCourse, ok := scalar(course.ID)
	if !ok || actualCourse != courseID || course.Section.Role != "StudentEnrollment" {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Attendance is limited to the caller's enrolled course")
	}
	if !contracts.ValidStudentRecordText(course.Name) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid attendance course title")
	}
	if id == contracts.AttendanceStatusCapability {
		body, e = s.get(ctx, session, token, statusPath+"?"+url.Values{"courseCode": {courseID}}.Encode())
		if e != nil {
			return domain.CampusPage{}, e
		}
		var state map[string]json.RawMessage
		if strictjson.Decode(body, &state) != nil || state == nil {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid rollcall status")
		}
		// The official read endpoint returns an empty object when idle. Only
		// this boolean is projected: no sign-in token or location is exposed.
		active := len(state) > 0
		row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "mlearning:status:" + courseID, Title: course.Name, Student: &contracts.StudentRecord{Active: &active}}
		return domain.CampusPage{AccountRef: actual, Rows: []contracts.CampusRecord{row}, Coverage: rules.Coverage}, nil
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for page := 1; page <= 4; page++ {
		body, e = s.get(ctx, session, token, recordPath+"?"+url.Values{"courseCode": {courseID}, "pageNum": {strconv.Itoa(page)}, "pageSize": {"500"}}.Encode())
		if e != nil {
			return domain.CampusPage{}, e
		}
		items, e := parseRecords(body, q.ResourceRef, course.Name)
		if e != nil {
			return domain.CampusPage{}, e
		}
		for _, row := range items {
			if seen[row.Ref] {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Repeated attendance record")
			}
			seen[row.Ref] = true
			rows = append(rows, row)
		}
		if len(items) < 500 {
			break
		}
		if page == 4 {
			return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Attendance record snapshot exceeds bound")
		}
	}
	selected, continuation, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	return domain.CampusPage{AccountRef: actual, Rows: selected, Next: continuation, Coverage: rules.Coverage}, nil
}

func parseRecords(body []byte, resource, title string) ([]contracts.CampusRecord, error) {
	var v struct {
		Rows []struct {
			Number  json.RawMessage `json:"rollcallNum"`
			State   string          `json:"state"`
			Updated string          `json:"updatedDate"`
		} `json:"dataList"`
	}
	if strictjson.Decode(body, &v) != nil || v.Rows == nil || len(v.Rows) > 500 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Attendance records changed")
	}
	rows := make([]contracts.CampusRecord, 0, len(v.Rows))
	for _, r := range v.Rows {
		number, ok := scalar(r.Number)
		if !ok || r.State == "" || !contracts.ValidStudentRecordText(r.State) || !contracts.ValidStudentRecordText(r.Updated) {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid attendance record")
		}
		row := contracts.CampusRecord{ResourceRef: resource, Title: title, Student: &contracts.StudentRecord{RollcallNumber: number, Status: r.State, UpdatedAt: r.Updated}}
		raw, _ := json.Marshal(row)
		sum := sha256.Sum256(raw)
		row.Ref = "mlearning:record:" + hex.EncodeToString(sum[:])
		rows = append(rows, row)
	}
	return rows, nil
}
