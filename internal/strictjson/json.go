// Package strictjson rejects ambiguous upstream JSON before typed decoding.
// Callers retain byte limits, media types, envelopes and business validation.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

var invalid = errors.New("invalid or ambiguous JSON")

// Decode accepts exactly one UTF-8 document with bounded nesting and no keys
// that encoding/json could match to the same struct field. Unknown additive
// fields remain allowed; providers project only their reviewed fields.
func Decode(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !value(d, 0) {
		return invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid
	}
	if json.Unmarshal(raw, target) != nil {
		return invalid
	}
	return nil
}

func fold(key string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, key)
}

func value(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			key = fold(key)
			if err != nil || !ok || seen[key] || !value(d, depth+1) {
				return false
			}
			seen[key] = true
		}
	case '[':
		for d.More() {
			if !value(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, err := d.Token()
	return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}
