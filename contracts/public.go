package contracts

import "encoding/json"

var diningCanteens = [...]string{"100", "200", "300", "400", "500", "600", "700", "800", "1000", "1200"}

// ValidDiningCanteen checks the reviewed parent resource set used by both the
// public input contract and the provider's fixed destination predicate.
func ValidDiningCanteen(id string) bool {
	for _, known := range diningCanteens {
		if id == known {
			return true
		}
	}
	return false
}

// PublicInputSchema defines service-specific filters outside the shared Engine.
// A schema describes accepted input; execution still requires policy admission.
func PublicInputSchema(id string) json.RawMessage {
	props := map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum items per call, default 10"},
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "Opaque continuation from the same capability, filters and limit"},
	}
	required := []string{}
	switch id {
	case "public.notices.search":
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "Keywords matched against recent RSS titles and summaries only"}
	case "life.dining.areas":
		props["canteen_id"] = map[string]any{"type": "string", "enum": diningCanteens, "description": "Reviewed parent canteen ID returned by life.dining.list"}
		required = append(required, "canteen_id")
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required})
	return raw
}
