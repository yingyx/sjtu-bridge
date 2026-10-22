package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/bridge"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func discover(ctx context.Context, profile, connection string) ([]contracts.Capability, error) {
	if connection != "" {
		c, err := client.Open(connection)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		return c.Capabilities(ctx)
	}
	api, err := bridge.New(bridge.Options{Profile: profile})
	if err != nil {
		return nil, err
	}
	defer api.Close()
	return api.Capabilities(), nil
}

func addDiscovery(root *cobra.Command, out io.Writer, profile, connection *string, jsonOutput, verbose *bool) {
	group := &cobra.Command{Use: "capabilities", GroupID: "discovery", Short: "查看当前可调用能力的技术契约", Long: "先用 search 查找任务，再用 describe 查看参数和调用示例。\n使用 --connection 加入应用获准的个人能力；--verbose 查看完整契约。"}
	for _, verb := range []string{"list", "search"} {
		filter := contracts.CapabilityFilter{}
		use, short, args := "list", "列出能力摘要，可按分类和服务筛选", cobra.NoArgs
		if verb == "search" {
			use, short, args = "search QUERY", "按中文任务、别名或关键词检索能力", cobra.ExactArgs(1)
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
			caps, err = contracts.FilterCapabilities(caps, filter)
			if err != nil {
				return err
			}
			if *jsonOutput {
				if *verbose {
					return writePrettyJSON(out, caps)
				}
				return writeJSON(out, discoverySummaries(caps, *connection != ""))
			}
			var text strings.Builder
			if len(caps) == 0 {
				fmt.Fprintln(&text, "当前可用能力中没有匹配项。可缩短关键词，或使用 sjtu capabilities list"+discoveryConnectionSuffix(*connection != "")+" 查看目录。")
			} else {
				for i, summary := range discoverySummaries(caps, *connection != "") {
					fmt.Fprintf(&text, "%s  %s · %s\n  %s\n  %s\n", summary.ID, summary.Title, deliveryLabel(summary.Delivery), summary.CLICommand, summary.Description)
					if *verbose {
						c := caps[i]
						fmt.Fprintf(&text, "  ID：%s；%s / %s / %s\n  %s; data=%s; effects=%v; reversibility=%s; execution=%s\n  %s\n", c.ID, c.CategoryLabel, c.TaskGroupLabel, c.Service, c.Operation, c.DataClass, c.Effects, c.Reversibility, c.ExecutionRule, c.Description)
					}
				}
				fmt.Fprintln(&text, "参数与示例：sjtu capabilities describe 能力ID"+discoveryConnectionSuffix(*connection != "")+"；完整契约：追加 --verbose。")
			}
			_, err = io.WriteString(out, text.String())
			return err
		}}
		command.Flags().StringVar(&filter.Category, "category", "", "业务分类 ID 或中文名称，如 learning / 学习与教学")
		command.Flags().StringVar(&filter.TaskGroup, "task-group", "", "任务组 ID 或中文名称，如 practice / 实践修业与达标；包含显式交叉入口")
		command.Flags().StringVar(&filter.Service, "service", "", "服务名称，如 Canvas / 教务处")
		if verb == "search" {
			command.Example = "  sjtu capabilities search 校历\n  sjtu capabilities search \"作业 截止时间\" --connection ABSOLUTE_PATH --json"
			command.Long = "按任务名称、别名和关键词检索当前可用能力，最相关的结果在前。\n大小写不敏感，空白分隔的所有词都须匹配；分类匹配排在任务和关键词之后。"
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "describe ID", Short: "查看调用参数和示例；--verbose 查看完整契约", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		caps, err := discover(cmd.Context(), *profile, *connection)
		if err != nil {
			return err
		}
		for _, c := range caps {
			if c.Authorized && c.ID == args[0] {
				if *verbose {
					return writePrettyJSON(out, c)
				}
				return writeCapabilityUsage(out, root, c, *jsonOutput)
			}
		}
		return unavailableCapability(args[0], caps)
	}})
	root.AddCommand(group)
}
