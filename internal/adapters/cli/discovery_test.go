package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
		{[]string{"list"}, []string{"administration.fees.get", "administration.fees.list", "administration.payments.navigate", "campus.buildings.list", "campus.maps.navigate", "campus.organizations.list", "development.activities.navigate", "development.notices.get", "development.notices.list", "digital.network.navigate", "digital.software.list", "digital.software.navigate", "digital.support.list", "learning.practice.navigate", "learning.semester.current", "library.borrowing.list", "library.loans.navigate", "library.spaces.navigate", "life.dining.areas", "life.dining.list", "life.dining.series", "life.health.get", "life.health.list", "life.sports.navigate", "life.utilities.navigate", "public.calendars.list", "public.notices.list", "public.notices.search", "public.services.list", "research.computing.navigate", "research.hardware.list"}},
		{[]string{"search", "校历"}, []string{"learning.semester.current", "public.calendars.list"}},
		{[]string{"search", "教务 通知"}, []string{"public.notices.list", "public.notices.search"}},
		{[]string{"list", "--category", "learning", "--service", "教务处"}, []string{"public.calendars.list", "public.notices.list", "public.notices.search"}},
		{[]string{"search", "作业"}, []string{}},
		{[]string{"search", "宿舍洗浴"}, []string{"life.utilities.navigate"}},
		{[]string{"search", "软件目录"}, []string{"digital.software.list"}},
		{[]string{"search", "当前学期"}, []string{"learning.semester.current"}},
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
		var caps []contracts.Capability
		if json.Unmarshal(out.Bytes(), &caps) != nil || caps == nil || len(caps) != len(test.ids) {
			t.Fatal(out.String())
		}
		for i, c := range caps {
			if c.ID != test.ids[i] || c.Title == "" || c.Service == "" || c.Category == "" || c.CLICommand == "" || c.MCPTool == "" || len(c.Aliases) == 0 || c.InputSchema != nil || c.OutputSchema != nil {
				t.Fatal(out.String())
			}
		}
	}
	var out, diag bytes.Buffer
	if Execute(context.Background(), []string{"capabilities", "describe", "public.calendars.list"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var detail contracts.Capability
	if json.Unmarshal(out.Bytes(), &detail) != nil || len(detail.InputSchema) == 0 || len(detail.OutputSchema) == 0 {
		t.Fatal("missing detail", out.String())
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
