package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/app"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/local"
)

func confirm(input io.Reader, diag io.Writer, preview any) error {
	if e := auth.RequireTerminal(input); e != nil {
		return e
	}
	if e := writeJSON(diag, preview); e != nil {
		return e
	}
	fmt.Fprint(diag, "确认以上具体操作？输入 yes: ")
	answer, e := bufio.NewReader(input).ReadString('\n')
	if e != nil || strings.TrimSpace(answer) != "yes" {
		return contracts.Fail("CANCELED", "Owner did not confirm the operation")
	}
	return nil
}
func addPrivate(root *cobra.Command, out, diag io.Writer, input io.Reader, connection *string, jsonOutput, verbose *bool) {
	write := func(value any) error { return writeResult(out, diag, value, *jsonOutput, *verbose) }
	bc := &cobra.Command{Use: "broker", GroupID: "management", Short: "初始化并前台运行本机凭据服务"}
	bc.AddCommand(&cobra.Command{Use: "init", Short: "初始化本机凭据存储与管理身份", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if e := confirm(input, diag, "初始化系统 keyring 主密钥、独立 owner 凭证与私有状态；已有状态不可重置"); e != nil {
			return e
		}
		if e := broker.Initialize(); e != nil {
			return e
		}
		return write(map[string]string{"status": "initialized"})
	}})
	bc.AddCommand(&cobra.Command{Use: "run", Short: "在前台运行单实例 Broker", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return broker.Run(cmd.Context()) }})
	root.AddCommand(bc)
	authCmd := &cobra.Command{Use: "auth", GroupID: "management", Short: "用户凭据导入、移除与会话诊断"}
	provider, label := "", "main"
	var browserContext bool
	importCmd := &cobra.Command{Use: "import", Short: "隐藏输入并核验 Token 或 Cookie 后导入", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if provider == "jaccount" {
			return contracts.Fail("NOT_READY", "JAAuthCookie stable identity is not ready for persistent import")
		}
		if provider != "canvas" && provider != "teaching" && provider != "sjtumail" && provider != "activity" && provider != "electric" {
			return contracts.Fail("INVALID_ARGUMENT", "Use --provider canvas, teaching, sjtumail, activity or electric")
		}
		if browserContext && provider == "canvas" {
			return contracts.Fail("INVALID_ARGUMENT", "Browser session context is only for campus JAAuthCookie providers")
		}
		normalizer := provider
		if provider != "canvas" {
			normalizer = "jaccount"
		}
		secret, e := auth.ReadCredential(input, diag, normalizer, true)
		if e != nil {
			return e
		}
		defer clear(secret)
		if browserContext {
			contextSecret, err := auth.ReadCampusContext(input, diag, secret)
			if err != nil {
				return err
			}
			clear(secret)
			secret = contextSecret
			defer clear(contextSecret)
		}
		q := broker.AdminRequest{Action: "verify", Provider: provider, Secret: secret}
		var verified map[string]string
		if e = broker.OwnerCall(cmd.Context(), q, &verified); e != nil {
			return e
		}
		q.Action = "import"
		q.ExpectedAccount = verified["account_ref"]
		q.Label = label
		if e = confirm(input, diag, map[string]string{"action": "import", "provider": provider, "account_ref": q.ExpectedAccount, "label": label}); e != nil {
			return e
		}
		var v any
		if e = broker.OwnerCall(cmd.Context(), q, &v); e != nil {
			return e
		}
		return write(v)
	}}
	importCmd.Flags().StringVar(&provider, "provider", "", "canvas (Token), teaching / sjtumail / activity / electric (JAAuthCookie, independently verified portal account)")
	importCmd.Flags().StringVar(&label, "account", "main", "Local account label (not an authorization key)")
	importCmd.Flags().BoolVar(&browserContext, "browser-context", false, "Optionally import trusted-device Cookie and original browser User-Agent using hidden terminal input")
	authCmd.AddCommand(importCmd)
	authCmd.AddCommand(campusLoginCommand(out, diag, input, jsonOutput, verbose))
	checkProvider := ""
	check := &cobra.Command{Use: "check", Short: "临时检查 jAccount 会话状态", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if checkProvider != "jaccount" {
			return contracts.Fail("INVALID_ARGUMENT", "Use --provider jaccount")
		}
		secret, e := auth.ReadTransient(input, diag, "jaccount")
		if e != nil {
			return e
		}
		defer clear(secret)
		v, e := app.CheckJAccount(cmd.Context(), secret)
		if e != nil {
			return e
		}
		return write(v)
	}}
	check.Flags().StringVar(&checkProvider, "provider", "", "Diagnostic provider (jaccount)")
	authCmd.AddCommand(check)
	ownerCommand := func(use, action string, q *broker.AdminRequest, mutating bool) *cobra.Command {
		descriptions := map[string]string{"status": "查看已保存凭据、应用与授权状态", "credential_remove": "移除凭据并撤销关联应用授权", "courses": "列出已验证 Canvas 账号的课程", "resources": "查询本人宿舍电费账号的绑定房间", "app_revoke": "撤销应用连接及其授权", "grant": "授予应用指定账号、资源和视图的访问权", "grant_revoke": "撤销应用当前的个人能力授权"}
		return &cobra.Command{Use: use, Short: descriptions[action], Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if e := auth.RequireTerminal(input); e != nil {
				return e
			}
			q.Action = action
			if mutating {
				if e := confirm(input, diag, q); e != nil {
					return e
				}
			}
			var v any
			if e := broker.OwnerCall(cmd.Context(), *q, &v); e != nil {
				return e
			}
			return write(v)
		}}
	}
	statusQ := &broker.AdminRequest{}
	authCmd.AddCommand(ownerCommand("status", "status", statusQ, false))
	removeQ := &broker.AdminRequest{}
	remove := ownerCommand("remove", "credential_remove", removeQ, true)
	remove.Flags().StringVar(&removeQ.CredentialID, "credential", "", "Credential ID to revoke for all apps")
	_ = remove.MarkFlagRequired("credential")
	authCmd.AddCommand(remove)
	courseQ := &broker.AdminRequest{}
	courses := ownerCommand("courses", "courses", courseQ, false)
	courses.Flags().StringVar(&courseQ.CredentialID, "credential", "", "Verified credential ID")
	_ = courses.MarkFlagRequired("credential")
	authCmd.AddCommand(courses)
	resourcesQ := &broker.AdminRequest{}
	resources := ownerCommand("resources", "resources", resourcesQ, false)
	resources.Flags().StringVar(&resourcesQ.CredentialID, "credential", "", "Independently verified electricity credential ID")
	_ = resources.MarkFlagRequired("credential")
	authCmd.AddCommand(resources)
	root.AddCommand(authCmd)
	apps := &cobra.Command{Use: "apps", GroupID: "management", Short: "创建、轮换和撤销应用连接"}
	for _, action := range []string{"create", "rotate"} {
		q := &broker.AdminRequest{}
		description := "创建应用并交付连接文件"
		if action == "rotate" {
			description = "轮换应用令牌并交付新的连接文件"
		}
		c := &cobra.Command{Use: action + " NAME", Short: description, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			q.Action = "app_" + action
			q.ClientID = args[0]
			if e := auth.RequireTerminal(input); e != nil {
				return e
			}
			if !filepath.IsAbs(q.Path) {
				return contracts.Fail("INVALID_ARGUMENT", "Use an absolute --output path outside the workspace")
			}
			cwd, e := os.Getwd()
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(cwd, q.Path)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return contracts.Fail("INVALID_ARGUMENT", "Application connections must be delivered outside the workspace")
			}
			if e = confirm(input, diag, q); e != nil {
				return e
			}
			parent := filepath.Dir(q.Path)
			if _, e = os.Lstat(parent); os.IsNotExist(e) {
				if e = local.SecureDir(parent); e != nil {
					return contracts.Fail("STORAGE_UNAVAILABLE", "Cannot create private delivery directory")
				}
			}
			var v any
			if e = broker.OwnerCall(cmd.Context(), *q, &v); e != nil {
				return e
			}
			return write(v)
		}}
		c.Flags().StringVar(&q.Path, "output", "", "Absolute new connection file in a private directory, outside workspace")
		_ = c.MarkFlagRequired("output")
		c.Flags().IntVar(&q.Days, "days", 30, "Token lifetime (1-30 days)")
		apps.AddCommand(c)
	}
	ar := &broker.AdminRequest{}
	revokeApp := ownerCommand("revoke", "app_revoke", ar, true)
	revokeApp.Flags().StringVar(&ar.ClientID, "app", "", "Application ID")
	_ = revokeApp.MarkFlagRequired("app")
	apps.AddCommand(revokeApp)
	root.AddCommand(apps)
	permissions := &cobra.Command{Use: "permissions", GroupID: "management", Short: "授予或撤销应用的课程与字段视图权限"}
	g := &broker.AdminRequest{}
	grant := ownerCommand("grant", "grant", g, true)
	grant.Flags().StringVar(&g.ClientID, "app", "", "Application ID")
	grant.Flags().StringVar(&g.CredentialID, "credential", "", "Verified credential ID")
	grant.Flags().StringVar(&g.AccountRef, "account", "", "Stable account_ref (canvas:user:ID)")
	grant.Flags().StringSliceVar(&g.Courses, "courses", nil, "Exact course_refs, comma separated")
	grant.Flags().StringVar(&g.Capability, "capability", contracts.AssignmentsCapability, "Exact capability ID; one active grant per app")
	grant.Flags().StringSliceVar(&g.Resources, "resources", nil, "Exact portal resource_refs; use auth resources for the verified electricity room")
	grant.Flags().StringVar(&g.View, "view", "", "Canvas: summary_v1/deadlines_v1; timetable: schedule_v1; inbox: headers_v1; activities: summaries_v1; electricity: balance_v1 (limit=1)")
	grant.Flags().IntVar(&g.Limit, "limit", 20, "Per-call item cap (1-100)")
	grant.Flags().IntVar(&g.Days, "days", 30, "Grant lifetime (1-30 days)")
	for _, flag := range []string{"app", "credential", "account", "view"} {
		_ = grant.MarkFlagRequired(flag)
	}
	permissions.AddCommand(grant)
	r := &broker.AdminRequest{}
	revokeGrant := ownerCommand("revoke", "grant_revoke", r, true)
	revokeGrant.Flags().StringVar(&r.ClientID, "app", "", "Application ID")
	_ = revokeGrant.MarkFlagRequired("app")
	permissions.AddCommand(revokeGrant)
	root.AddCommand(permissions)
	canvas := &cobra.Command{Use: "canvas", GroupID: "services", Short: "Canvas 学习任务与作业查询"}
	assignments := &cobra.Command{Use: "assignments", Short: "课程作业与截止时间"}
	var q contracts.AssignmentQuery
	var assignmentInput *jsonInputOptions
	assignmentSchema := contracts.AssignmentInputSchema()
	assignmentFlags := []string{"account", "course", "view", "limit", "cursor"}
	list := &cobra.Command{Use: "list", Short: "查询获准课程的作业与截止时间", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if e := requirePrivateConnection(*connection); e != nil {
			return e
		}
		raw, e := assignmentInput.read(cmd, input, q, assignmentFlags, assignmentSchema)
		if e != nil {
			return e
		}
		c, e := client.Open(*connection)
		if e != nil {
			return e
		}
		defer c.Close()
		v, e := c.Invoke(cmd.Context(), contracts.Invocation{Capability: contracts.AssignmentsCapability, Input: raw})
		if e != nil {
			return e
		}
		return write(v)
	}}
	list.Example = "  sjtu capabilities describe canvas.assignments.list --connection CONNECTION\n  sjtu canvas assignments list --connection CONNECTION --account ACCOUNT --course COURSE --view deadlines_v1 --limit 20"
	list.Flags().StringVar(&q.AccountRef, "account", "", "")
	list.Flags().StringVar(&q.CourseRef, "course", "", "")
	list.Flags().StringVar(&q.View, "view", "", "")
	list.Flags().IntVar(&q.Limit, "limit", 20, "")
	list.Flags().StringVar(&q.Cursor, "cursor", "", "")
	applySchemaFlagHelp(list, assignmentSchema)
	assignmentInput = addJSONInputFlags(list)
	assignments.AddCommand(list)
	canvas.AddCommand(assignments)
	root.AddCommand(canvas)
	for _, id := range []string{contracts.TimetableCapability, contracts.MailCapability, contracts.ActivityCapability, contracts.ElectricityBalanceCapability} {
		metadata, _ := contracts.PersonalMetadata(id)
		rules, _ := contracts.CampusRules(id)
		schema := contracts.CampusInputSchema(id)
		parts := strings.Split(id, ".")
		group := findCommand(root, parts[0])
		if group == nil {
			group = &cobra.Command{Use: parts[0], GroupID: "services", Short: metadata.CategoryLabel}
			root.AddCommand(group)
		}
		resource := findCommand(group, parts[1])
		if resource == nil {
			resource = &cobra.Command{Use: parts[1], Short: metadata.Title}
			group.AddCommand(resource)
		}
		var q contracts.CampusQuery
		var jsonInput *jsonInputOptions
		businessFlags := []string{"account", "resource", "view", "limit", "cursor"}
		list := &cobra.Command{Use: parts[2], Short: metadata.Title, Long: metadata.Description, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if e := requirePrivateConnection(*connection); e != nil {
				return e
			}
			raw, e := jsonInput.read(cmd, input, q, businessFlags, schema)
			if e != nil {
				return e
			}
			c, e := client.Open(*connection)
			if e != nil {
				return e
			}
			defer c.Close()
			v, e := c.Invoke(cmd.Context(), contracts.Invocation{Capability: id, Input: raw})
			if e != nil {
				return e
			}
			return write(v)
		}}
		list.Example = "  sjtu capabilities describe " + id + " --connection CONNECTION\n  sjtu " + strings.ReplaceAll(id, ".", " ") + " --connection CONNECTION --account ACCOUNT --resource RESOURCE --view " + rules.View + " --limit " + fmt.Sprint(min(20, rules.MaxLimit))
		list.Flags().StringVar(&q.AccountRef, "account", "", "")
		list.Flags().StringVar(&q.ResourceRef, "resource", "", "")
		list.Flags().StringVar(&q.View, "view", "", "")
		list.Flags().IntVar(&q.Limit, "limit", min(20, rules.MaxLimit), "")
		list.Flags().StringVar(&q.Cursor, "cursor", "", "")
		if rules.QueryAllowed {
			list.Flags().StringVar(&q.Query, "query", "", "")
			businessFlags = append(businessFlags, "query")
		}
		applySchemaFlagHelp(list, schema)
		jsonInput = addJSONInputFlags(list)
		resource.AddCommand(list)
	}
}

func requirePrivateConnection(connection string) error {
	if strings.TrimSpace(connection) != "" {
		return nil
	}
	return &cliError{Code: "CLIENT_UNAUTHORIZED", Message: "个人查询缺少 --connection 应用连接文件", Hint: "使用 --connection 连接文件绝对路径；先运行 sjtu capabilities describe 能力ID --connection 连接文件查看获准参数"}
}

func findCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
