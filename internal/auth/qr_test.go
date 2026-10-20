package auth

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestQRChallengeCannotBeRedirected(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "redirected")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var log bytes.Buffer
	for _, out := range []interface{ Write([]byte) (int, error) }{&log, f} {
		if e := DisplayQR(strings.NewReader(""), out, "SYNTHETIC_LOGIN_CHALLENGE"); e == nil {
			t.Fatal("non-terminal accepted")
		}
	}
	if log.Len() != 0 {
		t.Fatal("challenge entered diagnostics")
	}
	data, e := os.ReadFile(f.Name())
	if e != nil || len(data) != 0 {
		t.Fatal("challenge entered redirected file")
	}
}
