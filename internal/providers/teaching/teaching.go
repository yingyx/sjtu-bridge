// Package teaching reads the student's own regular-course timetable. It does
// not accept a student selector, arbitrary endpoint, or enrollment operation.
package teaching

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/campussession"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "i.sjtu.edu.cn"
const endpoint = "https://i.sjtu.edu.cn/kbcx/xskbcx_cxXsgrkb.html?gnmkdm=N2151"

type Service struct {
	Session campussession.Service
	Now     func() time.Time
}

func Allowed(u *url.URL) bool {
	return campussession.LoginAllowed(u, Host) || (transport.ExactHTTPS(u, Host) && u.String() == endpoint)
}
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure("jaccount.sjtu.edu.cn", campussession.AuthAllowed)
	return &Service{Session: campussession.Service{Host: Host, Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: p, Auth: a}, Now: time.Now}, func() { p.Close(); a.Close() }
}

var student = regexp.MustCompile(`^[a-zA-Z0-9]{4,32}$`)

func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	now = now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	year := now.Year()
	term := "1"
	if now.Month() < 9 {
		year--
		if now.Month() != 1 {
			term = "2"
		}
	}
	_, account, e := s.fetch(ctx, secret, "teaching:term:"+strconv.Itoa(year)+":"+term)
	return account, e
}
func safe(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsAny(s, "\x00\r\n")
}
func (s *Service) fetch(ctx context.Context, secret []byte, resource string) ([]contracts.CampusRecord, string, error) {
	if !contracts.ValidCampusResource(contracts.TimetableCapability, resource) {
		return nil, "", contracts.Fail("INVALID_ARGUMENT", "Choose a valid academic term resource")
	}
	parts := strings.Split(resource, ":")
	year, term := parts[2], parts[3]
	xqm := "3"
	if term == "2" {
		xqm = "12"
	}
	session, e := s.Session.Login(ctx, secret)
	if e != nil {
		return nil, "", e
	}
	form := url.Values{"xnm": {year}, "xqm": {xqm}, "kzlx": {"ck"}, "xsdm": {""}}
	r, e := session.Post(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if e != nil {
		return nil, "", e
	}
	if e = transport.Status(r.Status); e != nil {
		return nil, "", e
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Expected authenticated teaching JSON")
	}
	return parse(r.Body, resource, year, xqm)
}
func parse(body []byte, resource, year, xqm string) ([]contracts.CampusRecord, string, error) {
	fail := func() ([]contracts.CampusRecord, string, error) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Teaching timetable identity or structure changed")
	}
	var v struct {
		Student *struct {
			ID   string `json:"XH"`
			Year string `json:"XNM"`
			Term string `json:"XQM"`
		} `json:"xsxx"`
		Rows []struct {
			Title    string          `json:"kcmc"`
			Code     string          `json:"kch"`
			Location string          `json:"cdmc"`
			Campus   string          `json:"xqmc"`
			Day      json.RawMessage `json:"xqj"`
			Periods  string          `json:"jcs"`
			Weeks    string          `json:"zcd"`
		} `json:"kbList"`
	}
	if json.Unmarshal(body, &v) != nil || v.Student == nil || !student.MatchString(v.Student.ID) || v.Student.Year != year || v.Student.Term != xqm || v.Rows == nil || len(v.Rows) > 500 {
		return fail()
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, r := range v.Rows {
		day := ""
		if json.Unmarshal(r.Day, &day) != nil {
			var d int
			if json.Unmarshal(r.Day, &d) != nil {
				return fail()
			}
			day = strconv.Itoa(d)
		}
		if r.Title == "" || len(day) != 1 || day < "1" || day > "7" || r.Periods == "" || r.Weeks == "" {
			return fail()
		}
		for _, s := range []string{r.Title, r.Code, r.Location, r.Campus, r.Periods, r.Weeks} {
			if !safe(s) {
				return fail()
			}
		}
		row := contracts.CampusRecord{ResourceRef: resource, Title: r.Title, CourseCode: r.Code, Location: r.Location, Campus: r.Campus, Weekday: day, Periods: r.Periods, Weeks: r.Weeks}
		b, _ := json.Marshal(row)
		digest := sha256.Sum256(b)
		row.Ref = "teaching:meeting:" + hex.EncodeToString(digest[:])
		if seen[row.Ref] {
			return fail()
		}
		seen[row.Ref] = true
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ref < rows[j].Ref })
	return rows, "teaching:student:" + v.Student.ID, nil
}

// Read refetches the same bounded term, verifies its identity, and binds offset
// pagination to the returned snapshot. No personal response is cached.
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.AccountRef != account || q.View != "schedule_v1" || q.Query != "" || q.Limit < 1 || q.Limit > 100 || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected an own-account fixed-view timetable query")
	}
	rows, actual, e := s.fetch(ctx, secret, q.ResourceRef)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Teaching account changed")
	}
	b, _ := json.Marshal(rows)
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	offset := 0
	if next != "" {
		parts := strings.Split(next, ":")
		if len(parts) != 2 || parts[1] != digest {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Timetable changed; restart query")
		}
		offset, e = strconv.Atoi(parts[0])
		if e != nil || offset < 1 || offset >= len(rows) {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid teaching continuation")
		}
	}
	end := min(offset+q.Limit, len(rows))
	page := domain.CampusPage{AccountRef: actual, Rows: rows[offset:end], Coverage: "regular_courses_only"}
	if end < len(rows) {
		page.Next = strconv.Itoa(end) + ":" + digest
	}
	return page, nil
}
