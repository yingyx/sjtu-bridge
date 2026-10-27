package contracts

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	GradesCapability            = "learning.grades.list"
	GradesComponentsCapability  = "learning.grades.components"
	ExamsCapability             = "learning.exams.list"
	AttendanceRecordsCapability = "learning.attendance.records"
	AttendanceStatusCapability  = "learning.attendance.status"
	CardBalanceCapability       = "life.card.balance"
	CardTransactionsCapability  = "life.card.transactions"
)

// StudentRecord is a union at the provider boundary. The Engine must rebuild
// the selected fixed view; a provider cannot expose another view's fields.
type StudentRecord struct {
	Grade                string `json:"grade,omitempty"`
	Credits              string `json:"credits,omitempty"`
	GradePoint           string `json:"grade_point,omitempty"`
	WeightedGradePoint   string `json:"weighted_grade_point,omitempty"`
	CourseNature         string `json:"course_nature,omitempty"`
	RecordedAt           string `json:"recorded_at,omitempty"`
	ComponentName        string `json:"component_name,omitempty"`
	ComponentGrade       string `json:"component_grade,omitempty"`
	ExamName             string `json:"exam_name,omitempty"`
	ExamTime             string `json:"exam_time,omitempty"`
	ExamMethod           string `json:"exam_method,omitempty"`
	Makeup               string `json:"makeup,omitempty"`
	Status               string `json:"status,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	RollcallNumber       string `json:"rollcall_number,omitempty"`
	Active               *bool  `json:"active,omitempty"`
	BalanceCNY           string `json:"balance_cny,omitempty"`
	TransitionBalanceCNY string `json:"transition_balance_cny,omitempty"`
	LostStatus           string `json:"lost_status,omitempty"`
	FrozenStatus         string `json:"frozen_status,omitempty"`
	AmountCNY            string `json:"amount_cny,omitempty"`
	OccurredAt           string `json:"occurred_at,omitempty"`
	Merchant             string `json:"merchant,omitempty"`
	System               string `json:"system,omitempty"`
}

var attendanceCourse = regexp.MustCompile(`^mlearning:course:[1-9][0-9]{0,15}$`)
var gradeCourse = regexp.MustCompile(`^teaching:grade-course:20[0-9]{2}:[12]:[A-Za-z0-9._()+-]{1,64}$`)
var studentMoney = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,2})?$`)

func IsStudentRecordCapability(id string) bool {
	switch id {
	case GradesCapability, GradesComponentsCapability, ExamsCapability, AttendanceRecordsCapability, AttendanceStatusCapability, CardBalanceCapability, CardTransactionsCapability:
		return true
	}
	return false
}

func StudentRecordCapabilityIDs() []string {
	return []string{GradesCapability, GradesComponentsCapability, ExamsCapability, AttendanceRecordsCapability, AttendanceStatusCapability, CardBalanceCapability, CardTransactionsCapability}
}

func StudentRecordRules(id string) (CampusReadRules, bool) {
	r := CampusReadRules{capability: id, MaxResources: 8, MaxLimit: 100, Paginated: true}
	switch id {
	case GradesCapability:
		r.View, r.Coverage = "grades_v1", "own_term_grades_only"
	case GradesComponentsCapability:
		r.View, r.Coverage = "grade_components_v1", "own_exact_course_grade_components_only"
	case ExamsCapability:
		r.View, r.Coverage = "exams_v1", "own_term_exams_only"
	case AttendanceRecordsCapability:
		r.View, r.Coverage = "attendance_records_v1", "own_course_attendance_records_only"
	case AttendanceStatusCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "attendance_status_v1", "own_course_rollcall_status_only", 1, false
	case CardBalanceCapability:
		r.View, r.Coverage, r.MaxLimit, r.MaxResources, r.Paginated = "card_balance_v1", "own_card_balance_only", 1, 1, false
	case CardTransactionsCapability:
		r.View, r.Coverage, r.MaxResources = "card_transactions_v1", "own_card_recent_30_days_only", 1
	default:
		return CampusReadRules{}, false
	}
	return r, true
}

func ValidStudentRecordResource(id, ref string) bool {
	switch id {
	case GradesCapability, ExamsCapability:
		return semesterRef.MatchString(ref)
	case GradesComponentsCapability:
		return gradeCourse.MatchString(ref)
	case AttendanceRecordsCapability, AttendanceStatusCapability:
		return attendanceCourse.MatchString(ref)
	case CardBalanceCapability, CardTransactionsCapability:
		return ref == "card:self"
	}
	return false
}

func addStudentRecordInput(id string, props map[string]any, required *[]string) {
	resource := props["resource_ref"].(map[string]any)
	switch id {
	case GradesCapability, ExamsCapability:
		resource["pattern"] = semesterRef.String()
	case GradesComponentsCapability:
		resource["pattern"] = gradeCourse.String()
	case AttendanceRecordsCapability, AttendanceStatusCapability:
		resource["pattern"] = attendanceCourse.String()
	case CardBalanceCapability, CardTransactionsCapability:
		resource["enum"] = []string{"card:self"}
	}
}

func studentRecordFields(id string) (fields, required []string) {
	switch id {
	case GradesCapability:
		return []string{"grade", "credits", "grade_point", "weighted_grade_point", "course_nature", "recorded_at"}, []string{"credits"}
	case GradesComponentsCapability:
		return []string{"component_name", "component_grade", "grade", "credits"}, []string{"component_name"}
	case ExamsCapability:
		return []string{"exam_name", "exam_time", "exam_method", "makeup", "credits"}, []string{"exam_time"}
	case AttendanceRecordsCapability:
		return []string{"status", "updated_at", "rollcall_number"}, []string{"status", "rollcall_number"}
	case AttendanceStatusCapability:
		return []string{"active"}, []string{"active"}
	case CardBalanceCapability:
		return []string{"balance_cny", "transition_balance_cny", "lost_status", "frozen_status"}, []string{"balance_cny"}
	case CardTransactionsCapability:
		return []string{"amount_cny", "balance_cny", "occurred_at", "merchant", "system"}, []string{"amount_cny", "balance_cny", "occurred_at"}
	}
	return nil, nil
}

func StudentRecordSchema(id string) map[string]any {
	fields, required := studentRecordFields(id)
	props := map[string]any{}
	for _, field := range fields {
		s := map[string]any{"type": "string", "minLength": 1, "maxLength": 2048}
		if strings.HasSuffix(field, "_cny") {
			s["pattern"] = studentMoney.String()
			s["maxLength"] = 16
		}
		if field == "active" {
			s = map[string]any{"type": "boolean"}
		}
		props[field] = s
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

func addStudentRecordOutput(id string, props map[string]any, required *[]string, str map[string]any) {
	props["student"] = StudentRecordSchema(id)
	*required = append(*required, "student")
	if id == GradesCapability || id == GradesComponentsCapability || id == ExamsCapability {
		props["course_code"] = str
	}
	if id == ExamsCapability {
		props["location"] = str
		props["campus"] = str
	}
}

func ValidStudentRecordText(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsAny(s, "\x00\r\n")
}

func StudentRecordMetadata() []Capability {
	type definition struct{ id, title, provider, category, group, service, description string }
	defs := []definition{
		{GradesCapability, "本人学期成绩", "teaching", "learning", "exams", "教学信息服务网", "读取本人指定学期课程成绩、学分和源站给定绩点，不触发GPA排名统计或推导毕业资格。"},
		{GradesComponentsCapability, "本人课程分项成绩", "teaching", "learning", "exams", "教学信息服务网", "读取明确获准学年、学期和课程号的项目成绩与原始权重标签；与总评成绩独立授权，不自行重算正式总评。资源例 teaching:grade-course:2026:1:MA101，课程号取本人总评的course_code。"},
		{ExamsCapability, "本人考试安排", "teaching", "learning", "exams", "教学信息服务网", "读取本人指定学期已公布考试时间及考场；未排考不证明无考试资格。"},
		{AttendanceRecordsCapability, "本人课程考勤记录", "mlearning", "learning", "courses", "课堂互动", "读取指定获准课程的本人点名记录；不执行签到、补签或获取他人记录。"},
		{AttendanceStatusCapability, "当前课程点名状态", "mlearning", "learning", "courses", "课堂互动", "读取指定获准课程是否存在当前点名；不返回签到票据，不轮询或执行签到。"},
		{CardBalanceCapability, "本人校园卡余额", "campuscard", "life", "dining", "校园卡", "读取本人校园卡余额与源站提供的挂失/冻结状态，不含银行卡、充值或账户安全操作。"},
		{CardTransactionsCapability, "本人校园卡最近消费", "campuscard", "life", "dining", "校园卡", "读取官网当前返回的最近30天交易，金额保持十进制字符串；不声称覆盖任意历史。"},
	}
	out := make([]Capability, 0, len(defs))
	for _, d := range defs {
		categoryLabel, groupLabel := "学习与教学", "考试与成绩"
		if d.category == "life" {
			categoryLabel, groupLabel = "校园生活与健康", "餐饮与校园支付"
		}
		if d.group == "courses" {
			groupLabel = "日常课程学习"
		}
		out = append(out, Capability{ID: d.id, ContractVersion: "1", Title: d.title, Category: d.category, CategoryLabel: categoryLabel, TaskGroup: d.group, TaskGroupLabel: groupLabel, Service: d.service, Aliases: []string{d.title}, Keywords: []string{d.service, d.title}, Description: d.description, DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: d.provider})
	}
	return out
}
