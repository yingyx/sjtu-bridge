package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/broker"
)

func TestReuseLoginTargetsRequiresActiveCampusSource(t *testing.T) {
	for _, test := range []struct {
		name, raw, source, code string
	}{
		{"invalid_metadata", `{`, "source", "UPSTREAM_CHANGED"},
		{"missing", `{"credentials":{}}`, "source", "AUTH_REQUIRED"},
		{"inactive", `{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":false}}}`, "source", "AUTH_REQUIRED"},
		{"canvas", `{"credentials":{"source":{"provider":"canvas","kind":"CanvasToken","active":true}}}`, "source", "AUTH_REQUIRED"},
		{"wrong_kind", `{"credentials":{"source":{"provider":"teaching","kind":"CanvasToken","active":true}}}`, "source", "AUTH_REQUIRED"},
		{"unsupported_portal", `{"credentials":{"source":{"provider":"jaccount","kind":"JAAuthCookie","active":true}}}`, "source", "AUTH_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			targets, err := reuseLoginTargets([]byte(test.raw), test.source, []string{"teaching", "sjtumail"}, false)
			if err == nil || loginErrorCode(err) != test.code || targets != nil {
				t.Fatal("invalid source was accepted")
			}
		})
	}
}

func TestReuseLoginTargetsSelectsOnlyOtherPortal(t *testing.T) {
	for _, test := range []struct {
		provider, target string
	}{
		{"teaching", "sjtumail"}, {"sjtumail", "teaching"},
	} {
		raw := []byte(`{"credentials":{"source":{"provider":"` + test.provider + `","kind":"JAAuthCookie","active":true}}}`)
		for _, explicit := range []bool{false, true} {
			providers := []string{"teaching", "sjtumail"}
			if explicit {
				providers = []string{test.target}
			}
			got, err := reuseLoginTargets(raw, "source", providers, explicit)
			if err != nil || !reflect.DeepEqual(got, []string{test.target}) {
				t.Fatal("other portal was not selected")
			}
		}
		for _, providers := range [][]string{{test.provider}, {test.provider, test.target}} {
			if _, err := reuseLoginTargets(raw, "source", providers, true); err == nil || loginErrorCode(err) != "INVALID_ARGUMENT" {
				t.Fatal("explicit source portal was accepted as target")
			}
		}
	}
}

func TestReuseLoginTargetsExplicitActivityPortal(t *testing.T) {
	raw := []byte(`{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":true}}}`)
	got, e := reuseLoginTargets(raw, "source", []string{"activity"}, true)
	if e != nil || !reflect.DeepEqual(got, []string{"activity"}) {
		t.Fatal("explicit activity target unavailable", e)
	}
	raw = []byte(`{"credentials":{"source":{"provider":"activity","kind":"JAAuthCookie","active":true}}}`)
	if _, e = reuseLoginTargets(raw, "source", []string{"activity"}, true); e == nil {
		t.Fatal("activity source selected as its own target")
	}
}

func TestPortalLoginRetainsCommittedStateAndPerPortalFailures(t *testing.T) {
	results := []portalLoginResult{
		{Provider: "teaching", Status: "verified", AccountRef: "synthetic:teaching"},
		{Provider: "sjtumail", Status: "verified", AccountRef: "synthetic:mail"},
		{Provider: "activity"},
	}
	recordPortalLoginFailure(&results[2], contracts.Fail("UPSTREAM_CHANGED", "Synthetic portal structure changed"), false)
	calls := []string{}
	importVerifiedPortals(context.Background(), results, nil, "synthetic-source", "main", func(_ context.Context, q broker.AdminRequest, out any) error {
		calls = append(calls, q.Provider)
		if q.Action != "import" || q.SourceCredentialID != "synthetic-source" || q.ExpectedAccount == "" || q.Label != "main" {
			t.Fatal("import lost its verified context")
		}
		if q.Provider == "sjtumail" {
			return contracts.Fail("AUDIT_UNAVAILABLE", "State committed; audit unavailable")
		}
		return json.Unmarshal([]byte(`{"credential_id":"synthetic-credential"}`), out)
	})
	if !reflect.DeepEqual(calls, []string{"teaching", "sjtumail"}) {
		t.Fatal("unverified portal was imported", calls)
	}
	if results[0].Status != "imported" || results[0].CredentialID != "synthetic-credential" || !results[0].StateCommitted {
		t.Fatal("successful import lost", results[0])
	}
	if results[1].Status != "imported_audit_failed" || !results[1].StateCommitted || results[1].ErrorCode != "AUDIT_UNAVAILABLE" || !strings.Contains(results[1].Hint, "auth status") {
		t.Fatal("committed import mislabeled", results[1])
	}
	if results[2].Status != "verification_failed" || results[2].ErrorCode != "UPSTREAM_CHANGED" || !strings.Contains(results[2].Message, "structure changed") {
		t.Fatal("portal failure reason lost", results[2])
	}
	var issue *cliError
	if !errors.As(loginResultsError(results), &issue) || issue.Code != "PARTIAL_FAILURE" {
		t.Fatal("partial failure reported as authentication failure", issue)
	}
}

func TestPortalLoginFailureDoesNotGuessCommitOrAuthenticationState(t *testing.T) {
	for _, test := range []struct {
		name, code string
		failure    error
		importing  bool
	}{
		{"verification", "UPSTREAM_UNAVAILABLE", contracts.Fail("UPSTREAM_UNAVAILABLE", "Synthetic unavailable portal"), false},
		{"import", "BROKER_UNAVAILABLE", contracts.Fail("BROKER_UNAVAILABLE", "Synthetic lost connection"), true},
		{"unknown", "CHECK_FAILED", errors.New("sensitive raw transport detail"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := portalLoginResult{Provider: "teaching"}
			recordPortalLoginFailure(&result, test.failure, test.importing)
			if result.StateCommitted || result.ErrorCode != test.code || strings.Contains(result.Message, "sensitive") {
				t.Fatal(result)
			}
			if test.importing && !strings.Contains(result.Hint, "auth status") {
				t.Fatal("ambiguous import must direct state inspection", result)
			}
			var issue *cliError
			if !errors.As(loginResultsError([]portalLoginResult{result}), &issue) || issue.Code != test.code {
				t.Fatal("failure reason collapsed", issue)
			}
		})
	}
	if err := loginResultsError([]portalLoginResult{{Status: "imported", StateCommitted: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestDektLoginProviderAndReuseStayExplicit(t *testing.T) {
	if !campusCredentialProvider("dekt") || campusCredentialProvider("dektcjd") {
		t.Fatal("reviewed dekt provider alias changed")
	}
	raw := []byte(`{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":true}}}`)
	got, e := reuseLoginTargets(raw, "source", []string{"activity", "dekt"}, true)
	if e != nil || !reflect.DeepEqual(got, []string{"activity", "dekt"}) {
		t.Fatal("explicit distinct portal targets unavailable", e)
	}
	raw = []byte(`{"credentials":{"source":{"provider":"dekt","kind":"JAAuthCookie","active":true}}}`)
	if _, e = reuseLoginTargets(raw, "source", []string{"dekt"}, true); e == nil || loginErrorCode(e) != "INVALID_ARGUMENT" {
		t.Fatal("source reused as its own portal target")
	}
	got, e = reuseLoginTargets(raw, "source", []string{"teaching", "sjtumail"}, false)
	if e != nil || !reflect.DeepEqual(got, []string{"teaching", "sjtumail"}) {
		t.Fatal("dekt source silently changed default target portals", e)
	}
}

func TestBathLoginProviderAndReuseStayExplicit(t *testing.T) {
	if !campusCredentialProvider("bath") {
		t.Fatal("reviewed bath provider unavailable")
	}
	raw := []byte(`{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":true}}}`)
	got, err := reuseLoginTargets(raw, "source", []string{"bath"}, true)
	if err != nil || !reflect.DeepEqual(got, []string{"bath"}) {
		t.Fatal("explicit bath target unavailable", err)
	}
	raw = []byte(`{"credentials":{"source":{"provider":"bath","kind":"JAAuthCookie","active":true}}}`)
	if _, err := reuseLoginTargets(raw, "source", []string{"bath"}, true); err == nil || loginErrorCode(err) != "INVALID_ARGUMENT" {
		t.Fatal("bath source reused as its own portal target")
	}
	got, err = reuseLoginTargets(raw, "source", []string{"teaching", "sjtumail"}, false)
	if err != nil || !reflect.DeepEqual(got, []string{"teaching", "sjtumail"}) {
		t.Fatal("bath source changed default target portals", err)
	}
}

func TestCampusLoginAcceptsAllReviewedPortalsBeforeTerminalCheck(t *testing.T) {
	for _, test := range []struct {
		name, providers, code string
	}{
		{"all_six", "teaching,sjtumail,activity,dekt,electric,bath", "AUTH_REQUIRED"},
		{"bath", "bath", "AUTH_REQUIRED"},
		{"duplicate", "bath,bath", "INVALID_ARGUMENT"},
		{"unknown", "unknown", "INVALID_ARGUMENT"},
		{"too_many", "teaching,sjtumail,activity,dekt,electric,bath,unknown", "INVALID_ARGUMENT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			jsonOutput, verbose := false, false
			cmd := campusLoginCommand(&out, &diag, strings.NewReader(""), &jsonOutput, &verbose)
			if err := cmd.Flags().Set("providers", test.providers); err != nil {
				t.Fatal(err)
			}
			// Synthetic nonterminal input stops before Broker access or QR login.
			err := cmd.RunE(cmd, nil)
			if err == nil || loginErrorCode(err) != test.code {
				t.Fatalf("got %v, want %s", err, test.code)
			}
			if test.name == "unknown" && !strings.Contains(err.Error(), "bath") {
				t.Fatal("provider guidance omits bath")
			}
		})
	}
}
