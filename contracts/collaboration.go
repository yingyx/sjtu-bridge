package contracts

import (
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	TranscriptionListCapability = "digital.ai.transcriptions.list"
	TranscriptionGetCapability  = "digital.ai.transcriptions.get"
	CalendarListCapability      = "digital.calendar.calendars"
	CalendarEventsCapability    = "digital.calendar.events"
	MessageUnreadCapability     = "digital.messages.unread"
	MessageGroupsCapability     = "digital.messages.groups"
	PortalTasksCapability       = "administration.tasks.list"
	MeetingListCapability       = "digital.meetings.list"
	MeetingDetailsCapability    = "digital.meetings.get"
	MeetingCalendarCapability   = "digital.meetings.calendar"
	MeetingBusyCapability       = "digital.meetings.busy"
)

// CollaborationRecord is a fixed projection, never a raw portal response.
// Identifiers only refer to objects of the currently authenticated account.
type CollaborationRecord struct {
	Kind               string `json:"kind"`
	OriginalText       string `json:"original_text,omitempty"`
	OptimizedText      string `json:"optimized_text,omitempty"`
	OriginalTruncated  bool   `json:"original_truncated,omitempty"`
	OptimizedTruncated bool   `json:"optimized_truncated,omitempty"`
	Description        string `json:"description,omitempty"`
	StartAt            string `json:"start_at,omitempty"`
	EndAt              string `json:"end_at,omitempty"`
	Status             string `json:"status,omitempty"`
	CalendarRef        string `json:"calendar_ref,omitempty"`
	GroupRef           string `json:"group_ref,omitempty"`
	ProcessRef         string `json:"process_ref,omitempty"`
	Count              *int   `json:"count,omitempty"`
	AllDay             *bool  `json:"all_day,omitempty"`
	DurationMinutes    *int   `json:"duration_minutes,omitempty"`
	JoinURL            string `json:"join_url,omitempty"`
	MeetingCode        string `json:"meeting_code,omitempty"`
	Passcode           string `json:"passcode,omitempty"`
}

var collaborationObjectID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)
var meetingID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var collaborationAccount = regexp.MustCompile(`^(myportal|calendar):account:[a-zA-Z0-9_.-]{1,64}$`)
var meetingAccount = regexp.MustCompile(`^meeting:account:[a-zA-Z0-9_.@-]{1,112}$`)

func ValidCollaborationAccount(provider, account string) bool {
	if provider == "transcribe" {
		return strings.HasPrefix(account, "transcribe:account:") && collaborationAccount.MatchString("myportal:account:"+strings.TrimPrefix(account, "transcribe:account:"))
	}
	if provider == "meeting" {
		return meetingAccount.MatchString(account)
	}
	return (provider == "myportal" || provider == "calendar") && strings.HasPrefix(account, provider+":account:") && collaborationAccount.MatchString(account)
}

func CollaborationCapabilityIDs() []string {
	return []string{TranscriptionListCapability, TranscriptionGetCapability, CalendarListCapability, CalendarEventsCapability, MessageUnreadCapability, MessageGroupsCapability, PortalTasksCapability, MeetingListCapability, MeetingDetailsCapability, MeetingCalendarCapability, MeetingBusyCapability}
}
func IsCollaborationCapability(id string) bool { _, ok := CollaborationRules(id); return ok }
func CollaborationRules(id string) (CampusReadRules, bool) {
	r := CampusReadRules{capability: id, MaxLimit: 50, MaxResources: 8, Paginated: true}
	switch id {
	case TranscriptionListCapability:
		r.View, r.Coverage = "transcriptions_v1", "own_visible_transcription_metadata_only"
	case TranscriptionGetCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "transcription_v1", "selected_own_transcription_text_64kib_per_part", 1, false
	case CalendarListCapability:
		r.View, r.Coverage = "calendars_v1", "visible_calendar_metadata_only"
	case CalendarEventsCapability:
		r.View, r.Coverage = "events_v1", "authorized_calendar_range_only"
	case MessageUnreadCapability:
		r.View, r.Coverage, r.MaxLimit, r.MaxResources, r.Paginated = "unread_v1", "own_message_unread_count_only", 1, 1, false
	case MessageGroupsCapability:
		r.View, r.Coverage = "groups_v1", "own_message_groups_only"
	case PortalTasksCapability:
		r.View, r.Coverage = "tasks_v1", "own_pending_process_steps_only"
	case MeetingListCapability:
		r.View, r.Coverage, r.QueryAllowed = "meetings_v1", "own_meeting_summaries_only", true
	case MeetingDetailsCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "meeting_details_v1", "own_meeting_details_only", 1, false
	case MeetingCalendarCapability:
		r.View, r.Coverage = "meeting_calendar_v1", "own_meetings_in_date_range_only"
	case MeetingBusyCapability:
		r.View, r.Coverage = "busy_v1", "selected_meeting_group_half_hour_availability_only"
	default:
		return CampusReadRules{}, false
	}
	return r, true
}

func ValidCollaborationResource(id, ref string) bool {
	switch id {
	case TranscriptionListCapability:
		return ref == "transcribe:self"
	case TranscriptionGetCapability:
		return strings.HasPrefix(ref, "transcribe:task:") && collaborationObjectID.MatchString(strings.TrimPrefix(ref, "transcribe:task:"))
	case CalendarListCapability:
		return ref == "calendar:visible"
	case CalendarEventsCapability:
		return strings.HasPrefix(ref, "calendar:calendar:") && collaborationObjectID.MatchString(strings.TrimPrefix(ref, "calendar:calendar:"))
	case MessageUnreadCapability:
		return ref == "myportal:messages:unread"
	case MessageGroupsCapability:
		return ref == "myportal:messages:groups"
	case PortalTasksCapability:
		return ref == "myportal:tasks:pending"
	case MeetingListCapability, MeetingCalendarCapability:
		return ref == "meeting:self"
	case MeetingDetailsCapability:
		return strings.HasPrefix(ref, "meeting:record:") && meetingID.MatchString(strings.TrimPrefix(ref, "meeting:record:"))
	case MeetingBusyCapability:
		return strings.HasPrefix(ref, "meeting:group:") && meetingID.MatchString(strings.TrimPrefix(ref, "meeting:group:"))
	}
	return false
}

func collaborationField(id, key string) bool {
	switch key {
	case "begin", "end":
		return id == CalendarEventsCapability || id == MeetingListCapability || id == MeetingCalendarCapability
	case "date":
		return id == MeetingBusyCapability
	}
	return false
}
func validCollaborationFilters(id string, q CampusQuery) bool {
	if !IsCollaborationCapability(id) {
		return true
	}
	fixed := CampusQuery{AccountRef: q.AccountRef, ResourceRef: q.ResourceRef, View: q.View, Limit: q.Limit, Cursor: q.Cursor}
	if id == MeetingListCapability {
		fixed.Query = q.Query
	}
	if collaborationField(id, "begin") {
		fixed.Begin, fixed.End = q.Begin, q.End
		if !ValidDateRange(q.Begin, q.End, 31) {
			return false
		}
	}
	if id == MeetingBusyCapability {
		fixed.Date = q.Date
		if !ValidActivityDate(q.Date) {
			return false
		}
	}
	return reflect.DeepEqual(fixed, q)
}
func ValidateCollaborationQuery(id string, q CampusQuery) error {
	r, ok := CollaborationRules(id)
	provider := "myportal"
	if id == TranscriptionListCapability || id == TranscriptionGetCapability {
		provider = "transcribe"
	}
	if id == CalendarListCapability || id == CalendarEventsCapability {
		provider = "calendar"
	}
	if strings.HasPrefix(id, "digital.meetings.") {
		provider = "meeting"
	}
	if !ok || !ValidCollaborationAccount(provider, q.AccountRef) || !r.ValidViewLimit(q.View, q.Limit) || !ValidCollaborationResource(id, q.ResourceRef) || !validCollaborationFilters(id, q) {
		return Fail("INVALID_ARGUMENT", "Expected a bounded collaboration query in the selected fixed view")
	}
	if !utf8.ValidString(q.Query) || len(q.Query) > 128 || strings.IndexFunc(q.Query, unicode.IsControl) >= 0 {
		return Fail("INVALID_ARGUMENT", "Invalid collaboration search text")
	}
	return nil
}

func addCollaborationInput(id string, props map[string]any, required []string) []string {
	if !IsCollaborationCapability(id) {
		return required
	}
	resource := map[string]any{"type": "string"}
	switch id {
	case TranscriptionListCapability:
		resource["const"] = "transcribe:self"
	case TranscriptionGetCapability:
		resource["pattern"] = `^transcribe:task:[a-zA-Z0-9_-]{1,96}$`
	case CalendarListCapability:
		resource["const"] = "calendar:visible"
	case CalendarEventsCapability:
		resource["pattern"] = `^calendar:calendar:[a-zA-Z0-9_-]{1,96}$`
	case MessageUnreadCapability:
		resource["const"] = "myportal:messages:unread"
	case MessageGroupsCapability:
		resource["const"] = "myportal:messages:groups"
	case PortalTasksCapability:
		resource["const"] = "myportal:tasks:pending"
	case MeetingListCapability, MeetingCalendarCapability:
		resource["const"] = "meeting:self"
	case MeetingDetailsCapability:
		resource["pattern"] = `^meeting:record:[1-9][0-9]{0,17}$`
	case MeetingBusyCapability:
		resource["pattern"] = `^meeting:group:[1-9][0-9]{0,17}$`
	}
	props["resource_ref"] = resource
	if id == MeetingListCapability {
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "本人会议搜索文本；不查询他人会议或参与者"}
	}
	for _, key := range []string{"begin", "end", "date"} {
		if collaborationField(id, key) {
			props[key] = map[string]any{"type": "string", "format": "date", "description": "上海时区日期 YYYY-MM-DD，区间最多31日"}
			required = append(required, key)
		}
	}
	return required
}

func CollaborationRecordSchema(id string) map[string]any {
	text := func(n int) map[string]any { return map[string]any{"type": "string", "maxLength": n} }
	p := map[string]any{"kind": text(40)}
	var fields []string
	switch id {
	case TranscriptionListCapability, TranscriptionGetCapability:
		fields = []string{"status", "start_at"}
		if id == TranscriptionGetCapability {
			p["original_text"] = text(65536)
			p["optimized_text"] = text(65536)
			p["original_truncated"] = map[string]any{"type": "boolean"}
			p["optimized_truncated"] = map[string]any{"type": "boolean"}
		}
	case CalendarListCapability:
		fields = []string{"calendar_ref"}
	case CalendarEventsCapability:
		fields = []string{"calendar_ref", "start_at", "end_at", "description"}
		p["all_day"] = map[string]any{"type": "boolean"}
	case MessageUnreadCapability, MessageGroupsCapability:
		p["count"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000}
		if id == MessageGroupsCapability {
			fields = []string{"group_ref", "description"}
		}
	case PortalTasksCapability:
		fields = []string{"process_ref", "status", "start_at", "description"}
	case MeetingListCapability, MeetingCalendarCapability:
		fields = []string{"start_at", "status", "group_ref"}
		p["duration_minutes"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1440}
	case MeetingDetailsCapability:
		fields = []string{"start_at", "status", "group_ref", "join_url", "meeting_code", "passcode", "description"}
		p["duration_minutes"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1440}
	case MeetingBusyCapability:
		fields = []string{"start_at", "status", "group_ref"}
	}
	for _, key := range fields {
		p[key] = text(2048)
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": []string{"kind"}}
}
func addCollaborationOutput(id string, props map[string]any, required []string) []string {
	if IsCollaborationCapability(id) {
		props["collaboration"] = CollaborationRecordSchema(id)
		if id == CalendarEventsCapability {
			props["location"] = map[string]any{"type": "string", "maxLength": 2048}
		}
		required = append(required, "collaboration")
	}
	return required
}

func CollaborationMetadata() []Capability {
	items := []struct{ id, title, provider, category, group, description string }{
		{TranscriptionListCapability, "查询本人校园AI转写任务", "transcribe", "digital", "software", "只读取本人未删除的转写任务名称、状态和创建时间；不读取转写正文，不发起或取消任务。"},
		{TranscriptionGetCapability, "查询本人校园AI转写结果", "transcribe", "digital", "software", "只读取明确获准的本人任务原始转写与优化文本，每部分最多64KiB并明确标记截断；不调用模型或下载音视频。"},
		{CalendarListCapability, "查询本人可见日历", "calendar", "digital", "communication", "读取本人日历和学校共享日历名称与引用；不读取事件、成员或修改订阅。"},
		{CalendarEventsCapability, "查询获准日历事件", "calendar", "digital", "communication", "按获准日历和最多31日区间读取日程；不扩大到其他日历，不生成提醒或修改事件。"},
		{MessageUnreadCapability, "查询交我办未读消息数", "myportal", "digital", "communication", "只读取本人全局未读数；不读取消息正文，不标记已读。"},
		{MessageGroupsCapability, "查询交我办消息分组", "myportal", "digital", "communication", "读取本人消息分组与未读数；不调用有标记已读副作用的messagelist。"},
		{PortalTasksCapability, "查询交我办本人待办", "myportal", "administration", "services", "读取本人待办步骤与流程摘要；不代办、不读取任意表单或推断已办历史。"},
		{MeetingListCapability, "查询本人视频会议", "meeting", "digital", "communication", "读取本人会议摘要；按最多31日范围分页，不能据此读取其他人的会议信息。"},
		{MeetingDetailsCapability, "查询本人视频会议详情", "meeting", "digital", "communication", "读取明确获准且出现在本人会议列表中的会议详情与入会信息；不发送通知、不修改会议。"},
		{MeetingCalendarCapability, "查询本人会议日历", "meeting", "digital", "communication", "按最多31日区间读取本人会议日历；不转发共享会议日历中的其他人记录。"},
		{MeetingBusyCapability, "查询获准视频会议组占用", "meeting", "digital", "communication", "读取指定获准会议组半小时占用状态，不包含参会者或其他会议详情；不轮询抢占。"},
	}
	out := make([]Capability, 0, len(items))
	for _, i := range items {
		cl := "账号、网络与数字工具"
		gl := "通信与协作"
		if i.group == "software" {
			gl = "软件与生产力工具"
		}
		if i.category == "administration" {
			cl = "行政与财务"
			gl = "综合办事与文书"
		}
		out = append(out, Capability{ID: i.id, ContractVersion: "1", Title: i.title, Provider: i.provider, Category: i.category, CategoryLabel: cl, TaskGroup: i.group, TaskGroupLabel: gl, Service: map[string]string{"transcribe": "校园AI转写", "calendar": "校园个人日历", "myportal": "交我办", "meeting": "云视频会议"}[i.provider], Aliases: []string{i.title}, Keywords: []string{i.provider}, Description: i.description, DataClass: PersonalData, ExecutionRule: GrantExecution, Operation: Read, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}})
	}
	return out
}
