package auth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCampusCredentialContextAndLegacy(t *testing.T) {
	for _, raw := range []string{"SYNTHETIC_ROOT%2B==", "JAAuthCookie=SYNTHETIC_ROOT%2B=="} {
		got, err := NormalizeCampusCredential([]byte(raw))
		if err != nil || string(got) != "SYNTHETIC_ROOT%2B==" {
			t.Fatal("legacy credential changed", err)
		}
	}
	c := CampusCredential{Version: 1, JAAuthCookie: "SYNTHETIC_ROOT%2B==", JATrustCookie: "SYNTHETIC_TRUST%3D", UserAgent: "Mozilla/5.0 (Synthetic Browser)"}
	raw, _ := json.Marshal(c)
	normalized, err := NormalizeCampusCredential(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseCampusCredential(normalized)
	if err != nil || got != c {
		t.Fatal("browser context not preserved", err)
	}
	for _, raw := range []string{
		`{}`, `{"version":2,"ja_auth_cookie":"SYNTHETIC_ROOT"}`,
		`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","password":"SYNTHETIC_OTHER"}`,
		`{"version":1,"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT"}`,
		`{"version":1,"ja_auth_cookie":null}`,
		`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","user_agent":null}`,
		`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","ja_trust_cookie":"x; stolen=y"}`,
		`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","user_agent":"x\r\nCookie: SYNTHETIC_OTHER"}`,
		`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","user_agent":"   "}`,
		string(raw) + ` {}`, strings.Repeat("x", 32769),
	} {
		_, err := NormalizeCampusCredential([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "SYNTHETIC_") {
			t.Fatal("invalid context accepted or disclosed")
		}
	}
}

func TestCampusContextCannotReadNonTerminal(t *testing.T) {
	var diag bytes.Buffer
	if _, err := ReadCampusContext(strings.NewReader("SYNTHETIC_TRUST"), &diag, []byte("SYNTHETIC_ROOT")); err == nil || diag.Len() != 0 {
		t.Fatal("browser context read outside terminal")
	}
}
