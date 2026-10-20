package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityNavigationAndPrivateListShareCLIResource(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if Execute(context.Background(), []string{"development", "activities", "--help"}, &out, &diagnostics, strings.NewReader("")) != 0 {
		t.Fatal(diagnostics.String())
	}
	for _, name := range []string{"list", "navigate"} {
		if !strings.Contains(out.String(), name) {
			t.Fatal("activity CLI entry was shadowed", name, out.String())
		}
	}
	out.Reset()
	if Execute(context.Background(), []string{"development", "activities", "navigate", "--json"}, &out, &diagnostics, strings.NewReader("")) != 0 || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "activity.sjtu.edu.cn") {
		t.Fatal("public activity navigation no longer reachable", diagnostics.String())
	}
	for _, args := range [][]string{{"life", "electricity", "balance", "--help"}, {"auth", "resources", "--help"}} {
		out.Reset()
		if Execute(context.Background(), args, &out, &diagnostics, strings.NewReader("")) != 0 || !strings.Contains(out.String(), "credential") && args[0] == "auth" {
			t.Fatal("bound electric entry missing", diagnostics.String())
		}
	}
}
