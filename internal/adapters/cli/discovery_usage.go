package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

type capabilityUsage struct {
	contracts.CapabilitySummary
	InputSchema json.RawMessage   `json:"input_schema"`
	CLIFlags    map[string]string `json:"cli_flags"`
	Example     string            `json:"example"`
}

func discoveryConnectionSuffix(connected bool) string {
	if connected {
		return " --connection CONNECTION_FILE"
	}
	return ""
}

func discoverySummaries(caps []contracts.Capability, connected bool) []contracts.CapabilitySummary {
	summaries := contracts.CapabilitySummaries(caps)
	for i := range summaries {
		summaries[i].CLICommand += discoveryConnectionSuffix(connected || summaries[i].AuthorizationRequired)
	}
	return summaries
}

func capabilityAccessLabel(c contracts.CapabilitySummary) string {
	if !c.AuthorizationRequired {
		return "凭据：无需；授权：无需"
	}
	resource := "资源"
	for _, field := range c.GrantScope {
		if field == "courses" {
			resource = "课程"
		}
	}
	state := "需配置已授权应用连接；本次未检查保存的凭据"
	if c.Authorized {
		state = "当前应用已获授权，调用时重新校验"
	}
	return fmt.Sprintf("凭据：%s；授权：按应用、能力、凭据、本人账号、%s、视图和限额；%s", strings.Join(c.Credentials, " / "), resource, state)
}

func deliveryLabel(delivery contracts.Delivery) string {
	if delivery == contracts.NavigationDelivery {
		return "官网入口"
	}
	return "数据查询"
}

func writeCapabilityUsage(out io.Writer, root *cobra.Command, c contracts.Capability, jsonOutput bool) error {
	schema, err := decodeInputSchema(c.InputSchema)
	if err != nil {
		return err
	}
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	connection, _ := root.PersistentFlags().GetString("connection")
	connected := connection != "" || c.DataClass == contracts.PersonalData
	usage := capabilityUsage{
		CapabilitySummary: discoverySummaries([]contracts.Capability{c}, connected)[0],
		InputSchema:       c.InputSchema, CLIFlags: map[string]string{},
	}
	command, _, _ := root.Find(contracts.CLIPath(c.ID))
	example := []string{usage.CLICommand}
	for _, name := range names {
		flag := cliFlagName(name)
		usage.CLIFlags[name] = "--" + flag
		if required[name] {
			example = append(example, "--"+flag, usageExampleValue(name, schema.Properties[name], command))
		}
	}
	usage.Example = strings.Join(append(example, "--json"), " ")
	if jsonOutput {
		return writeJSON(out, usage)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s · %s\n%s\n\n命令：%s\n", c.Title, deliveryLabel(c.Delivery), usage.Description, usage.CLICommand)
	fmt.Fprintln(&text, capabilityAccessLabel(usage.CapabilitySummary))
	if connection != "" {
		fmt.Fprintln(&text, "连接：使用与发现时相同的应用连接文件绝对路径")
	} else if c.DataClass == contracts.PersonalData {
		fmt.Fprintln(&text, "配置：先核验并导入凭据，用 sjtu auth status 查看本人账号和凭据ID，再创建应用并授予上述范围，最后使用 --connection 调用。管理命令：sjtu auth --help；sjtu apps create --help；sjtu permissions grant --help。")
	}
	fmt.Fprintln(&text, "\n参数：")
	for _, name := range names {
		property := schema.Properties[name]
		description := schemaFlagDescription(name, property, required[name])
		if command != nil && len(property.Default) == 0 {
			if flag := command.Flags().Lookup(cliFlagName(name)); flag != nil && flag.DefValue != "" && flag.DefValue != "0" && flag.DefValue != "false" {
				description += "；CLI 默认 " + flag.DefValue
				if value, err := strconv.ParseFloat(flag.DefValue, 64); err == nil && property.Maximum != nil && value > *property.Maximum {
					description += fmt.Sprintf("，超过当前授权；请显式指定不大于 %g 的值", *property.Maximum)
				}
			}
		}
		fmt.Fprintf(&text, "  --%s  %s\n", cliFlagName(name), description)
	}
	detailCommand := "sjtu capabilities describe " + c.ID + discoveryConnectionSuffix(connection != "")
	fmt.Fprintf(&text, "\n示例（替换占位值）：\n  %s\n\n完整契约：%s --verbose\n", usage.Example, detailCommand)
	_, err = io.WriteString(out, text.String())
	return err
}

func usageExampleValue(name string, property *jsonschema.Schema, command *cobra.Command) string {
	if len(property.Enum) > 0 {
		return quoteCLIValue(fmt.Sprint(property.Enum[0]))
	}
	if name == "date" {
		return "YYYY-MM-DD"
	}
	if property.Type == "integer" {
		value := 1
		if command != nil {
			if flag := command.Flags().Lookup(cliFlagName(name)); flag != nil {
				if parsed, err := strconv.Atoi(flag.DefValue); err == nil && parsed > 0 {
					value = parsed
				}
			}
		}
		if property.Minimum != nil && value < int(*property.Minimum) {
			value = int(*property.Minimum)
		}
		if property.Maximum != nil && value > int(*property.Maximum) {
			value = int(*property.Maximum)
		}
		return strconv.Itoa(value)
	}
	return strings.ToUpper(name)
}

func quoteCLIValue(value string) string {
	// Quoted examples work in PowerShell and POSIX shells for ordinary IDs.
	// Never turn arbitrary schema strings into executable shell syntax.
	if strings.ContainsAny(value, "'\r\n") {
		return "VALUE"
	}
	return "'" + value + "'"
}

func unavailableCapability(id string, caps []contracts.Capability) error {
	message := "当前目录中没有此能力。使用 sjtu capabilities search 关键词 查找已接入能力；带连接时仅显示应用获准范围。"
	if !utf8.ValidString(id) || utf8.RuneCountInString(id) > 256 {
		return contracts.Fail("INVALID_ARGUMENT", "能力 ID 必须是有效 UTF-8，且不超过 256 个字符")
	}
	type candidate struct {
		id       string
		distance int
	}
	var candidates []candidate
	for _, c := range caps {
		distance := capabilityIDDistance(strings.ToLower(id), strings.ToLower(c.ID))
		if distance <= 3 {
			candidates = append(candidates, candidate{c.ID, distance})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) > 0 {
		ids := make([]string, 0, min(3, len(candidates)))
		for _, candidate := range candidates[:min(3, len(candidates))] {
			ids = append(ids, candidate.id)
		}
		message += " 可能要找：" + strings.Join(ids, "、") + "。"
	}
	return contracts.Fail("PERMISSION_DENIED", message)
}

func capabilityIDDistance(a, b string) int {
	left, right := []rune(a), []rune(b)
	previous := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, x := range left {
		current := make([]int, len(right)+1)
		current[0] = i + 1
		for j, y := range right {
			cost := 0
			if x != y {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(right)]
}
