package contracts

import "encoding/json"

const AIStatusCapability = "digital.ai.status"
const AIUsageCapability = "digital.ai.usage"

func AIPublicInputSchema(id string) json.RawMessage {
	if id != AIStatusCapability && id != AIUsageCapability {
		return nil
	}
	p := map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页模型条数"},
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "同一查询的短期快照续页；数据变化须重查"},
	}
	if id == AIUsageCapability {
		p["kind"] = map[string]any{"type": "string", "enum": []string{"week", "month"}, "description": "week 近7天（默认），month 近30天；仅公开模型汇总"}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": p})
	return raw
}
