package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// JSON results retain the business contract, including all coverage and status
// fields. Text is a presentation of that contract, not a different query.
func writeResult(out, diagnostics io.Writer, value any, asJSON, verbose bool) error {
	if asJSON {
		if verbose {
			return writePrettyJSON(out, value)
		}
		return writeJSON(out, value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	var meta map[string]json.RawMessage
	_ = json.Unmarshal(object["meta"], &meta)
	var text, notes strings.Builder
	if _, public := meta["sources"]; public && object["data"] != nil {
		var response contracts.Response
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		renderPublic(&text, &notes, response, verbose)
	} else {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return err
		}
		if object["data"] != nil && meta != nil {
			renderPrivate(&text, decoded.(map[string]any), verbose)
		} else {
			renderValue(&text, decoded, "")
		}
	}
	if _, err := io.WriteString(out, terminalText(text.String())); err != nil {
		return err
	}
	if notes.Len() > 0 {
		_, err = io.WriteString(diagnostics, terminalText(notes.String()))
	}
	return err
}

func writePrettyJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func renderPublic(out, diagnostics *strings.Builder, r contracts.Response, verbose bool) {
	for i, item := range r.Data.Items {
		if i > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintln(out, item.Title)
		if item.SourceID != "" {
			fmt.Fprintf(out, "  ID：%s\n", item.SourceID)
		}
		if item.PublishedDate != nil {
			fmt.Fprintf(out, "  日期：%s\n", *item.PublishedDate)
		}
		if item.URL != "" {
			fmt.Fprintf(out, "  %s\n", item.URL)
		}
		for _, field := range item.Fields {
			fmt.Fprintf(out, "  %s：%s\n", field.Label, field.Value)
		}
		if content := item.Content; content != nil {
			if content.Text != "" {
				fmt.Fprintln(out, content.Text)
			}
			for _, link := range content.Links {
				fmt.Fprintf(out, "  %s（%s）：%s\n", link.Title, link.Kind, link.URL)
			}
			if content.Truncated {
				fmt.Fprintln(out, "  正文已截断。")
			}
		}
		if n := item.Navigation; n != nil {
			fmt.Fprintf(out, "  官网入口：%s\n  认证：%s\n", n.Purpose, n.Upstream.Authentication)
			if len(n.RelatedCapabilities) > 0 {
				fmt.Fprintf(out, "  相关能力：%s\n", strings.Join(n.RelatedCapabilities, "、"))
			}
			if verbose {
				fmt.Fprintf(out, "  未接入说明：%s（%s）\n  官网操作：%s / %s；影响=%v；可恢复性=%s\n  依据核对：%s；本次链接检查：%t\n", n.Upstream.Reason, n.Upstream.ReasonCode, n.Upstream.Operation, n.Upstream.ExecutionRule, n.Upstream.Effects, n.Upstream.Reversibility, n.VerifiedAt, n.LinkCheckedThisCall)
				for _, source := range n.Sources {
					fmt.Fprintf(out, "  入口依据：%s %s\n", source.Title, source.URL)
				}
			}
		}
	}
	state := "声明范围内完整"
	if r.Meta.Completeness != "complete" {
		state = "部分范围"
	}
	if r.Meta.Stale {
		state += "；含过期或时效未确认的数据"
	}
	fmt.Fprintf(out, "\n本页 %d 条 · %s", len(r.Data.Items), state)
	if len(r.Data.Items) == 0 {
		out.WriteString(" · 当前查询无匹配结果")
	}
	if r.Meta.HasMore {
		out.WriteString(" · 还有下一页")
	} else {
		out.WriteString(" · 无后续页")
	}
	out.WriteByte('\n')
	if !r.Meta.FetchedAt.IsZero() {
		fmt.Fprintf(out, "读取时间：%s\n", r.Meta.FetchedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	seen := map[string]bool{}
	for _, source := range r.Meta.Sources {
		key := source.Provider + "/" + source.Coverage + "/" + source.Status
		if verbose {
			key += "/" + source.URL
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(out, "来源：%s · %s · %s\n", source.Provider, source.Coverage, source.Status)
		if verbose {
			fmt.Fprintf(out, "  %s\n", source.URL)
		}
	}
	for _, warning := range r.Warnings {
		// Stable scope information belongs with the result. Actual degradation
		// remains conspicuous on stderr; unknown future warnings are never hidden.
		switch warning.Code {
		case "NAVIGATION_ONLY":
			if verbose {
				fmt.Fprintf(out, "范围：%s\n", warning.Message)
			} else {
				fmt.Fprintln(out, "范围：官网导航，需前往官网办理；本次未检查链接。")
			}
		case "BUNDLED_DIRECTORY":
			if verbose {
				fmt.Fprintf(out, "范围：%s\n", warning.Message)
			} else {
				fmt.Fprintln(out, "范围：内置网站目录，非实时查询。")
			}
		case "SOURCE_SCOPE", "RECENT_FEED_ONLY", "MEAL_WINDOW_ONLY", "CROWD_SCOPE":
			fmt.Fprintf(out, "范围：%s\n", warning.Message)
		default:
			fmt.Fprintf(diagnostics, "注意 [%s]：%s\n", warning.Code, warning.Message)
		}
	}
	renderCursor(out, r.Meta.NextCursor)
	if verbose && r.RequestID != "" {
		fmt.Fprintf(out, "请求：%s\n", r.RequestID)
	}
}

func renderCursor(out *strings.Builder, cursor *string) {
	if cursor != nil {
		fmt.Fprintf(out, "下一页：保持其他查询参数及 --limit 不变，添加 --cursor。\nnext_cursor: %s\n", *cursor)
	}
}

func renderPrivate(out *strings.Builder, response map[string]any, verbose bool) {
	data, _ := response["data"].(map[string]any)
	items, _ := data["items"].([]any)
	for i, item := range items {
		if i > 0 {
			out.WriteByte('\n')
		}
		renderValue(out, item, "")
	}
	meta, _ := response["meta"].(map[string]any)
	unavailable, _ := meta["unavailable_modules"].([]any)
	fmt.Fprintf(out, "\n本页 %d 条", len(items))
	if len(items) == 0 {
		if len(unavailable) > 0 {
			out.WriteString(" · 已提供模块无匹配记录")
		} else {
			out.WriteString(" · 当前查询无匹配结果")
		}
	}
	if meta["has_more"] == true {
		out.WriteString(" · 还有下一页")
	} else {
		out.WriteString(" · 无后续页")
	}
	out.WriteByte('\n')
	if len(unavailable) > 0 {
		modules := make([]string, 0, len(unavailable))
		for _, module := range unavailable {
			modules = append(modules, fmt.Sprint(module))
		}
		fmt.Fprintf(out, "部分覆盖：源站未提供模块 %s；以上结果不包含这些模块。\n", strings.Join(modules, ", "))
	}
	for _, key := range []string{"provider", "coverage", "fetched_at"} {
		if value, ok := meta[key]; ok {
			fmt.Fprintf(out, "%s：%v\n", outputLabel(key), value)
		}
	}
	if cursor, ok := meta["next_cursor"].(string); ok {
		renderCursor(out, &cursor)
	}
	if verbose && response["request_id"] != nil {
		fmt.Fprintf(out, "请求：%v\n", response["request_id"])
	}
}

func renderValue(out *strings.Builder, value any, indent string) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if title, ok := v["title"].(string); ok {
			fmt.Fprintln(out, indent+title)
		}
		for _, key := range keys {
			if key == "title" {
				continue
			}
			switch child := v[key].(type) {
			case map[string]any, []any:
				fmt.Fprintf(out, "%s%s：\n", indent, outputLabel(key))
				renderValue(out, child, indent+"  ")
			default:
				text := "未提供"
				if child != nil {
					text = fmt.Sprint(child)
				}
				fmt.Fprintf(out, "%s%s：%s\n", indent, outputLabel(key), text)
			}
		}
	case []any:
		if len(v) == 0 {
			fmt.Fprintln(out, indent+"无记录")
		}
		for i, item := range v {
			if i > 0 {
				out.WriteByte('\n')
			}
			renderValue(out, item, indent)
		}
	default:
		if v == nil {
			fmt.Fprintln(out, indent+"未提供")
		} else {
			fmt.Fprintln(out, indent+fmt.Sprint(v))
		}
	}
}

func outputLabel(key string) string {
	labels := map[string]string{
		"assignment_ref": "作业 ID", "course_ref": "课程", "due_at": "截止时间", "resource_ref": "资源", "record_ref": "记录 ID",
		"course_code": "课程代码", "location": "地点", "campus": "校区", "weekday": "星期", "periods": "节次", "weeks": "周次",
		"sender": "发件人", "sent_at": "发送时间", "activity_time": "活动时间", "remaining_kwh": "自购剩余电量 (kWh)", "subsidy_kwh": "补贴剩余电量 (kWh)",
		"provider": "来源", "coverage": "范围", "fetched_at": "读取时间", "status": "状态", "account_ref": "账号", "credential_id": "凭据 ID",
		"error_code": "错误码", "message": "说明", "hint": "下一步", "state_committed": "状态已提交",
		"parent_ref": "父分类", "activity_type_id": "活动类型 ID", "calendar_enabled": "支持日历", "sponsor": "主办方", "date": "日期", "count": "数量",
		"used_kwh": "日用电量 (kWh)", "available": "有读数", "bound": "已绑定楼栋", "building_ref": "楼栋引用", "available_count": "空闲设备数", "in_use_count": "使用中设备数", "amount_cny": "消费金额（元）",
		"community": "课程社区数据", "course": "课程", "teacher": "教师", "teacher_ref": "教师引用", "review": "点评", "review_ref": "点评引用", "filter": "筛选值", "trend": "评分趋势", "name": "名称", "credit": "学分", "department": "院系", "language": "语言", "target_years": "适用年级", "categories": "类别", "last_semester": "最近开课学期", "main_teacher": "主讲教师", "teacher_group": "教师组", "offerings": "开课批次", "related_code_refs": "同代码课程引用", "related_teacher_refs": "相关教师课程引用", "rating": "社区星级", "average": "平均星级", "score": "源站加权评分", "distribution": "1至5星分布", "semester": "学期", "content": "社区文本", "content_truncated": "文本已截断", "like_count": "赞数", "dislike_count": "踩数", "created_at": "创建时间", "value": "取值", "hot_score": "热榜活跃度", "period": "榜单周期", "period_key": "榜单时间标识",
		"module": "记录模块", "organization": "组织", "awarded_at": "获奖时间", "level": "源级别/类型", "role": "角色", "started_at": "开始时间", "ended_at": "结束时间", "duration": "时长", "duration_unit": "时长单位", "data_source": "记录来源",
		"ref": "对象 ID", "code": "代码", "body": "正文", "truncated": "已截断", "links": "关联入口", "state": "状态", "kind": "类型", "content_ref": "内容 ID", "position": "顺序", "completed": "已完成", "locked": "已锁定/关闭", "size": "大小（字节）", "content_type": "内容类型", "unlock_at": "开放时间", "lock_at": "锁定时间", "posted_at": "发布时间", "updated_at": "更新时间", "start_at": "开始时间", "end_at": "结束时间", "submitted_at": "提交时间", "attempt": "提交次数", "late": "迟交", "missing": "缺交",
	}
	if label, ok := labels[key]; ok {
		return label
	}
	return key
}

func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '\uFFFD'
		}
		return r
	}, s)
}
