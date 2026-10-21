package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func readPublicTestInput(t *testing.T, id string, flags []string, input io.Reader) ([]byte, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "query"}
	schema := contracts.PublicInputSchema(id)
	value, names := addPublicInputFlags(cmd, schema)
	options := addJSONInputFlags(cmd)
	if err := cmd.Flags().Parse(flags); err != nil {
		t.Fatal(err)
	}
	return options.read(cmd, input, value, names, schema)
}

func TestJSONInputSourcesAndFlags(t *testing.T) {
	file := filepath.Join(t.TempDir(), "业务输入.json")
	if err := os.WriteFile(file, []byte("\ufeff{\"limit\":3}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, input string
		flags       []string
		limit       float64
	}{
		{"flags", "unused", []string{"--limit", "3"}, 3},
		{"file_bom", "unused", []string{"--input-file", file}, 3},
		{"stdin_bom", "\ufeff{\"limit\":3}", []string{"--input-json-stdin"}, 3},
		{"default_flags", "unused", nil, 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := readPublicTestInput(t, "public.calendars.list", test.flags, strings.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if json.Unmarshal(raw, &value) != nil || value["limit"] != test.limit {
				t.Fatal(string(raw))
			}
		})
	}
	// JSON mode does not silently merge CLI defaults into the caller's object.
	raw, err := readPublicTestInput(t, "public.calendars.list", []string{"--input-json-stdin"}, strings.NewReader(`{}`))
	if err != nil || string(raw) != `{}` {
		t.Fatal(string(raw), err)
	}
}

func TestJSONInputRejectsAmbiguityBeforeReading(t *testing.T) {
	for _, flags := range [][]string{
		{"--input-file", "missing", "--input-json-stdin"},
		{"--input-json-stdin", "--limit", "10"},
		{"--input-file", "missing", "--limit", "10"},
	} {
		_, err := readPublicTestInput(t, "public.calendars.list", flags, unreadableInput{})
		var issue *cliError
		if !errors.As(err, &issue) || issue.Code != "INVALID_ARGUMENT" || !strings.Contains(issue.Message, "只能选择一种") {
			t.Fatal(flags, err)
		}
	}
}

func TestExplicitZeroAndEmptyFlagsCannotBecomeDefaults(t *testing.T) {
	for _, test := range []struct {
		id, field string
		flags     []string
	}{
		{"public.calendars.list", "limit", []string{"--limit", "0"}},
		{"life.dining.series", "day", []string{"--canteen-id", "100", "--day="}},
		{"life.lostfound.list", "kind", []string{"--kind="}},
		{"learning.classrooms.available", "start_section", []string{"--building-id", "24", "--date", "2026-10-21", "--start-section", "0", "--end-section", "2"}},
	} {
		_, err := readPublicTestInput(t, test.id, test.flags, nil)
		var issue *cliError
		if !errors.As(err, &issue) || len(issue.Fields) != 1 || issue.Fields[0].Field != test.field {
			t.Fatal(test.flags, err)
		}
	}
	// An explicit empty facility object remains a valid no-filter value.
	raw, err := readPublicTestInput(t, "learning.classrooms.list", []string{"--building-id", "24", "--room-features", `{}`}, nil)
	if err != nil || !bytes.Contains(raw, []byte(`"room_features":{}`)) {
		t.Fatal(string(raw), err)
	}
}

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) {
	return 0, errors.New("SYNTHETIC_SECRET_READ_FAILURE")
}

func TestJSONInputValidationIsDetailedWithoutEchoingValues(t *testing.T) {
	for _, test := range []struct {
		name, id, raw, field, contains string
	}{
		{"limit", "public.calendars.list", `{"limit":101}`, "limit", "1–100"},
		{"limit_type", "public.calendars.list", `{"limit":"SYNTHETIC_SECRET"}`, "limit", "整数"},
		{"required_dining", "life.dining.areas", `{}`, "canteen_id", "--canteen-id"},
		{"enum_dining", "life.dining.series", `{"canteen_id":"100","day":"SYNTHETIC_SECRET"}`, "day", "today"},
		{"required", "learning.classrooms.available", `{}`, "building_id", "--building-id"},
		{"enum", "life.lostfound.list", `{"kind":"SYNTHETIC_SECRET"}`, "kind", "looking_for_item"},
		{"unknown_key", "public.calendars.list", `{"SYNTHETIC_SECRET":true}`, "", "不支持的字段"},
		{"nested_unknown_key", "learning.classrooms.list", `{"building_id":"24","room_features":{"SYNTHETIC_SECRET":["1"]}}`, "room_features", "内部字段"},
		{"duplicate", "public.calendars.list", `{"limit":1,"LIMIT":"SYNTHETIC_SECRET"}`, "", "重复字段"},
		{"documents", "public.calendars.list", `{} {"SYNTHETIC_SECRET":1}`, "", "多个文档"},
		{"syntax", "public.calendars.list", `{"limit":"SYNTHETIC_SECRET"`, "", "JSON 输入无效"},
		{"null", "public.calendars.list", `null`, "", "必须是对象"},
		{"array", "public.calendars.list", `[]`, "", "必须是对象"},
		{"invalid_utf8", "public.calendars.list", string([]byte{0xff}), "", "UTF-8"},
		{"empty", "public.calendars.list", "\ufeff \n", "", "输入为空"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := readPublicTestInput(t, test.id, []string{"--input-json-stdin"}, strings.NewReader(test.raw))
			var issue *cliError
			if raw != nil || !errors.As(err, &issue) || issue.Code != "INVALID_ARGUMENT" || issue.Hint != "query --help" {
				t.Fatal(string(raw), err)
			}
			encoded, _ := json.Marshal(issue)
			if bytes.Contains(encoded, []byte("SYNTHETIC_SECRET")) || !bytes.Contains(encoded, []byte(test.contains)) {
				t.Fatal(string(encoded))
			}
			if test.field != "" {
				found := false
				for _, field := range issue.Fields {
					found = found || field.Field == test.field
				}
				if !found {
					t.Fatal("missing trusted field", string(encoded))
				}
			}
		})
	}
}

func TestJSONInputBoundAndReadFailures(t *testing.T) {
	atLimit := `{}` + strings.Repeat(" ", contracts.MaxJSONBytes-2)
	if _, err := readPublicTestInput(t, "public.calendars.list", []string{"--input-json-stdin"}, strings.NewReader(atLimit)); err != nil {
		t.Fatal("exact bound rejected", err)
	}
	for _, test := range []struct {
		flags []string
		input io.Reader
		want  string
	}{
		{[]string{"--input-json-stdin"}, strings.NewReader(atLimit + " "), "256 KiB"},
		{[]string{"--input-json-stdin"}, unreadableInput{}, "无法读取 JSON"},
		{[]string{"--input-json-stdin"}, nil, "标准输入不可读"},
		{[]string{"--input-file="}, nil, "文件路径"},
		{[]string{"--input-file", filepath.Join(t.TempDir(), "SYNTHETIC_SECRET")}, nil, "文件是否存在"},
	} {
		_, err := readPublicTestInput(t, "public.calendars.list", test.flags, test.input)
		var issue *cliError
		if !errors.As(err, &issue) || !strings.Contains(issue.Message, test.want) || strings.Contains(issue.Message, "SYNTHETIC_SECRET") {
			t.Fatal(test.flags, err)
		}
	}
}

func TestPersonalJSONUsesTheSameFieldValidation(t *testing.T) {
	schema := contracts.AssignmentCapability("synthetic-account", []string{"synthetic-course"}, "deadlines_v1", 5).InputSchema
	newCommand := func() (*cobra.Command, *jsonInputOptions, *contracts.AssignmentQuery) {
		cmd := &cobra.Command{Use: "assignments"}
		value := &contracts.AssignmentQuery{Limit: 5}
		cmd.Flags().StringVar(&value.AccountRef, "account", "", "")
		cmd.Flags().StringVar(&value.CourseRef, "course", "", "")
		cmd.Flags().StringVar(&value.View, "view", "", "")
		cmd.Flags().IntVar(&value.Limit, "limit", 5, "")
		return cmd, addJSONInputFlags(cmd), value
	}
	cmd, options, value := newCommand()
	if err := cmd.Flags().Parse([]string{"--input-json-stdin"}); err != nil {
		t.Fatal(err)
	}
	input := `{"account_ref":"synthetic-account","course_ref":"synthetic-course","view":"deadlines_v1","limit":5}`
	if _, err := options.read(cmd, strings.NewReader(input), value, []string{"account", "course", "view", "limit"}, schema); err != nil {
		t.Fatal(err)
	}
	cmd, options, value = newCommand()
	_, err := options.read(cmd, nil, value, []string{"account", "course", "view", "limit"}, schema)
	var issue *cliError
	if !errors.As(err, &issue) || len(issue.Fields) != 3 || issue.Fields[0].Field != "account_ref" || !strings.Contains(issue.Fields[0].Message, "--account") {
		t.Fatal(err)
	}
	if err := cmd.Flags().Parse([]string{"--input-json-stdin"}); err != nil {
		t.Fatal(err)
	}
	_, err = options.read(cmd, strings.NewReader(strings.ReplaceAll(input, "synthetic-account", "SYNTHETIC_SECRET")), value, []string{"account", "course", "view", "limit"}, schema)
	if !errors.As(err, &issue) {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(issue)
	if bytes.Contains(encoded, []byte("synthetic-account")) || bytes.Contains(encoded, []byte("SYNTHETIC_SECRET")) {
		t.Fatal(string(encoded))
	}
}

func TestJSONIntegerSpellingMatchesExecutionContracts(t *testing.T) {
	for _, number := range []string{"1.0", "1e0", "1E+0", "1.00000000000000000000000000001", "0.99999999999999999999999999999"} {
		var out, diag bytes.Buffer
		input := `{"limit":` + number + `}`
		args := []string{"public", "services", "list", "--input-json-stdin", "--json"}
		if exit := Execute(context.Background(), args, &out, &diag, strings.NewReader(input)); exit != 1 || out.Len() != 0 {
			t.Fatal(number, exit, out.String(), diag.String())
		}
		var result struct {
			Error cliError `json:"error"`
		}
		if json.Unmarshal(diag.Bytes(), &result) != nil || result.Error.Code != "INVALID_ARGUMENT" || len(result.Error.Fields) != 1 || result.Error.Fields[0].Field != "limit" || !strings.Contains(result.Error.Fields[0].Message, "整数写法") {
			t.Fatal(number, diag.String())
		}
		if strings.Contains(diag.String(), number) || result.Error.RequestID != "" {
			t.Fatal("value echoed or error reached execution", diag.String())
		}
	}
	var out, diag bytes.Buffer
	if exit := Execute(context.Background(), []string{"public", "services", "list", "--input-json-stdin", "--json"}, &out, &diag, strings.NewReader(`{"limit":1}`)); exit != 0 || diag.Len() != 0 {
		t.Fatal(exit, out.String(), diag.String())
	}
	var response contracts.Response
	if json.Unmarshal(out.Bytes(), &response) != nil || len(response.Data.Items) != 1 || !response.Meta.HasMore {
		t.Fatal("integer query did not reach bundled provider", out.String())
	}
}
