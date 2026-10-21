package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"
)

// decodeInputSchema reads the same trusted contract used by discovery and the
// execution boundary. CLI validation is guidance, not an authorization check.
func decodeInputSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema.Type != "object" || schema.Properties == nil {
		return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法读取命令的参数定义。"}
	}
	return &schema, nil
}

func cliFlagName(name string) string {
	switch name {
	case "account_ref":
		return "account"
	case "course_ref":
		return "course"
	case "resource_ref":
		return "resource"
	default:
		return strings.ReplaceAll(name, "_", "-")
	}
}

func schemaFlagDescription(name string, property *jsonschema.Schema, required bool) string {
	parts := []string{}
	if required {
		parts = append(parts, "必填")
	}
	parts = append(parts, schemaConstraints(property)...)
	if len(property.Default) != 0 {
		parts = append(parts, "默认 "+string(property.Default))
	}
	description := strings.Join(parts, "；")
	if property.Description != "" {
		if description != "" {
			description += "。"
		}
		description += property.Description
	}
	return description
}

// applySchemaFlagHelp deliberately does not use MarkFlagRequired: required
// business values may arrive through a JSON input file or stdin instead.
func applySchemaFlagHelp(command *cobra.Command, raw json.RawMessage) {
	schema, err := decodeInputSchema(raw)
	if err != nil {
		panic("invalid CLI input schema")
	}
	for name, property := range schema.Properties {
		flag := command.Flags().Lookup(cliFlagName(name))
		if flag == nil || property == nil {
			continue
		}
		flag.Usage = schemaFlagDescription(name, property, slices.Contains(schema.Required, name))
		if slices.Contains(schema.Required, name) && flag.DefValue != "" && flag.DefValue != "0" && flag.DefValue != "false" {
			flag.Usage = strings.Replace(flag.Usage, "必填", "JSON 必填；CLI 可用默认值", 1)
		}
		// Cobra already prints nonzero flag defaults in help. Describe can use
		// schema defaults, while the actual CLI default stays with the binding.
	}
}

func schemaConstraints(property *jsonschema.Schema) []string {
	if property == nil {
		return nil
	}
	parts := []string{}
	types := map[string]string{"string": "字符串", "integer": "整数", "number": "数值", "boolean": "布尔值", "object": "JSON 对象", "array": "JSON 数组"}
	if label := types[property.Type]; label != "" {
		parts = append(parts, label)
	}
	if len(property.Enum) > 0 {
		values := make([]string, 0, len(property.Enum))
		for _, value := range property.Enum {
			raw, _ := json.Marshal(value)
			values = append(values, string(raw))
		}
		parts = append(parts, "可选 "+strings.Join(values, " / "))
	}
	switch {
	case property.Minimum != nil && property.Maximum != nil:
		parts = append(parts, fmt.Sprintf("范围 %g–%g", *property.Minimum, *property.Maximum))
	case property.Minimum != nil:
		parts = append(parts, fmt.Sprintf("至少 %g", *property.Minimum))
	case property.Maximum != nil:
		parts = append(parts, fmt.Sprintf("至多 %g", *property.Maximum))
	}
	if property.MinLength != nil && *property.MinLength > 0 {
		parts = append(parts, fmt.Sprintf("至少 %d 个字符", *property.MinLength))
	}
	if property.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("至多 %d 个字符", *property.MaxLength))
	}
	return parts
}
