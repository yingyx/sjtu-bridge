package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
)

func addServiceDiscovery(root *cobra.Command, out io.Writer, profile, connection *string, jsonOutput, verbose *bool) {
	group := &cobra.Command{Use: "services", GroupID: "discovery", Short: "查找校园事项、官方入口和当前可调用动作", Long: "发现事项不授予业务权限。官网办理范围与当前应用可调用动作分别展示；本次不探测官方链接。"}
	for _, verb := range []string{"list", "search"} {
		filter := contracts.ServiceFilter{Limit: 20}
		use, short, args := "list", "列出校园事项，可按分类和服务筛选", cobra.NoArgs
		if verb == "search" {
			use, short, args = "search QUERY", "按任务、别名或关键词查找校园事项", cobra.ExactArgs(1)
		}
		command := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			if verb == "search" {
				filter.Query = strings.TrimSpace(args[0])
				if filter.Query == "" {
					return contracts.Fail("INVALID_ARGUMENT", "Search query cannot be empty")
				}
			}
			caps, err := discover(cmd.Context(), *profile, *connection)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("limit") && filter.Limit < 1 {
				return contracts.Fail("INVALID_ARGUMENT", "Service limit must be 1-100")
			}
			page, err := servicecatalog.Search(caps, filter)
			if err != nil {
				return err
			}
			if *jsonOutput {
				for i := range page.Items {
					page.Items[i] = serviceEntryForCLI(page.Items[i], *connection != "")
				}
				if *verbose {
					return writePrettyJSON(out, page)
				}
				return writeJSON(out, page)
			}
			for _, entry := range page.Items {
				if err := writeServiceSummary(out, entry, *connection != ""); err != nil {
					return err
				}
			}
			var summary strings.Builder
			if len(page.Items) == 0 {
				fmt.Fprintln(&summary, "没有匹配事项。可缩短关键词，或使用 sjtu services list 查看目录。")
			}
			fmt.Fprintf(&summary, "目录范围：%s；版本：%s；共 %d 项，本页 %d 项；本次未探测链接。\n", page.Scope, page.Revision, page.Total, len(page.Items))
			if page.NextOffset != nil {
				fmt.Fprintf(&summary, "后续页：使用相同筛选条件并追加 --offset %d --limit %d\n", *page.NextOffset, page.Limit)
			}
			_, err = io.WriteString(out, summary.String())
			return err
		}}
		command.Flags().StringVar(&filter.Category, "category", "", "业务分类 ID 或中文名称")
		command.Flags().StringVar(&filter.TaskGroup, "task-group", "", "任务组 ID 或中文名称；包含显式交叉入口")
		command.Flags().StringVar(&filter.Service, "service", "", "服务名称，如 Canvas / 教务处")
		command.Flags().IntVar(&filter.Offset, "offset", 0, "结果偏移，不小于 0")
		command.Flags().IntVar(&filter.Limit, "limit", 20, "每页条数，1–100")
		if verb == "search" {
			command.Example = "  sjtu services search 作业\n  sjtu services search 校园网 --json"
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "describe ID", Short: "查看事项范围、依据、人工办理边界及当前可调用动作", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		caps, err := discover(cmd.Context(), *profile, *connection)
		if err != nil {
			return err
		}
		entry, err := servicecatalog.Describe(caps, args[0])
		if err != nil {
			return err
		}
		if *jsonOutput {
			entry = serviceEntryForCLI(entry, *connection != "")
			if *verbose {
				return writePrettyJSON(out, entry)
			}
			return writeJSON(out, entry)
		}
		return writeServiceEntry(out, entry, *connection != "")
	}})
	root.AddCommand(group)
}

func writeServiceEntry(out io.Writer, entry contracts.ServiceEntry, connected bool) error {
	var text strings.Builder
	if entry.Audience == "" {
		entry.Audience = "尚无明确对象说明"
	}
	fmt.Fprintf(&text, "%s  %s\n  用途：%s\n  对象：%s\n  服务：%s；事项归属：%s/%s\n", entry.ID, entry.Title, entry.Purpose, entry.Audience, entry.Service, entry.Primary.Category, entry.Primary.TaskGroup)
	for _, destination := range entry.Destinations {
		fmt.Fprintf(&text, "  官方入口：%s · %s\n    %s\n", destination.Title, destination.Kind, destination.URL)
		if destination.Upstream != nil {
			fmt.Fprintf(&text, "    官网操作：%s；执行：%s；%s\n", destination.Upstream.Operation, destination.Upstream.ExecutionRule, destination.Upstream.Reason)
		}
	}
	for _, boundary := range entry.ManualLimitations {
		fmt.Fprintf(&text, "  办理边界：%s\n", boundary)
	}
	for _, tool := range entry.ExternalClients {
		fmt.Fprintf(&text, "  外部工具：%s · %s\n    文档：%s\n", tool.Title, tool.ID, tool.DocumentationURL)
		if tool.InstallURL != "" {
			fmt.Fprintf(&text, "    安装来源：%s\n", tool.InstallURL)
		}
		fmt.Fprintf(&text, "    认证：%s\n    范围：%s\n    验证：%s\n", tool.Authentication, tool.Scope, tool.Verification)
		for _, step := range tool.Setup {
			fmt.Fprintf(&text, "    配置：%s\n", step)
		}
		for _, limitation := range tool.Limitations {
			fmt.Fprintf(&text, "    限制：%s\n", limitation)
		}
	}
	fmt.Fprintf(&text, "  资料核验：%s；本次未探测链接。\n", entry.VerifiedAt)
	for _, source := range entry.Sources {
		fmt.Fprintf(&text, "  依据：%s %s\n", source.Title, source.URL)
	}
	fmt.Fprintln(&text, "  当前可调用动作：")
	if len(entry.AvailableCapabilities) == 0 {
		fmt.Fprintln(&text, "    无。事项说明及官网入口不代表当前应用具有调用权限。")
	}
	for _, action := range entry.AvailableCapabilities {
		fmt.Fprintf(&text, "    %s · %s\n      %s%s\n      参数：sjtu capabilities describe %s%s\n", action.ID, action.Title, action.CLICommand, discoveryConnectionSuffix(connected), action.ID, discoveryConnectionSuffix(connected))
	}
	fmt.Fprintln(&text)
	_, err := io.WriteString(out, text.String())
	return err
}

// JSON invocation hints preserve the chosen connection context without leaking
// a user's path. This is presentation only; shared capability metadata is intact.
func serviceEntryForCLI(entry contracts.ServiceEntry, connected bool) contracts.ServiceEntry {
	entry.AvailableCapabilities = append([]contracts.CapabilitySummary{}, entry.AvailableCapabilities...)
	if connected {
		for i := range entry.AvailableCapabilities {
			entry.AvailableCapabilities[i].CLICommand += discoveryConnectionSuffix(true)
		}
	}
	return entry
}

func writeServiceSummary(out io.Writer, entry contracts.ServiceEntry, connected bool) error {
	var text strings.Builder
	fmt.Fprintf(&text, "%s  %s\n  %s\n", entry.ID, entry.Title, entry.Purpose)
	if entry.Audience != "" {
		fmt.Fprintf(&text, "  对象：%s\n", entry.Audience)
	}
	if len(entry.Destinations) > 0 {
		fmt.Fprintf(&text, "  官方入口：%s\n", entry.Destinations[0].URL)
	}
	if len(entry.ManualLimitations) > 0 {
		fmt.Fprintf(&text, "  办理边界摘要：%s\n", entry.ManualLimitations[0])
	}
	fmt.Fprintf(&text, "  资料核验：%s；本次未探测链接；当前可调用 %d 项。\n", entry.VerifiedAt, len(entry.AvailableCapabilities))
	for _, action := range entry.AvailableCapabilities[:min(3, len(entry.AvailableCapabilities))] {
		fmt.Fprintf(&text, "    %s%s\n", action.CLICommand, discoveryConnectionSuffix(connected))
	}
	if len(entry.AvailableCapabilities) > 3 {
		fmt.Fprintln(&text, "    以上仅列前 3 项；全部动作见事项详情。")
	}
	fmt.Fprintf(&text, "  详情：sjtu services describe %s%s\n\n", entry.ID, discoveryConnectionSuffix(connected))
	_, err := io.WriteString(out, text.String())
	return err
}
