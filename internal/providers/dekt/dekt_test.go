package dekt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const start = "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id=" + clientID + "&scope=basic+essential&response_type=code&redirect_uri=http%3A%2F%2Fdektcjd.sjtu.edu.cn%2Fui%2Fmanage&state=SYNTHETIC_STATE"
const loginURL = "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC_CLIENT&returl=SYNTHETIC_RETURN&se=SYNTHETIC_SE"
const ticket = "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC_CONTEXT&jatkt=SYNTHETIC_TICKET"
const returnURL = "http://dektcjd.sjtu.edu.cn/ui/manage?code=SYNTHETIC_CODE&state=SYNTHETIC_STATE"

type fake struct {
	t             *testing.T
	account       string
	entities      map[string]string
	calls         []string
	identityCalls int
	alter         func(*url.URL, transport.Reply) transport.Reply
}

func entity(modules []module, first string) string {
	v := map[string]json.RawMessage{}
	for _, m := range modules {
		v[m.key] = json.RawMessage(`[]`)
	}
	if first != "" {
		v[modules[0].key] = json.RawMessage("[" + first + "]")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls = append(f.calls, u.Host+u.Path)
	if u.Scheme != "https" || h.Get("Authorization") != "" {
		f.t.Fatal("non-HTTPS or unreviewed authorization")
	}
	r := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}}
	if u.Host == Host {
		if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
			f.t.Fatal("unreviewed portal request or root credential leak")
		}
		if u.Path == entryPath {
			r.Status = 302
			if u.Query().Get("code") == "" {
				r.Header.Set("Location", start)
				r.Header.Add("Set-Cookie", "JSESSIONID=SYNTHETIC_PORTAL; Path=/; HttpOnly")
			} else {
				if !strings.Contains(h.Get("Cookie"), "JSESSIONID=SYNTHETIC_PORTAL") {
					f.t.Fatal("anonymous portal session was lost")
				}
				r.Header.Set("Location", "http://"+Host+entryPath)
			}
		} else if u.Path == "/basicInformation/getBasic" {
			f.identityCalls++
			r.Body = []byte(`{"errno":0,"entities":[{"userId":` + strconvQuote(f.account) + `,"name":"MUST_NOT_RETURN","politicalStatus":"MUST_NOT_RETURN","classNo":"MUST_NOT_RETURN"}]}`)
		} else {
			r.Body = []byte(`{"errno":0,"entities":[` + f.entities[u.Path] + `]}`)
		}
	} else {
		if !AuthAllowed(u) || strings.Contains(h.Get("Cookie"), "JSESSIONID=SYNTHETIC_PORTAL") {
			f.t.Fatal("unreviewed authentication destination or portal cookie leak")
		}
		r.Status = 302
		switch {
		case initialAuthorize(u):
			r.Header.Set("Location", loginURL)
		case sso.OAuthLogin(u):
			if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_ROOT") {
				f.t.Fatal("root absent from reviewed login")
			}
			r.Header.Set("Location", ticket)
		default:
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
				f.t.Fatal("root sent outside reviewed login")
			}
			r.Header.Set("Location", returnURL)
		}
	}
	if f.alter != nil {
		r = f.alter(u, r)
	}
	return r, nil
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func setup(t *testing.T) (*Service, *fake, contracts.CampusQuery) {
	f := &fake{t: t, account: "SYNTHETIC_USER", entities: map[string]string{
		"/personalHonor/getHonor":        entity(honors, `{"id":7,"applyType":"合成奖学金","grantUnit":"合成单位","schoolYear":"2025-2026","level":"校级","amount":"MUST_NOT_RETURN","userid":"SYNTHETIC_USER","dataSource":"个人新增"}`),
		"/voluntaryPracticeInfo/getInfo": entity(practice, `{"id":8,"projectName":"合成公益","begintime":"2026-10-20","endtime":"2026-10-21","duration":"1.5小时"}`),
		"/student/education/labor":       entity(labor, `{"id":9,"activityName":"合成劳动","laborTypeDetail":"合成类型","duration":1.5,"activeStartTime":"2026-10-20","activeEndTime":"2026-10-21"}`),
		"/student/education/noLabor":     entity(education, `{"id":10,"activityName":"合成讲座","activeStartTime":"2026-10-21"}`),
	}}
	s := &Service{Portal: f, Auth: f}
	return s, f, contracts.CampusQuery{AccountRef: "dekt:user:SYNTHETIC_USER", ResourceRef: "dekt:honors:self", View: "honors_v1", Limit: 1}
}

func TestIdentityAndFixedHTTPSCallback(t *testing.T) {
	s, f, q := setup(t)
	got, err := s.Identity(context.Background(), []byte("SYNTHETIC_ROOT"))
	if err != nil || got != q.AccountRef || f.identityCalls != 1 {
		t.Fatal("identity", got, err)
	}
	for _, raw := range []string{
		"http://dektcjd.sjtu.edu.cn/ui/manage?code=SYNTHETIC&state=OTHER",
		"http://dektcjd.sjtu.edu.cn:80/ui/manage?code=SYNTHETIC&state=SYNTHETIC_STATE",
		"http://dektcjd.sjtu.edu.cn/other?code=SYNTHETIC&state=SYNTHETIC_STATE",
		"http://evil.invalid/ui/manage?code=SYNTHETIC&state=SYNTHETIC_STATE",
		returnURL + "&extra=1", returnURL + "&code=DUPLICATE", returnURL + "#fragment",
	} {
		u, _ := url.Parse(raw)
		if _, ok := callback(u, "SYNTHETIC_STATE"); ok {
			t.Fatal("unsafe callback accepted", raw)
		}
	}
	for _, raw := range []string{"http://" + Host + entryPath, rootURL + entryPath} {
		u, _ := url.Parse(raw)
		if !cleanCallback(u) {
			t.Fatal("fixed callback cleanup rejected")
		}
	}
	for _, raw := range []string{"http://evil.invalid/ui/manage", "http://" + Host + "/other", "http://" + Host + entryPath + "?", "http://" + Host + ":80" + entryPath, "http://" + Host + entryPath + "#fragment", "http://" + Host + entryPath + "?code=SYNTHETIC"} {
		u, _ := url.Parse(raw)
		if cleanCallback(u) {
			t.Fatal("unsafe callback cleanup accepted", raw)
		}
	}
}

func TestReadProjectionAndSnapshot(t *testing.T) {
	s, f, q := setup(t)
	f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], `"studentScholarshipVos":[`, `"studentScholarshipVos":[{"id":6,"applyType":"另一个合成奖学金"},`, 1)
	f.entities["/personalHonor/getHonor"] = strings.TrimSuffix(f.entities["/personalHonor/getHonor"], "}") + `,"partyHonorVos":[{"awardName":"MUST_NOT_RETURN"}]}`
	p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if err != nil || len(p.Rows) != 1 || p.Next == "" || !strings.HasPrefix(p.Rows[0].Ref, "dekt:record:") || len(p.Rows[0].Ref) != 76 {
		t.Fatal("page", p, err)
	}
	next, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, p.Next)
	if err != nil || len(next.Rows) != 1 || next.Next != "" || next.Rows[0].Module != "scholarship" || next.Rows[0].DataSource != "个人新增" {
		t.Fatal("next", next, err)
	}
	raw, _ := json.Marshal(next.Rows)
	for _, forbidden := range []string{"MUST_NOT_RETURN", "SYNTHETIC_ROOT", "SYNTHETIC_USER", "politicalStatus", "amount", "userid", "isPrint", "SYNTHETIC_CODE"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("unexpected data returned", forbidden)
		}
	}
	f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], "合成单位", "变更单位", 1)
	if _, err = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, p.Next); err == nil {
		t.Fatal("changed snapshot accepted")
	}
}

func TestEducationAndPractice(t *testing.T) {
	for _, group := range []string{"education", "practice"} {
		t.Run(group, func(t *testing.T) {
			s, _, q := setup(t)
			q.ResourceRef = "dekt:" + group + ":self"
			q.View = group + "_v1"
			q.Limit = 50
			p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
			if err != nil || len(p.Rows) == 0 || p.Rows[0].Duration != "1.5" {
				t.Fatal("read", p, err)
			}
			if group == "practice" && p.Rows[0].DurationUnit != "小时" || group == "education" && (p.Rows[0].DurationUnit != "学时" || len(p.Rows) != 2) {
				t.Fatal("duration or module coverage", p)
			}
		})
	}
}

func TestEmptyAndMissingModules(t *testing.T) {
	s, f, q := setup(t)
	f.entities["/personalHonor/getHonor"] = entity(honors, "")
	p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if err != nil || p.Rows == nil || len(p.Rows) != 0 {
		t.Fatal("empty", p, err)
	}
	for _, body := range []string{`{}`, `{"studentScholarshipVos":null}`, strings.Replace(entity(honors, ""), `"studentLeaderVos":[]`, `"studentLeaderVos":null`, 1)} {
		f.entities["/personalHonor/getHonor"] = body
		if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); err == nil {
			t.Fatal("incomplete record modules accepted")
		}
	}
}

func TestOwnerAndIdentityChanges(t *testing.T) {
	for _, when := range []string{"before", "after", "row"} {
		t.Run(when, func(t *testing.T) {
			s, f, q := setup(t)
			if when == "row" {
				f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], `"userid":"SYNTHETIC_USER"`, `"userid":"SYNTHETIC_OTHER"`, 1)
			}
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				if u.Path == "/basicInformation/getBasic" && (when == "before" || when == "after" && f.identityCalls == 2) {
					r.Body = []byte(`{"errno":0,"entities":[{"userId":"SYNTHETIC_OTHER"}]}`)
				}
				return r
			}
			if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); err == nil {
				t.Fatal("owner changed")
			}
			if when == "before" {
				for _, path := range f.calls {
					if strings.Contains(path, "getHonor") {
						t.Fatal("business read before identity verified")
					}
				}
			}
		})
	}
}

func TestRejectedScopeAndRoutes(t *testing.T) {
	for _, alter := range []func(*contracts.CampusQuery){func(q *contracts.CampusQuery) { q.AccountRef += "OTHER" }, func(q *contracts.CampusQuery) { q.ResourceRef = "dekt:honors:other" }, func(q *contracts.CampusQuery) { q.View = "unknown" }, func(q *contracts.CampusQuery) { q.Limit = 51 }, func(q *contracts.CampusQuery) { q.Query = "other" }} {
		s, f, q := setup(t)
		account := q.AccountRef
		alter(&q)
		if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), account, q, ""); err == nil || len(f.calls) != 0 {
			t.Fatal("invalid scope reached network")
		}
	}
	for _, path := range []string{"/update/print", "/update/sort", "/delete/basicInfo", "/information/export", "/url/return", "/authPhotoUrl/getPhoto", "/basicInformation/getBasic?userId=OTHER", "/student/education/labor?", "/personalHonor/getHonor#fragment"} {
		u, _ := url.Parse(rootURL + path)
		if Allowed(u) {
			t.Fatal("unreviewed route", path)
		}
	}
}

func TestMalformedAndUnreviewedLogin(t *testing.T) {
	for _, stage := range []string{"state", "host", "path", "login", "body", "identity", "duplicate", "media", "errno"} {
		t.Run(stage, func(t *testing.T) {
			s, f, q := setup(t)
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				if sso.TicketAuthorize(u) {
					switch stage {
					case "state":
						r.Header.Set("Location", strings.Replace(returnURL, "SYNTHETIC_STATE", "OTHER", 1))
					case "host":
						r.Header.Set("Location", strings.Replace(returnURL, Host, "evil.invalid", 1))
					case "path":
						r.Header.Set("Location", strings.Replace(returnURL, entryPath, "/other", 1))
					}
				}
				if stage == "login" && sso.OAuthLogin(u) {
					r.Status = 200
					r.Body = []byte("MUST_NOT_RETURN")
				}
				if stage == "identity" && u.Path == "/basicInformation/getBasic" {
					r.Body = []byte(`{"errno":0,"entities":[{"name":"MUST_NOT_RETURN"}]}`)
				}
				if u.Path == "/personalHonor/getHonor" {
					switch stage {
					case "body":
						r.Body = []byte(`{"errno":0,"entities":[]}`)
					case "duplicate":
						r.Body = []byte(`{"errno":0,"ERRNO":0,"entities":[{}]}`)
					case "media":
						r.Header.Set("Content-Type", "text/html")
					case "errno":
						r.Body = []byte(`{"errno":10004,"error":"MUST_NOT_RETURN"}`)
					}
				}
				return r
			}
			if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); err == nil || strings.Contains(err.Error(), "MUST_NOT_RETURN") {
				t.Fatal("unsafe error", err)
			}
		})
	}
}

func TestInvalidDurationAndDuplicateRecords(t *testing.T) {
	for _, row := range []string{`{"id":1,"projectName":"合成","duration":"-1"}`, `{"id":1,"projectName":"合成","duration":"1到2小时"}`, `{"id":1,"projectName":"合成","duration":{}}`, `{"id":1,"projectName":"合成"},{"id":1,"projectName":"合成"}`} {
		_, err := project(json.RawMessage(entity(practice, row)), practice, "dekt:user:SYNTHETIC_USER", "dekt:practice:self")
		if err == nil {
			t.Fatal("malformed duration or duplicate accepted")
		}
	}
}

func TestReferencesRemainScopedWithoutSourceID(t *testing.T) {
	rows, err := project(json.RawMessage(entity(practice, `{"projectName":"合成条目"}`)), practice, "dekt:user:SYNTHETIC_USER", "dekt:practice:self")
	if err != nil || len(rows) != 1 || len(rows[0].Ref) != 76 {
		t.Fatal("missing source id did not produce a bounded reference", err)
	}
	s, _, q := setup(t)
	for _, cursor := range []string{"0:" + strings.Repeat("a", 64), "1:" + strings.Repeat("z", 64), "1:short", "10000:" + strings.Repeat("a", 64)} {
		if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, cursor); err == nil {
			t.Fatal("malformed continuation accepted")
		}
	}
}

func TestPracticeTypeAndLaborTypeRemainSourceLabels(t *testing.T) {
	raw := json.RawMessage(`{"studentVoluntaryPublicWelfareVos":[],"studentSocialPracticeVos":[{"id":1,"practiceName":"合成实践","practiceType":"合成类型","role":"成员"}]}`)
	rows, err := project(raw, practice, "dekt:user:SYNTHETIC_USER", "dekt:practice:self")
	if err != nil || len(rows) != 1 || rows[0].Level != "合成类型" || rows[0].Role != "成员" {
		t.Fatal("source practice type dropped", err)
	}
}

func TestRealEnvelopeTotalAndUnavailableHonorModule(t *testing.T) {
	s, f, q := setup(t)
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if r.Status == 200 {
			r.Body = []byte(strings.Replace(string(r.Body), `"errno":0`, `"errno":0,"total":0`, 1))
		}
		return r
	}
	f.entities["/personalHonor/getHonor"] = `{"studentScholarshipVos":[],"studentKechuangAwardVos":[],"studentLeaderVos":[],"totalScholarshipNo":0,"totalKechuangAwardNo":0,"totalStudentLeaderNo":0,"totalSocialPracticeNo":0,"totalStylisticArtsNo":0,"totalVolunteeringNo":0}`
	p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if err != nil || p.Rows == nil || len(p.Rows) != 0 || len(p.UnavailableModules) != 1 || p.UnavailableModules[0] != "outstanding_graduate" {
		t.Fatal("source-unavailable module hidden as empty", p, err)
	}
	f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], `"totalVolunteeringNo":0`, `"totalVolunteeringNo":1`, 1)
	if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); err == nil {
		t.Fatal("missing nonzero honor array accepted as empty")
	}
}

func TestUnavailableModulesBindSnapshot(t *testing.T) {
	s, f, q := setup(t)
	f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], `"studentScholarshipVos":[`, `"studentScholarshipVos":[{"id":6,"applyType":"合成第二条"},`, 1)
	p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if err != nil || p.Next == "" {
		t.Fatal("first page", err)
	}
	f.entities["/personalHonor/getHonor"] = strings.Replace(f.entities["/personalHonor/getHonor"], `"outstandingGraduatesVos":[],`, "", 1)
	if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, p.Next); err == nil {
		t.Fatal("unavailable-module change did not invalidate snapshot")
	}
}
