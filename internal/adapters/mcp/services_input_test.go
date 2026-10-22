package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestServiceInputRejectsAmbiguousOrNonObjectJSON(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"string"`, `{} {}`, `{"query":"first","query":"second"}`, `{"query":"first","QUERY":"second"}`, "{\"query\":\"" + string([]byte{0xff}) + "\"}", `{"url":"https://arbitrary.invalid/"}`, `{"query":null}`, `{"limit":null}`, `{"offset":null}`, `{"category":null}`, `{"QUERY":"作业"}`, strings.Repeat(" ", contracts.MaxJSONBytes+1)} {
		var input contracts.ServiceFilter
		if decodeServiceInput(json.RawMessage(raw), &input, serviceSearchValidator) == nil {
			t.Fatalf("ambiguous/non-object input accepted: %.100q", raw)
		}
	}
	var input contracts.ServiceFilter
	if err := decodeServiceInput(json.RawMessage(`{"query":"作业","limit":2}`), &input, serviceSearchValidator); err != nil || input.Query != "作业" || input.Limit != 2 {
		t.Fatal("valid UTF-8 input rejected", input, err)
	}
}

func TestServiceResultRejectsOversizedOutput(t *testing.T) {
	result, err := serviceResult(map[string]string{"large": strings.Repeat("x", contracts.MaxJSONBytes)})
	if err != nil || result == nil || !result.IsError || result.StructuredContent != nil {
		t.Fatal("oversized catalog was published", result, err)
	}
}
