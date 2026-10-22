package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONStdinAndFailureChannels(t *testing.T) {
	for _, test := range []struct {
		args  []string
		input string
		want  int
	}{
		{[]string{"campus", "websites", "list", "--input-json-stdin", "--json"}, "\ufeff{\"limit\":10}", 0},
		{[]string{"campus", "websites", "list", "--input-json-stdin", "--json"}, `{"confirmed":true}`, 1},
		{[]string{"campus", "websites", "list", "--input-json-stdin", "--json"}, "\ufeff{}" + strings.Repeat(" ", 256<<10), 1},
		{[]string{"campus", "websites", "list", "--input-json-stdin", "--limit", "10"}, `{}`, 1},
		{[]string{"campus", "websites", "list", "--profile", "study"}, ``, 1},
		{[]string{"doctor", "--json"}, ``, 0},
		{[]string{"learning", "canvas", "courses", "list", "--interactive", "--json"}, "SYNTHETIC_SECRET", 1},
		{[]string{"learning", "canvas", "courses", "list", "--json"}, "SYNTHETIC_SECRET", 1},
		{[]string{"auth", "check", "--provider", "jaccount", "--json"}, "SYNTHETIC_SECRET", 1},
		{[]string{"broker", "init"}, "yes\n", 1},
		{[]string{"auth", "import", "--provider", "canvas"}, "SYNTHETIC_SECRET", 1},
		{[]string{"auth", "import", "--provider", "jaccount"}, "SYNTHETIC_SECRET", 1},
		{[]string{"auth", "login"}, "SYNTHETIC_SECRET", 1},
		{[]string{"auth", "login", "--providers", "canvas"}, "SYNTHETIC_SECRET", 1},
		{[]string{"auth", "login", "--providers", "teaching,teaching"}, "SYNTHETIC_SECRET", 1},
		{[]string{"apps", "create", "synthetic", "--output", "C:\\synthetic\\connection.json"}, "yes\n", 1},
		{[]string{"learning", "canvas", "assignments", "list", "--account", "canvas:user:7", "--course", "canvas:course:1", "--view", "deadlines_v1"}, "", 1},
	} {
		var out, diagnostics bytes.Buffer
		args := append(append([]string{}, test.args...), "--json")
		exit := Execute(context.Background(), args, &out, &diagnostics, strings.NewReader(test.input))
		if exit != test.want {
			t.Fatal(test, exit, out.String(), diagnostics.String())
		}
		if exit == 0 {
			if !json.Valid(out.Bytes()) || diagnostics.Len() != 0 {
				t.Fatal(out.String(), diagnostics.String())
			}
		} else {
			if out.Len() != 0 || !json.Valid(diagnostics.Bytes()) {
				t.Fatal(out.String(), diagnostics.String())
			}
		}
	}
}
