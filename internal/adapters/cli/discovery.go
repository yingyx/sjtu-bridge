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

func addDiscovery(root *cobra.Command, out io.Writer, profile, connection *string, jsonOutput *bool) {
	group := &cobra.Command{Use: "capabilities", GroupID: "discovery", Short: "按任务查找当前可调用的校园能力", Long: "未连接时发现公共能力；已连接时发现公共能力与本应用获授权的个人能力。\nlist/search 返回摘要，describe 返回完整契约。发现结果不代替调用时的权限检查。"}
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
				return writeJSON(out, contracts.CapabilitySummaries(caps))
			}
			if len(caps) == 0 {
				fmt.Fprintln(out, "当前可用能力中没有匹配项。")
				return nil
			}
			for _, c := range caps {
				fmt.Fprintf(out, "%s\t%s\n  %s (%s) / %s\n  %s\n", c.ID, c.Title, c.CategoryLabel, c.Category, c.Service, c.Description)
			}
			fmt.Fprintln(out, "使用 sjtu capabilities describe ID 查看完整参数和返回字段。")
			return nil
		}}
		command.Flags().StringVar(&filter.Category, "category", "", "业务分类 ID 或中文名称，如 learning / 学习与教学")
		command.Flags().StringVar(&filter.Service, "service", "", "服务名称，如 Canvas / 教务处")
		if verb == "search" {
			command.Example = "  sjtu capabilities search 校历\n  sjtu capabilities search \"作业 截止时间\" --connection ABSOLUTE_PATH --json"
			command.Long = "在当前可用能力的标题、分类、服务、中文别名和关键词中检索。\n大小写不敏感，空白分隔的所有词都须匹配；不搜索业务数据或限制说明。"
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "describe ID", Short: "查看完整契约、获准参数范围和调用入口", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		caps, err := discover(cmd.Context(), *profile, *connection)
		if err != nil {
			return err
		}
		for _, c := range caps {
			if c.Authorized && c.ID == args[0] {
				return writeJSON(out, c)
			}
		}
		return contracts.Fail("PERMISSION_DENIED", "Capability is not available in this application context")
	}})
	root.AddCommand(group)
}
