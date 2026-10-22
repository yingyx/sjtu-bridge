package contracts

import (
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

const TimetableCapability = "learning.timetable.list"
const MailCapability = "digital.mail.list"
const ActivityCapability = "development.activities.list"
const ActivityTypesCapability = "development.activities.types"
const ActivityCalendarCapability = "development.activities.calendar"
const ActivityCountsCapability = "development.activities.counts"
const ActivityRegistrationsCapability = "development.activities.registrations"
const ActivityCollectionsCapability = "development.activities.collections"
const SecondClassHonorsCapability = "development.secondclass.honors"
const SecondClassPracticeCapability = "development.secondclass.practice"
const SecondClassEducationCapability = "development.secondclass.education"
const ElectricityBalanceCapability = "life.electricity.balance"

// ReadScope is internal authorization context, never caller-reported identity.
type ReadScope struct {
	Capability, AccountRef, ResourceRef, View string
	Limit                                     int
}
type CampusQuery struct {
	Department     string   `json:"department,omitempty"`
	Language       string   `json:"language,omitempty"`
	Category       string   `json:"category,omitempty"`
	TargetYear     string   `json:"target_year,omitempty"`
	Credit         string   `json:"credit,omitempty"`
	HasReview      string   `json:"has_review,omitempty"`
	OrderBy        string   `json:"order_by,omitempty"`
	Ascend         string   `json:"ascend,omitempty"`
	TeacherTitle   string   `json:"teacher_title,omitempty"`
	Semester       string   `json:"semester,omitempty"`
	Rating         string   `json:"rating,omitempty"`
	Period         string   `json:"period,omitempty"`
	AccountRef     string   `json:"account_ref"`
	ResourceRef    string   `json:"resource_ref"`
	View           string   `json:"view"`
	Limit          int      `json:"limit"`
	Query          string   `json:"query,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	ActivityTypeID string   `json:"activity_type_id,omitempty"`
	ChildTypeIDs   []string `json:"child_type_ids,omitempty"`
	States         []string `json:"states,omitempty"`
	Date           string   `json:"date,omitempty"`
	Begin          string   `json:"begin,omitempty"`
	End            string   `json:"end,omitempty"`
	Month          string   `json:"month,omitempty"`
}

func (q CampusQuery) Scope(id string) ReadScope {
	return ReadScope{id, q.AccountRef, q.ResourceRef, q.View, q.Limit}
}

var semesterRef = regexp.MustCompile(`^teaching:term:20[0-9]{2}:[12]$`)
var electricRoomRef = regexp.MustCompile(`^electric:room:[1-9][0-9]{0,17}$`)

func ValidCampusResource(id, ref string) bool {
	switch id {
	case TimetableCapability:
		return semesterRef.MatchString(ref)
	case MailCapability:
		return ref == "mail:folder:inbox"
	case ActivityCapability:
		return ref == "activity:feed:visible"
	case ActivityTypesCapability:
		return ref == "activity:types:visible"
	case ActivityCalendarCapability:
		return ref == "activity:calendar:visible"
	case ActivityCountsCapability:
		return ref == "activity:counts:visible"
	case ActivityRegistrationsCapability:
		return ref == "activity:registrations:self"
	case ActivityCollectionsCapability:
		return ref == "activity:collections:self"
	case SecondClassHonorsCapability:
		return ref == "dekt:honors:self"
	case SecondClassPracticeCapability:
		return ref == "dekt:practice:self"
	case SecondClassEducationCapability:
		return ref == "dekt:education:self"
	case ElectricityBalanceCapability, ElectricityHistoryCapability:
		return electricRoomRef.MatchString(ref)
	case BathBindingCapability, BathDevicesCapability, BathUsageCapability:
		return ref == "bath:self"
	default:
		return jcourseResource(id, ref)
	}
}
func PersonalMetadata(id string) (Capability, bool) {
	for _, c := range catalog {
		if c.ID == id && c.DataClass == PersonalData {
			return CloneCapability(c), true
		}
	}
	return Capability{}, false
}

// CampusInputSchema describes reviewed input shape without granting access.
func CampusInputSchema(id string) json.RawMessage {
	input, ok := campusInputDefinition(id)
	if !ok {
		return nil
	}
	raw, _ := json.Marshal(input)
	return raw
}

func campusInputDefinition(id string) (map[string]any, bool) {
	rules, ok := CampusRules(id)
	if !ok {
		return nil, false
	}
	resource := map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "获准资源；JSON 字段 resource_ref；从 capabilities describe 获取"}
	switch id {
	case TimetableCapability:
		resource["pattern"] = semesterRef.String()
	case MailCapability:
		resource["enum"] = []string{"mail:folder:inbox"}
	case ActivityCapability:
		resource["enum"] = []string{"activity:feed:visible"}
	case ActivityTypesCapability:
		resource["enum"] = []string{"activity:types:visible"}
	case ActivityCalendarCapability:
		resource["enum"] = []string{"activity:calendar:visible"}
	case ActivityCountsCapability:
		resource["enum"] = []string{"activity:counts:visible"}
	case ActivityRegistrationsCapability:
		resource["enum"] = []string{"activity:registrations:self"}
	case ActivityCollectionsCapability:
		resource["enum"] = []string{"activity:collections:self"}
	case SecondClassHonorsCapability:
		resource["enum"] = []string{"dekt:honors:self"}
	case SecondClassPracticeCapability:
		resource["enum"] = []string{"dekt:practice:self"}
	case SecondClassEducationCapability:
		resource["enum"] = []string{"dekt:education:self"}
	case ElectricityBalanceCapability, ElectricityHistoryCapability:
		resource["pattern"] = electricRoomRef.String()
	}
	if id == BathBindingCapability || id == BathDevicesCapability || id == BathUsageCapability {
		resource["enum"] = []string{"bath:self"}
	}
	if op := JCourseOperation(id); op != "" {
		switch op {
		case "course", "reviews", "review_filters", "review_trend":
			resource["pattern"] = jcourseCourse.String()
		case "teacher", "teacher_courses":
			resource["pattern"] = jcourseTeacher.String()
		default:
			ref := "jcourse:courses:catalog"
			switch op {
			case "teachers", "teacher_filters":
				ref = "jcourse:teachers:catalog"
			case "enrolled", "followed", "ignored":
				ref = "jcourse:" + op + ":self"
			}
			resource["enum"] = []string{ref}
		}
	}
	props := map[string]any{
		"account_ref":  map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "获准门户账号；JSON 字段 account_ref；从 capabilities describe 获取"},
		"resource_ref": resource,
		"view":         map[string]any{"type": "string", "enum": []string{rules.View}, "description": "本能力的固定视图"},
		"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": rules.MaxLimit, "description": "每页条数，须在应用授权上限内"},
		"cursor":       map[string]any{"type": "string", "maxLength": 64, "description": "上一页的 next_cursor；保持其他参数相同"},
	}
	if rules.QueryAllowed {
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "邮件标题关键词，空白分词且须全部命中；不支持查询语法或正文搜索"}
		if IsActivityCapability(id) {
			props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "源站活动搜索文本；无首尾空白，不接受查询语法，不检索活动详情"}
		}
	}
	if !rules.Paginated {
		props["cursor"] = map[string]any{"type": "string", "enum": []string{""}, "description": "此能力不分页，请省略"}
	}
	required := []string{"account_ref", "resource_ref", "view", "limit"}
	addActivityInput(id, props, &required)
	addUtilityInput(id, props, &required)
	if JCourseOperation(id) != "" {
		addJCourseInput(id, props)
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}, true
}

func CampusCapability(id, account string, resources []string, view string, limit int) (Capability, bool) {
	c, ok := PersonalMetadata(id)
	rules, known := CampusRules(id)
	if !ok || !known || !utf8.ValidString(account) || account == "" || len(account) > 128 || !rules.ValidGrant(view, limit, resources) {
		return Capability{}, false
	}
	input, _ := campusInputDefinition(id)
	props := input["properties"].(map[string]any)
	props["account_ref"].(map[string]any)["enum"] = []string{account}
	props["resource_ref"].(map[string]any)["enum"] = resources
	props["view"].(map[string]any)["enum"] = []string{view}
	props["limit"].(map[string]any)["maximum"] = limit
	c.InputSchema, _ = json.Marshal(input)
	str := map[string]any{"type": "string", "maxLength": 2048}
	rowProps := map[string]any{"resource_ref": map[string]any{"type": "string", "enum": resources}, "record_ref": str, "title": str}
	required := []string{"resource_ref", "record_ref", "title"}
	coverage := rules.Coverage
	if id == TimetableCapability {
		for _, key := range []string{"course_code", "location", "campus", "weekday", "periods", "weeks"} {
			rowProps[key] = str
		}
		required = append(required, "weekday", "periods", "weeks")
	} else if id == MailCapability {
		rowProps["sender"], rowProps["sent_at"] = str, str
		required = append(required, "sent_at")
	} else if IsActivityCapability(id) {
		addActivityOutput(id, rowProps, &required, str)
	} else if IsSecondClassCapability(id) {
		addSecondClassOutput(id, rowProps, &required, str)
	} else if JCourseOperation(id) != "" {
		addJCourseOutput(id, rowProps, &required)
	} else if id != ElectricityBalanceCapability {
		addUtilityOutput(id, rowProps, &required, str)
	} else {
		amount := map[string]any{"type": "string", "maxLength": 64, "pattern": `^-?[0-9]+(\.[0-9]+)?$`}
		rowProps["remaining_kwh"], rowProps["subsidy_kwh"] = amount, amount
		required = append(required, "remaining_kwh", "subsidy_kwh")
	}
	object := func(p map[string]any, r []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": r}
	}
	metaProps := map[string]any{"provider": map[string]any{"const": c.Provider}, "coverage": map[string]any{"const": coverage}, "fetched_at": str, "next_cursor": map[string]any{"type": []string{"string", "null"}, "maxLength": 64}, "has_more": map[string]any{"type": "boolean"}}
	if IsSecondClassCapability(id) {
		modules := SecondClassModules(id)
		metaProps["unavailable_modules"] = map[string]any{"type": "array", "minItems": 1, "maxItems": len(modules), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": modules}, "description": "源站未明确提供的模块；这些模块不应解释为空记录"}
	}
	meta := object(metaProps, []string{"provider", "coverage", "fetched_at", "next_cursor", "has_more"})
	c.OutputSchema, _ = json.Marshal(object(map[string]any{"schema_version": map[string]any{"const": "1"}, "request_id": str, "data": object(map[string]any{"items": map[string]any{"type": "array", "maxItems": limit, "items": object(rowProps, required)}}, []string{"items"}), "meta": meta}, []string{"schema_version", "request_id", "data", "meta"}))
	c.Authorized = true
	return c, true
}

type CampusRecord struct {
	Community       *CommunityRecord `json:"community,omitempty"`
	UsedKWh         string           `json:"used_kwh,omitempty"`
	Available       *bool            `json:"available,omitempty"`
	Bound           *bool            `json:"bound,omitempty"`
	BuildingRef     string           `json:"building_ref,omitempty"`
	AvailableCount  *int             `json:"available_count,omitempty"`
	InUseCount      *int             `json:"in_use_count,omitempty"`
	AmountCNY       string           `json:"amount_cny,omitempty"`
	ResourceRef     string           `json:"resource_ref"`
	Ref             string           `json:"record_ref"`
	Title           string           `json:"title"`
	CourseCode      string           `json:"course_code,omitempty"`
	Location        string           `json:"location,omitempty"`
	Campus          string           `json:"campus,omitempty"`
	Weekday         string           `json:"weekday,omitempty"`
	Periods         string           `json:"periods,omitempty"`
	Weeks           string           `json:"weeks,omitempty"`
	Sender          string           `json:"sender,omitempty"`
	SentAt          string           `json:"sent_at,omitempty"`
	ActivityTime    []string         `json:"activity_time,omitempty"`
	RemainingKWh    string           `json:"remaining_kwh,omitempty"`
	SubsidyKWh      string           `json:"subsidy_kwh,omitempty"`
	ParentRef       string           `json:"parent_ref,omitempty"`
	ActivityTypeID  string           `json:"activity_type_id,omitempty"`
	CalendarEnabled *bool            `json:"calendar_enabled,omitempty"`
	Sponsor         string           `json:"sponsor,omitempty"`
	Status          string           `json:"status,omitempty"`
	Date            string           `json:"date,omitempty"`
	Count           *int             `json:"count,omitempty"`
	Module          string           `json:"module,omitempty"`
	Organization    string           `json:"organization,omitempty"`
	AwardedAt       string           `json:"awarded_at,omitempty"`
	Level           string           `json:"level,omitempty"`
	Role            string           `json:"role,omitempty"`
	StartedAt       string           `json:"started_at,omitempty"`
	EndedAt         string           `json:"ended_at,omitempty"`
	Duration        string           `json:"duration,omitempty"`
	DurationUnit    string           `json:"duration_unit,omitempty"`
	DataSource      string           `json:"data_source,omitempty"`
}
type CampusResponse struct {
	SchemaVersion string     `json:"schema_version"`
	RequestID     string     `json:"request_id"`
	Data          CampusData `json:"data"`
	Meta          PageMeta   `json:"meta"`
}
type CampusData struct {
	Items []CampusRecord `json:"items"`
}
