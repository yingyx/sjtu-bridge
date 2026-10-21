package cli

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
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
		"day": &in.Day, "item_id": &in.ItemID, "building_id": &in.BuildingID,
		"room_id": &in.RoomID, "date": &in.Date, "room_type": &in.RoomType,
		"campus_id": &in.CampusID, "org_id": &in.OrgID, "kind": &in.Kind, "seat_range": &in.SeatRange,
	}
	integersByName := map[string]*int{"limit": &in.Limit, "start_section": &in.StartSection, "end_section": &in.EndSection}
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
		} else if name == "room_features" {
			command.Flags().Var(roomFeaturesFlag{target: &in.RoomFeatures}, flag, description+" (UTF-8 JSON object)")
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

type roomFeaturesFlag struct{ target *map[string][]string }

func (v roomFeaturesFlag) String() string {
	if *v.target == nil {
		return ""
	}
	raw, _ := json.Marshal(*v.target)
	return string(raw)
}
func (v roomFeaturesFlag) Type() string { return "json" }
func (v roomFeaturesFlag) Set(raw string) error {
	var value map[string][]string
	if len(raw) > 4096 || strictjson.Decode([]byte(raw), &value) != nil || value == nil {
		return errors.New("room-features requires one bounded UTF-8 JSON object")
	}
	*v.target = value
	return nil
}
