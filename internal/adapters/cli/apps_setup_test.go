package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/capabilities"
)

type setupFake struct {
	status     map[string]any
	requests   []broker.AdminRequest
	prepared   bool
	failAction string
	failCap    string
}

func (f *setupFake) deps() setupDependencies {
	return setupDependencies{
		requireTerminal: func(io.Reader) error { return nil },
		prepareDelivery: func(string) error { f.prepared = true; return nil },
		call: func(_ context.Context, q broker.AdminRequest, out any) error {
			f.requests = append(f.requests, q)
			if q.Action == f.failAction && (f.failCap == "" || q.Capability == f.failCap) {
				return contracts.Fail("AUDIT_UNAVAILABLE", "synthetic operation failed")
			}
			var value any
			switch q.Action {
			case "status":
				value = f.status
			case "courses":
				value = []map[string]string{{"course_ref": "canvas:course:42", "title": "合成课程"}}
			case "resources":
				value = map[string]any{"account_ref": "electric:user:synthetic", "resource_refs": []string{"electric:room:7"}}
			case "grant":
				value = map[string]string{"grant_id": "grant-" + q.Capability}
			default:
				value = map[string]string{}
			}
			data, _ := json.Marshal(value)
			return json.Unmarshal(data, out)
		},
	}
}

func syntheticSetupStatus(existing bool) map[string]any {
	status := map[string]any{
		"apps": []any{},
		"credentials": map[string]any{"credential-synthetic": map[string]any{
			"provider": "canvas", "account_ref": "canvas:user:7", "label": "合成账号", "active": true,
		}},
		"grants": map[string]any{},
	}
	if existing {
		status["apps"] = []any{map[string]any{"id": "study", "active": true, "expires": time.Now().Add(time.Hour)}}
		status["grants"] = map[string]any{
			"existing-grant":  map[string]any{"client_id": "study", "capability": contracts.AssignmentsCapability, "account_ref": "canvas:user:7", "view": "summary_v1", "limit": 10, "active": true, "expires": time.Now().Add(time.Hour)},
			"other-app-grant": map[string]any{"client_id": "other", "capability": "canvas.files.list", "active": true},
		}
	}
	return status
}

func setupCapabilityNumber(provider, id string) int {
	n := 0
	for _, c := range capabilities.Catalog(nil) {
		if c.Provider == provider {
			n++
			if c.ID == id {
				return n
			}
		}
	}
	panic("test capability is unavailable")
}

func canvasSetupInput(confirm string) string {
	// Account; two capabilities; each course, view if needed, and limit;
	// finish adding accounts; one final confirmation.
	return fmt.Sprintf("1\n%d,%d\n1\n2\n10\n1\n15\n\n%s\n", setupCapabilityNumber("canvas", contracts.AssignmentsCapability), setupCapabilityNumber("canvas", "canvas.files.list"), confirm)
}

func mutationRequests(requests []broker.AdminRequest) []broker.AdminRequest {
	result := []broker.AdminRequest{}
	for _, q := range requests {
		if q.Action != "status" && q.Action != "courses" && q.Action != "resources" {
			result = append(result, q)
		}
	}
	return result
}

func TestAppSetupCreatesOneConnectionAndMultipleCapabilities(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(false)}
	path := filepath.Join(t.TempDir(), "study.json")
	var diag bytes.Buffer
	result, err := runAppSetup(context.Background(), "study", path, 7, strings.NewReader(canvasSetupInput("yes")), &diag, f.deps())
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, diag.String())
	}
	mutations := mutationRequests(f.requests)
	if !f.prepared || result.Status != "complete" || len(mutations) != 3 || len(result.Steps) != 3 {
		t.Fatalf("result=%+v requests=%+v", result, mutations)
	}
	if mutations[0].Action != "app_create" || mutations[0].Path != path || result.Steps[0].Connection != path {
		t.Fatalf("connection: %+v", mutations[0])
	}
	for _, q := range mutations[1:] {
		if q.Action != "grant" || q.ClientID != "study" || q.CredentialID != "credential-synthetic" || q.AccountRef != "canvas:user:7" || len(q.Courses) != 1 || q.Courses[0] != "canvas:course:42" || q.Days != 7 {
			t.Fatalf("wrong grant: %+v", q)
		}
	}
	if mutations[1].View != "deadlines_v1" || mutations[1].Limit != 10 || mutations[2].View != "metadata_v1" || mutations[2].Limit != 15 {
		t.Fatalf("wrong views/limits: %+v", mutations)
	}
	if !strings.Contains(diag.String(), "合成课程") || !strings.Contains(diag.String(), "同一能力") {
		t.Fatal("missing scope/replace preview")
	}
}

func TestAppSetupExistingAppRevokesOnlySelectedGrantAndKeepsConnection(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(true)}
	// Remove existing-grant, then configure files metadata only.
	input := fmt.Sprintf("1\n1\n%d\n1\n\n\nyes\n", setupCapabilityNumber("canvas", "canvas.files.list"))
	result, err := runAppSetup(context.Background(), "study", "", 30, strings.NewReader(input), io.Discard, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	mutations := mutationRequests(f.requests)
	if f.prepared || len(mutations) != 2 || mutations[0].Action != "grant_revoke" || mutations[0].GrantID != "existing-grant" || mutations[0].ClientID != "study" || mutations[1].Capability != "canvas.files.list" || result.Steps[0].GrantID != "existing-grant" {
		t.Fatalf("requests=%+v result=%+v", mutations, result)
	}
}

func TestAppSetupCancellationNeverMutates(t *testing.T) {
	for _, input := range []string{"q\n", "1\n", canvasSetupInput("no"), canvasSetupInput("q")} {
		f := &setupFake{status: syntheticSetupStatus(false)}
		result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(input), io.Discard, f.deps())
		if err == nil || result != nil || f.prepared || len(mutationRequests(f.requests)) != 0 {
			t.Fatalf("cancel applied changes: result=%+v err=%v requests=%+v", result, err, f.requests)
		}
	}
}

func TestAppSetupReportsPartialFailureWithoutRollback(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(false), failAction: "grant", failCap: "canvas.files.list"}
	result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(canvasSetupInput("yes")), io.Discard, f.deps())
	if err == nil || result == nil || result.Status != "incomplete" || len(result.Steps) != 3 || result.Steps[1].Status != "applied" || result.Steps[2].Status != "failed_state_unknown" || result.Steps[2].ErrorCode != "AUDIT_UNAVAILABLE" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(mutationRequests(f.requests)) != 3 {
		t.Fatalf("unexpected rollback: %+v", f.requests)
	}
}

func TestAppSetupRejectsExpiredAppAndNonTerminalBeforeMutation(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(true)}
	f.status["apps"] = []any{map[string]any{"id": "study", "active": true, "expires": time.Now().Add(-time.Hour)}}
	if _, err := runAppSetup(context.Background(), "study", "", 30, strings.NewReader(""), io.Discard, f.deps()); err == nil || len(mutationRequests(f.requests)) != 0 {
		t.Fatal("expired app accepted")
	}
	deps := f.deps()
	deps.requireTerminal = func(io.Reader) error { return contracts.Fail("INTERACTIVE_REQUIRED", "terminal required") }
	f.requests = nil
	if _, err := runAppSetup(context.Background(), "study", "", 30, strings.NewReader(""), io.Discard, deps); err == nil || len(f.requests) != 0 {
		t.Fatal("nonterminal reached owner API")
	}
}

func TestAppSetupFixedResourceUsesSharedSchema(t *testing.T) {
	f := &setupFake{status: map[string]any{
		"apps": []any{}, "grants": map[string]any{},
		"credentials": map[string]any{"credential-synthetic": map[string]any{"provider": "sjtumail", "account_ref": "sjtumail:user:synthetic", "active": true}},
	}}
	input := fmt.Sprintf("1\n%d\n1\n5\n\nyes\n", setupCapabilityNumber("sjtumail", contracts.MailCapability))
	result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(input), io.Discard, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	grants := mutationRequests(f.requests)
	if len(grants) != 2 || len(grants[1].Resources) != 1 || grants[1].Resources[0] != "mail:folder:inbox" || result.Status != "complete" {
		t.Fatalf("schema scope missing: %+v", grants)
	}
}

func TestAppSetupAddsDifferentProvidersInOneSession(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(false)}
	f.status["credentials"].(map[string]any)["z-mail"] = map[string]any{"provider": "sjtumail", "account_ref": "sjtumail:user:synthetic", "active": true}
	input := fmt.Sprintf("1\n%d\n1\n1\n10\n2\n%d\n1\n5\n\nyes\n", setupCapabilityNumber("canvas", contracts.AssignmentsCapability), setupCapabilityNumber("sjtumail", contracts.MailCapability))
	result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(input), io.Discard, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	mutations := mutationRequests(f.requests)
	if result.Status != "complete" || len(mutations) != 3 || mutations[1].CredentialID != "credential-synthetic" || mutations[2].CredentialID != "z-mail" || mutations[2].Capability != contracts.MailCapability {
		t.Fatalf("requests=%+v result=%+v", mutations, result)
	}
}

func TestAppSetupDiscoversBoundResourceBeforeConfirmation(t *testing.T) {
	f := &setupFake{status: map[string]any{
		"credentials": map[string]any{"credential-synthetic": map[string]any{"provider": "electric", "account_ref": "electric:user:synthetic", "active": true}},
	}}
	input := fmt.Sprintf("1\n%d\n1\n\n\nyes\n", setupCapabilityNumber("electric", contracts.ElectricityBalanceCapability))
	result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(input), io.Discard, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 4 || f.requests[1].Action != "resources" || f.requests[2].Action != "app_create" || len(f.requests[3].Resources) != 1 || f.requests[3].Resources[0] != "electric:room:7" || result.Status != "complete" {
		t.Fatalf("requests=%+v result=%+v", f.requests, result)
	}
}

func TestAppSetupReadFailureAndInvalidScopeNeverMutate(t *testing.T) {
	f := &setupFake{status: syntheticSetupStatus(false), failAction: "courses"}
	input := fmt.Sprintf("1\n%d\n", setupCapabilityNumber("canvas", contracts.AssignmentsCapability))
	if result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader(input), io.Discard, f.deps()); err == nil || result != nil || f.prepared || len(mutationRequests(f.requests)) != 0 {
		t.Fatalf("failed discovery mutated: result=%+v err=%v", result, err)
	}
	f = &setupFake{status: map[string]any{
		"credentials": map[string]any{"credential-synthetic": map[string]any{"provider": "teaching", "account_ref": "teaching:user:synthetic", "active": true}},
	}}
	if result, err := runAppSetup(context.Background(), "study", filepath.Join(t.TempDir(), "study.json"), 30, strings.NewReader("1\n1\ninvalid-resource\n\n"), io.Discard, f.deps()); err == nil || result != nil || f.prepared || len(mutationRequests(f.requests)) != 0 {
		t.Fatalf("invalid scope mutated: result=%+v err=%v", result, err)
	}
}

func TestPermissionsListFiltersAndIncludesInactiveGrants(t *testing.T) {
	var status setupStatus
	raw, _ := json.Marshal(syntheticSetupStatus(true))
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	grant := status.Grants["existing-grant"]
	grant.Active = false
	status.Grants["existing-grant"] = grant
	rows := permissionsFor(status, "study")
	if len(rows) != 1 || rows[0].ID != "existing-grant" || rows[0].Active || rows[0].ClientID != "study" || len(permissionsFor(status, "")) != 2 {
		t.Fatalf("wrong grants: %+v", rows)
	}
}

func TestAppSetupAndIndividualPermissionHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{[]string{"apps", "setup", "--help"}, "--output"}, {[]string{"permissions", "list", "--help"}, "--app"}, {[]string{"permissions", "revoke", "--help"}, "--grant"}} {
		var out, diag bytes.Buffer
		if code := Execute(context.Background(), tc.args, &out, &diag, strings.NewReader("")); code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v: code=%d out=%s diag=%s", tc.args, code, out.String(), diag.String())
		}
	}
}
