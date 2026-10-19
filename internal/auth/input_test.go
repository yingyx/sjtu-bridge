package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, s := range []string{"", " a", "a\nb", "a;b=c", "Cookie: x", "Bearer x", "a,b", "a\"b", "值", strings.Repeat("a", 8193)} {
		if _, err := Normalize("jaccount", []byte(s)); err == nil {
			t.Fatalf("accepted invalid credential of length %d", len(s))
		}
	}
	got, err := Normalize("jaccount", []byte("JAAuthCookie=synthetic%2Bvalue=="))
	if err != nil || string(got) != "synthetic%2Bvalue==" {
		t.Fatal("normalization changed encoded cookie", err)
	}
}
func TestTerminalRequiredBeforeRead(t *testing.T) {
	var diagnostics bytes.Buffer
	_, err := ReadTransient(strings.NewReader("SYNTHETIC_SECRET"), &diagnostics, "canvas")
	if err == nil || diagnostics.Len() != 0 || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
		t.Fatal("pipe must be refused without reading/echoing it")
	}
}
