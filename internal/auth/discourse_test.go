package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticDiscourseKey = "0123456789abcdef0123456789abcdef"

func discourseTestSession(t *testing.T) (*DiscourseAuthorization, *rsa.PublicKey, url.Values) {
	t.Helper()
	session, err := NewDiscourseAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	session.now = func() time.Time { return session.created }
	u, err := url.Parse(session.AuthorizationURL())
	if err != nil {
		t.Fatal("invalid local authorization URL")
	}
	query := u.Query()
	block, _ := pem.Decode([]byte(query.Get("public_key")))
	if block == nil || block.Type != "PUBLIC KEY" {
		t.Fatal("missing SPKI public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	public, ok := key.(*rsa.PublicKey)
	if !ok || public.N.BitLen() != 2048 {
		t.Fatal("authorization must use RSA 2048")
	}
	return session, public, query
}

func discourseTestPayload(t *testing.T, public *rsa.PublicKey, value any) []byte {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, public, plain)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(ciphertext)
	return []byte(base64.StdEncoding.EncodeToString(ciphertext))
}

func TestDiscourseAuthorizationFixedReadOnlyRequestAndFreshCorrelation(t *testing.T) {
	first, _, query := discourseTestSession(t)
	u, _ := url.Parse(first.AuthorizationURL())
	if u.Scheme != "https" || u.Host != "shuiyuan.sjtu.edu.cn" || u.Path != "/user-api-key/new" || u.User != nil || len(query) != 5 || query.Get("application_name") != "SJTU Bridge" || query.Get("scopes") != "read" {
		t.Fatal("authorization request escaped the fixed site/read scope")
	}
	for _, key := range []string{"auth_redirect", "push_url", "password", "api_key", "key"} {
		if query.Has(key) {
			t.Fatal("request includes callback, push or credential parameter")
		}
	}
	if len(query.Get("nonce")) != 48 || len(query.Get("client_id")) != 48 || query.Get("nonce") == query.Get("client_id") {
		t.Fatal("missing independent random correlation values")
	}
	second, _, next := discourseTestSession(t)
	if first.AuthorizationURL() == second.AuthorizationURL() || query.Get("nonce") == next.Get("nonce") || query.Get("client_id") == next.Get("client_id") {
		t.Fatal("authorization attempt reused key or correlation values")
	}
	raw, err := json.Marshal(first)
	if err != nil || string(raw) != "{}" {
		t.Fatal("private authorization state can be serialized")
	}
}

func TestDiscourseAuthorizationDecryptsOnceAndClearsPendingState(t *testing.T) {
	for _, expiry := range []bool{false, true} {
		session, public, query := discourseTestSession(t)
		value := map[string]any{"key": syntheticDiscourseKey, "nonce": query.Get("nonce"), "push": false, "api": 3}
		if expiry {
			value["expires_at"] = session.created.Add(time.Hour).UTC().Format(time.RFC3339)
		}
		payload := discourseTestPayload(t, public, value)
		// Server-side Base64.encode64 permits whitespace around wrapped lines.
		wrapped := append([]byte(" \n"), payload[:64]...)
		wrapped = append(wrapped, '\n')
		wrapped = append(wrapped, payload[64:]...)
		private := session.privateKey
		key, err := session.Consume(wrapped)
		if err != nil || string(key) != syntheticDiscourseKey {
			t.Fatal("synthetic authorization response was not accepted", err)
		}
		clear(key)
		if session.AuthorizationURL() != "" || session.privateKey != nil || private.D.Sign() != 0 {
			t.Fatal("consumed session retained pending private state")
		}
		if key, err = session.Consume(payload); err == nil || key != nil {
			t.Fatal("authorization payload was replayed")
		}
	}
}

func TestDiscourseAuthorizationRejectsInvalidResponsesWithoutEcho(t *testing.T) {
	for _, name := range []string{"wrong_nonce", "missing_nonce", "missing_key", "wrong_key_length", "not_hex", "uppercase_key", "duplicate_key", "duplicate_nonce", "duplicate_expiry", "expired_key", "bad_expiry", "numeric_expiry", "push", "malformed_base64", "oversized", "wrong_ciphertext", "plaintext_key", "ttl", "clock_rollback"} {
		t.Run(name, func(t *testing.T) {
			session, public, query := discourseTestSession(t)
			value := map[string]any{"key": syntheticDiscourseKey, "nonce": query.Get("nonce"), "push": false, "api": 3}
			switch name {
			case "wrong_nonce":
				value["nonce"] = "another-attempt"
			case "missing_nonce":
				delete(value, "nonce")
			case "missing_key":
				delete(value, "key")
			case "wrong_key_length":
				value["key"] = strings.Repeat("a", 64)
			case "not_hex":
				value["key"] = strings.Repeat("z", 32)
			case "uppercase_key":
				value["key"] = strings.ToUpper(syntheticDiscourseKey)
			case "expired_key":
				value["expires_at"] = session.created.Add(-time.Minute).UTC().Format(time.RFC3339)
			case "bad_expiry":
				value["expires_at"] = "not-a-timestamp"
			case "numeric_expiry":
				value["expires_at"] = 100
			case "push":
				value["push"] = true
			case "ttl":
				session.now = func() time.Time { return session.created.Add(discourseAuthorizationTTL) }
			case "clock_rollback":
				session.now = func() time.Time { return session.created.Add(-time.Second) }
			}
			payload := discourseTestPayload(t, public, value)
			switch name {
			case "duplicate_key", "duplicate_nonce", "duplicate_expiry":
				extra := `"key":"` + syntheticDiscourseKey + `"`
				if name == "duplicate_nonce" {
					extra = `"nonce":"` + query.Get("nonce") + `"`
				} else if name == "duplicate_expiry" {
					extra = `"expires_at":null,"expires_at":null`
				}
				payload = discourseTestPayload(t, public, json.RawMessage(`{"key":"`+syntheticDiscourseKey+`","nonce":"`+query.Get("nonce")+`",`+extra+`}`))
			case "malformed_base64":
				payload = []byte("SYNTHETIC_INVALID_PAYLOAD!")
			case "oversized":
				payload = bytes.Repeat([]byte("a"), maxDiscoursePayload+1)
			case "wrong_ciphertext":
				payload = []byte(base64.StdEncoding.EncodeToString(make([]byte, 256)))
			case "plaintext_key":
				payload = []byte(syntheticDiscourseKey)
			}
			key, err := session.Consume(payload)
			if err == nil || key != nil || strings.Contains(err.Error(), syntheticDiscourseKey) || strings.Contains(err.Error(), string(payload)) || strings.Contains(err.Error(), query.Get("nonce")) {
				t.Fatal("invalid authorization response accepted or echoed")
			}
			if session.privateKey != nil || session.AuthorizationURL() != "" {
				t.Fatal("failed attempt retained a reusable authorization session")
			}
		})
	}
}

func TestDiscourseAuthorizationConcurrentReplayHasOneWinner(t *testing.T) {
	session, public, query := discourseTestSession(t)
	payload := discourseTestPayload(t, public, map[string]any{"key": syntheticDiscourseKey, "nonce": query.Get("nonce")})
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			key, err := session.Consume(payload)
			if err == nil {
				successes.Add(1)
				clear(key)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatal("payload consumption is not single-use")
	}
}

func TestReadDiscoursePayloadRefusesPipeBeforePrompt(t *testing.T) {
	var diagnostics bytes.Buffer
	key, err := ReadDiscoursePayload(strings.NewReader("SYNTHETIC_ENCRYPTED_PAYLOAD"), &diagnostics)
	if err == nil || key != nil || diagnostics.Len() != 0 || strings.Contains(err.Error(), "SYNTHETIC_ENCRYPTED_PAYLOAD") {
		t.Fatal("non-terminal payload input was read or echoed")
	}
}

func TestReadDiscoursePayloadLinesAcceptsOfficialWrappingAndStopsWhenComplete(t *testing.T) {
	_, public, query := discourseTestSession(t)
	payload := discourseTestPayload(t, public, map[string]any{"key": syntheticDiscourseKey, "nonce": query.Get("nonce")})
	wrapped := []string{}
	for offset := 0; offset < len(payload); offset += 64 {
		wrapped = append(wrapped, " \t"+string(payload[offset:min(offset+64, len(payload))])+"\r\n")
	}
	for _, lines := range [][]string{{string(payload)}, wrapped} {
		calls := 0
		result, err := readDiscoursePayloadLines(func() ([]byte, error) {
			if calls >= len(lines) {
				t.Fatal("complete payload requested an extra line or sentinel")
			}
			line := []byte(lines[calls])
			calls++
			return line, nil
		})
		if err != nil || !bytes.Equal(result, payload) || calls != len(lines) {
			t.Fatal("single-line or official wrapped payload was not accumulated")
		}
		clear(result)
	}
}

func TestReadDiscoursePayloadLinesRejectsMalformedAndOversizedInputWithoutEcho(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 256))
	for name, lines := range map[string][]string{
		"bad_character":        {"SYNTHETIC!BAD", encoded},
		"early_padding":        {"=", encoded},
		"extra_character":      {encoded + "A"},
		"oversized_line":       {strings.Repeat(" ", maxDiscoursePayload+1)},
		"oversized_multiline":  {strings.Repeat(" ", 2048), strings.Repeat("\t", 2048)},
		"invalid_padding_bits": {encoded[:341] + "B=="},
		"truncated":            {encoded[:64]},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			result, err := readDiscoursePayloadLines(func() ([]byte, error) {
				if calls >= len(lines) {
					return nil, io.EOF
				}
				line := []byte(lines[calls])
				calls++
				return line, nil
			})
			if err == nil || result != nil || strings.Contains(err.Error(), encoded) || strings.Contains(err.Error(), "SYNTHETIC!BAD") {
				t.Fatal("invalid line input was accepted or echoed")
			}
			if (name == "bad_character" || name == "early_padding") && calls != 1 {
				t.Fatal("malformed base64 did not fail before reading another line")
			}
		})
	}
}
