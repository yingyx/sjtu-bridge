package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRejectAmbiguityAndMalformedDocuments(t *testing.T) {
	for _, raw := range []string{
		`{"code":500,"code":200}`, `{"code":500,"CODE":200}`,
		`{"data":[{"id":9,"ID":1}]}`, `{"id":1,"\u0069d":2}`,
		`{"s":1,"\u017f":2}`, `{"k":1,"\u212a":2}`,
		`{"a":1} {"b":2}`, `{"a":1} trailing`, `{"a":[1}}`, ``,
		string([]byte{'"', 255, '"'}), strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34),
	} {
		if Decode([]byte(raw), new(any)) == nil {
			t.Fatal("ambiguous or malformed document accepted")
		}
	}
}

func TestDistinctObjectsAndLargeNumbers(t *testing.T) {
	var result struct {
		Data []struct {
			ID json.Number `json:"id"`
		} `json:"data"`
	}
	if err := Decode([]byte(`{"data":[{"id":123456789012345678},{"ID":2}],"new_field":true}`), &result); err != nil || len(result.Data) != 2 || result.Data[0].ID.String() != "123456789012345678" {
		t.Fatal("valid additive response or numeric identity lost", err)
	}
	if err := Decode([]byte(strings.Repeat("[", 32)+"0"+strings.Repeat("]", 32)), new(any)); err != nil {
		t.Fatal("bounded document rejected", err)
	}
	var wrong struct {
		ID int `json:"id"`
	}
	if err := Decode([]byte(`{"id":"wrong type"}`), &wrong); err == nil {
		t.Fatal("typed decode failure hidden")
	}
}
