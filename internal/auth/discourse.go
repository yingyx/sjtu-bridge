package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"golang.org/x/term"
)

const discourseAuthorizationTTL = 10 * time.Minute
const maxDiscoursePayload = 4096

// DiscourseAuthorization is one in-memory, read-scope authorization attempt.
// No key material is persisted, and no callback listener or HTTP client is used.
type DiscourseAuthorization struct {
	mu         sync.Mutex
	privateKey *rsa.PrivateKey
	nonce      string
	authURL    string
	created    time.Time
	now        func() time.Time
}

// NewDiscourseAuthorization builds the fixed Shuiyuan authorization page URL.
// The user opens it and approves the read scope in their own browser session.
func NewDiscourseAuthorization() (*DiscourseAuthorization, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, contracts.Fail("AUTH_REQUIRED", "Cannot prepare local authorization")
	}
	a := &DiscourseAuthorization{privateKey: key, created: time.Now(), now: time.Now}
	var random [48]byte
	if _, err = io.ReadFull(rand.Reader, random[:]); err != nil {
		a.Close()
		return nil, contracts.Fail("AUTH_REQUIRED", "Cannot prepare local authorization")
	}
	defer clear(random[:])
	a.nonce = hex.EncodeToString(random[:24])
	clientID := hex.EncodeToString(random[24:])
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		a.Close()
		return nil, contracts.Fail("AUTH_REQUIRED", "Cannot prepare local authorization")
	}
	query := url.Values{
		"application_name": {"SJTU Bridge"},
		"scopes":           {"read"},
		"client_id":        {clientID},
		"nonce":            {a.nonce},
		"public_key":       {string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))},
	}
	a.authURL = "https://shuiyuan.sjtu.edu.cn/user-api-key/new?" + query.Encode()
	return a, nil
}

// AuthorizationURL contains a public key and correlation values, never a key
// granting access. Empty after Close or Consume; it cannot be used to resume.
func (a *DiscourseAuthorization) AuthorizationURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authURL
}

// Consume accepts the base64 ciphertext copied from the official result page.
// Every attempt consumes the local RSA key, including malformed/expired input.
// Success returns only the User API Key, owned by the caller and to be cleared.
func (a *DiscourseAuthorization) Consume(payload []byte) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.closeLocked()
	fail := func() ([]byte, error) {
		return nil, contracts.Fail("AUTH_REQUIRED", "Authorization response is invalid, expired or already used; start authorization again")
	}
	if a.privateKey == nil || a.now == nil {
		return fail()
	}
	now := a.now()
	if now.Before(a.created) || !now.Before(a.created.Add(discourseAuthorizationTTL)) || len(payload) == 0 || len(payload) > maxDiscoursePayload {
		return fail()
	}
	// Discourse's Base64.encode64 may wrap lines. Only base64 whitespace is
	// ignored; URLs, JSON wrappers, cookies and plaintext credentials are refused.
	compact := make([]byte, 0, len(payload))
	defer func() { clear(compact) }()
	for _, b := range payload {
		if b == ' ' || b == '\r' || b == '\n' || b == '\t' {
			continue
		}
		compact = append(compact, b)
	}
	ciphertext := make([]byte, base64.StdEncoding.DecodedLen(len(compact)))
	defer clear(ciphertext)
	n, err := base64.StdEncoding.Strict().Decode(ciphertext, compact)
	if err != nil || n != a.privateKey.Size() {
		return fail()
	}
	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, a.privateKey, ciphertext[:n])
	if err != nil {
		return fail()
	}
	defer clear(plaintext)
	var result struct {
		Key       string          `json:"key"`
		Nonce     string          `json:"nonce"`
		Push      bool            `json:"push"`
		API       int             `json:"api"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if strictjson.Decode(plaintext, &result) != nil || subtle.ConstantTimeCompare([]byte(result.Nonce), []byte(a.nonce)) != 1 || result.Push || len(result.Key) != 32 {
		return fail()
	}
	for _, b := range []byte(result.Key) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return fail()
		}
	}
	if len(result.ExpiresAt) != 0 && !bytes.Equal(result.ExpiresAt, []byte("null")) {
		var value string
		if json.Unmarshal(result.ExpiresAt, &value) != nil {
			return fail()
		}
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil || !now.Before(expires) {
			return fail()
		}
	}
	return []byte(result.Key), nil
}

// Close discards the pending attempt and best-effort clears private integers.
// Go's runtime may retain internal copies; no serialized private key is made.
func (a *DiscourseAuthorization) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeLocked()
}

func (a *DiscourseAuthorization) closeLocked() {
	if k := a.privateKey; k != nil {
		wipe := func(n *big.Int) {
			if n != nil {
				clear(n.Bits())
				n.SetInt64(0)
			}
		}
		wipe(k.D)
		for _, p := range k.Primes {
			wipe(p)
		}
		wipe(k.Precomputed.Dp)
		wipe(k.Precomputed.Dq)
		wipe(k.Precomputed.Qinv)
		for _, values := range k.Precomputed.CRTValues {
			wipe(values.Exp)
			wipe(values.Coeff)
			wipe(values.R)
		}
	}
	a.privateKey, a.authURL, a.nonce = nil, "", ""
}

// ReadDiscoursePayload reads ciphertext without echoing it or consulting a
// browser, ordinary file, environment variable or command argument.
func ReadDiscoursePayload(input io.Reader, diagnostics io.Writer) ([]byte, error) {
	if err := RequireTerminal(input); err != nil {
		return nil, err
	}
	fmt.Fprint(diagnostics, "粘贴官网返回的完整加密 payload（隐藏输入，支持直接粘贴多行；不是 Cookie 或明文 Key）: ")
	return readDiscoursePayloadLines(func() ([]byte, error) {
		raw, err := term.ReadPassword(int(input.(*os.File).Fd()))
		fmt.Fprintln(diagnostics)
		return raw, err
	})
}

// Only this flow's RSA-2048 response is accepted: 256 ciphertext bytes encode to
// 344 base64 characters, ending in ==. Stop as soon as that complete response is
// read, so a single-line paste needs no extra blank line or sentinel.
func readDiscoursePayloadLines(readLine func() ([]byte, error)) ([]byte, error) {
	const ciphertextBytes = 256
	const encodedBytes = 344
	compact := make([]byte, 0, encodedBytes)
	defer func() { clear(compact) }()
	total := 0
	fail := func() ([]byte, error) {
		return nil, contracts.Fail("AUTH_REQUIRED", "Cannot read encrypted authorization response from terminal")
	}
	for {
		raw, err := readLine()
		total += len(raw) + 1 // Count line separators as well as pasted whitespace.
		if err != nil || total > maxDiscoursePayload {
			clear(raw)
			return fail()
		}
		valid := true
		for _, b := range raw {
			if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
				continue
			}
			index := len(compact)
			alphabet := b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/'
			if index >= encodedBytes || index < encodedBytes-2 && !alphabet || index >= encodedBytes-2 && b != '=' {
				valid = false
				break
			}
			compact = append(compact, b)
		}
		clear(raw)
		if !valid {
			return fail()
		}
		if len(compact) == encodedBytes {
			var ciphertext [ciphertextBytes]byte
			n, err := base64.StdEncoding.Strict().Decode(ciphertext[:], compact)
			clear(ciphertext[:])
			if err != nil || n != ciphertextBytes {
				return fail()
			}
			return append([]byte(nil), compact...), nil
		}
	}
}
