package cli

import (
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
	Provider     string `json:"provider"`
	Status       string `json:"status"`
	AccountRef   string `json:"account_ref,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

func loginErrorCode(err error) string {
	var e *contracts.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "CHECK_FAILED"
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
	if !c.Active || c.Kind != "JAAuthCookie" || (c.Provider != "teaching" && c.Provider != "sjtumail") {
		return nil, contracts.Fail("AUTH_REQUIRED", "Active campus source credential required")
	}
	if !explicit {
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

func campusLoginCommand(out, diag io.Writer, input io.Reader) *cobra.Command {
	providers := []string{"teaching", "sjtumail"}
	label := "main"
	sourceCredential := ""
	c := &cobra.Command{Use: "login", Short: "通过 jAccount 扫码，一次登录并分别导入教务、邮箱账号", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if len(providers) == 0 || len(providers) > 2 || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(label) {
			return contracts.Fail("INVALID_ARGUMENT", "Select teaching and/or sjtumail and a valid local account label")
		}
		seen := map[string]bool{}
		for _, p := range providers {
			if (p != "teaching" && p != "sjtumail") || seen[p] {
				return contracts.Fail("INVALID_ARGUMENT", "Select teaching and/or sjtumail once each")
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
				results[i].ErrorCode = loginErrorCode(e)
				fmt.Fprintln(diag, p, "账号核验未通过：", results[i].ErrorCode)
				continue
			}
			if v["account_ref"] == "" {
				results[i].ErrorCode = "UPSTREAM_CHANGED"
				continue
			}
			results[i].Status = "verified"
			results[i].AccountRef = v["account_ref"]
			verified = append(verified, map[string]string{"provider": p, "account_ref": v["account_ref"], "label": label})
		}
		if len(verified) == 0 {
			if e := writeJSON(out, results); e != nil {
				return e
			}
			return contracts.Fail("AUTH_REQUIRED", "Campus session did not verify any selected portal; no credential was imported")
		}
		if e := confirm(input, diag, verified); e != nil {
			return e
		}
		failed := false
		for i, r := range results {
			if r.Status != "verified" {
				failed = true
				continue
			}
			var v struct {
				ID string `json:"credential_id"`
			}
			e := broker.OwnerCall(cmd.Context(), broker.AdminRequest{Action: "import", Provider: r.Provider, Secret: secret, SourceCredentialID: sourceCredential, ExpectedAccount: r.AccountRef, Label: label}, &v)
			if e != nil {
				results[i].Status = "import_failed"
				results[i].ErrorCode = loginErrorCode(e)
				failed = true
				continue
			}
			results[i].Status = "imported"
			results[i].CredentialID = v.ID
		}
		if e := writeJSON(out, results); e != nil {
			return e
		}
		if failed {
			return contracts.Fail("AUTH_REQUIRED", "Some selected portals were not imported; inspect per-portal status before retrying")
		}
		return nil
	}}
	c.Flags().StringSliceVar(&providers, "providers", providers, "待独立核验的门户：teaching,sjtumail；默认两项")
	c.Flags().StringVar(&label, "account", "main", "本地账号标签；同标签重导入撤销旧凭据及其授权")
	c.Flags().StringVar(&sourceCredential, "from-credential", "", "由 Broker 内部复用另一门户的已保存凭据 ID；仍需核验并确认目标账号")
	return c
}
