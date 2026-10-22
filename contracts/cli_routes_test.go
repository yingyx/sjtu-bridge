package contracts

import (
	"reflect"
	"strings"
	"testing"
)

func TestDomainRoutesPreserveCapabilityIdentity(t *testing.T) {
	for _, test := range []struct {
		id, route string
	}{
		{AssignmentsCapability, "learning canvas assignments list"},
		{"canvas.modules.items", "learning canvas modules items"},
		{"public.calendars.list", "learning calendars list"},
		{"public.notices.list", "learning notices list"},
		{"public.notices.search", "learning notices search"},
		{"public.services.list", "campus websites list"},
		{"campus.notices.list", "learning ids notices list"},
		{"campus.notices.get", "learning ids notices get"},
		{SecondClassEducationCapability, "learning secondclass education"},
		{SecondClassPracticeCapability, "development secondclass practice"},
		{"digital.software.list", "digital software list"},
	} {
		t.Run(test.id, func(t *testing.T) {
			path := CLIPath(test.id)
			if !reflect.DeepEqual(path, strings.Fields(test.route)) {
				t.Fatalf("route = %v, want %q", path, test.route)
			}
			definition := cloneCapability(Capability{ID: test.id})
			if definition.ID != test.id || definition.CLICommand != "sjtu "+test.route || definition.MCPTool != strings.ReplaceAll(test.route, " ", "_") {
				t.Fatal("presentation changed execution identity or diverged", definition)
			}
			path[0] = "changed"
			if CLIPath(test.id)[0] == "changed" {
				t.Fatal("caller can mutate shared routes")
			}
		})
	}
}

func TestCatalogRoutesAreUniqueAndUsePrimaryDomain(t *testing.T) {
	commands, tools := map[string]string{}, map[string]string{}
	for _, definition := range catalog {
		path := CLIPath(definition.ID)
		if len(path) < 3 || path[0] != definition.Category {
			t.Fatalf("%s route %v differs from primary domain %s", definition.ID, path, definition.Category)
		}
		command, tool := strings.Join(path, " "), MCPToolName(definition.ID)
		if previous := commands[command]; previous != "" {
			t.Fatalf("%s and %s share CLI route %s", previous, definition.ID, command)
		}
		if previous := tools[tool]; previous != "" {
			t.Fatalf("%s and %s share MCP name %s", previous, definition.ID, tool)
		}
		commands[command], tools[tool] = definition.ID, definition.ID
	}
}
