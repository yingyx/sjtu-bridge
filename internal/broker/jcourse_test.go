package broker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func TestJCourseKeyUsesSharedGrantButCannotBecomeCampusRoot(t *testing.T) {
	h := setup(t)
	f := &campusFake{account: "jcourse:user:7", read: func(q contracts.CampusQuery) domain.CampusPage {
		return domain.CampusPage{AccountRef: q.AccountRef, Coverage: "jcourse_teachers_only", Rows: []contracts.CampusRecord{{ResourceRef: q.ResourceRef, Ref: "jcourse:teacher:8", Title: "合成教师", Community: &contracts.CommunityRecord{Teacher: &contracts.JCourseTeacher{Ref: "jcourse:teacher:8", Name: "合成教师", Department: "院系"}, Semester: "MUST_NOT_LEAK"}, Sender: "MUST_NOT_LEAK"}}}
	}}
	h.b.Engine.CampusProviders = map[string]domain.Campus{"jcourse": f, "bath": &campusFake{account: "bath:user:synthetic"}}
	value, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "jcourse", Secret: []byte("SYNTHETIC_KEY"), ExpectedAccount: f.account, Label: "main"})
	if e != nil {
		t.Fatal(e)
	}
	c := value.(storage.Credential)
	if c.Kind != "JCourseAPIKey" {
		t.Fatal("wrong key kind")
	}
	if e := h.b.Store.CheckSourceCredential(c); e == nil {
		t.Fatal("API key accepted as JAAuthCookie source")
	}
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "verify", Provider: "bath", SourceCredentialID: c.ID}); e == nil {
		t.Fatal("API key reused for campus SSO")
	}
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "verify", Provider: "jcourse", SourceCredentialID: h.cred}); e == nil {
		t.Fatal("Canvas key reused for jCourse")
	}
	id := contracts.JCoursePrefix + "teachers"
	q := contracts.CampusQuery{AccountRef: c.AccountRef, ResourceRef: "jcourse:teachers:catalog", View: "teachers_v1", Limit: 1}
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: c.ID, AccountRef: c.AccountRef, Capability: id, Resources: []string{q.ResourceRef}, View: q.View, Limit: 1}); e != nil {
		t.Fatal(e)
	}
	raw, e := invokeCampusTest(h.b, h.a, id, q)
	if e != nil || strings.Contains(string(raw), "MUST_NOT_LEAK") {
		t.Fatal("fixed projection or grant failed", e)
	}
	before := f.calls.Load()
	input, _ := json.Marshal(q)
	var m map[string]any
	_ = json.Unmarshal(input, &m)
	m["code"] = "OTHER"
	input, _ = json.Marshal(m)
	e = h.b.invoke(context.Background(), h.a, contracts.Invocation{Capability: id, Input: input}, func([]byte) error { t.Fatal("unexpected output"); return nil })
	if e == nil || f.calls.Load() != before {
		t.Fatal("unreviewed selector reached provider")
	}
	// The generic checked credential path introduced for API keys still denies a
	// revoked generation; shared grant/revocation tests cover the publication fence.
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: c.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.b.Store.CheckedCredentialSecret(c); e == nil {
		t.Fatal("revoked key decrypted")
	}
}
