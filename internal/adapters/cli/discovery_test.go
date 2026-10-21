package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestHelpSeparatesTasksManagementAndRuntime(t *testing.T) {
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"--help"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	help := out.String()
	discovery := strings.Index(help, "能力发现：")
	services := strings.Index(help, "校园服务：")
	management := strings.Index(help, "用户管理：")
	runtime := strings.Index(help, "接入与诊断：")
	if discovery < 0 || services <= discovery || management <= services || runtime <= management {
		t.Fatal(help)
	}
	for _, check := range []struct {
		start, end int
		names      []string
	}{
		{discovery, services, []string{"capabilities"}},
		{services, management, []string{"canvas", "public"}},
		{management, runtime, []string{"auth", "apps", "broker", "permissions"}},
		{runtime, len(help), []string{"doctor", "mcp"}},
	} {
		for _, name := range check.names {
			if !strings.Contains(help[check.start:check.end], name) {
				t.Fatalf("%s in wrong help section:\n%s", name, help)
			}
		}
	}
}

func TestPublicTaskDiscoveryAndDetails(t *testing.T) {
	for _, test := range []struct {
		args []string
		ids  []string
	}{
		{[]string{"list"}, []string{"administration.fees.get", "administration.fees.list", "administration.payments.navigate", "campus.buildings.details", "campus.buildings.list", "campus.contacts.details", "campus.contacts.list", "campus.maps.navigate", "campus.notices.get", "campus.notices.list", "campus.organizations.list", "development.activities.navigate", "development.notices.get", "development.notices.list", "digital.network.navigate", "digital.software.list", "digital.software.navigate", "digital.support.list", "learning.classrooms.available", "learning.classrooms.details", "learning.classrooms.filters", "learning.classrooms.list", "learning.classrooms.navigate", "learning.classrooms.schedule", "learning.classrooms.types", "learning.courses.search", "learning.practice.navigate", "learning.semester.current", "learning.semester.periods", "library.borrowing.list", "library.loans.navigate", "library.spaces.navigate", "life.dining.areas", "life.dining.list", "life.dining.series", "life.health.get", "life.health.list", "life.lostfound.list", "life.lostfound.navigate", "life.sports.navigate", "life.utilities.navigate", "public.calendars.list", "public.notices.list", "public.notices.search", "public.services.list", "research.computing.navigate", "research.hardware.list"}},
		{[]string{"search", "校历"}, []string{"public.calendars.list", "campus.contacts.details", "campus.contacts.list", "learning.classrooms.available", "learning.classrooms.details", "learning.classrooms.filters", "learning.classrooms.list", "learning.classrooms.navigate", "learning.classrooms.schedule", "learning.classrooms.types", "learning.courses.search", "learning.semester.current", "learning.semester.periods"}},
		{[]string{"search", "教务 通知"}, []string{"public.notices.list", "public.notices.search"}},
		{[]string{"list", "--category", "learning", "--service", "教务处"}, []string{"public.calendars.list", "public.notices.list", "public.notices.search"}},
		{[]string{"search", "作业"}, []string{}},
		{[]string{"search", "宿舍洗浴"}, []string{"life.utilities.navigate"}},
		{[]string{"search", "软件目录"}, []string{"digital.software.list"}},
		{[]string{"search", "当前学期"}, []string{"learning.semester.current"}},
		{[]string{"search", "空闲教室"}, []string{"learning.classrooms.available"}},
		{[]string{"search", "失物招领"}, []string{"life.lostfound.list", "life.lostfound.navigate"}},
		{[]string{"list", "--category", "development", "--task-group", "activities"}, []string{"development.activities.navigate", "development.notices.get", "development.notices.list", "learning.practice.navigate"}},
		{[]string{"search", "二课", "--category", "learning", "--task-group", "practice"}, []string{"development.activities.navigate", "learning.practice.navigate"}},
		{[]string{"list", "--category", "research", "--task-group", "activities"}, []string{}},
	} {
		var out, diag bytes.Buffer
		args := append([]string{"capabilities"}, test.args...)
		args = append(args, "--json")
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(diag.String())
		}
		var caps []contracts.CapabilitySummary
		if json.Unmarshal(out.Bytes(), &caps) != nil || caps == nil || len(caps) != len(test.ids) {
			t.Fatal(out.String())
		}
		for i, c := range caps {
			if c.ID != test.ids[i] || c.Title == "" || c.Description == "" || c.Service == "" || c.Category == "" || c.CLICommand == "" || c.MCPTool == "" {
				t.Fatal(out.String())
			}
		}
	}
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"capabilities", "describe", "public.calendars.list", "--verbose"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var detail contracts.Capability
	if json.Unmarshal(out.Bytes(), &detail) != nil || len(detail.InputSchema) == 0 || len(detail.OutputSchema) == 0 {
		t.Fatal("missing detail", out.String())
	}
	if !strings.Contains(out.String(), "\n  ") {
		t.Fatal("verbose JSON should be readable", out.String())
	}
}

func TestDiscoveryUsesTaskSummariesAndActionableDescriptions(t *testing.T) {
	for _, args := range [][]string{
		{"capabilities", "search", "空闲教室"},
		{"capabilities", "search", "空闲教室", "--json"},
	} {
		var out, diag bytes.Buffer
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(diag.String())
		}
		if !strings.Contains(out.String(), "sjtu learning classrooms available") || strings.Contains(out.String(), "execution_rule") || strings.Contains(out.String(), "reversibility") || strings.Contains(out.String(), "不提供轮询") {
			t.Fatal("default discovery is not concise", out.String())
		}
	}
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"capabilities", "describe", "learning.classrooms.available"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	for _, wanted := range []string{"--building-id", "--date", "--start-section", "--end-section", "必填", "YYYY-MM-DD", "sjtu learning classrooms available"} {
		if !strings.Contains(out.String(), wanted) {
			t.Fatalf("missing %s: %s", wanted, out.String())
		}
	}
	if strings.Contains(out.String(), "output_schema") {
		t.Fatal("default describe expanded output schema")
	}
	out.Reset()
	diag.Reset()
	if Execute(context.Background(), []string{"capabilities", "describe", "learning.classrooms.list", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var usage capabilityUsage
	if json.Unmarshal(out.Bytes(), &usage) != nil || len(usage.InputSchema) == 0 || usage.CLIFlags["building_id"] != "--building-id" || usage.Example == "" {
		t.Fatal("incomplete JSON call guide", out.String())
	}
	if strings.Contains(out.String(), "output_schema") || strings.Contains(out.String(), "execution_rule") {
		t.Fatal("default JSON call guide contains full contract", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatal("default JSON should be compact", out.String())
	}
}

func TestDescribeSuggestionsUseOnlyVisibleCapabilities(t *testing.T) {
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"capabilities", "describe", "public.calendars.lsit", "--json"}, &out, &diag, strings.NewReader("")) == 0 || !strings.Contains(diag.String(), "public.calendars.list") || out.Len() != 0 {
		t.Fatal("missing visible spelling suggestion", out.String(), diag.String())
	}
	hidden := contracts.AssignmentCapability("canvas:user:7", []string{"canvas:course:1"}, "deadlines_v1", 10)
	hidden.Authorized = false
	err := unavailableCapability("canvas.assignments.lis", []contracts.Capability{hidden})
	if err == nil || strings.Contains(err.Error(), contracts.AssignmentsCapability) || strings.Contains(err.Error(), "canvas:user") {
		t.Fatal("suggestion leaked hidden capability", err)
	}
}

func TestPersonalDescribeMapsSchemaToCLIFlags(t *testing.T) {
	root := New(io.Discard, io.Discard, strings.NewReader(""))
	c := contracts.AssignmentCapability("canvas:user:7", []string{"canvas:course:1"}, "deadlines_v1", 3)
	var out bytes.Buffer
	if err := writeCapabilityUsage(&out, root, c, true); err != nil {
		t.Fatal(err)
	}
	var usage capabilityUsage
	if json.Unmarshal(out.Bytes(), &usage) != nil || usage.CLIFlags["account_ref"] != "--account" || usage.CLIFlags["course_ref"] != "--course" || !strings.Contains(usage.Example, "--limit 3") || strings.Count(usage.Example, "--connection CONNECTION_FILE") != 1 {
		t.Fatal(out.String())
	}
}

func TestConnectedPublicGuidePreservesContextWithoutExposingPath(t *testing.T) {
	var capability contracts.Capability
	for _, c := range contracts.PublicCapabilities() {
		if c.ID == "public.services.list" {
			capability = c
		}
	}
	capability.InputSchema = contracts.PublicInputSchema(capability.ID)
	for _, connected := range []bool{false, true} {
		root := New(io.Discard, io.Discard, strings.NewReader(""))
		if connected {
			if err := root.PersistentFlags().Set("connection", "C:/SYNTHETIC_PRIVATE_PATH/connection.json"); err != nil {
				t.Fatal(err)
			}
		}
		for _, asJSON := range []bool{false, true} {
			var out bytes.Buffer
			if err := writeCapabilityUsage(&out, root, capability, asJSON); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "SYNTHETIC_PRIVATE_PATH") || strings.Contains(out.String(), "--connection CONNECTION_FILE") != connected {
				t.Fatalf("connection context lost or path exposed: %s", out.String())
			}
			if asJSON {
				var usage capabilityUsage
				if json.Unmarshal(out.Bytes(), &usage) != nil || strings.Count(usage.Example, "--connection") != map[bool]int{false: 0, true: 1}[connected] {
					t.Fatal(out.String())
				}
			}
		}
		summary := discoverySummaries([]contracts.Capability{capability}, connected)[0]
		if strings.HasSuffix(summary.CLICommand, " --connection CONNECTION_FILE") != connected {
			t.Fatal("summary lost connection context", summary)
		}
	}
	if strings.Contains(contracts.CapabilitySummaries([]contracts.Capability{capability})[0].CLICommand, "--connection") {
		t.Fatal("CLI context changed the shared capability mapping")
	}
}

func TestDiscoveryRejectsUnknownFilters(t *testing.T) {
	for _, flags := range [][]string{{"--category", "learnng"}, {"--task-group", "SYNTHETIC_PRIVATE_VALUE"}} {
		for _, verb := range [][]string{{"list"}, {"search", "校历"}} {
			args := append([]string{"capabilities"}, verb...)
			args = append(args, flags...)
			args = append(args, "--json")
			var out, diag bytes.Buffer
			if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "INVALID_ARGUMENT") || !strings.Contains(diag.String(), flags[0]) || strings.Contains(diag.String(), "SYNTHETIC_PRIVATE_VALUE") {
				t.Fatal(args, out.String(), diag.String())
			}
		}
	}
}

func TestDiscoveryDoesNotReportSuccessWhenOutputFails(t *testing.T) {
	for _, args := range [][]string{
		{"capabilities", "list"},
		{"capabilities", "search", "校历"},
		{"capabilities", "search", "no-such-synthetic-task"},
		{"capabilities", "describe", "public.services.list"},
		{"capabilities", "describe", "public.services.list", "--json"},
		{"capabilities", "describe", "public.services.list", "--verbose"},
	} {
		var diag bytes.Buffer
		if Execute(context.Background(), args, failingOutput{}, &diag, strings.NewReader("")) == 0 || !strings.Contains(diag.String(), "INTERNAL_ERROR") {
			t.Fatal("output failure reported as success", args, diag.String())
		}
	}
}

func TestConnectedDiscoveryNeverFallsBackToPublic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.connection")
	for _, args := range [][]string{
		{"capabilities", "list"}, {"capabilities", "search", "校历"},
		{"capabilities", "describe", "public.services.list"}, {"public", "services", "list"},
	} {
		var out, diag bytes.Buffer
		args = append(args, "--connection", path, "--json")
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "CLIENT_UNAUTHORIZED") {
			t.Fatalf("connection silently ignored: %v\n%s\n%s", args, out.String(), diag.String())
		}
	}
}

func TestExplicitEmptyConnectionIsRejected(t *testing.T) {
	for _, args := range [][]string{
		{"capabilities", "list"}, {"capabilities", "search", "校历"},
		{"capabilities", "describe", "public.services.list"},
		{"public", "services", "list"}, {"mcp", "serve"},
	} {
		for _, value := range []string{"", "   "} {
			var out, diag bytes.Buffer
			command := append(append([]string{}, args...), "--connection="+value, "--json")
			if Execute(context.Background(), command, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "CLIENT_UNAUTHORIZED") {
				t.Fatalf("explicit empty connection accepted: %v\n%s\n%s", command, out.String(), diag.String())
			}
		}
	}
}
