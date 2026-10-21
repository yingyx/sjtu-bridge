package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestResultRenderingPreservesScopeStateAndContinuation(t *testing.T) {
	cursor := "synthetic-cursor"
	r := contracts.Response{
		SchemaVersion: "1", RequestID: "synthetic-request",
		Data:     contracts.Data{Items: []contracts.Item{{SourceID: "123", Title: "合成通知", URL: "https://example.invalid/notice", Content: &contracts.PublicContent{Text: "正文", Truncated: true}}}},
		Meta:     contracts.Meta{Completeness: "partial", Stale: true, Sources: []contracts.Source{{Provider: "synthetic", Coverage: "article_html", Status: "ok"}}, FetchedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), HasMore: true, NextCursor: &cursor},
		Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅指定公告。"}, {Code: "SOURCE_ATTACHMENT_UNREAD", Message: "附件未读取。"}},
	}
	var out, diag bytes.Buffer
	if err := writeResult(&out, &diag, r, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID：123", "本页 1 条", "部分范围", "过期", "读取时间", "还有下一页", "synthetic-cursor", "正文已截断", "仅指定公告", "article_html"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
	if !strings.Contains(diag.String(), "SOURCE_ATTACHMENT_UNREAD") || strings.Contains(diag.String(), "SOURCE_SCOPE") {
		t.Fatal(diag.String())
	}
	for _, verbose := range []bool{false, true} {
		out.Reset()
		diag.Reset()
		if err := writeResult(&out, &diag, r, true, verbose); err != nil {
			t.Fatal(err)
		}
		var decoded contracts.Response
		if json.Unmarshal(out.Bytes(), &decoded) != nil || !reflect.DeepEqual(r, decoded) || diag.Len() != 0 {
			t.Fatalf("JSON result changed: %s %s", out.String(), diag.String())
		}
		if verbose == (bytes.Count(out.Bytes(), []byte("\n")) == 1) {
			t.Fatal("wrong JSON indentation", out.String())
		}
	}
}

func TestPrivateTextAndEmptyResults(t *testing.T) {
	r := contracts.AssignmentResponse{SchemaVersion: "1", Data: contracts.AssignmentData{Items: []contracts.Assignment{{CourseRef: "synthetic-course", AssignmentRef: "synthetic-assignment"}}}, Meta: contracts.PageMeta{Provider: "canvas", Coverage: "complete"}}
	var out, diag bytes.Buffer
	raw, _ := json.Marshal(r)
	if err := writeResult(&out, &diag, json.RawMessage(raw), false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"作业 ID：synthetic-assignment", "课程：synthetic-course", "截止时间：未提供", "本页 1 条", "范围：complete"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	if strings.Contains(out.String(), "标题") {
		t.Fatal("fabricated restricted title", out.String())
	}
	out.Reset()
	empty := contracts.Response{Data: contracts.Data{Items: []contracts.Item{}}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{}}}
	if err := writeResult(&out, &diag, empty, false, false); err != nil || !strings.Contains(out.String(), "本页 0 条") || !strings.Contains(out.String(), "部分范围") {
		t.Fatal(err, out.String())
	}
}

func TestNavigationDefaultAndVerbose(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var out, diag bytes.Buffer
		args := []string{"life", "utilities", "navigate", "--limit", "1"}
		if verbose {
			args = append(args, "--verbose")
		}
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(diag.String())
		}
		if !strings.Contains(out.String(), "官网入口") || !strings.Contains(out.String(), "范围：") || !strings.Contains(out.String(), "next_cursor") {
			t.Fatal(out.String())
		}
		if strings.Contains(out.String(), "manual_only") != verbose || strings.Contains(out.String(), "未接入说明") != verbose {
			t.Fatal(out.String())
		}
	}
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("synthetic output failure") }

func TestOutputFailureAndTerminalControls(t *testing.T) {
	if err := writeResult(failingOutput{}, &bytes.Buffer{}, map[string]string{"status": "ok"}, false, false); err == nil {
		t.Fatal("ignored output failure")
	}
	var out bytes.Buffer
	if err := writeResult(&out, &bytes.Buffer{}, map[string]string{"title": "合成\x1b[2J文本"}, false, false); err != nil || strings.ContainsRune(out.String(), '\x1b') {
		t.Fatal(err, out.String())
	}
}

func TestVerbosePreservesDistinctSourcePages(t *testing.T) {
	r := contracts.Response{Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{
		{Provider: "synthetic", Coverage: "archive_page", Status: "ok", URL: "https://example.invalid/page/1"},
		{Provider: "synthetic", Coverage: "archive_page", Status: "ok", URL: "https://example.invalid/page/2"},
	}}}
	var out, diag bytes.Buffer
	if err := writeResult(&out, &diag, r, false, true); err != nil {
		t.Fatal(err)
	}
	for _, source := range r.Meta.Sources {
		if !strings.Contains(out.String(), source.URL) {
			t.Fatal("lost source", out.String())
		}
	}
}
