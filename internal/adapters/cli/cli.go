package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/bridge"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	mcpadapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	return encoder.Encode(value)
}

// New constructs commands from the same registry exposed by the Go API and MCP.
func New(out, diagnostics io.Writer, input io.Reader) *cobra.Command {
	profile := "public"
	jsonOutput := false
	verbose := false
	connection := ""
	root := &cobra.Command{Use: "sjtu", Short: "SJTU Bridge：按应用授权的校园服务接入层", Long: "SJTU Bridge：按应用授权的校园服务接入层。\n\n先用 services list/search 发现事项，再用 services describe 查看范围、官网入口与当前可调用动作。\ncapabilities describe 查看具体能力的参数与限制。应用使用 --connection 指定自己的连接文件。", SilenceUsage: true, SilenceErrors: true}
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
		&cobra.Group{ID: "discovery", Title: "事项与能力发现："},
		&cobra.Group{ID: "services", Title: "校园服务："},
		&cobra.Group{ID: "management", Title: "用户管理："},
		&cobra.Group{ID: "runtime", Title: "接入与诊断："},
	)
	root.SetHelpCommandGroupID("runtime")
	root.SetCompletionCommandGroupID("runtime")
	root.PersistentFlags().StringVar(&profile, "profile", "public", "公共查询策略（仅 public）")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "输出结构化 JSON")
	root.PersistentFlags().BoolVar(&verbose, "verbose", false, "展开完整契约和来源说明；JSON 使用缩进排版")
	_ = root.PersistentFlags().MarkHidden("profile")
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
	addDiscovery(root, out, &profile, &connection, &jsonOutput, &verbose)
	addServiceDiscovery(root, out, &profile, &connection, &jsonOutput, &verbose)
	root.AddCommand(&cobra.Command{Use: "doctor", GroupID: "runtime", Short: "离线实现状态与诊断（不探测校园网站）", Args: cobra.NoArgs, RunE: withEngine(func(cmd *cobra.Command, api *bridge.Engine) error {
		return writeResult(out, diagnostics, map[string]any{"version": "0.3.0", "profile": profile, "network_probed": false, "credential_store": "broker_system_keyring_aes_gcm", "providers": map[string]string{"publicinfo": "implemented", "servicenav": "bundled_navigation_only", "campuspublic": "anonymous_source_page_queries", "canvas": "broker_course_scoped_learning_read_only", "jaccount": "portal_specific_identity_required", "teaching": "broker_timetable_read_only", "sjtumail": "broker_inbox_headers_read_only", "activity": "broker_activity_discovery_calendar_and_own_records_read_only", "dekt": "broker_secondclass_fixed_records_read_only", "electric": "broker_bound_room_balance_and_daily_usage_read_only", "bath": "broker_bound_building_and_own_consumption_read_only", "jcourse": "broker_user_api_key_community_read_only", "ids": "anonymous_teaching_rooms_and_public_services", "campusfeed": "anonymous_rss_and_dining_queries"}, "capabilities": len(api.Capabilities())}, jsonOutput, verbose)
	})})
	registry, err := bridge.New(bridge.Options{})
	if err != nil {
		panic(err)
	}
	defer registry.Close()
	for _, c := range registry.Capabilities() {
		parts := contracts.CLIPath(c.ID)
		resource := ensureCommandPath(root, parts[:len(parts)-1], c.CategoryLabel+"公开查询与服务导航")
		var jsonInput *jsonInputOptions
		var publicInput *contracts.ListInput
		var businessFlags []string
		command := &cobra.Command{Use: parts[len(parts)-1], Short: c.Title, Long: c.Description, Args: cobra.NoArgs, RunE: withEngine(func(cmd *cobra.Command, api *bridge.Engine) error {
			raw, err := jsonInput.read(cmd, input, publicInput, businessFlags, c.InputSchema)
			if err != nil {
				return err
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
			return writeResult(out, diagnostics, response, jsonOutput, verbose)
		})}
		publicInput, businessFlags = addPublicInputFlags(command, c.InputSchema)
		jsonInput = addJSONInputFlags(command)
		addBusinessExample(command, c)
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
	addPrivate(root, out, diagnostics, input, &connection, &jsonOutput, &verbose)
	root.SetHelpCommand(&cobra.Command{Use: "help [command]", GroupID: "runtime", Short: "查看指定命令的帮助", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		target, remaining, err := root.Find(args)
		if target == nil {
			target = root
		}
		if err != nil || len(remaining) > 0 {
			return &cliError{Code: "INVALID_ARGUMENT", Message: "没有找到对应的帮助主题。", Hint: target.CommandPath() + " --help"}
		}
		return target.Help()
	}})
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	completeCommandHelp(root)
	configureCommandErrors(root)
	return root
}

func ensureCommandPath(root *cobra.Command, path []string, short string) *cobra.Command {
	parent := root
	for _, name := range path {
		child := findCommand(parent, name)
		if child == nil {
			child = &cobra.Command{Use: name}
			if parent == root {
				child.GroupID, child.Short = "services", short
			}
			parent.AddCommand(child)
		}
		parent = child
	}
	return parent
}

func Execute(ctx context.Context, args []string, out, diagnostics io.Writer, input io.Reader) int {
	root := New(out, diagnostics, input)
	root.SetArgs(args)
	if cmd, err := root.ExecuteContextC(ctx); err != nil {
		if cmd == nil {
			cmd = root
		}
		_ = writeError(diagnostics, presentError(cmd, err), jsonRequested(cmd, args))
		return 1
	}
	return 0
}
