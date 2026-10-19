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
func addPrivate(root *cobra.Command, out, diag io.Writer, input io.Reader, connection *string) {
	bc := &cobra.Command{Use: "broker", GroupID: "management", Short: "初始化并前台运行本机凭据服务"}
	bc.AddCommand(&cobra.Command{Use: "init", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if e := confirm(input, diag, "初始化系统 keyring 主密钥、独立 owner 凭证与私有状态；已有状态不可重置"); e != nil {
			return e
		}
		if e := broker.Initialize(); e != nil {
			return e
		}
		return writeJSON(out, map[string]string{"status": "initialized"})
	}})
	bc.AddCommand(&cobra.Command{Use: "run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return broker.Run(cmd.Context()) }})
	root.AddCommand(bc)
	authCmd := &cobra.Command{Use: "auth", GroupID: "management", Short: "用户凭据导入、移除与会话诊断"}
	provider, label := "", "main"
	importCmd := &cobra.Command{Use: "import", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if provider == "jaccount" {
			return contracts.Fail("NOT_READY", "JAAuthCookie stable identity is not ready for persistent import")
		}
		if provider != "canvas" {
			return contracts.Fail("INVALID_ARGUMENT", "Use --provider canvas")
		}
		secret, e := auth.ReadCredential(input, diag, provider, true)
		if e != nil {
			return e
		}
		defer clear(secret)
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
		return writeJSON(out, v)
	}}
	importCmd.Flags().StringVar(&provider, "provider", "", "Credential provider (canvas; jaccount not ready)")
	importCmd.Flags().StringVar(&label, "account", "main", "Local account label (not an authorization key)")
	authCmd.AddCommand(importCmd)
	checkProvider := ""
	check := &cobra.Command{Use: "check", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
		return writeJSON(out, v)
	}}
	check.Flags().StringVar(&checkProvider, "provider", "", "Diagnostic provider (jaccount)")
	authCmd.AddCommand(check)
	ownerCommand := func(use, action string, q *broker.AdminRequest, mutating bool) *cobra.Command {
		return &cobra.Command{Use: use, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
			return writeJSON(out, v)
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
	root.AddCommand(authCmd)
	apps := &cobra.Command{Use: "apps", GroupID: "management", Short: "创建、轮换和撤销应用连接"}
	for _, action := range []string{"create", "rotate"} {
		q := &broker.AdminRequest{}
		c := &cobra.Command{Use: action + " NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
			return writeJSON(out, v)
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
	grant.Flags().StringVar(&g.View, "view", "", "summary_v1 or deadlines_v1")
	grant.Flags().IntVar(&g.Limit, "limit", 20, "Per-call item cap (1-100)")
	grant.Flags().IntVar(&g.Days, "days", 30, "Grant lifetime (1-30 days)")
	for _, flag := range []string{"app", "credential", "account", "courses", "view"} {
		_ = grant.MarkFlagRequired(flag)
	}
	permissions.AddCommand(grant)
	r := &broker.AdminRequest{}
	revokeGrant := ownerCommand("revoke", "grant_revoke", r, true)
	revokeGrant.Flags().StringVar(&r.ClientID, "app", "", "Application ID")
	_ = revokeGrant.MarkFlagRequired("app")
	permissions.AddCommand(revokeGrant)
	root.AddCommand(permissions)
	canvas := &cobra.Command{Use: "canvas", GroupID: "services", Short: "Canvas 学习任务与作业查询", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return contracts.Fail("UNSUPPORTED", "Use canvas assignments list through a Broker connection")
	}}
	assignments := &cobra.Command{Use: "assignments"}
	var q contracts.AssignmentQuery
	list := &cobra.Command{Use: "list", Short: "查询获准课程的作业与截止时间", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := client.Open(*connection)
		if e != nil {
			return e
		}
		defer c.Close()
		v, e := c.Assignments(cmd.Context(), q)
		if e != nil {
			return e
		}
		return writeJSON(out, v)
	}}
	list.Flags().StringVar(&q.AccountRef, "account", "", "Granted stable account_ref")
	list.Flags().StringVar(&q.CourseRef, "course", "", "Granted course_ref")
	list.Flags().StringVar(&q.View, "view", "", "Granted fixed view")
	list.Flags().IntVar(&q.Limit, "limit", 20, "Maximum items for one page")
	list.Flags().StringVar(&q.Cursor, "cursor", "", "Opaque Broker continuation (5-minute lifetime)")
	assignments.AddCommand(list)
	canvas.AddCommand(assignments)
	root.AddCommand(canvas)
}
