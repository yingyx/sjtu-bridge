package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestActionableErrorsInTextAndJSON(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"capabilites"}, []string{"capabilities", "--help"}},
		{[]string{"learning", "canvas", "assignmnts"}, []string{"assignments", "--help"}},
		{[]string{"learning", "classroms"}, []string{"classrooms", "--help"}},
		{[]string{"learning", "canvas", "courses", "list"}, []string{"--connection"}},
		{[]string{"completion", "wrong"}, []string{"--help"}},
		{[]string{"help", "wrong"}, []string{"帮助主题", "--help"}},
		{[]string{"capabilities", "search"}, []string{"QUERY", "sjtu capabilities search --help"}},
		{[]string{"learning", "calendars", "list", "--limit", "SYNTHETIC_SECRET"}, []string{"limit", "int"}},
		{[]string{"learning", "calendars", "list", "--limt", "1"}, []string{"--limit"}},
		{[]string{"learning", "calendars", "list", "--limit"}, []string{"limit", "缺少值"}},
		{[]string{"auth", "remove"}, []string{"credential", "必填"}},
		{[]string{"learning", "calendars", "list", "--limit", "101"}, []string{"limit", "1–100"}},
		{[]string{"learning", "classrooms", "available"}, []string{"building_id", "date", "start_section", "end_section"}},
		{[]string{"learning", "classrooms", "list", "--room-features", "[]"}, []string{"room-features", "json"}},
		{[]string{"learning", "timetable", "list"}, []string{"--connection"}},
	} {
		for _, asJSON := range []bool{false, true} {
			args := append([]string{}, tc.args...)
			if asJSON {
				args = append([]string{"--json"}, args...)
			}
			var out, diag bytes.Buffer
			if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 1 || out.Len() != 0 {
				t.Fatalf("%v: %s / %s", args, out.String(), diag.String())
			}
			if asJSON != json.Valid(diag.Bytes()) {
				t.Fatalf("wrong output mode %v: %s", args, diag.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(diag.String(), want) {
					t.Fatalf("%v missing %q: %s", args, want, diag.String())
				}
			}
			if strings.Contains(diag.String(), "SYNTHETIC_SECRET") || strings.Contains(diag.String(), "Invalid command; use --help") {
				t.Fatal(diag.String())
			}
		}
	}
}

func TestErrorDoesNotEchoUnknownInputOrMisclassifyInternalFailure(t *testing.T) {
	for _, args := range [][]string{{"SYNTHETIC_SECRET"}, {"doctor", "--SYNTHETIC_SECRET=value"}, {"doctor", "SYNTHETIC_SECRET"}} {
		var out, diag bytes.Buffer
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 1 || strings.Contains(diag.String(), "SYNTHETIC_SECRET") {
			t.Fatal(args, diag.String())
		}
	}
	root := New(&bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader(""))
	e := presentError(root, errors.New("SYNTHETIC_SECRET"))
	if e.Code != "INTERNAL_ERROR" || strings.Contains(e.Message, "SYNTHETIC_SECRET") {
		t.Fatal(e)
	}
}

func TestJSONSwitchDoesNotInterpretParameterValues(t *testing.T) {
	root := New(&bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader(""))
	cmd, _, _ := root.Find([]string{"learning", "calendars", "list"})
	for _, args := range [][]string{{"--input-file", "--json"}, {"--", "--json"}, {"--json=false"}} {
		if jsonRequested(cmd, args) {
			t.Fatal(args)
		}
	}
}

func TestErrorJSONModeSurvivesEarlierParseFailures(t *testing.T) {
	for _, test := range []struct {
		args []string
		json bool
	}{
		{[]string{"campus", "websites", "list", "--limit", "bad", "--json"}, true},
		{[]string{"campus", "websites", "list", "--json", "--limit", "bad", "--json=false"}, false},
		{[]string{"campus", "websites", "list", "--json=false", "--limit", "bad", "--json"}, true},
		{[]string{"campus", "websites", "list", "--json", "--input-file", "--json=false"}, true},
		{[]string{"campus", "websites", "list", "--input-file=--json"}, false},
		{[]string{"campus", "websites", "list", "--SYNTHETIC_SECRET", "--json"}, true},
		{[]string{"campus", "websites", "list", "--", "--json"}, false},
	} {
		var out, diag bytes.Buffer
		if exit := Execute(context.Background(), test.args, &out, &diag, strings.NewReader("")); exit != 1 || out.Len() != 0 {
			t.Fatal(test.args, exit, out.String(), diag.String())
		}
		if json.Valid(diag.Bytes()) != test.json || strings.Contains(diag.String(), "SYNTHETIC_SECRET") {
			t.Fatal(test.args, diag.String())
		}
	}
}

func TestRecoveryHintsDistinguishCursorAndAuditFailures(t *testing.T) {
	root := New(&bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader(""))
	for _, test := range []struct {
		name, path, code, message string
		want, absent              []string
	}{
		{"private_cursor", "learning canvas assignments list", "PERMISSION_DENIED", "Cursor unavailable for this authorization", []string{"移除 --cursor", "第一页", "--limit", "--connection", "核对授权"}, nil},
		{"other_permission", "learning canvas assignments list", "PERMISSION_DENIED", "Capability is not authorized", []string{"capabilities list", "授权"}, []string{"移除 --cursor"}},
		{"public_cursor", "campus websites list", "INVALID_ARGUMENT", "Directory cursor expired or does not match this query", []string{"移除 --cursor", "第一页", "--limit"}, nil},
		{"query_audit", "learning canvas assignments list", "AUDIT_UNAVAILABLE", "Private result withheld because audit is unavailable", []string{"查询结果未发布", "审计存储", "重新查询"}, []string{"管理操作", "可能已生效", "auth status"}},
		{"admin_audit", "auth remove", "AUDIT_UNAVAILABLE", "State committed; audit unavailable", []string{"管理操作可能已生效", "auth status", "再决定是否重试"}, []string{"查询结果未发布"}},
		{"grant_audit", "permissions grant", "AUDIT_UNAVAILABLE", "State committed; audit unavailable", []string{"管理操作可能已生效", "auth status"}, []string{"查询结果未发布"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd, remaining, err := root.Find(strings.Fields(test.path))
			if err != nil || len(remaining) != 0 {
				t.Fatal(err, remaining)
			}
			cause := contracts.Fail(test.code, test.message)
			cause.RequestID = "synthetic-request"
			display := presentError(cmd, cause)
			if display.Code != cause.Code || display.RequestID != cause.RequestID || display.Retryable != cause.Retryable {
				t.Fatal("error metadata changed", display)
			}
			for _, want := range test.want {
				if !strings.Contains(display.Hint, want) {
					t.Fatal(want, display.Hint)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(display.Hint, absent) {
					t.Fatal(absent, display.Hint)
				}
			}
		})
	}
}
