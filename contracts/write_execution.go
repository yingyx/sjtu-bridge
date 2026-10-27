package contracts

import (
	"encoding/base64"
	"encoding/json"
	"path"
	"strings"
)

// PanWriteQuery creates only one new immediate child of an explicitly granted
// parent. It cannot overwrite, move, share or initialize a personal space.
type PanWriteQuery struct {
	AccountRef  string `json:"account_ref"`
	ResourceRef string `json:"resource_ref"`
	View        string `json:"view"`
	Limit       int    `json:"limit"`
	Path        string `json:"path"`
	DataBase64  string `json:"data_base64,omitempty"`
}

func PanWriteIDs() []string     { return []string{PanMkdirCapability, PanUploadCapability} }
func IsPanWrite(id string) bool { return id == PanMkdirCapability || id == PanUploadCapability }
func PanDirectoryResource(parent string) string {
	if parent == "" {
		parent = "/"
	}
	return "pan:directory:" + parent
}
func validPanDirectoryResource(ref string) bool {
	if ref == "pan:directory:/" {
		return true
	}
	return len(ref) <= 128 && strings.HasPrefix(ref, "pan:directory:") && ValidPanPath(strings.TrimPrefix(ref, "pan:directory:"), false)
}
func GrantRules(id string) (CampusReadRules, bool) {
	if IsPanWrite(id) {
		return CampusReadRules{capability: id, View: "created_v1", MaxLimit: 1, MaxResources: 8}, true
	}
	return CampusRules(id)
}
func (q PanWriteQuery) Scope(id string) ReadScope {
	return ReadScope{Capability: id, AccountRef: q.AccountRef, ResourceRef: q.ResourceRef, View: q.View, Limit: q.Limit}
}
func (q PanWriteQuery) Valid(id string) bool {
	if !IsPanWrite(id) || q.AccountRef == "" || len(q.AccountRef) > 128 || q.View != "created_v1" || q.Limit != 1 || (!ValidPanPath(q.Path, false) || strings.Contains(q.Path, "%")) || !validPanDirectoryResource(q.ResourceRef) {
		return false
	}
	parent := path.Dir(q.Path)
	if parent == "." {
		parent = ""
	}
	if PanDirectoryResource(parent) != q.ResourceRef {
		return false
	}
	if id == PanMkdirCapability {
		return q.DataBase64 == ""
	}
	if len(q.DataBase64) > base64.StdEncoding.EncodedLen(32<<10) {
		return false
	}
	b, e := base64.StdEncoding.Strict().DecodeString(q.DataBase64)
	defer clear(b)
	return e == nil && len(b) <= 32<<10 && base64.StdEncoding.EncodeToString(b) == q.DataBase64
}
func PanWriteInputSchema(id string) json.RawMessage {
	if !IsPanWrite(id) {
		return nil
	}
	text := func(n int) map[string]any { return map[string]any{"type": "string", "minLength": 1, "maxLength": n} }
	props := map[string]any{"account_ref": text(128), "resource_ref": text(128), "view": map[string]any{"type": "string", "enum": []string{"created_v1"}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 1}, "path": text(1024)}
	props["resource_ref"].(map[string]any)["description"] = "精确父目录：根目录 pan:directory:/，其他如 pan:directory:docs；仅允许创建直接子项"
	required := []string{"account_ref", "resource_ref", "view", "limit", "path"}
	if id == PanUploadCapability {
		props["data_base64"] = map[string]any{"type": "string", "maxLength": base64.StdEncoding.EncodedLen(32 << 10), "description": "至多32KiB文件的规范标准Base64；建议通过JSON输入文件或stdin提供"}
		required = append(required, "data_base64")
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required})
	return raw
}
func PanWriteCapability(id, account string, resources []string, view string, limit int) (Capability, bool) {
	c, ok := PersonalMetadata(id)
	r, known := GrantRules(id)
	if !IsPanWrite(id) || !ok || !known || account == "" || len(account) > 128 || !r.ValidGrant(view, limit, resources) {
		return Capability{}, false
	}
	var input map[string]any
	_ = json.Unmarshal(PanWriteInputSchema(id), &input)
	p := input["properties"].(map[string]any)
	p["account_ref"].(map[string]any)["enum"] = []string{account}
	p["resource_ref"].(map[string]any)["enum"] = resources
	c.InputSchema, _ = json.Marshal(input)
	fields := map[string]any{}
	for _, key := range []string{"action", "path", "kind", "version", "status"} {
		fields[key] = map[string]any{"type": "string"}
	}
	fields["size"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 32 << 10}
	c.OutputSchema, _ = json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": fields, "required": []string{"action", "path", "kind", "size", "version", "status"}})
	c.Authorized = true
	return c, true
}
func panWriteMetadata() []Capability {
	var result []Capability
	for _, d := range []struct{ id, title, description string }{{PanMkdirCapability, "在本人云盘新建目录", "仅在明确获准的父目录新建一个直接子目录；已存在时拒绝，不初始化空间、不覆盖。"}, {PanUploadCapability, "上传本人云盘新文件", "仅在明确获准父目录上传一个至多32KiB的新文件；初始化与确认均禁止覆盖，不自动重试。结果未知时先去官网核对。"}} {
		result = append(result, Capability{ID: d.id, ContractVersion: "1", Title: d.title, Category: "digital", CategoryLabel: categoryLabels["digital"], TaskGroup: "communication", TaskGroupLabel: taskGroupLabels[TaskPath{"digital", "communication"}], Service: "交大云盘 Pan", Provider: "pan", Aliases: []string{d.title}, Keywords: []string{"云盘", "Pan", "upload"}, Description: d.description, Operation: Write, DataClass: PersonalData, Effects: []Effect{NoEffect}, Reversibility: Reversible, ExecutionRule: GrantExecution, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}})
	}
	return result
}
