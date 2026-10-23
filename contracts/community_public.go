package contracts

import "encoding/json"

const CareerListCapability = "development.career.list"
const CampusNewsCapability = "campus.news.list"

// CommunityPublicInputSchema describes the two bounded anonymous indexes.
// A nil result means the capability belongs to another input contract.
func CommunityPublicInputSchema(id string) json.RawMessage {
	if id != CareerListCapability && id != CampusNewsCapability {
		return nil
	}
	props := map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "同一能力、kind、query及limit的首屏快照续页，15分钟内有效"},
		"query":  map[string]any{"type": "string", "maxLength": 128, "description": "仅在当次首屏标题中按空白分词全部匹配；不是全站搜索"},
	}
	required := []string{}
	if id == CareerListCapability {
		props["kind"] = map[string]any{"type": "string", "enum": []string{"jobs", "internships", "talks"}, "description": "jobs 招聘信息，internships 实习信息，talks 专场宣讲会；仅就业网首页对应卡片"}
		required = append(required, "kind")
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required})
	return raw
}
