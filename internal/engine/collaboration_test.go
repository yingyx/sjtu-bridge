package engine

import (
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestCollaborationProjectionRejectsCrossScopeAndStripsText(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "transcribe:self"}
	r := contracts.CampusRecord{Ref: "transcribe:task:task1", ResourceRef: q.ResourceRef, Title: "合成.mp3", Location: "PRIVATE_LOCATION", Collaboration: &contracts.CollaborationRecord{Kind: "transcription", Status: "completed", StartAt: "2026-10-26T09:00:00+08:00", OriginalText: "PRIVATE_TRANSCRIPT", JoinURL: "https://other.invalid"}}
	// Even a malformed upstream adapter cannot smuggle transcript body or other
	// business fields into the metadata-only application grant.
	r.Collaboration.JoinURL = ""
	p, e := projectCollaborationRecord(contracts.TranscriptionListCapability, q, r)
	if e != nil || p.Collaboration.OriginalText != "" || p.Location != "" {
		t.Fatal("metadata grant expanded", e)
	}
	q.ResourceRef = "transcribe:task:task1"
	r.ResourceRef = q.ResourceRef
	r.Collaboration.OriginalText = "第一行\n第二行"
	p, e = projectCollaborationRecord(contracts.TranscriptionGetCapability, q, r)
	if e != nil || p.Collaboration.OriginalText != "第一行\n第二行" {
		t.Fatal(e)
	}
	r.Ref = "transcribe:task:other"
	if _, e = projectCollaborationRecord(contracts.TranscriptionGetCapability, q, r); e == nil {
		t.Fatal("foreign task passed projector")
	}
	r.Ref = q.ResourceRef
	r.Collaboration.OriginalText = strings.Repeat("x", 65537)
	if _, e = projectCollaborationRecord(contracts.TranscriptionGetCapability, q, r); e == nil {
		t.Fatal("overlong result passed projector")
	}
}
func TestCollaborationProjectionDoesNotAliasCounts(t *testing.T) {
	n := 2
	q := contracts.CampusQuery{ResourceRef: "myportal:messages:unread"}
	r := contracts.CampusRecord{Ref: q.ResourceRef, ResourceRef: q.ResourceRef, Title: "未读", Collaboration: &contracts.CollaborationRecord{Kind: "unread", Count: &n}}
	p, e := projectCollaborationRecord(contracts.MessageUnreadCapability, q, r)
	if e != nil {
		t.Fatal(e)
	}
	n = 9
	if *p.Collaboration.Count != 2 {
		t.Fatal("mutable provider pointer leaked")
	}
}
