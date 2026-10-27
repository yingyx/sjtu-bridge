package transcribe

import (
	"strings"
	"testing"
)

func TestCanonicalStringAndNumericTranscriptionStatus(t *testing.T) {
	for _, status := range []string{`0`, `"0"`} {
		r := js(`{"errno":` + status + `,"data":{"account":"synthetic_user"}}`)
		if _, e := payload(r); e != nil {
			t.Fatal(status, e)
		}
	}
	for _, status := range []string{`null`, `false`, `0.0`, `"00"`, `" 0"`, `"+0"`, `"0.0"`, `"-1"`, `{}`, `[]`} {
		r := js(`{"errno":` + status + `,"data":{}}`)
		if _, e := payload(r); e == nil {
			t.Fatal("invalid status accepted", status)
		}
	}
	for _, status := range []string{`"40100"`, `10004`} {
		r := js(`{"errno":` + status + `,"data":{}}`)
		if _, e := payload(r); e == nil || !containsCode(e, "AUTH_REQUIRED") {
			t.Fatal("auth failure hidden", status, e)
		}
	}
}
func containsCode(e error, codeValue string) bool {
	return e != nil && strings.Contains(code(e), codeValue)
}
