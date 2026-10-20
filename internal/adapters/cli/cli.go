package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/bridge"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	mcpadapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// New constructs commands from the same registry exposed by the Go API and MCP.
func New(out, diagnostics io.Writer, input io.Reader) *cobra.Command {
	profile := "public"
	jsonOutput := false
	connection := ""
	root := &cobra.Command{Use: "sjtu", Short: "SJTU Bridge：按应用授权的校园服务接入层", Long: "SJTU Bridge：按应用授权的校园服务接入层。\n\n先用 capabilities list/search 发现任务，再用 describe 查看参数与限制。\n应用使用 --connection 指定自己的连接文件；凭据导入和授权由用户管理命令完成。", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(out)
	root.SetErr(diagnostics)
	root.SetIn(input)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("connection") && strings.TrimSpace(connection) == "" {
			return contracts.Fail("CLIENT_UNAUTHORIZED", "Explicit application connection cannot be empty")
		}
		return nil
	}
	root.AddGroup(
		&cobra.Group{ID: "discovery", Title: "能力发现："},
		&cobra.Group{ID: "services", Title: "校园服务："},
		&cobra.Group{ID: "management", Title: "用户管理："},
		&cobra.Group{ID: "runtime", Title: "接入与诊断："},
	)
	root.SetHelpCommandGroupID("runtime")
	root.SetCompletionCommandGroupID("runtime")
	root.PersistentFlags().StringVar(&profile, "profile", "public", "公共查询策略（仅 public）")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "输出结构化 JSON")
	root.PersistentFlags().StringVar(&connection, "connection", "", "应用连接文件绝对路径；显式连接的发现和业务调用都经 Broker")
	withEngine := func(run func(*cobra.Command, *bridge.Engine) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			api, err := bridge.New(bridge.Options{Profile: profile})
			if err != nil {
				return err
			}
			defer api.Close()
			return run(cmd, api)
		}
	}
	addDiscovery(root, out, &profile, &connection, &jsonOutput)
	root.AddCommand(&cobra.Command{Use: "doctor", GroupID: "runtime", Short: "离线实现状态与诊断（不探测校园网站）", Args: cobra.NoArgs, RunE: withEngine(func(cmd *cobra.Command, api *bridge.Engine) error {
		return writeJSON(out, map[string]any{"version": "0.3.0", "profile": profile, "network_probed": false, "credential_store": "broker_system_keyring_aes_gcm", "providers": map[string]string{"publicinfo": "implemented", "servicenav": "bundled_navigation_only", "campuspublic": "anonymous_source_page_queries", "canvas": "broker_assignments_read_only", "jaccount": "portal_specific_identity_required", "teaching": "broker_timetable_read_only", "sjtumail": "broker_inbox_headers_read_only", "campusfeed": "anonymous_rss_and_dining_queries"}, "capabilities": len(api.Capabilities())})
	})})
	registry, err := bridge.New(bridge.Options{})
	if err != nil {
		panic(err)
	}
	defer registry.Close()
	groups := map[string]*cobra.Command{}
	resources := map[string]*cobra.Command{}
	for _, c := range registry.Capabilities() {
		parts := strings.Split(c.ID, ".")
		group := groups[parts[0]]
		if group == nil {
			group = &cobra.Command{Use: parts[0], GroupID: "services", Short: c.CategoryLabel + "公开查询与服务导航"}
			groups[parts[0]] = group
			root.AddCommand(group)
		}
		resource := resources[parts[0]+"."+parts[1]]
		if resource == nil {
			resource = &cobra.Command{Use: parts[1]}
			resources[parts[0]+"."+parts[1]] = resource
			group.AddCommand(resource)
		}
		var limit int
		var cursor, inputFile, query, canteenID string
		var stdinJSON bool
		command := &cobra.Command{Use: parts[2], Short: c.Title, Long: c.Description, Args: cobra.NoArgs, RunE: withEngine(func(cmd *cobra.Command, api *bridge.Engine) error {
			var raw []byte
			var err error
			if inputFile != "" || stdinJSON {
				if cmd.Flags().Changed("limit") || cmd.Flags().Changed("cursor") || cmd.Flags().Changed("query") || cmd.Flags().Changed("canteen-id") || (inputFile != "" && stdinJSON) {
					return contracts.Fail("INVALID_ARGUMENT", "Use flags or one JSON input source")
				}
				reader := input
				if inputFile != "" {
					file, e := os.Open(inputFile)
					if e != nil {
						return contracts.Fail("INVALID_ARGUMENT", "Cannot read input file")
					}
					defer file.Close()
					reader = file
				}
				raw, err = io.ReadAll(io.LimitReader(reader, contracts.MaxJSONBytes+1))
				if err != nil {
					return contracts.Fail("INVALID_ARGUMENT", "Cannot read JSON input")
				}
				if len(raw) > contracts.MaxJSONBytes {
					return contracts.Fail("RESPONSE_TOO_LARGE", "Input file or stream exceeds 256 KiB")
				}
				// PowerShell UTF-8 files can have a BOM; JSON itself remains strictly UTF-8.
				raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
				if len(raw) == 0 {
					return contracts.Fail("INVALID_ARGUMENT", "JSON input is empty")
				}
			} else {
				raw, err = json.Marshal(contracts.ListInput{Limit: limit, Cursor: cursor, Query: query, CanteenID: canteenID})
				if err != nil {
					return err
				}
			}
			var response contracts.Response
			invocation := contracts.Invocation{Capability: c.ID, Input: raw}
			if connection != "" {
				connected, e := client.Open(connection)
				if e != nil {
					return e
				}
				defer connected.Close()
				var result json.RawMessage
				result, err = connected.Invoke(cmd.Context(), invocation)
				if err == nil && json.Unmarshal(result, &response) != nil {
					return contracts.Fail("BROKER_UNAVAILABLE", "Invalid public response from Broker")
				}
			} else {
				response, err = api.Invoke(cmd.Context(), invocation)
			}
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(out, response)
			}
			for _, item := range response.Data.Items {
				date := ""
				if item.PublishedDate != nil {
					date = *item.PublishedDate
				}
				fmt.Fprintf(out, "%s\t%s\n  %s\n", date, item.Title, item.URL)
				for _, field := range item.Fields {
					fmt.Fprintf(out, "  %s：%s\n", field.Label, field.Value)
				}
				if n := item.Navigation; n != nil {
					fmt.Fprintf(out, "  用途：%s\n  主归属：%s/%s\n  目的地：%s\n  官方后续操作：%s / %s / %s\n  官网动作影响：%v；可恢复性：%s（仅描述，Bridge 不执行）\n  未接入原因：%s (%s)\n  官网认证：%s\n", n.Purpose, n.Primary.Category, n.Primary.TaskGroup, n.DestinationKind, n.Upstream.Status, n.Upstream.Operation, n.Upstream.ExecutionRule, n.Upstream.Effects, n.Upstream.Reversibility, n.Upstream.Reason, n.Upstream.ReasonCode, n.Upstream.Authentication)
					for _, path := range n.CrossListings {
						fmt.Fprintf(out, "  交叉入口：%s/%s\n", path.Category, path.TaskGroup)
					}
					for _, id := range n.RelatedCapabilities {
						fmt.Fprintln(out, "  相关能力："+id)
					}
					for _, source := range n.Sources {
						fmt.Fprintf(out, "  入口依据：%s %s\n", source.Title, source.URL)
					}
					fmt.Fprintf(out, "  依据核对：%s；本次链接探测：%t\n", n.VerifiedAt, n.LinkCheckedThisCall)
				}
			}
			for _, warning := range response.Warnings {
				fmt.Fprintln(diagnostics, warning.Code+": "+warning.Message)
			}
			if response.Meta.NextCursor != nil {
				fmt.Fprintln(out, "next_cursor: "+*response.Meta.NextCursor)
			}
			return nil
		})}
		command.Flags().IntVar(&limit, "limit", 10, "Maximum items (1-100)")
		command.Flags().StringVar(&cursor, "cursor", "", "Opaque continuation from the same query")
		if c.ID == "public.notices.search" {
			command.Flags().StringVar(&query, "query", "", "在近期 RSS 标题与摘要中匹配全部关键词；不是全站搜索")
		}
		if c.ID == "life.dining.areas" {
			command.Flags().StringVar(&canteenID, "canteen-id", "", "life.dining.list 返回的食堂 ID（必填）")
		}
		command.Flags().StringVar(&inputFile, "input-file", "", "Read business JSON from a UTF-8 file")
		command.Flags().BoolVar(&stdinJSON, "input-json-stdin", false, "Read business JSON from stdin (not credentials)")
		resource.AddCommand(command)
	}
	mcpCommand := &cobra.Command{Use: "mcp", GroupID: "runtime", Short: "启动 MCP stdio 接入（供宿主配置）"}
	mcpCommand.AddCommand(&cobra.Command{Use: "serve", Args: cobra.NoArgs, RunE: withEngine(func(cmd *cobra.Command, api *bridge.Engine) error {
		if connection != "" {
			c, e := client.Open(connection)
			if e != nil {
				return e
			}
			defer c.Close()
			return mcpadapter.ServeRemote(cmd.Context(), c)
		}
		return mcpadapter.Serve(cmd.Context(), api)
	})})
	root.AddCommand(mcpCommand)
	addPrivate(root, out, diagnostics, input, &connection)
	return root
}

func Execute(ctx context.Context, args []string, out, diagnostics io.Writer, input io.Reader) int {
	root := New(out, diagnostics, input)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		safe := contracts.Fail("INVALID_ARGUMENT", "Invalid command; use --help")
		var typed *contracts.Error
		if errors.As(err, &typed) {
			safe = typed
		}
		_ = writeJSON(diagnostics, map[string]any{"error": safe})
		return 1
	}
	return 0
}
