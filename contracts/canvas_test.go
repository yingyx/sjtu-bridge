package contracts

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestCanvasReadContractsRemainCourseScoped(t *testing.T) {
	for _, id := range CanvasCapabilityIDs() {
		t.Run(id, func(t *testing.T) {
			r, _ := CanvasRules(id)
			c, ok := CanvasCapability(id, "canvas:user:7", []string{"canvas:course:1"}, r.View, 2)
			if !ok {
				t.Fatal("missing contract")
			}
			q := CanvasQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:1", View: r.View, Limit: 2}
			if r.Directory {
				q.CourseRef = ""
			}
			if r.Item {
				q.ItemID = "9"
			}
			if r.Module {
				q.ModuleID = "5"
			}
			if r.Dates {
				q.StartDate = "2030-01-01"
				q.EndDate = "2030-01-31"
			}
			raw, _ := json.Marshal(q)
			var value any
			_ = json.Unmarshal(raw, &value)
			var schema jsonschema.Schema
			if json.Unmarshal(c.InputSchema, &schema) != nil {
				t.Fatal("schema")
			}
			resolved, e := schema.Resolve(nil)
			if e != nil || resolved.Validate(value) != nil || !r.ValidQuery(q) {
				t.Fatal("valid scoped query rejected", e)
			}
			fields := value.(map[string]any)
			fields["include"] = []string{"grades"}
			if resolved.Validate(fields) == nil {
				t.Fatal("arbitrary query field admitted")
			}
			delete(fields, "include")
			fields["account_ref"] = "canvas:user:8"
			if resolved.Validate(fields) == nil {
				t.Fatal("account scope widened")
			}
			if r.ValidGrant(r.View, 2, []string{"canvas:course:1", "canvas:course:1"}) || r.ValidGrant(r.View, 2, []string{"canvas:group:1"}) {
				t.Fatal("invalid course authority")
			}
			if len(c.OutputSchema) == 0 || c.Credentials[0] != "CanvasToken" {
				t.Fatal("missing output/credential")
			}
		})
	}
	for _, u := range []string{"https://oc.sjtu.edu.cn/courses/2/files/9", "https://oc.sjtu.edu.cn/courses/1/files/9/download", "https://oc.sjtu.edu.cn/courses/1/files/9?verifier=SYNTHETIC", "https://oc.sjtu.edu.cn/courses/1/external_tools/9", "https://oc.sjtu.edu.cn/courses/11/files/9", "https://evil.example/courses/1/pages/9"} {
		if ValidCanvasNavigation(u, "canvas:course:1") {
			t.Fatal("unsafe navigation", u)
		}
	}
}
