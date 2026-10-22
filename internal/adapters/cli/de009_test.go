package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDE009CommandsBindOnlyTheirSchemaFields(t *testing.T) {
	ids := []string{contracts.ActivityCapability, contracts.ActivityTypesCapability, contracts.ActivityCalendarCapability, contracts.ActivityCountsCapability, contracts.ActivityRegistrationsCapability, contracts.ActivityCollectionsCapability, contracts.SecondClassHonorsCapability, contracts.SecondClassPracticeCapability, contracts.SecondClassEducationCapability}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			var out, diag bytes.Buffer
			root := New(&out, &diag, strings.NewReader(""))
			cmd, args, e := root.Find(contracts.CLIPath(id))
			if e != nil || len(args) != 0 || cmd.Name() != contracts.CLIPath(id)[2] {
				t.Fatal("capability command absent", e, args)
			}
			schema, e := decodeInputSchema(contracts.CampusInputSchema(id))
			if e != nil {
				t.Fatal(e)
			}
			for _, field := range []string{"query", "activity_type_id", "child_type_ids", "states", "date", "month"} {
				_, want := schema.Properties[field]
				flag := cmd.Flags().Lookup(cliFlagName(field))
				if (flag != nil) != want {
					t.Fatal("CLI/schema filter mismatch", field, want)
				}
			}
			rules, _ := contracts.CampusRules(id)
			if cmd.Flags().Lookup("view") == nil || !strings.Contains(cmd.Flags().Lookup("view").Usage, rules.View) {
				t.Fatal("fixed view omitted from CLI guidance")
			}
			if id == contracts.ActivityCountsCapability && cmd.Flags().Lookup("limit").DefValue != "31" {
				t.Fatal("complete month default would truncate counts")
			}
		})
	}
}

func TestDE009FilterFlagsReachConnectionValidation(t *testing.T) {
	for _, test := range []struct {
		path, resource, view string
		flags                []string
	}{
		{"development activities list", "activity:feed:visible", "summaries_v1", []string{"--query", "合成活动", "--activity-type-id", "1", "--child-type-ids", "2,3", "--states", "1,3"}},
		{"development activities calendar", "activity:calendar:visible", "calendar_v1", []string{"--date", "2026-10-21", "--activity-type-id", "1"}},
		{"development activities counts", "activity:counts:visible", "counts_v1", []string{"--month", "2026-10", "--activity-type-id", "1"}},
		{"development activities registrations", "activity:registrations:self", "registrations_v1", []string{"--states", "participated", "--query", "合成"}},
		{"learning secondclass education", "dekt:education:self", "education_v1", nil},
	} {
		t.Run(test.path, func(t *testing.T) {
			var out, diag bytes.Buffer
			root := New(&out, &diag, strings.NewReader(""))
			args := append(strings.Fields(test.path), "--connection", "synthetic-unused-path", "--account", "synthetic-account", "--resource", test.resource, "--view", test.view)
			root.SetArgs(append(args, test.flags...))
			e := root.ExecuteContext(context.Background())
			var failure *contracts.Error
			if !errors.As(e, &failure) || failure.Code != "CLIENT_UNAUTHORIZED" {
				t.Fatal("valid flags did not reach connection validation", e)
			}
		})
	}
}

func TestDE009CalendarCommandsRejectMissingTypeBeforeConnecting(t *testing.T) {
	for _, test := range []struct{ name, resource, view, key, value string }{
		{"calendar", "activity:calendar:visible", "calendar_v1", "date", "2026-10-21"},
		{"counts", "activity:counts:visible", "counts_v1", "month", "2026-10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			root := New(&out, &diag, strings.NewReader(""))
			root.SetArgs([]string{"development", "activities", test.name, "--connection", "synthetic-unused-path", "--account", "synthetic-account", "--resource", test.resource, "--view", test.view, "--" + test.key, test.value})
			var failure *cliError
			e := root.ExecuteContext(context.Background())
			if !errors.As(e, &failure) || failure.Code != "INVALID_ARGUMENT" {
				t.Fatal("unscoped calendar query reached connection", e)
			}
		})
	}
}
