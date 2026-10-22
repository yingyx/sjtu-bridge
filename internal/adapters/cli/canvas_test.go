package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestCanvasReadCommandFamilyAndRelevantFlags(t *testing.T) {
	for _, id := range contracts.CanvasCapabilityIDs() {
		r, _ := contracts.CanvasRules(id)
		args := contracts.CLIPath(id)
		var out, diag bytes.Buffer
		if Execute(context.Background(), append(append([]string{}, args...), "--help"), &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(id, diag.String())
		}
		for _, text := range []string{r.View, "--connection", "--input-file", "--input-json-stdin", "capabilities describe"} {
			if !strings.Contains(out.String(), text) {
				t.Fatal(id, "missing help", text)
			}
		}
		for _, field := range []struct {
			flag    string
			present bool
		}{{"--course ", !r.Directory}, {"--cursor ", r.List}, {"--item-id ", r.Item}, {"--module-id ", r.Module}, {"--start-date ", r.Dates}} {
			if strings.Contains(out.String(), field.flag) != field.present {
				t.Fatal(id, "mixed filter", field.flag)
			}
		}
		out.Reset()
		diag.Reset()
		if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "--connection") {
			t.Fatal("private command bypassed connection", id)
		}
	}
}
