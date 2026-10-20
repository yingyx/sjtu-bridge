package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/term"
)

// CampusCredential is Broker-private browser session context. It is encrypted
// as one credential and never returned in discovery or business responses.
// Legacy imports containing only a JAAuthCookie remain valid.
type CampusCredential struct {
	Version       int    `json:"version"`
	JAAuthCookie  string `json:"ja_auth_cookie"`
	JATrustCookie string `json:"ja_trust_cookie,omitempty"`
	UserAgent     string `json:"user_agent,omitempty"`
}

func ParseCampusCredential(raw []byte) (CampusCredential, error) {
	var c CampusCredential
	if len(raw) == 0 || len(raw) > 32768 {
		return c, contracts.Fail("INVALID_ARGUMENT", "Invalid campus credential size")
	}
	if raw[0] != '{' {
		value, err := Normalize("jaccount", raw)
		defer clear(value)
		if err != nil {
			return c, err
		}
		c.Version, c.JAAuthCookie = 1, string(value)
		return c, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return c, invalidCampus()
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return c, invalidCampus()
		}
		seen[key] = true
		var target any
		switch key {
		case "version":
			target = &c.Version
		case "ja_auth_cookie":
			target = &c.JAAuthCookie
		case "ja_trust_cookie":
			target = &c.JATrustCookie
		case "user_agent":
			target = &c.UserAgent
		default:
			return c, invalidCampus()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) || json.Unmarshal(value, target) != nil {
			return c, invalidCampus()
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return c, invalidCampus()
	}
	if _, err := d.Token(); err != io.EOF || c.Version != 1 {
		return c, invalidCampus()
	}
	ja, err := Normalize("jaccount", []byte(c.JAAuthCookie))
	defer clear(ja)
	if err != nil {
		return c, err
	}
	c.JAAuthCookie = string(ja)
	if c.JATrustCookie != "" {
		trust, err := Normalize("jaccount", []byte(strings.TrimPrefix(c.JATrustCookie, "JATrustCookie=")))
		defer clear(trust)
		if err != nil {
			return c, err
		}
		c.JATrustCookie = string(trust)
	}
	if len(c.UserAgent) > 512 {
		return c, invalidCampus()
	}
	for _, b := range []byte(c.UserAgent) {
		if b < 32 || b > 126 {
			return c, invalidCampus()
		}
	}
	if c.UserAgent != "" && strings.TrimSpace(c.UserAgent) == "" {
		return c, invalidCampus()
	}
	return c, nil
}

func invalidCampus() error {
	return contracts.Fail("INVALID_ARGUMENT", "Invalid campus browser session context")
}

func NormalizeCampusCredential(raw []byte) ([]byte, error) {
	c, err := ParseCampusCredential(raw)
	if err != nil {
		return nil, err
	}
	if c.JATrustCookie == "" && c.UserAgent == "" {
		return []byte(c.JAAuthCookie), nil
	}
	return json.Marshal(c)
}

// ReadCampusContext is an optional recovery step, not a prerequisite to import.
// No browser, clipboard, environment variable, file or command argument is read.
func ReadCampusContext(input io.Reader, diagnostics io.Writer, root []byte) ([]byte, error) {
	if err := RequireTerminal(input); err != nil {
		return nil, err
	}
	c, err := ParseCampusCredential(root)
	if err != nil {
		return nil, err
	}
	f := input.(*os.File)
	read := func(label string) ([]byte, error) {
		fmt.Fprintf(diagnostics, "%s（隐藏输入；没有或不提供可直接回车）: ", label)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(diagnostics)
		if err != nil {
			clear(raw)
			return nil, contracts.Fail("AUTH_REQUIRED", "Could not read browser session context")
		}
		return raw, nil
	}
	trust, err := read("JATrustCookie 的 Value")
	defer clear(trust)
	if err != nil {
		return nil, err
	}
	ua, err := read("原登录浏览器的 User-Agent")
	defer clear(ua)
	if err != nil {
		return nil, err
	}
	c.JATrustCookie, c.UserAgent = string(trust), string(ua)
	raw, err := json.Marshal(c)
	defer clear(raw)
	if err != nil {
		return nil, invalidCampus()
	}
	return NormalizeCampusCredential(raw)
}
