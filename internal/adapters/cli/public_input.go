package cli

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

// Flag exposure follows the same reviewed input schema as Go and MCP. This
// adapter only translates names and values; business validation stays in the
// contracts and provider.
func addPublicInputFlags(command *cobra.Command, raw json.RawMessage) (*contracts.ListInput, []string) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		panic("invalid public input schema")
	}
	in := &contracts.ListInput{}
	stringsByName := map[string]*string{
		"cursor": &in.Cursor, "query": &in.Query, "canteen_id": &in.CanteenID,
		"day": &in.Day, "item_id": &in.ItemID,
	}
	integersByName := map[string]*int{"limit": &in.Limit}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	flags := make([]string, 0, len(names))
	for _, name := range names {
		flag := strings.ReplaceAll(name, "_", "-")
		description := schema.Properties[name].Description
		if target := stringsByName[name]; target != nil {
			command.Flags().StringVar(target, flag, "", description)
		} else if target := integersByName[name]; target != nil {
			defaultValue := 0
			if name == "limit" {
				defaultValue = 10
			}
			command.Flags().IntVar(target, flag, defaultValue, description)
		} else {
			panic("public input field has no CLI binding")
		}
		flags = append(flags, flag)
	}
	applySchemaFlagHelp(command, raw)
	return in, flags
}
