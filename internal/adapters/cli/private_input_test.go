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

func TestPrivateHelpHasOnlyRelevantViewsAndLimits(t *testing.T) {
	for _, test := range []struct {
		path, view, limit, reference string
	}{
		{"canvas assignments list", "deadlines_v1", "1–100", "course_ref"},
		{"learning timetable list", "schedule_v1", "1–100", "resource_ref"},
		{"digital mail list", "headers_v1", "1–50", "resource_ref"},
		{"development activities list", "summaries_v1", "1–20", "resource_ref"},
		{"life electricity balance", "balance_v1", "1–1", "resource_ref"},
	} {
		t.Run(test.path, func(t *testing.T) {
			var out, diag bytes.Buffer
			if code := Execute(context.Background(), append(strings.Fields(test.path), "--help"), &out, &diag, strings.NewReader("")); code != 0 {
				t.Fatal(diag.String())
			}
			for _, expected := range []string{test.view, test.limit, "account_ref", test.reference, "必填", "--input-file", "--input-json-stdin", "--connection", "capabilities describe"} {
				if !strings.Contains(out.String(), expected) {
					t.Fatalf("missing %s in help: %s", expected, out.String())
				}
			}
			for _, view := range []string{"schedule_v1", "headers_v1", "summaries_v1", "balance_v1"} {
				if view != test.view && strings.Contains(out.String(), view) {
					t.Fatalf("unrelated view %s in help", view)
				}
			}
		})
	}
}

func TestPrivateCommandsExplainMissingConnectionBeforeReadingInput(t *testing.T) {
	for _, path := range []string{"canvas assignments list", "learning timetable list", "digital mail list", "development activities list", "life electricity balance"} {
		var out, diag bytes.Buffer
		args := append(strings.Fields(path), "--input-json-stdin", "--json")
		if code := Execute(context.Background(), args, &out, &diag, strings.NewReader("not JSON")); code == 0 || out.Len() != 0 {
			t.Fatal(path, out.String())
		}
		var result struct {
			Error cliError `json:"error"`
		}
		if err := json.Unmarshal(diag.Bytes(), &result); err != nil {
			t.Fatal(diag.String(), err)
		}
		if result.Error.Code != "CLIENT_UNAUTHORIZED" || !strings.Contains(result.Error.Message, "--connection") {
			t.Fatal(path, diag.String())
		}
	}
}

func TestPrivateInputsValidateBeforeOpeningConnection(t *testing.T) {
	for _, test := range []struct {
		path     string
		flags    []string
		input    string
		fragment string
	}{
		{"canvas assignments list", nil, "", "--account"},
		{"digital mail list", []string{"--account", "synthetic", "--resource", "mail:folder:inbox", "--view", "schedule_v1"}, "", "headers_v1"},
		{"life electricity balance", []string{"--account", "synthetic", "--resource", "electric:room:1", "--view", "balance_v1", "--limit", "2"}, "", "--limit"},
		{"learning timetable list", []string{"--input-json-stdin", "--limit", "1"}, `{}`, "一种输入方式"},
		{"canvas assignments list", []string{"--input-json-stdin"}, `{"account":"synthetic","course_ref":"synthetic-course","view":"deadlines_v1","limit":2}`, "account"},
		{"development activities list", []string{"--input-json-stdin"}, `{"account_ref":"synthetic","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":21}`, "limit"},
	} {
		t.Run(test.path+test.fragment, func(t *testing.T) {
			var out, diag bytes.Buffer
			root := New(&out, &diag, strings.NewReader(test.input))
			args := append(strings.Fields(test.path), "--connection", "synthetic-unused-path")
			root.SetArgs(append(args, test.flags...))
			var issue *cliError
			err := root.ExecuteContext(context.Background())
			if !errors.As(err, &issue) || issue.Code != "INVALID_ARGUMENT" {
				t.Fatal("input was not rejected before connection open", err)
			}
			raw, _ := json.Marshal(issue)
			if !strings.Contains(string(raw), test.fragment) {
				t.Fatal(string(raw))
			}
		})
	}
}

func TestPrivateJSONInputReachesConnectionCheck(t *testing.T) {
	for _, test := range []struct{ path, input string }{
		{"canvas assignments list", `{"account_ref":"synthetic","course_ref":"synthetic-course","view":"deadlines_v1","limit":2}`},
		{"learning timetable list", `{"account_ref":"synthetic","resource_ref":"teaching:term:2026:1","view":"schedule_v1","limit":2}`},
		{"digital mail list", `{"account_ref":"synthetic","resource_ref":"mail:folder:inbox","view":"headers_v1","limit":2,"query":"通知"}`},
		{"development activities list", `{"account_ref":"synthetic","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":2}`},
		{"life electricity balance", `{"account_ref":"synthetic","resource_ref":"electric:room:1","view":"balance_v1","limit":1}`},
	} {
		var out, diag bytes.Buffer
		root := New(&out, &diag, strings.NewReader(test.input))
		// A relative connection is rejected locally, with no filesystem or IPC access.
		root.SetArgs(append(strings.Fields(test.path), "--connection", "synthetic-unused-path", "--input-json-stdin"))
		err := root.ExecuteContext(context.Background())
		var connectionError *contracts.Error
		if !errors.As(err, &connectionError) || connectionError.Code != "CLIENT_UNAUTHORIZED" {
			t.Fatalf("%s: valid JSON did not reach connection validation: %v", test.path, err)
		}
	}
}
