package contracts

import (
	"encoding/json"
	"testing"
)

func TestCommunityPublicSchema(t *testing.T) {
	for _, id := range []string{CareerListCapability, CampusNewsCapability} {
		var schema struct {
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		}
		if err := json.Unmarshal(CommunityPublicInputSchema(id), &schema); err != nil {
			t.Fatal(err)
		}
		if schema.AdditionalProperties || len(schema.Properties["query"]) == 0 || len(schema.Properties["limit"]) == 0 {
			t.Fatal("unbounded index schema")
		}
		if id == CareerListCapability && (len(schema.Required) != 1 || schema.Required[0] != "kind" || len(schema.Properties["kind"]) == 0) {
			t.Fatal("career requires reviewed kind")
		}
		if id == CampusNewsCapability && (len(schema.Required) != 0 || len(schema.Properties["kind"]) != 0) {
			t.Fatal("news must remain a fixed channel")
		}
	}
	if CommunityPublicInputSchema("unknown") != nil {
		t.Fatal("claimed unknown contract")
	}
}
