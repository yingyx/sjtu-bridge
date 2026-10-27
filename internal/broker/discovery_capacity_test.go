package broker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func largestCampusResources(t *testing.T, id string) []string {
	t.Helper()
	rules, _ := contracts.GrantRules(id)
	var schema struct {
		Properties map[string]struct {
			Enum  []string `json:"enum"`
			Const string   `json:"const"`
		} `json:"properties"`
	}
	rawSchema := contracts.CampusInputSchema(id)
	if contracts.IsPanWrite(id) {
		rawSchema = contracts.PanWriteInputSchema(id)
	}
	if json.Unmarshal(rawSchema, &schema) != nil {
		t.Fatal("invalid campus schema")
	}
	if values := schema.Properties["resource_ref"].Enum; len(values) != 0 {
		return values
	}
	if value := schema.Properties["resource_ref"].Const; value != "" {
		return []string{value}
	}
	resources := make([]string, rules.MaxResources)
	for i := range resources {
		switch {
		case contracts.IsPanWrite(id):
			resources[i] = "pan:directory:" + strings.Repeat("x", 110) + fmt.Sprint(i)
		case id == contracts.TimetableCapability || id == contracts.GradesCapability || id == contracts.ExamsCapability:
			resources[i] = fmt.Sprintf("teaching:term:%d:%d", 2099-i/2, i%2+1)
		case id == contracts.ElectricityBalanceCapability || id == contracts.ElectricityHistoryCapability:
			resources[i] = "electric:room:999999999999999999"
		case id == contracts.GradesComponentsCapability:
			resources[i] = fmt.Sprintf("teaching:grade-course:2099:1:%s%d", strings.Repeat("X", 61), i)
		case id == contracts.AttendanceRecordsCapability || id == contracts.AttendanceStatusCapability:
			resources[i] = fmt.Sprintf("mlearning:course:%d", 9999999999999999-i)
		case id == contracts.CalendarEventsCapability:
			resources[i] = "calendar:calendar:" + fmt.Sprint(i+1)
		case id == contracts.TranscriptionGetCapability:
			resources[i] = "transcribe:task:" + fmt.Sprint(i+1)
		case id == contracts.MeetingDetailsCapability:
			resources[i] = "meeting:record:" + fmt.Sprint(i+1)
		case id == contracts.MeetingBusyCapability:
			resources[i] = "meeting:group:" + fmt.Sprint(i+1)
		case id == contracts.MailSearchCapability || id == contracts.MailMessageCapability || id == contracts.MailAttachmentCapability:
			resources[i] = "mail:folder:" + fmt.Sprint(i+1)
		case id == contracts.ShuiyuanSearchCapability || id == contracts.ShuiyuanTopicsCapability || id == contracts.ShuiyuanTopicCapability || id == contracts.ShuiyuanPostsCapability:
			resources[i] = fmt.Sprintf("shuiyuan:category:%d", 9999999999-i)
		case contracts.JCourseOperation(id) == "teacher" || contracts.JCourseOperation(id) == "teacher_courses":
			resources[i] = fmt.Sprintf("jcourse:teacher:%d", 999999999999999999-i)
		case contracts.JCourseOperation(id) != "":
			resources[i] = fmt.Sprintf("jcourse:course:%d", 999999999999999999-i)
		default:
			t.Fatalf("add maximum-resource fixture for %s", id)
		}
	}
	if !rules.ValidGrant(rules.View, rules.MaxLimit, resources) {
		t.Fatalf("invalid capacity fixture: %s", id)
	}
	return resources
}

// One app can legitimately grant every current personal capability. Exercise
// complete scoped schemas with maximum resource counts, rather than summaries.
func TestDiscoveryCapacityCoversAllCurrentPersonalCapabilities(t *testing.T) {
	h := setup(t)
	h.b.Engine.Provider = &canvasReaderFake{Canvas: h.fake}
	h.b.Engine.CampusProviders = map[string]domain.Campus{}
	account := strings.Repeat("s", 128)
	credentials := map[string]storage.Credential{}
	grants := map[string]storage.Grant{}
	courses := make([]string, 100)
	for i := range courses {
		courses[i] = fmt.Sprintf("canvas:course:%d", 999999999999999999-i)
	}
	ids := append([]string{contracts.AssignmentsCapability}, contracts.CanvasCapabilityIDs()...)
	ids = append(ids, contracts.CampusCapabilityIDs()...)
	ids = append(ids, contracts.PanWriteIDs()...)
	for _, id := range ids {
		metadata, known := contracts.PersonalMetadata(id)
		if !known {
			t.Fatalf("missing metadata: %s", id)
		}
		credential, exists := credentials[metadata.Provider]
		if !exists {
			kind, _ := contracts.CredentialKind(metadata.Provider)
			credential = storage.Credential{ID: "synthetic-" + metadata.Provider, Provider: metadata.Provider, Kind: kind, AccountRef: account, Generation: 1, Active: true}
			if err := h.b.Store.Encrypt(credential, []byte("SYNTHETIC_CAPACITY_CREDENTIAL")); err != nil {
				t.Fatal(err)
			}
			credentials[metadata.Provider] = credential
			if metadata.Provider != "canvas" {
				h.b.Engine.CampusProviders[metadata.Provider] = &campusFake{account: account}
			}
		}
		grant := storage.Grant{ID: "synthetic-" + id, ClientID: "a", Capability: id, Version: "1", AccountRef: account, CredentialID: credential.ID, Generation: 1, Active: true, Expires: time.Now().Add(time.Hour)}
		if id == contracts.AssignmentsCapability {
			grant.Courses, grant.View, grant.Limit = courses, "summary_v1", 100
		} else if rules, known := contracts.CanvasRules(id); known {
			grant.Courses, grant.View, grant.Limit = courses, rules.View, 100
		} else {
			rules, _ := contracts.GrantRules(id)
			grant.Resources, grant.View, grant.Limit = largestCampusResources(t, id), rules.View, rules.MaxLimit
		}
		grants[grant.ID] = grant
	}
	if len(grants) > storage.MaxAppGrants {
		t.Fatal("capacity fixture exceeds supported app grant count")
	}
	if err := h.b.Store.Update(nil, func(s *storage.State) error {
		for _, credential := range credentials {
			s.Credentials[credential.ID] = credential
		}
		s.Grants = grants
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var size, count int
	if err := h.b.discover(h.a, func(caps []contracts.Capability) error {
		raw, err := json.Marshal(caps)
		size, count = len(raw), len(caps)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("full directory: %d bytes; %d personal + %d public capabilities", size, len(ids), len(h.b.public.Capabilities()))
	if count != len(ids)+len(h.b.public.Capabilities()) {
		t.Fatal("capacity fixture lost an authorized capability")
	}
	if size > contracts.MaxDiscoveryJSONBytes {
		t.Fatalf("complete scoped schemas need %d bytes; review discovery capacity", size)
	}
	w := request(h.b, "GET", "/v1/capabilities", h.a, nil)
	if w.Code != http.StatusOK || len(w.Body.Bytes()) != size {
		t.Fatalf("valid complete discovery exceeds endpoint budget: %d bytes, HTTP %d", size, w.Code)
	}
}

type fixedDiscovery struct {
	publicAPI
	caps []contracts.Capability
}

func (d *fixedDiscovery) Capabilities() []contracts.Capability {
	return append([]contracts.Capability(nil), d.caps...)
}

func TestDiscoveryEndpointHasIndependentBoundedBudget(t *testing.T) {
	h := setup(t)
	directory := &fixedDiscovery{publicAPI: h.b.public, caps: []contracts.Capability{{ID: "public.synthetic", Description: "x"}}}
	h.b.public = directory
	base := 0
	if err := h.b.discover(h.a, func(caps []contracts.Capability) error { raw, err := json.Marshal(caps); base = len(raw); return err }); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{contracts.MaxJSONBytes + 1, contracts.MaxDiscoveryJSONBytes, contracts.MaxDiscoveryJSONBytes + 1} {
		directory.caps[0].Description = strings.Repeat("x", size-base+1)
		w := request(h.b, "GET", "/v1/capabilities", h.a, nil)
		if size <= contracts.MaxDiscoveryJSONBytes {
			if w.Code != http.StatusOK || w.Body.Len() != size {
				t.Fatalf("discovery budget rejected %d bytes: HTTP %d", size, w.Code)
			}
		} else if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "RESPONSE_TOO_LARGE") {
			t.Fatal("oversized discovery published")
		}
	}
	for _, size := range []int{contracts.MaxJSONBytes, contracts.MaxJSONBytes + 1} {
		w := httptest.NewRecorder()
		writeJSON(w, strings.Repeat("x", size-2))
		if (w.Code == http.StatusOK) != (size <= contracts.MaxJSONBytes) {
			t.Fatal("regular JSON budget changed")
		}
	}
}
