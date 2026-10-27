package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/providers/qrlogin"
)

type portalLoginResult struct {
	Provider       string `json:"provider"`
	Status         string `json:"status"`
	AccountRef     string `json:"account_ref,omitempty"`
	CredentialID   string `json:"credential_id,omitempty"`
	StateCommitted bool   `json:"state_committed,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Message        string `json:"message,omitempty"`
	Hint           string `json:"hint,omitempty"`
}

func loginErrorCode(err error) string {
	var e *contracts.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "CHECK_FAILED"
}

func recordPortalLoginFailure(result *portalLoginResult, err error, importing bool) {
	result.ErrorCode = loginErrorCode(err)
	result.Message = "门户操作失败，未提供可公开的详细原因"
	var e *contracts.Error
	if errors.As(err, &e) {
		result.Message = e.Message
	}
	if !importing {
		result.Status = "verification_failed"
		result.Hint = "修复该门户的核验错误后，可用 --providers 单独重试"
		return
	}
	result.Status = "import_failed"
	result.Hint = "先运行 sjtu auth status 核对该门户的凭据状态，再决定是否重试"
	if result.ErrorCode == "AUDIT_UNAVAILABLE" {
		// Broker import reports this only after its state transaction commits.
		result.Status = "imported_audit_failed"
		result.StateCommitted = true
		result.Message = "凭据已导入，但审计记录写入失败"
		result.Hint = "运行 sjtu auth status 核对已保存凭据；修复审计存储后再继续，无需直接重复导入"
	}
}

func importVerifiedPortals(ctx context.Context, results []portalLoginResult, secret []byte, source, label string, call func(context.Context, broker.AdminRequest, any) error) {
	for i := range results {
		r := &results[i]
		if r.Status != "verified" {
			continue
		}
		var value struct {
			ID string `json:"credential_id"`
		}
		err := call(ctx, broker.AdminRequest{Action: "import", Provider: r.Provider, Secret: secret, SourceCredentialID: source, ExpectedAccount: r.AccountRef, Label: label}, &value)
		if err != nil {
			recordPortalLoginFailure(r, err, true)
			continue
		}
		r.Status, r.CredentialID, r.StateCommitted = "imported", value.ID, true
	}
}

func loginResultsError(results []portalLoginResult) error {
	failed, committed := 0, false
	code := ""
	for _, result := range results {
		committed = committed || result.StateCommitted
		if result.Status == "imported" {
			continue
		}
		failed++
		if code == "" {
			code = result.ErrorCode
		} else if code != result.ErrorCode {
			code = "LOGIN_FAILED"
		}
	}
	if failed == 0 {
		return nil
	}
	if committed {
		return &cliError{Code: "PARTIAL_FAILURE", Message: "部分门户操作未完成；已提交的凭据状态见逐门户结果", Hint: "先运行 sjtu auth status 核对状态；仅对需要重试的门户使用 --providers"}
	}
	if code == "" {
		code = "LOGIN_FAILED"
	}
	return &cliError{Code: code, Message: "所选门户未完成导入；具体原因见逐门户结果", Hint: "按每个门户的 message 和 hint 处理；导入阶段失败时先运行 sjtu auth status 核对状态"}
}

func reuseLoginTargets(raw []byte, source string, providers []string, explicit bool) ([]string, error) {
	var state struct {
		Credentials map[string]struct {
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			Active   bool   `json:"active"`
		}
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid Broker credential metadata")
	}
	c := state.Credentials[source]
	if !c.Active || c.Kind != "JAAuthCookie" || !campusCredentialProvider(c.Provider) {
		return nil, contracts.Fail("AUTH_REQUIRED", "Active campus source credential required")
	}
	if !explicit {
		if c.Provider == "activity" || c.Provider == "dekt" || c.Provider == "electric" || c.Provider == "bath" {
			return []string{"teaching", "sjtumail"}, nil
		}
		if c.Provider == "teaching" {
			return []string{"sjtumail"}, nil
		}
		return []string{"teaching"}, nil
	}
	for _, p := range providers {
		if p == c.Provider {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Select a target portal different from the source credential")
		}
	}
	return providers, nil
}

func campusLoginCommand(out, diag io.Writer, input io.Reader, jsonOutput, verbose *bool) *cobra.Command {
	providers := []string{"teaching", "sjtumail"}
	label := "main"
	sourceCredential := ""
	c := &cobra.Command{Use: "login", Short: "通过 jAccount 扫码，分别核验并导入所选校园门户账号", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if len(providers) == 0 || len(providers) > 13 || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(label) {
			return contracts.Fail("INVALID_ARGUMENT", "Select supported campus portals and a valid local account label")
		}
		seen := map[string]bool{}
		for _, p := range providers {
			if !campusCredentialProvider(p) || seen[p] {
				return contracts.Fail("INVALID_ARGUMENT", "Select teaching, sjtumail, activity, dekt, electric, bath, pan, mlearning, campuscard, calendar, myportal, meeting or transcribe once each")
			}
			seen[p] = true
		}
		if e := auth.RequireQRDisplay(input, diag); e != nil {
			return e
		}
		// Fail before asking the user to scan if the local Broker is unavailable.
		var status json.RawMessage
		if e := broker.OwnerCall(cmd.Context(), broker.AdminRequest{Action: "status"}, &status); e != nil {
			return e
		}
		if sourceCredential != "" {
			var e error
			providers, e = reuseLoginTargets(status, sourceCredential, providers, cmd.Flags().Changed("providers"))
			if e != nil {
				clear(status)
				return e
			}
		}
		clear(status)
		service, close := qrlogin.New()
		defer close()
		var secret []byte
		var e error
		if sourceCredential == "" {
			fmt.Fprintln(diag, "请用交我办扫描官方 jAccount 二维码并确认登录；二维码会自动刷新，三分钟后结束。")
			secret, e = service.Login(cmd.Context(), func(value string) error { return auth.DisplayQR(input, diag, value) })
		} else {
			fmt.Fprintln(diag, "由 Broker 内部复用已保存的校园凭据；目标门户仍须独立核验账号，不自动授予应用权限。")
		}
		if e != nil {
			d := qrlogin.Diagnose(e)
			fmt.Fprintf(diag, "登录失败阶段：%s；端点：%s；步骤：%d；HTTP：%d；回调：%s/%s\n", d.Stage, d.Endpoint, d.Hop, d.Status, d.RedirectTarget, d.RedirectShape)
			return e
		}
		defer clear(secret)
		results := make([]portalLoginResult, len(providers))
		verified := []map[string]string{}
		for i, p := range providers {
			results[i] = portalLoginResult{Provider: p, Status: "verification_failed"}
			var v map[string]string
			if e := broker.OwnerCall(cmd.Context(), broker.AdminRequest{Action: "verify", Provider: p, Secret: secret, SourceCredentialID: sourceCredential}, &v); e != nil {
				recordPortalLoginFailure(&results[i], e, false)
				continue
			}
			if v["account_ref"] == "" {
				recordPortalLoginFailure(&results[i], contracts.Fail("UPSTREAM_CHANGED", "门户核验结果缺少稳定账号标识"), false)
				continue
			}
			results[i].Status = "verified"
			results[i].AccountRef = v["account_ref"]
			verified = append(verified, map[string]string{"provider": p, "account_ref": v["account_ref"], "label": label})
		}
		if len(verified) == 0 {
			if e := writeResult(out, diag, results, *jsonOutput, *verbose); e != nil {
				return e
			}
			return loginResultsError(results)
		}
		for _, result := range results {
			if result.Status == "verification_failed" {
				fmt.Fprintf(diag, "%s 核验失败 [%s]：%s\n", result.Provider, result.ErrorCode, result.Message)
			}
		}
		if e := confirm(input, diag, verified); e != nil {
			return e
		}
		importVerifiedPortals(cmd.Context(), results, secret, sourceCredential, label, broker.OwnerCall)
		if e := writeResult(out, diag, results, *jsonOutput, *verbose); e != nil {
			return e
		}
		return loginResultsError(results)
	}}
	c.Flags().StringSliceVar(&providers, "providers", providers, "待独立核验的门户：teaching,sjtumail,activity,dekt,electric,bath,pan,mlearning,campuscard,calendar,myportal,meeting,transcribe；默认前两项")
	c.Flags().StringVar(&label, "account", "main", "本地账号标签；同标签重导入撤销旧凭据及其授权")
	c.Flags().StringVar(&sourceCredential, "from-credential", "", "由 Broker 内部复用另一门户的已保存凭据 ID；仍需核验并确认目标账号")
	return c
}

func campusCredentialProvider(provider string) bool {
	switch provider {
	case "teaching", "sjtumail", "activity", "dekt", "electric", "bath", "pan", "mlearning", "campuscard", "calendar", "myportal", "meeting", "transcribe":
		return true
	default:
		return false
	}
}
