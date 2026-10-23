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

func TestShuiyuanAuthorizeRefusesNonTerminalWithoutDisplayingLink(t *testing.T) {
	var out, diag bytes.Buffer
	jsonOutput, verbose := false, false
	command := shuiyuanAuthorizeCommand(&out, &diag, strings.NewReader("SYNTHETIC_PAYLOAD"), &jsonOutput, &verbose)
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(&out)
	command.SetErr(&diag)
	command.SetArgs([]string{"--provider", "shuiyuan", "--account", "main"})
	err := command.ExecuteContext(context.Background())
	var typed *contracts.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_REQUIRED" || out.Len() != 0 || diag.Len() != 0 || strings.Contains(err.Error(), "SYNTHETIC_PAYLOAD") {
		t.Fatal("authorization accepted redirected input or disclosed a request")
	}
	for _, flag := range []string{"token", "cookie", "payload", "secret", "callback", "scopes", "public-key", "private-key"} {
		if command.Flags().Lookup(flag) != nil {
			t.Fatalf("unexpected credential or handshake override flag: %s", flag)
		}
	}
}

func TestShuiyuanAuthorizeRejectsOtherProvidersAndInvalidLabels(t *testing.T) {
	for _, args := range [][]string{{}, {"--provider", "canvas"}, {"--provider", "shuiyuan", "--account", "../private"}} {
		var out, diag bytes.Buffer
		jsonOutput, verbose := false, false
		command := shuiyuanAuthorizeCommand(&out, &diag, strings.NewReader(""), &jsonOutput, &verbose)
		command.SilenceUsage, command.SilenceErrors = true, true
		command.SetOut(&out)
		command.SetErr(&diag)
		command.SetArgs(args)
		err := command.ExecuteContext(context.Background())
		var typed *contracts.Error
		if !errors.As(err, &typed) || typed.Code != "INVALID_ARGUMENT" || out.Len() != 0 || diag.Len() != 0 {
			t.Fatal("unsupported authorization request was not rejected before interaction")
		}
	}
}

func TestShuiyuanAuthorizationVerifiesConfirmsThenImportsExactIdentity(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	defer clear(secret)
	var calls []string
	owner := func(_ context.Context, q broker.AdminRequest, target any) error {
		calls = append(calls, q.Action)
		if q.Provider != "shuiyuan" || !bytes.Equal(q.Secret, secret) || q.SourceCredentialID != "" || q.ClientID != "" {
			t.Fatal("authorization did not send only the forum key to its own provider")
		}
		switch q.Action {
		case "verify":
			if q.ExpectedAccount != "" || q.Label != "" {
				t.Fatal("identity was trusted before verification")
			}
			return json.Unmarshal([]byte(`{"account_ref":"shuiyuan:user:123","secret":"SHOULD_NOT_BE_OUTPUT"}`), target)
		case "import":
			if q.ExpectedAccount != "shuiyuan:user:123" || q.Label != "main" {
				t.Fatal("import did not retain the verified and confirmed identity")
			}
			return json.Unmarshal([]byte(`{"credential_id":"synthetic-credential","secret":"SHOULD_NOT_BE_OUTPUT"}`), target)
		default:
			t.Fatal("unexpected owner operation")
			return nil
		}
	}
	approve := func(value any) error {
		calls = append(calls, "confirm")
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), string(secret)) || strings.Contains(string(raw), "SHOULD_NOT_BE_OUTPUT") {
			t.Fatal("confirmation contained key material")
		}
		preview, ok := value.(map[string]string)
		if !ok || preview["provider"] != "shuiyuan" || preview["account_ref"] != "shuiyuan:user:123" || preview["scope"] != "read" || preview["label"] != "main" {
			t.Fatal("confirmation did not identify the operation")
		}
		return nil
	}
	result, err := importShuiyuanAuthorization(context.Background(), secret, "main", owner, approve)
	if err != nil || !reflect.DeepEqual(calls, []string{"verify", "confirm", "import"}) || result.CredentialID != "synthetic-credential" || result.AccountRef != "shuiyuan:user:123" || result.Status != "imported" {
		t.Fatal("authorization import sequence failed", err)
	}
	raw, err := json.Marshal(result)
	if err != nil || strings.Contains(string(raw), string(secret)) || strings.Contains(string(raw), "SHOULD_NOT_BE_OUTPUT") {
		t.Fatal("authorization result exposed a credential")
	}
}

func TestShuiyuanAuthorizationDoesNotImportFailedOrUnconfirmedIdentity(t *testing.T) {
	for _, name := range []string{"verify_failed", "missing_identity", "wrong_provider_identity", "confirmation_canceled", "import_failed", "missing_receipt"} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			owner := func(_ context.Context, q broker.AdminRequest, target any) error {
				calls = append(calls, q.Action)
				if q.Action == "verify" {
					switch name {
					case "verify_failed":
						return contracts.Fail("AUTH_REQUIRED", "Synthetic denied credential")
					case "missing_identity":
						return json.Unmarshal([]byte(`{}`), target)
					case "wrong_provider_identity":
						return json.Unmarshal([]byte(`{"account_ref":"canvas:user:123"}`), target)
					default:
						return json.Unmarshal([]byte(`{"account_ref":"shuiyuan:user:123"}`), target)
					}
				}
				if name == "import_failed" {
					return contracts.Fail("BROKER_UNAVAILABLE", "Synthetic disconnected Broker")
				}
				if name == "missing_receipt" {
					return json.Unmarshal([]byte(`{}`), target)
				}
				t.Fatal("import was attempted without valid verification and confirmation")
				return nil
			}
			approve := func(any) error {
				calls = append(calls, "confirm")
				if name == "confirmation_canceled" {
					return contracts.Fail("CANCELED", "Synthetic owner cancellation")
				}
				return nil
			}
			result, err := importShuiyuanAuthorization(context.Background(), []byte("SYNTHETIC_KEY"), "main", owner, approve)
			if err == nil || result != (shuiyuanAuthorizationResult{}) {
				t.Fatal("failure reported as successful import")
			}
			want := []string{"verify"}
			if name == "confirmation_canceled" {
				want = append(want, "confirm")
			} else if name == "import_failed" || name == "missing_receipt" {
				want = append(want, "confirm", "import")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("unexpected operation after failed authorization", calls)
			}
		})
	}
}
