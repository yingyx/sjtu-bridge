package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestServiceDiscoveryShowsScopeManualLinksAndCurrentActions(t *testing.T) {
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"services", "list", "--limit", "2", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var page contracts.ServicePage
	if json.Unmarshal(out.Bytes(), &page) != nil || len(page.Items) != 2 || page.Total <= 2 || page.NextOffset == nil || page.Scope == "" || page.Revision == "" || page.LinkCheckedThisCall {
		t.Fatal("unbounded or unqualified catalog", out.String())
	}
	out.Reset()
	diag.Reset()
	if Execute(context.Background(), []string{"services", "describe", "canvas", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var entry contracts.ServiceEntry
	if json.Unmarshal(out.Bytes(), &entry) != nil || entry.Purpose == "" || entry.VerifiedAt == "" || len(entry.Destinations) == 0 || len(entry.Sources) == 0 || len(entry.SupportedCapabilityIDs) == 0 || len(entry.AvailableCapabilities) != 0 {
		t.Fatal("static support mistaken for authorization", out.String())
	}
	out.Reset()
	diag.Reset()
	if Execute(context.Background(), []string{"services", "describe", "canvas"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	for _, text := range []string{"用途：", "对象：", "官方入口：", "依据：", "资料核验：", "本次未探测链接", "当前可调用动作：", "无。"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing useful service detail", text, out.String())
		}
	}
	if strings.Contains(out.String(), "sjtu learning canvas") {
		t.Fatal("ungranted action rendered as callable", out.String())
	}
}

func TestServiceDiscoveryFailsClosedAndHonorsOutputErrors(t *testing.T) {
	for _, args := range [][]string{{"services", "list"}, {"services", "search", "作业"}, {"services", "describe", "canvas"}} {
		for _, connection := range []string{"", filepath.Join(t.TempDir(), "missing.connection")} {
			var out, diag bytes.Buffer
			command := append(append([]string{}, args...), "--connection", connection, "--json")
			if Execute(context.Background(), command, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "CLIENT_UNAUTHORIZED") {
				t.Fatal("connection ignored or public fallback", command, out.String(), diag.String())
			}
		}
		var diag bytes.Buffer
		if Execute(context.Background(), args, failingOutput{}, &diag, strings.NewReader("")) == 0 {
			t.Fatal("output failure reported success", args)
		}
	}
	for _, args := range [][]string{{"services", "search", " "}, {"services", "list", "--limit", "101"}, {"services", "list", "--offset", "-1"}, {"services", "list", "--category", "unreviewed"}, {"services", "describe", "unknown-task"}} {
		var out, diag bytes.Buffer
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 {
			t.Fatal("invalid service input accepted", args, out.String())
		}
	}
}

func TestBusinessCommandRoutesMatchDiscoveryAndDomain(t *testing.T) {
	root := New(io.Discard, io.Discard, strings.NewReader(""))
	capabilities := append(contracts.PublicCapabilities(), contracts.AssignmentMetadata())
	for _, id := range append(contracts.CanvasCapabilityIDs(), contracts.CampusCapabilityIDs()...) {
		metadata, ok := contracts.PersonalMetadata(id)
		if !ok {
			t.Fatal("personal definition absent", id)
		}
		capabilities = append(capabilities, metadata)
	}
	for _, capability := range capabilities {
		path := contracts.CLIPath(capability.ID)
		command, remaining, err := root.Find(path)
		if err != nil || len(remaining) != 0 || command.RunE == nil || command.CommandPath() != capability.CLICommand || path[0] != capability.Category || capability.MCPTool != contracts.MCPToolName(capability.ID) {
			t.Fatal("command, discovery and domain diverged", capability.ID, command, remaining, err)
		}
		if !strings.Contains(command.Example, capability.CLICommand) {
			t.Fatal("example does not use published route", capability.ID, command.Example)
		}
	}
	scratch := &cobra.Command{Use: "test"}
	parent := ensureCommandPath(scratch, []string{"one", "two", "three"}, "test commands")
	parent.AddCommand(&cobra.Command{Use: "four"})
	command, remaining, err := scratch.Find([]string{"one", "two", "three", "four"})
	if err != nil || len(remaining) != 0 || command.CommandPath() != "test one two three four" {
		t.Fatal("command path depth was truncated", command, remaining, err)
	}
}

func TestRemovedPresentationRoutesHaveNoAliases(t *testing.T) {
	for _, path := range []string{"canvas assignments list", "public calendars list", "public notices list", "public notices search", "public services list", "campus notices list", "campus notices get", "development secondclass education"} {
		var out, diag bytes.Buffer
		args := append(strings.Fields(path), "--json")
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "INVALID_ARGUMENT") {
			t.Fatal("removed route still accepted", path, out.String(), diag.String())
		}
	}
}

func TestServiceJSONAndTextPreserveConnectionWithoutPrivatePath(t *testing.T) {
	base := contracts.ServiceEntry{ID: "synthetic", AvailableCapabilities: []contracts.CapabilitySummary{{ID: "canvas.assignments.list", CLICommand: "sjtu learning canvas assignments list"}}}
	presented := serviceEntryForCLI(base, true)
	if presented.AvailableCapabilities[0].CLICommand != "sjtu learning canvas assignments list --connection CONNECTION_FILE" || strings.Contains(base.AvailableCapabilities[0].CLICommand, "--connection") {
		t.Fatal("connection presentation mutated shared metadata", base, presented)
	}
	var out bytes.Buffer
	if err := writeServiceEntry(&out, base, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "对象：尚无明确对象说明") || !strings.Contains(out.String(), presented.AvailableCapabilities[0].CLICommand) {
		t.Fatal("text and JSON connection hints differ", out.String())
	}
	out.Reset()
	base.AvailableCapabilities = append(base.AvailableCapabilities, base.AvailableCapabilities[0], base.AvailableCapabilities[0], base.AvailableCapabilities[0])
	if err := writeServiceSummary(&out, base, true); err != nil || strings.Count(out.String(), "sjtu learning canvas") != 3 || !strings.Contains(out.String(), "仅列前 3 项") {
		t.Fatal("search dumped all action descriptions", out.String(), err)
	}
}
