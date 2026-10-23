package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
)

func TestServiceDescribeExposesExternalGuidanceSeparatelyFromCapabilities(t *testing.T) {
	for _, test := range []struct {
		id         string
		capability []string
	}{
		{"notes", nil},
		{"gitlab", nil},
		{"campus-ai", []string{contracts.AIStatusCapability, contracts.AIUsageCapability}},
	} {
		t.Run(test.id, func(t *testing.T) {
			expected, err := servicecatalog.Describe(nil, test.id)
			if err != nil || len(expected.ExternalClients) == 0 {
				t.Fatalf("missing task guidance: %#v, %v", expected, err)
			}
			var out, diag bytes.Buffer
			execute := func(asJSON bool) string {
				t.Helper()
				out.Reset()
				diag.Reset()
				args := []string{"services", "describe", test.id}
				if asJSON {
					args = append(args, "--json")
				}
				if Execute(context.Background(), args, &out, &diag, strings.NewReader("")) != 0 {
					t.Fatalf("services describe failed: %s", diag.String())
				}
				return out.String()
			}

			text := execute(false)
			actionsIndex := strings.Index(text, "当前可调用动作：")
			if actionsIndex < 0 || !strings.Contains(text[:actionsIndex], "外部工具：") {
				t.Fatalf("external guidance not separate from actions: %s", text)
			}
			for _, tool := range expected.ExternalClients {
				fields := append([]string{tool.Title, tool.InstallURL, tool.DocumentationURL, tool.Authentication, tool.Scope, tool.Verification}, tool.Setup...)
				fields = append(fields, tool.Limitations...)
				for _, want := range fields {
					if want == "" || !strings.Contains(text[:actionsIndex], want) {
						t.Fatalf("default text omits external guidance %q: %s", want, text)
					}
				}
				if strings.Contains(text[actionsIndex:], tool.InstallURL) {
					t.Fatalf("installation source rendered as callable action: %s", text)
				}
			}
			for _, label := range []string{"安装来源：", "配置：", "认证：", "验证："} {
				if !strings.Contains(text[:actionsIndex], label) {
					t.Fatalf("default text omits %s: %s", label, text)
				}
			}

			raw := execute(true)
			var entry contracts.ServiceEntry
			if err := json.Unmarshal([]byte(raw), &entry); err != nil {
				t.Fatal(err, raw)
			}
			if entry.ID != test.id || !reflect.DeepEqual(entry.ExternalClients, expected.ExternalClients) || !strings.Contains(raw, `"external_clients"`) {
				t.Fatalf("JSON omits or changes external guidance: %s", raw)
			}
			var available []string
			for _, c := range entry.AvailableCapabilities {
				available = append(available, c.ID)
			}
			if !slices.Equal(available, test.capability) || !slices.Equal(entry.SupportedCapabilityIDs, test.capability) {
				t.Fatalf("external tools changed supported/available capabilities: %s", raw)
			}
			for _, tool := range entry.ExternalClients {
				if slices.Contains(entry.SupportedCapabilityIDs, tool.ID) || slices.Contains(available, tool.ID) {
					t.Fatalf("external tool registered as a Bridge capability: %s", raw)
				}
			}
		})
	}
}
