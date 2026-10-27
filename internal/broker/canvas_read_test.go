package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

type canvasReaderFake struct {
	domain.Canvas
	brokerContentSource
	read func(string, contracts.CanvasQuery, []string, string) (domain.CanvasPage, error)
}

func (f *canvasReaderFake) ReadCanvas(_ context.Context, _ []byte, account, id string, q contracts.CanvasQuery, courses []string, next string) (domain.CanvasPage, error) {
	return f.read(id, q, courses, next)
}

func TestCanvasReadFamilyThroughBrokerAndFixedViews(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	calls := 0
	fake := &canvasReaderFake{Canvas: h.fake}
	fake.read = func(id string, q contracts.CanvasQuery, courses []string, next string) (domain.CanvasPage, error) {
		calls++
		r, _ := contracts.CanvasRules(id)
		if len(courses) != 1 || courses[0] != "canvas:course:1" {
			t.Fatal("scope not copied from grant")
		}
		body := "合成课程正文"
		truncated := false
		kind := r.Kind
		if kind == "todo" {
			kind = "assignment"
		}
		ref := "canvas:" + kind + ":9"
		if r.Kind == "course" {
			ref = "canvas:course:1"
		}
		row := contracts.CanvasRecord{CourseRef: "canvas:course:1", Ref: ref, Title: "合成课程项", URL: "https://oc.sjtu.edu.cn/courses/1", Body: &body, Truncated: &truncated, Code: "SYNTHETIC_UNSCOPED_CODE"}
		if id == "canvas.submissions.feedback" {
			grade, score := "A", 95.0
			row.Grade, row.Score = &grade, &score
			row.Attachments = []contracts.CanvasAttachment{{ID: "11", Name: "synthetic.txt", Size: 3, MIMEType: "text/plain"}}
			row.Comments = []contracts.CanvasSubmissionComment{{ID: "12", Author: "合成教师", Body: "合成反馈", CreatedAt: "2026-10-25T00:00:00Z"}}
		}
		out := domain.CanvasPage{Rows: []contracts.CanvasRecord{row}}
		if r.List && next == "" {
			out.Next = "SYNTHETIC_NEXT"
		}
		return out, nil
	}
	h.b.Engine.Provider = fake
	invoke := func(inv contracts.Invocation) ([]byte, error) {
		var result []byte
		e := h.b.invoke(ctx, h.a, inv, func(raw []byte) error { result = append([]byte{}, raw...); return nil })
		return result, e
	}
	for index, id := range contracts.CanvasCapabilityIDs() {
		t.Run(id, func(t *testing.T) {
			r, _ := contracts.CanvasRules(id)
			_, e := h.b.Manage(ctx, AdminRequest{Action: "grant", ClientID: "a", CredentialID: h.cred, AccountRef: "canvas:user:7", Capability: id, Courses: []string{"canvas:course:1"}, View: r.View, Limit: 2})
			if e != nil {
				t.Fatal("grant", e)
			}
			caps, e := h.b.Engine.Capabilities(h.a)
			found := false
			for _, cap := range caps {
				found = found || cap.ID == id
			}
			if e != nil || len(caps) != index+2 || !found {
				t.Fatal("discovery", e)
			}
			q := contracts.CanvasQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:1", View: r.View, Limit: 2}
			if r.Directory {
				q.CourseRef = ""
			}
			if r.Item {
				q.ItemID = "9"
			}
			if r.Module {
				q.ModuleID = "5"
			}
			if id == contracts.CanvasRecordingContent {
				q.MediaKind, q.Index = "video", 1
			}
			if r.Dates {
				q.StartDate = "2030-01-01"
				q.EndDate = "2030-01-31"
			}
			raw, _ := json.Marshal(q)
			v, e := invoke(contracts.Invocation{Capability: id, Input: raw})
			if e != nil {
				t.Fatal("invoke", e)
			}
			if id == contracts.CanvasFileContent || id == contracts.CanvasRecordingContent {
				var d contracts.ContentDescriptor
				if json.Unmarshal(v, &d) != nil || !d.Valid() {
					t.Fatal("content descriptor", string(v))
				}
				request, _ := json.Marshal(contracts.ContentReadRequest{Ref: d.Ref, Version: d.Version, Offset: 0, Length: 3})
				req := httptest.NewRequest(http.MethodPost, "/v1/content/read", bytes.NewReader(request))
				req.Host = "bridge.local"
				req.Header.Set("Authorization", "Bearer "+h.a)
				recorder := httptest.NewRecorder()
				h.b.Handler().ServeHTTP(recorder, req)
				if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), []byte{0, 255, 3}) {
					t.Fatal("content channel", recorder.Code, recorder.Body.String())
				}
				q.CourseRef = "canvas:course:2"
				raw, _ = json.Marshal(q)
				if _, e := invoke(contracts.Invocation{Capability: id, Input: raw}); e == nil {
					t.Fatal("ungranted content course")
				}
				return
			}
			var out contracts.CanvasResponse
			if json.Unmarshal(v, &out) != nil || len(out.Data.Items) != 1 {
				t.Fatal("output")
			}
			bodyAllowed := false
			for _, field := range r.Fields() {
				bodyAllowed = bodyAllowed || field == "body"
			}
			if !bodyAllowed && strings.Contains(string(v), "合成课程正文") {
				t.Fatal("body leaked from index capability")
			}
			if id == "canvas.submissions.feedback" && (out.Data.Items[0].Grade == nil || out.Data.Items[0].Score == nil || len(out.Data.Items[0].Comments) != 1 || len(out.Data.Items[0].Attachments) != 1) {
				t.Fatal("feedback fields lost")
			}
			if r.Kind != "course" && strings.Contains(string(v), "SYNTHETIC_UNSCOPED_CODE") {
				t.Fatal("unreviewed provider field leaked")
			}
			if r.List {
				if !out.Meta.HasMore || out.Meta.NextCursor == nil {
					t.Fatal("lost pagination")
				}
				q.Cursor = *out.Meta.NextCursor
				if r.Module {
					q.ModuleID = "6"
					raw, _ = json.Marshal(q)
					before := calls
					if _, e := invoke(contracts.Invocation{Capability: id, Input: raw}); e == nil || calls != before {
						t.Fatal("cursor not bound to module")
					}
					q.ModuleID = "5"
				}
				raw, _ = json.Marshal(q)
				if _, e := invoke(contracts.Invocation{Capability: id, Input: raw}); e != nil {
					t.Fatal("continuation", e)
				}
			}
			if !r.Directory {
				q.Cursor = ""
				q.CourseRef = "canvas:course:2"
				raw, _ = json.Marshal(q)
				before := calls
				if _, e := invoke(contracts.Invocation{Capability: id, Input: raw}); e == nil || calls != before {
					t.Fatal("ungranted course reached provider")
				}
			}
		})
	}
}
