package cli

import (
	"bytes"
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
	"strings"
	"testing"
)

func TestUtilityAndCommunityCommandsUseScopedConnection(t *testing.T) {
	for _, id := range contracts.CampusCapabilityIDs() {
		if id != contracts.ElectricityHistoryCapability && id != contracts.BathBindingCapability && id != contracts.BathDevicesCapability && id != contracts.BathUsageCapability && contracts.JCourseOperation(id) == "" {
			continue
		}
		t.Run(id, func(t *testing.T) {
			var out, diag bytes.Buffer
			root := New(&out, &diag, strings.NewReader(""))
			cmd, args, e := root.Find(contracts.CLIPath(id))
			if e != nil || len(args) > 0 {
				t.Fatal("missing command", e)
			}
			rules, _ := contracts.CampusRules(id)
			for _, field := range []string{"begin", "end", "department", "language", "category", "target_year", "credit", "has_review", "order_by", "ascend", "teacher_title", "semester", "rating", "period", "code", "buildingcode"} {
				if (cmd.Flags().Lookup(cliFlagName(field)) != nil) != rules.AllowsInputField(field) {
					t.Fatal("command/schema mismatch", field)
				}
			}
			root.SetArgs(contracts.CLIPath(id))
			if e := root.ExecuteContext(context.Background()); e == nil {
				t.Fatal("personal command bypassed connection")
			}
		})
	}
}
