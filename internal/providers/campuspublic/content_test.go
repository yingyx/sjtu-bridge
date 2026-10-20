package campuspublic

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestPublicContentEncodedBudgetIsExplicit(t *testing.T) {
	v := item("Test", "https://hospital.sjtu.edu.cn/", nil)
	v.Content = &contracts.PublicContent{Text: strings.Repeat("<中文&", 10000), Links: []contracts.PublicLink{}}
	for n := 0; n < 80; n++ {
		v.Content.Links = append(v.Content.Links, contracts.PublicLink{Title: strings.Repeat("<", 1024), URL: "https://hospital.sjtu.edu.cn/" + strings.Repeat("a", 1900), Kind: "reference"})
	}
	boundPublicContent(&v)
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > contracts.MaxJSONBytes-4096 || !v.Content.Truncated || !utf8.ValidString(v.Content.Text) {
		t.Fatal(len(raw), err)
	}
}
