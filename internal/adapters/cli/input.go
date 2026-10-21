package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

type jsonInputOptions struct {
	file  string
	stdin bool
}

func addJSONInputFlags(command *cobra.Command) *jsonInputOptions {
	options := &jsonInputOptions{}
	command.Flags().StringVar(&options.file, "input-file", "", "从 UTF-8 文件读取业务 JSON；与业务参数互斥")
	command.Flags().BoolVar(&options.stdin, "input-json-stdin", false, "从标准输入读取业务 JSON；与业务参数互斥")
	return options
}

func (options *jsonInputOptions) read(command *cobra.Command, input io.Reader, value any, businessFlags []string, rawSchema json.RawMessage) ([]byte, error) {
	hint := command.CommandPath() + " --help"
	fail := func(message string) error {
		return &cliError{Code: "INVALID_ARGUMENT", Message: message, Hint: hint}
	}
	fileSelected := command.Flags().Changed("input-file")
	jsonSelected := fileSelected || options.stdin
	if fileSelected && options.stdin {
		return nil, fail("业务参数、--input-file 和 --input-json-stdin 只能选择一种输入方式。")
	}
	if jsonSelected {
		for _, name := range businessFlags {
			if command.Flags().Changed(name) {
				return nil, fail("业务参数、--input-file 和 --input-json-stdin 只能选择一种输入方式。")
			}
		}
	}
	var raw []byte
	if jsonSelected {
		reader := input
		if fileSelected {
			if strings.TrimSpace(options.file) == "" {
				return nil, fail("--input-file 需要一个 UTF-8 JSON 文件路径。")
			}
			file, err := os.Open(options.file)
			if err != nil {
				return nil, fail("无法读取 --input-file 指定的文件；请检查文件是否存在且可读。")
			}
			defer file.Close()
			reader = file
		}
		if reader == nil {
			return nil, fail("标准输入不可读；请提供业务 JSON 或使用 --input-file。")
		}
		var err error
		raw, err = io.ReadAll(io.LimitReader(reader, contracts.MaxJSONBytes+1))
		if err != nil {
			return nil, fail("无法读取 JSON 输入；请检查输入文件或管道。")
		}
	} else {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法生成命令输入。"}
		}
	}
	if len(raw) > contracts.MaxJSONBytes {
		return nil, fail("JSON 输入超过 256 KiB；请缩小文件或标准输入内容。")
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fail("JSON 输入为空；请提供一个 JSON 对象。")
	}
	if !utf8.Valid(raw) {
		return nil, fail("JSON 输入必须使用 UTF-8 编码。")
	}
	var decoded any
	if strictjson.Decode(raw, &decoded) != nil {
		return nil, fail("JSON 输入无效；请提供一个 JSON 对象，不要包含重复字段或多个文档。")
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fail("JSON 输入必须是对象，不能是数组、字符串或 null。")
	}
	schema, err := decodeInputSchema(rawSchema)
	if err != nil {
		return nil, err
	}
	if !jsonSelected {
		// Struct omitempty tags must not turn an explicitly invalid --limit 0
		// or an empty identifier into an omitted field with a valid default.
		if err := restoreExplicitFlags(command, object, schema); err != nil {
			return nil, err
		}
		raw, err = json.Marshal(object)
		if err != nil {
			return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法生成命令输入。"}
		}
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法验证命令的参数定义。"}
	}
	var encodedFields map[string]json.RawMessage
	if json.Unmarshal(raw, &encodedFields) != nil {
		return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法读取命令输入的字段。"}
	}
	issues, err := inputIssues(command, object, encodedFields, schema, !jsonSelected)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, &cliError{Code: "INVALID_ARGUMENT", Message: "请修正以下输入参数。", Hint: hint, Fields: issues}
	}
	if resolved.Validate(object) != nil {
		return nil, fail("输入不符合命令的参数定义；请检查参数组合。")
	}
	return raw, nil
}

func restoreExplicitFlags(command *cobra.Command, object map[string]any, schema *jsonschema.Schema) error {
	for name, property := range schema.Properties {
		flag := command.Flags().Lookup(cliFlagName(name))
		if _, exists := object[name]; exists || flag == nil || !flag.Changed {
			continue
		}
		if property.Type == "string" {
			object[name] = flag.Value.String()
			continue
		}
		// The current CLI binds integer and object fields to values whose
		// String representation is JSON. No caller-supplied keys are used.
		var value any
		if strictjson.Decode([]byte(flag.Value.String()), &value) != nil {
			return &cliError{Code: "INTERNAL_ERROR", Message: "无法读取命令参数的值。"}
		}
		object[name] = value
	}
	return nil
}

// inputIssues never formats a validation library error: those errors may quote
// submitted values or property names. Only known schema keys reach diagnostics.
func inputIssues(command *cobra.Command, object map[string]any, encodedFields map[string]json.RawMessage, schema *jsonschema.Schema, flagsMode bool) ([]inputIssue, error) {
	issues := []inputIssue{}
	for name := range object {
		if _, known := schema.Properties[name]; !known {
			issues = append(issues, inputIssue{Message: "输入含有不支持的字段；请只使用此命令列出的参数。"})
			break
		}
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		property := schema.Properties[name]
		value, exists := object[name]
		required := slices.Contains(schema.Required, name)
		flag := command.Flags().Lookup(cliFlagName(name))
		missingFlag := flagsMode && required && flag != nil && !flag.Changed && value == ""
		if required && (!exists || missingFlag) {
			issues = append(issues, inputIssue{Field: name, Message: "必填；请提供 --" + cliFlagName(name) + " 或 JSON 字段 " + name + "。"})
			continue
		}
		if !exists {
			continue
		}
		// JSON Schema treats 1.0 and 1e0 as integers, but the execution
		// contracts decode integer fields directly into Go ints. Check the
		// original spelling instead of rounding float64 values into new input.
		if _, numeric := value.(float64); numeric && property.Type == "integer" && bytes.ContainsAny(encodedFields[name], ".eE") {
			issues = append(issues, inputIssue{Field: name, Message: "--" + cliFlagName(name) + "：JSON 输入请使用整数写法（如 1），不要使用小数或指数写法。"})
			continue
		}
		resolved, err := property.Resolve(nil)
		if err != nil {
			return nil, &cliError{Code: "INTERNAL_ERROR", Message: "无法验证命令的字段定义。"}
		}
		if resolved.Validate(value) != nil {
			issues = append(issues, inputIssue{Field: name, Message: inputFieldMessage(name, property)})
		}
	}
	return issues, nil
}

func inputFieldMessage(name string, property *jsonschema.Schema) string {
	// Account/resource enum values can be private. Their allowed values remain
	// discoverable through the connected capability, not echoed in an error.
	if len(property.Enum) > 0 && (name == "account_ref" || name == "course_ref" || name == "resource_ref") {
		return "请使用当前连接允许的 --" + cliFlagName(name) + "；可通过 capabilities describe 查看获准范围。"
	}
	constraints := schemaConstraints(property)
	if property.Pattern != "" {
		constraints = append(constraints, "格式须符合参数说明")
	}
	if property.Type == "object" || property.Type == "array" {
		constraints = append(constraints, "内部字段和条目须符合参数定义")
	}
	message := "--" + cliFlagName(name) + "：" + strings.Join(constraints, "；")
	if property.Description != "" {
		message += "。" + property.Description
	}
	return message
}
