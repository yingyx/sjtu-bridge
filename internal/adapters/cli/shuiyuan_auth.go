package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/broker"
)

var shuiyuanLabel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var shuiyuanIdentity = regexp.MustCompile(`^shuiyuan:user:[1-9][0-9]{0,17}$`)

func shuiyuanAuthorizeCommand(out, diag io.Writer, input io.Reader, jsonOutput, verbose *bool) *cobra.Command {
	provider, label := "", "main"
	command := &cobra.Command{Use: "authorize", Short: "由用户在水源官网授权只读 User API Key，隐藏粘贴加密结果后核验并导入", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if provider != "shuiyuan" || !shuiyuanLabel.MatchString(label) {
			return contracts.Fail("INVALID_ARGUMENT", "Use --provider shuiyuan and a valid local account label")
		}
		if err := requireShuiyuanAuthorizationTerminal(input, out, diag); err != nil {
			return err
		}
		// Fail before generating the browser authorization request if the local
		// owner connection is unavailable. Status never contains credential values.
		var status json.RawMessage
		if err := broker.OwnerCall(cmd.Context(), broker.AdminRequest{Action: "status"}, &status); err != nil {
			clear(status)
			return err
		}
		clear(status)
		session, err := auth.NewDiscourseAuthorization()
		if err != nil {
			return err
		}
		defer session.Close()
		if _, err := fmt.Fprintf(diag, "在浏览器打开以下水源官网链接，核对账号与 read 权限并人工授权；十分钟内回到此终端。关闭命令后需重新开始。\n%s\n", session.AuthorizationURL()); err != nil {
			return contracts.Fail("AUTH_REQUIRED", "Cannot display local authorization request")
		}
		payload, err := auth.ReadDiscoursePayload(input, diag)
		if err != nil {
			return err
		}
		defer clear(payload)
		if cmd.Context().Err() != nil {
			return contracts.Fail("CANCELED", "Authorization canceled")
		}
		secret, err := session.Consume(payload)
		clear(payload)
		if err != nil {
			return err
		}
		defer clear(secret)
		result, err := importShuiyuanAuthorization(cmd.Context(), secret, label, broker.OwnerCall, func(preview any) error { return confirm(input, diag, preview) })
		if err != nil {
			return err
		}
		return writeResult(out, diag, result, *jsonOutput, *verbose)
	}}
	command.Flags().StringVar(&provider, "provider", "", "授权服务：shuiyuan（仅 read）")
	command.Flags().StringVar(&label, "account", "main", "本地账号标签；不是论坛账号或授权标识")
	return command
}

func requireShuiyuanAuthorizationTerminal(input io.Reader, outputs ...io.Writer) error {
	if err := auth.RequireTerminal(input); err != nil {
		return err
	}
	for _, output := range outputs {
		file, ok := output.(*os.File)
		if !ok || auth.RequireTerminal(file) != nil {
			return contracts.Fail("AUTH_REQUIRED", "Authorization requires real terminal input and display; stdout and stderr cannot be redirected")
		}
	}
	return nil
}

type shuiyuanAuthorizationResult struct {
	Status       string `json:"status"`
	Provider     string `json:"provider"`
	AccountRef   string `json:"account_ref"`
	CredentialID string `json:"credential_id"`
}

// The only transfer from the local handshake to the Broker is the plain key.
// Identity comes from upstream verification, never browser labels or payloads.
func importShuiyuanAuthorization(ctx context.Context, secret []byte, label string, owner func(context.Context, broker.AdminRequest, any) error, approve func(any) error) (shuiyuanAuthorizationResult, error) {
	var result shuiyuanAuthorizationResult
	q := broker.AdminRequest{Action: "verify", Provider: "shuiyuan", Secret: secret}
	var verified struct {
		AccountRef string `json:"account_ref"`
	}
	if err := owner(ctx, q, &verified); err != nil {
		return result, err
	}
	if !shuiyuanIdentity.MatchString(verified.AccountRef) {
		return result, contracts.Fail("UPSTREAM_CHANGED", "Verified forum identity is missing or invalid")
	}
	preview := map[string]string{"action": "import", "provider": "shuiyuan", "account_ref": verified.AccountRef, "label": label, "scope": "read"}
	if err := approve(preview); err != nil {
		return result, err
	}
	q.Action, q.ExpectedAccount, q.Label = "import", verified.AccountRef, label
	var imported struct {
		CredentialID string `json:"credential_id"`
	}
	if err := owner(ctx, q, &imported); err != nil {
		return result, err
	}
	if imported.CredentialID == "" {
		return result, contracts.Fail("UPSTREAM_CHANGED", "Import receipt is incomplete; inspect auth status before retrying")
	}
	return shuiyuanAuthorizationResult{Status: "imported", Provider: "shuiyuan", AccountRef: verified.AccountRef, CredentialID: imported.CredentialID}, nil
}
