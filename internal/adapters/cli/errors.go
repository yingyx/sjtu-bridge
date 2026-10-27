package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/yingyx/sjtu-bridge/contracts"
)

type inputIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// cliError adds actionable presentation details without changing Broker errors.
// Fields and hints are built from trusted command/schema metadata, not values.
type cliError struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Fields    []inputIssue `json:"fields,omitempty"`
	Hint      string       `json:"hint,omitempty"`
	Retryable bool         `json:"retryable"`
	RequestID string       `json:"request_id,omitempty"`
}

func (e *cliError) Error() string { return e.Code + ": " + e.Message }

func configureCommandErrors(cmd *cobra.Command) {
	cmd.SuggestionsMinimumDistance = 2
	if cmd.Run == nil && cmd.RunE == nil && cmd.HasSubCommands() {
		// Cobra's default group behavior silently accepts unknown child names.
		cmd.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
		cmd.Args = func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			e := &cliError{Code: "INVALID_ARGUMENT", Message: "无法识别命令或子命令。", Hint: c.CommandPath() + " --help"}
			if len(args[0]) <= 64 {
				if suggestions := c.SuggestionsFor(args[0]); len(suggestions) > 0 {
					e.Message += " 可能需要：" + strings.Join(suggestions, "、") + "。"
				}
			}
			return e
		}
	} else if original := cmd.Args; original != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if original(c, args) == nil {
				return nil
			}
			return &cliError{Code: "INVALID_ARGUMENT", Message: "位置参数数量不正确。用法：" + c.UseLine(), Hint: c.CommandPath() + " --help"}
		}
	}
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return flagError(c, err) })
	for _, child := range cmd.Commands() {
		configureCommandErrors(child)
	}
}

func flagError(cmd *cobra.Command, err error) *cliError {
	e := &cliError{Code: "INVALID_ARGUMENT", Message: "选项格式不正确。", Hint: cmd.CommandPath() + " --help"}
	var invalid *pflag.InvalidValueError
	var required *pflag.ValueRequiredError
	var unknown *pflag.NotExistError
	switch {
	case errors.As(err, &invalid):
		f := invalid.GetFlag()
		e.Message = "选项值的格式不正确。"
		e.Fields = []inputIssue{{Field: f.Name, Message: "--" + f.Name + " 需要 " + f.Value.Type() + " 类型。"}}
	case errors.As(err, &required):
		f := required.GetFlag()
		e.Message = "选项缺少值。"
		e.Fields = []inputIssue{{Field: f.Name, Message: "请提供 --" + f.Name + " 的值。"}}
	case errors.As(err, &unknown):
		e.Message = "未知选项。"
		name := unknown.GetSpecifiedName()
		var suggestions []string
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden && closeName(name, f.Name) {
				suggestions = append(suggestions, "--"+f.Name)
			}
		})
		if len(suggestions) > 0 {
			e.Message += " 可能需要：" + strings.Join(suggestions, "、") + "。"
		}
	}
	return e
}

// Bounded edit distance is used only to suggest known option names.
func closeName(a, b string) bool {
	if len(a) > 64 || len(a) < 2 || len(a)-len(b) > 2 || len(b)-len(a) > 2 {
		return false
	}
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)] <= 2
}

func presentError(cmd *cobra.Command, err error) *cliError {
	var local *cliError
	if errors.As(err, &local) {
		copy := *local
		if copy.Hint == "" {
			copy.Hint = cmd.CommandPath() + " --help"
		}
		return &copy
	}
	var typed *contracts.Error
	if errors.As(err, &typed) {
		e := &cliError{Code: typed.Code, Message: typed.Message, Retryable: typed.Retryable, RequestID: typed.RequestID}
		switch typed.Code {
		case "INVALID_ARGUMENT":
			e.Hint = cmd.CommandPath() + " --help"
			if strings.Contains(strings.ToLower(typed.Message), "cursor") {
				e.Hint = "移除 --cursor，从第一页重新查询；续页须保持相同过滤条件和 --limit。"
			}
		case "CLIENT_UNAUTHORIZED":
			e.Hint = "检查 --connection 指向的应用连接文件；由用户管理端核对应用是否已过期或撤销。"
		case "PERMISSION_DENIED":
			e.Hint = "使用相同 --connection 运行 sjtu capabilities list，核对当前可用能力与授权。"
			if strings.Contains(strings.ToLower(typed.Message), "cursor") {
				e.Hint = "移除 --cursor，从第一页重新查询；保持相同查询条件和 --limit。如仍被拒绝，使用相同 --connection 运行 sjtu capabilities list 核对授权。"
			}
		case "BROKER_UNAVAILABLE":
			e.Hint = "在另一终端运行 sjtu broker run。"
		case "WRITE_OUTCOME_UNKNOWN":
			e.Hint = "上游可能已完成写入；先在官网核对指定路径，不能直接重试。"
		case "CONFLICT":
			e.Hint = "目标已存在；本能力禁止覆盖，请核对路径。"
		case "CONTENT_UNAVAILABLE", "CONTENT_CHANGED":
			e.Hint = "使用同一应用连接，向原业务能力重新取得内容引用。"
		case "RESPONSE_INCOMPLETE":
			e.Hint = "此次未取得完整结果；核对来源和连接后重新请求，不将部分数据当作完整结果。"
		case "OUTPUT_UNAVAILABLE":
			e.Hint = "检查输出父目录是否可写，并指定尚不存在的文件路径。"
		case "OUTPUT_CLEANUP_FAILED":
			e.Hint = "本次输出可能不完整；检查并手动移除指定输出文件后再重试。"
		case "AUDIT_UNAVAILABLE":
			if isManagementCommand(cmd) && !strings.Contains(strings.ToLower(typed.Message), "private result withheld") {
				e.Hint = "管理操作可能已生效；先核对 sjtu auth status 和应用授权状态，再决定是否重试。"
			} else {
				e.Hint = "查询结果未发布；请先恢复 Broker 的审计存储，再重新查询。"
			}
		}
		return e
	}
	if errors.Is(err, context.Canceled) {
		return &cliError{Code: "CANCELED", Message: "操作已取消。"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &cliError{Code: "TIMEOUT", Message: "操作超时。", Retryable: true}
	}
	// Cobra's positional and required-flag errors are untyped. Only use known
	// flag names and suggestions; never print its raw argument-bearing message.
	if strings.HasPrefix(err.Error(), "required flag(s)") {
		var fields []inputIssue
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0 && !f.Changed {
				fields = append(fields, inputIssue{f.Name, "缺少 --" + f.Name})
			}
		})
		return &cliError{Code: "INVALID_ARGUMENT", Message: "缺少必填选项。", Fields: fields, Hint: cmd.CommandPath() + " --help"}
	}
	if strings.HasPrefix(err.Error(), "unknown command ") {
		e := &cliError{Code: "INVALID_ARGUMENT", Message: "无法识别命令或子命令。", Hint: cmd.CommandPath() + " --help"}
		quoted := strings.TrimPrefix(err.Error(), "unknown command ")
		if end := strings.Index(quoted, " for "); end >= 0 {
			if name, unquoteErr := strconv.Unquote(quoted[:end]); unquoteErr == nil && len(name) <= 64 {
				suggestions := cmd.SuggestionsFor(name)
				sort.Strings(suggestions)
				if len(suggestions) > 0 {
					e.Message += " 可能需要：" + strings.Join(suggestions, "、") + "。"
				}
			}
		}
		return e
	}
	return &cliError{Code: "INTERNAL_ERROR", Message: "命令执行失败，未返回完整结果。"}
}

func isManagementCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.GroupID == "management" {
			return true
		}
	}
	return false
}

// Find the explicit output switch even when flag parsing stopped at an earlier
// error. Values of known options and positional data after -- are not switches.
func jsonRequested(cmd *cobra.Command, args []string) bool {
	result := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if name == "json" {
			if !hasValue {
				result = true
			} else if v, err := strconv.ParseBool(value); err == nil {
				result = v
			}
			continue
		}
		f := cmd.Flags().Lookup(name)
		if f == nil {
			f = cmd.InheritedFlags().Lookup(name)
		}
		if f != nil && f.NoOptDefVal == "" && !hasValue {
			i++
		}
	}
	return result
}

func writeError(w io.Writer, e *cliError, asJSON bool) error {
	if asJSON {
		return writeJSON(w, map[string]any{"error": e})
	}
	var text strings.Builder
	fmt.Fprintf(&text, "错误 [%s]：%s\n", e.Code, e.Message)
	for _, field := range e.Fields {
		fmt.Fprintf(&text, "  %s\n", field.Message)
	}
	if e.Hint != "" {
		fmt.Fprintf(&text, "下一步：%s\n", e.Hint)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&text, "请求：%s\n", e.RequestID)
	}
	_, err := io.WriteString(w, text.String())
	return err
}
