package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestPublicHelpUsesSchemaRequirementsAndEnums(t *testing.T) {
	cmd := &cobra.Command{}
	addPublicInputFlags(cmd, contracts.PublicInputSchema("life.dining.series"))
	for name, fragments := range map[string][]string{
		"canteen-id": {"必填", "sjtu life dining list", "可选"},
		"day":        {"today", "yesterday", "both"},
		"limit":      {"整数", "1–100"},
		"cursor":     {"next_cursor", "相同命令"},
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatal("missing schema field", name)
		}
		for _, fragment := range fragments {
			if !strings.Contains(flag.Usage, fragment) {
				t.Fatalf("%s is missing %s: %s", name, fragment, flag.Usage)
			}
		}
		if len(flag.Annotations[cobra.BashCompOneRequiredFlag]) != 0 {
			t.Fatal("Cobra required flag would block JSON input", name)
		}
	}
	for _, capability := range contracts.PublicCapabilities() {
		// Every registered property must bind, without irrelevant generic flags.
		command := &cobra.Command{}
		raw := contracts.PublicInputSchema(capability.ID)
		_, flags := addPublicInputFlags(command, raw)
		schema, err := decodeInputSchema(raw)
		if err != nil || len(flags) != len(schema.Properties) {
			t.Fatalf("%s: flags=%v err=%v", capability.ID, flags, err)
		}
		for name := range schema.Properties {
			if command.Flags().Lookup(cliFlagName(name)) == nil {
				t.Fatalf("%s: no flag for %s", capability.ID, name)
			}
		}
	}
}
