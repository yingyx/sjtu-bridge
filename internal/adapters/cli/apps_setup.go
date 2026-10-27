package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/capabilities"
	"github.com/yingyx/sjtu-bridge/internal/local"
)

// These are owner API projections, not access to the credential store.
type setupCredential struct {
	ID         string     `json:"credential_id"`
	Provider   string     `json:"provider"`
	AccountRef string     `json:"account_ref"`
	Label      string     `json:"label"`
	Active     bool       `json:"active"`
	Expires    *time.Time `json:"expires"`
}

type permissionSummary struct {
	ID           string    `json:"grant_id"`
	ClientID     string    `json:"client_id"`
	Capability   string    `json:"capability"`
	CredentialID string    `json:"credential_id"`
	AccountRef   string    `json:"account_ref"`
	Courses      []string  `json:"course_refs,omitempty"`
	Resources    []string  `json:"resource_refs,omitempty"`
	View         string    `json:"view"`
	Limit        int       `json:"limit"`
	Active       bool      `json:"active"`
	Expires      time.Time `json:"expires"`
}

type setupStatus struct {
	Apps []struct {
		ID      string    `json:"id"`
		Active  bool      `json:"active"`
		Expires time.Time `json:"expires"`
	} `json:"apps"`
	Credentials map[string]setupCredential   `json:"credentials"`
	Grants      map[string]permissionSummary `json:"grants"`
}

type setupDependencies struct {
	call            func(context.Context, broker.AdminRequest, any) error
	requireTerminal func(io.Reader) error
	prepareDelivery func(string) error
}

func defaultSetupDependencies() setupDependencies {
	return setupDependencies{broker.OwnerCall, auth.RequireTerminal, prepareSetupDelivery}
}

func permissionsFor(status setupStatus, appID string) []permissionSummary {
	result := []permissionSummary{}
	for id, grant := range status.Grants {
		if appID == "" || grant.ClientID == appID {
			grant.ID = id
			result = append(result, grant)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ClientID != result[j].ClientID {
			return result[i].ClientID < result[j].ClientID
		}
		if result[i].Capability != result[j].Capability {
			return result[i].Capability < result[j].Capability
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func permissionsListCommand(out, diag io.Writer, input io.Reader, jsonOutput, verbose *bool) *cobra.Command {
	appID := ""
	c := &cobra.Command{Use: "list", Short: "逐项查看应用授权、范围与有效期", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := auth.RequireTerminal(input); err != nil {
			return err
		}
		var status setupStatus
		if err := broker.OwnerCall(cmd.Context(), broker.AdminRequest{Action: "status"}, &status); err != nil {
			return err
		}
		return writeResult(out, diag, permissionsFor(status, appID), *jsonOutput, *verbose)
	}}
	c.Flags().StringVar(&appID, "app", "", "Only show grants for this application")
	return c
}

func appsSetupCommand(out, diag io.Writer, input io.Reader, jsonOutput, verbose *bool) *cobra.Command {
	path, days := "", 30
	c := &cobra.Command{Use: "setup NAME", Short: "交互配置应用的账号、能力与资源授权", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := runAppSetup(cmd.Context(), args[0], path, days, input, diag, defaultSetupDependencies())
		if result != nil {
			if writeErr := writeResult(out, diag, result, *jsonOutput, *verbose); writeErr != nil {
				return writeErr
			}
		}
		return err
	}}
	c.Flags().StringVar(&path, "output", "", "New application connection file outside the workspace; asked interactively if omitted")
	c.Flags().IntVar(&days, "days", 30, "New token and changed grant lifetime (1-30 days)")
	c.Long = "选择已导入账号，为同一应用配置多项能力。每个应用、每项能力保留一条授权；重新配置该能力替换其账号和范围，其他能力保持原状。已有应用沿用连接文件。全部选择完成后统一预览，确认后逐项提交；失败时报告已完成项，不自动回滚。"
	return c
}

type setupStep struct {
	Action     string `json:"action"`
	Capability string `json:"capability,omitempty"`
	GrantID    string `json:"grant_id,omitempty"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code,omitempty"`
	Connection string `json:"connection_file,omitempty"`
}

type appSetupResult struct {
	ClientID string      `json:"client_id"`
	Status   string      `json:"status"`
	Steps    []setupStep `json:"steps"`
}

type setupPrompt struct {
	reader *bufio.Scanner
	diag   io.Writer
}

func (p setupPrompt) line(message string) (string, error) {
	fmt.Fprint(p.diag, message)
	if !p.reader.Scan() {
		return "", contracts.Fail("CANCELED", "Configuration input ended; no further changes were submitted")
	}
	v := strings.TrimSpace(p.reader.Text())
	if v == "q" || v == "quit" {
		return "", contracts.Fail("CANCELED", "Application setup canceled")
	}
	return v, nil
}

func (p setupPrompt) choose(message string, labels []string, multiple, allowEmpty bool) ([]int, error) {
	for i, label := range labels {
		fmt.Fprintf(p.diag, "  %d. %s\n", i+1, label)
	}
	for {
		v, err := p.line(message)
		if err != nil {
			return nil, err
		}
		if v == "" && allowEmpty {
			return nil, nil
		}
		parts, result, seen, valid := strings.Split(v, ","), []int{}, map[int]bool{}, true
		if !multiple && len(parts) != 1 {
			valid = false
		}
		for _, part := range parts {
			n, e := strconv.Atoi(strings.TrimSpace(part))
			if e != nil || n < 1 || n > len(labels) || seen[n] {
				valid = false
				break
			}
			seen[n] = true
			result = append(result, n-1)
		}
		if valid {
			return result, nil
		}
		fmt.Fprintln(p.diag, "请输入列表中的编号；多选用英文逗号分隔，不重复。输入 q 取消。")
	}
}

func checkSetupPath(path string) error {
	if !filepath.IsAbs(path) {
		return contracts.Fail("INVALID_ARGUMENT", "Use an absolute connection file path outside the workspace")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cwd, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return contracts.Fail("INVALID_ARGUMENT", "Application connections must be delivered outside the workspace")
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return contracts.Fail("INVALID_ARGUMENT", "Use a new connection file path")
	}
	return nil
}

func prepareSetupDelivery(path string) error {
	parent := filepath.Dir(path)
	if _, err := os.Lstat(parent); os.IsNotExist(err) {
		if err := local.SecureDir(parent); err != nil {
			return contracts.Fail("STORAGE_UNAVAILABLE", "Cannot create private delivery directory")
		}
	}
	return nil
}

func runAppSetup(ctx context.Context, appID, path string, days int, input io.Reader, diag io.Writer, deps setupDependencies) (*appSetupResult, error) {
	if err := deps.requireTerminal(input); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(appID) || days < 1 || days > 30 {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Use a valid app name and lifetime of 1-30 days")
	}
	var status setupStatus
	if err := deps.call(ctx, broker.AdminRequest{Action: "status"}, &status); err != nil {
		return nil, err
	}
	now, exists := time.Now(), false
	for _, app := range status.Apps {
		if app.ID == appID {
			exists = true
			if !app.Active || !now.Before(app.Expires) {
				return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Existing app is inactive or expired; rotate its connection before setup")
			}
		}
	}
	p := setupPrompt{bufio.NewScanner(input), diag}
	p.reader.Buffer(make([]byte, 1024), 8192)
	fmt.Fprintln(diag, "应用配置：输入 q 可随时取消选择；确认前不会修改应用或授权。")
	if exists && path != "" {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Existing app keeps its connection; omit --output")
	}
	if !exists {
		if path == "" {
			var err error
			path, err = p.line("新连接文件的绝对路径（项目目录之外）：")
			if err != nil {
				return nil, err
			}
		}
		if err := checkSetupPath(path); err != nil {
			return nil, err
		}
	}
	plan := []broker.AdminRequest{}
	if !exists {
		plan = append(plan, broker.AdminRequest{Action: "app_create", ClientID: appID, Path: path, Days: days})
	}
	current := permissionsFor(status, appID)
	active, labels := []permissionSummary{}, []string{}
	for _, grant := range current {
		if grant.Active {
			active = append(active, grant)
			labels = append(labels, fmt.Sprintf("%s · %s · %s · %s · limit=%d · expires=%s", grant.Capability, grant.AccountRef, grant.View, grant.ID, grant.Limit, grant.Expires.Format(time.RFC3339)))
		}
	}
	if len(active) != 0 {
		fmt.Fprintln(diag, "现有授权（后续再次选择同一能力将更新它）：")
		selected, err := p.choose("撤销哪些授权？多选，留空保留全部：", labels, true, true)
		if err != nil {
			return nil, err
		}
		for _, i := range selected {
			plan = append(plan, broker.AdminRequest{Action: "grant_revoke", ClientID: appID, GrantID: active[i].ID})
		}
	}
	available := capabilities.Catalog(nil)
	credentials := []setupCredential{}
	for id, credential := range status.Credentials {
		if !credential.Active || credential.Expires != nil && !now.Before(*credential.Expires) {
			continue
		}
		for _, capability := range available {
			if capability.Provider == credential.Provider {
				credential.ID = id
				credentials = append(credentials, credential)
				break
			}
		}
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	configured := map[string]bool{}
	for len(credentials) > 0 {
		labels = nil
		for _, credential := range credentials {
			labels = append(labels, fmt.Sprintf("%s · %s · %s", credential.Provider, credential.Label, credential.AccountRef))
		}
		selected, err := p.choose("选择账号添加/更新授权，留空结束选择：", labels, false, true)
		if err != nil {
			return nil, err
		}
		if len(selected) == 0 {
			break
		}
		credential := credentials[selected[0]]
		choices := []contracts.Capability{}
		labels = nil
		for _, capability := range available {
			if capability.Provider == credential.Provider && !configured[capability.ID] {
				choices = append(choices, capability)
				labels = append(labels, capability.Title+" · "+capability.ID)
			}
		}
		if len(choices) == 0 {
			fmt.Fprintln(diag, "本次已配置该门户的全部能力。")
			continue
		}
		selected, err = p.choose("选择能力（多选，英文逗号分隔）：", labels, true, false)
		if err != nil {
			return nil, err
		}
		for _, i := range selected {
			grant, err := setupGrant(ctx, p, deps.call, credential, choices[i])
			if err != nil {
				return nil, err
			}
			grant.Action, grant.ClientID, grant.Days = "grant", appID, days
			plan = append(plan, grant)
			configured[grant.Capability] = true
		}
	}
	if len(plan) == 0 || !exists && len(plan) == 1 {
		return nil, contracts.Fail("CANCELED", "No permission changes selected; import a credential first if no account was listed")
	}
	fmt.Fprintln(diag, "即将逐项提交；同一能力的新授权替换旧范围。未列出的授权保持原状：")
	if err := writeJSON(diag, plan); err != nil {
		return nil, err
	}
	answer, err := p.line("确认以上具体操作？输入 yes：")
	if err != nil || answer != "yes" {
		return nil, contracts.Fail("CANCELED", "Owner did not confirm application setup")
	}
	if !exists {
		if err := deps.prepareDelivery(path); err != nil {
			return nil, err
		}
	}
	result := &appSetupResult{ClientID: appID, Status: "complete", Steps: []setupStep{}}
	for _, request := range plan {
		step := setupStep{Action: request.Action, Capability: request.Capability, GrantID: request.GrantID, Status: "applied"}
		var value struct {
			GrantID string `json:"grant_id"`
		}
		if err := deps.call(ctx, request, &value); err != nil {
			step.Status, step.ErrorCode, result.Status = "failed_state_unknown", loginErrorCode(err), "incomplete"
			result.Steps = append(result.Steps, step)
			return result, &cliError{Code: "PARTIAL_FAILURE", Message: "配置中止；已完成项不会回滚，失败项可能已提交。", Hint: "运行 sjtu permissions list --app " + appID + " 和 sjtu auth status 核对；若连接交付失败，用 apps rotate 交付新连接后再配置。"}
		}
		if value.GrantID != "" {
			step.GrantID = value.GrantID
		}
		if request.Action == "app_create" {
			step.Connection = request.Path
		}
		result.Steps = append(result.Steps, step)
	}
	return result, nil
}

func setupGrant(ctx context.Context, p setupPrompt, call func(context.Context, broker.AdminRequest, any) error, credential setupCredential, capability contracts.Capability) (broker.AdminRequest, error) {
	q := broker.AdminRequest{CredentialID: credential.ID, AccountRef: credential.AccountRef, Capability: capability.ID}
	var schema struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Const       string   `json:"const"`
			Maximum     int      `json:"maximum"`
			Description string   `json:"description"`
			Pattern     string   `json:"pattern"`
		} `json:"properties"`
	}
	if json.Unmarshal(capability.InputSchema, &schema) != nil || len(schema.Properties["view"].Enum) == 0 || schema.Properties["limit"].Maximum < 1 {
		return q, contracts.Fail("INTERNAL_ERROR", "Capability setup schema is unavailable")
	}
	for key, value := range schema.Properties {
		if value.Const != "" {
			value.Enum = []string{value.Const}
			schema.Properties[key] = value
		}
	}
	fmt.Fprintln(p.diag, "配置："+capability.Title+" · "+capability.ID)
	if credential.Provider == "canvas" {
		var courses []struct {
			Ref   string `json:"course_ref"`
			Title string `json:"title"`
		}
		if err := call(ctx, broker.AdminRequest{Action: "courses", CredentialID: credential.ID}, &courses); err != nil {
			return q, err
		}
		if len(courses) == 0 {
			return q, contracts.Fail("NOT_FOUND", "No selectable courses were returned")
		}
		labels := []string{}
		for _, course := range courses {
			labels = append(labels, course.Title+" · "+course.Ref)
		}
		selected, err := p.choose("选择课程（多选，英文逗号分隔）：", labels, true, false)
		if err != nil {
			return q, err
		}
		for _, i := range selected {
			q.Courses = append(q.Courses, courses[i].Ref)
		}
	} else {
		resource := schema.Properties["resource_ref"]
		refs := resource.Enum
		if credential.Provider == "electric" || credential.Provider == "shuiyuan" {
			var resources struct {
				AccountRef string   `json:"account_ref"`
				Refs       []string `json:"resource_refs"`
			}
			if err := call(ctx, broker.AdminRequest{Action: "resources", CredentialID: credential.ID}, &resources); err != nil {
				return q, err
			}
			if resources.AccountRef != credential.AccountRef || len(resources.Refs) == 0 {
				return q, contracts.Fail("NOT_FOUND", "No matching account resources were returned")
			}
			refs = resources.Refs
		}
		if len(refs) > 0 {
			selected, err := p.choose("选择资源（多选，英文逗号分隔）：", refs, true, false)
			if err != nil {
				return q, err
			}
			for _, i := range selected {
				q.Resources = append(q.Resources, refs[i])
			}
		} else {
			fmt.Fprintf(p.diag, "%s\n资源格式：%s\n", resource.Description, resource.Pattern)
			v, err := p.line("输入精确 resource_ref（多个用英文逗号分隔）：")
			if err != nil {
				return q, err
			}
			for _, ref := range strings.Split(v, ",") {
				q.Resources = append(q.Resources, strings.TrimSpace(ref))
			}
		}
	}
	views := schema.Properties["view"].Enum
	q.View = views[0]
	if len(views) > 1 {
		selected, err := p.choose("选择固定字段视图：", views, false, false)
		if err != nil {
			return q, err
		}
		q.View = views[selected[0]]
	} else {
		fmt.Fprintln(p.diag, "固定视图："+q.View)
	}
	maximum := schema.Properties["limit"].Maximum
	q.Limit = min(20, maximum)
	v, err := p.line(fmt.Sprintf("每次调用条数上限 1-%d（留空 %d）：", maximum, q.Limit))
	if err != nil {
		return q, err
	}
	if v != "" {
		q.Limit, err = strconv.Atoi(v)
	}
	valid := err == nil && q.Limit >= 1 && q.Limit <= maximum
	if q.Capability == contracts.AssignmentsCapability {
		valid = valid && len(q.Courses) > 0 && len(q.Courses) <= 100
		for _, ref := range q.Courses {
			valid = valid && contracts.CanvasCourseRef.MatchString(ref)
		}
	} else if rules, ok := contracts.CanvasRules(q.Capability); ok {
		valid = valid && rules.ValidGrant(q.View, q.Limit, q.Courses)
	} else if rules, ok := contracts.GrantRules(q.Capability); ok {
		valid = valid && rules.ValidGrant(q.View, q.Limit, q.Resources)
	} else {
		valid = false
	}
	if !valid {
		return q, contracts.Fail("INVALID_ARGUMENT", "Selected resources or per-call limit do not match the capability contract; setup was not applied")
	}
	return q, nil
}
