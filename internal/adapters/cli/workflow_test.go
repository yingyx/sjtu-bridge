package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// Follow the same information available to an agent, using bundled navigation
// so the entire search/help/call/recovery workflow stays offline.
func TestPublicAgentWorkflow(t *testing.T) {
	run := func(args []string, input string, success bool) []byte {
		t.Helper()
		var out, diagnostics bytes.Buffer
		code := Execute(context.Background(), args, &out, &diagnostics, strings.NewReader(input))
		if (code == 0) != success {
			t.Fatalf("%v: exit %d, out=%s, diagnostics=%s", args, code, out.String(), diagnostics.String())
		}
		if success {
			if diagnostics.Len() != 0 {
				t.Fatal(diagnostics.String())
			}
			return out.Bytes()
		}
		if out.Len() != 0 {
			t.Fatalf("failed call published a result: %s", out.String())
		}
		return diagnostics.Bytes()
	}

	var matches []contracts.CapabilitySummary
	raw := run([]string{"capabilities", "search", "宿舍洗浴", "--json"}, "", true)
	if err := json.Unmarshal(raw, &matches); err != nil || len(matches) == 0 || matches[0].ID != "life.utilities.navigate" {
		t.Fatalf("search did not identify the task: %s (%v)", raw, err)
	}
	var usage capabilityUsage
	raw = run([]string{"capabilities", "describe", matches[0].ID, "--json"}, "", true)
	if err := json.Unmarshal(raw, &usage); err != nil || usage.CLIFlags["limit"] == "" || usage.CLIFlags["cursor"] == "" || len(usage.InputSchema) == 0 {
		t.Fatalf("incomplete invocation guide: %s (%v)", raw, err)
	}
	command := strings.Fields(usage.CLICommand)[1:]
	help := run(append(append([]string{}, command...), "--help"), "", true)
	if !bytes.Contains(help, []byte(usage.CLIFlags["limit"])) || !bytes.Contains(help, []byte("1–100")) || !bytes.Contains(help, []byte("--input-json-stdin")) {
		t.Fatalf("help disagrees with describe: %s", help)
	}
	// The generated example must be executable without discovering hidden
	// required switches. This navigation capability needs no placeholder values.
	example := strings.Fields(usage.Example)[1:]
	var complete contracts.Response
	raw = run(example, "", true)
	if json.Unmarshal(raw, &complete) != nil || len(complete.Data.Items) == 0 {
		t.Fatalf("example did not return data: %s", raw)
	}
	var failure struct {
		Error cliError `json:"error"`
	}
	args := append(append([]string{}, command...), usage.CLIFlags["limit"], "101", "--json")
	raw = run(args, "", false)
	if json.Unmarshal(raw, &failure) != nil || failure.Error.Code != "INVALID_ARGUMENT" || len(failure.Error.Fields) != 1 || failure.Error.Fields[0].Field != "limit" || failure.Error.Hint != usage.CLICommand+" --help" {
		t.Fatalf("error does not identify the correction: %s", raw)
	}
	args = append(append([]string{}, command...), usage.CLIFlags["limit"], "1", "--json")
	var first contracts.Response
	raw = run(args, "", true)
	if json.Unmarshal(raw, &first) != nil || len(first.Data.Items) != 1 || !first.Meta.HasMore || first.Meta.NextCursor == nil || len(first.Meta.Sources) == 0 || first.Meta.Completeness == "" {
		t.Fatalf("recovery lost range or pagination: %s", raw)
	}
	// Use the returned continuation with the same limit through JSON input.
	input, _ := json.Marshal(map[string]any{"limit": 1, "cursor": *first.Meta.NextCursor})
	args = append(append([]string{}, command...), "--input-json-stdin", "--json")
	var next contracts.Response
	raw = run(args, string(input), true)
	if json.Unmarshal(raw, &next) != nil || len(next.Data.Items) != 1 || next.Data.Items[0].SourceID == first.Data.Items[0].SourceID {
		t.Fatalf("continuation did not advance: %s", raw)
	}
}
