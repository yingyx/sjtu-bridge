package contracts

import "strings"

// CLIPath is the domain-based presentation route shared by CLI and MCP.
// Capability IDs remain the execution and authorization identity; never infer
// an ID from a command path or tool name.
func CLIPath(id string) []string {
	switch id {
	case "public.calendars.list":
		return []string{"learning", "calendars", "list"}
	case "public.notices.list":
		return []string{"learning", "notices", "list"}
	case "public.notices.search":
		return []string{"learning", "notices", "search"}
	case "public.services.list":
		return []string{"campus", "websites", "list"}
	case "campus.notices.list":
		return []string{"learning", "ids", "notices", "list"}
	case "campus.notices.get":
		return []string{"learning", "ids", "notices", "get"}
	case SecondClassEducationCapability:
		return []string{"learning", "secondclass", "education"}
	}
	if strings.HasPrefix(id, "canvas.") {
		return append([]string{"learning"}, strings.Split(id, ".")...)
	}
	return strings.Split(id, ".")
}

// MCPToolName renders the same domain route for MCP's flat tool namespace.
func MCPToolName(id string) string {
	return strings.Join(CLIPath(id), "_")
}
