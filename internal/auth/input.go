// Package auth contains local management operations, never Agent tools.
package auth

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/term"
)

// Normalize accepts one credential value, not an HTTP header or cookie dump.
func Normalize(provider string, secret []byte) ([]byte, error) {
	if provider != "canvas" && provider != "jaccount" && provider != "jcourse" {
		return nil, contracts.Fail("UNSUPPORTED", "Unknown credential provider")
	}
	value := string(secret)
	if provider == "jaccount" {
		value = strings.TrimPrefix(value, "JAAuthCookie=")
	}
	if len(value) < 1 || len(value) > 8192 {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Credential must contain 1-8192 bytes")
	}
	for _, b := range []byte(value) {
		if b <= 32 || b >= 127 || strings.ContainsRune(";,\"\\", rune(b)) {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Paste only one credential value, without whitespace or HTTP headers")
		}
	}
	if strings.HasPrefix(strings.ToLower(value), "bearer") || strings.HasPrefix(strings.ToLower(value), "cookie:") {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Paste only the credential value")
	}
	return []byte(value), nil
}

// ReadTransient deliberately does not store credentials. Stdin must itself be a
// real terminal: a pipe, file, environment variable or argument is never read.
func ReadTransient(input io.Reader, diagnostics io.Writer, provider string) ([]byte, error) {
	return ReadCredential(input, diagnostics, provider, false)
}
func ReadCredential(input io.Reader, diagnostics io.Writer, provider string, persistent bool) ([]byte, error) {
	if err := RequireTerminal(input); err != nil {
		return nil, err
	}
	f := input.(*os.File)
	label := "Canvas Token"
	if provider == "jcourse" {
		label = "jCourse API Key"
	}
	if provider == "jaccount" {
		label = "JAAuthCookie"
	}
	purpose := "仅本次诊断，不保存"
	if persistent {
		purpose = "验证并确认账号后，由 Broker 加密保存"
	}
	fmt.Fprintf(diagnostics, "%s（隐藏输入，%s）: ", label, purpose)
	raw, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(diagnostics)
	defer clear(raw)
	if err != nil {
		return nil, contracts.Fail("AUTH_REQUIRED", "Could not read credential from terminal")
	}
	return Normalize(provider, raw)
}

func RequireTerminal(input io.Reader) error {
	f, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return contracts.Fail("AUTH_REQUIRED", "Run this command in a real terminal; credentials cannot come from a pipe or file")
	}
	return nil
}
