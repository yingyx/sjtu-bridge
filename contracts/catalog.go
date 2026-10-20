package contracts

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Static task metadata is shared by all adapters. It conveys no authorization.
// Index positive tasks only: searching a limitation such as "grades are not
// returned" must not suggest a tool for reading grades.
var catalog = []Capability{
	{ID: TimetableCapability, ContractVersion: "1", Title: "查询个人学期课表", Category: "learning", CategoryLabel: "学习与教学", TaskGroup: "courses", TaskGroupLabel: "日常课程学习", CrossListings: []TaskPath{{"learning", "schedule"}}, Service: "教学信息服务网", Aliases: []string{"个人课表", "教务课表", "上课地点", "课程周次"}, Keywords: []string{"timetable", "schedule"}, Description: "通过 Broker 查询获准学期的常规课程课表，返回课程、地点、星期、节次及源站周次；不推算当前教学周，不包含成绩、实践教学列表或选课操作。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "teaching"},
	{ID: MailCapability, ContractVersion: "1", Title: "查询交大收件箱邮件标题", Category: "digital", CategoryLabel: "账号、网络与数字工具", TaskGroup: "communication", TaskGroupLabel: "通信与协作", Service: "交大邮箱", Aliases: []string{"邮箱", "邮件列表", "邮件标题", "邮件搜索", "收件箱"}, Keywords: []string{"email", "mail", "inbox"}, Description: "通过 Broker 查询获准收件箱的邮件标题、发件地址和时间；query 仅按标题字面关键词检索，不读取正文、摘要、附件或联系人，不标记已读、不发信。正文阅读仍需官网人工处理。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "sjtumail"},
	feedQuery("public.notices.search", "检索近期教务 RSS 摘要", "learning", "notices", "教务处", []string{"教务摘要", "教务搜索", "RSS", "近期教务通知"}, "读取教务处 RSS 的近期最多 50 条通知；可用 query 按空白分词、全部匹配标题与摘要。无匹配仅表示该 feed 未命中。与 public.notices.list 共享公告链接，后者用于归档索引翻页；不读取正文附件。"),
	feedQuery("life.dining.list", "查询食堂拥挤度概览", "life", "dining", "校园生活", []string{"食堂", "餐厅", "食堂拥挤度", "就餐"}, "读取校园生活食堂概览、食堂 ID 和源站指标。使用 ID 调用 life.dining.areas 查看分区；本调用不遍历分区。概览没有采样时间，零值或过期数据不能解释为空闲或营业保证。"),
	feedQuery("life.dining.areas", "查询食堂分区拥挤度", "life", "dining", "校园生活", []string{"食堂分区", "食堂楼层", "餐厅拥挤度"}, "用 life.dining.list 的 canteen_id 查询一座食堂的分区及最新采样；标注采样时间、缺失与过期状态。无采样、零值或关闭状态不能解释为空闲；不提供预测、人数或预约。"),
	{
		ID: "public.services.list", ContractVersion: "1",
		Title: "查找校园网站入口", Category: "campus", CategoryLabel: "校情与导览", TaskGroup: "directory", TaskGroupLabel: "机构与官方入口", Service: "校园网站目录",
		Aliases:     []string{"校园服务", "校园网站", "门户入口", "办事入口"},
		Keywords:    []string{"services", "portal", "website", "links", "directory"},
		Description: "查找教务处、教学信息服务网和 Canvas 的官方链接。仅为内置网站目录，不代表网站业务已接入，也不检查实时可用性。",
		DataClass:   PublicData, ExecutionRule: PublicExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Provider: "publicinfo",
		Operation: Read, Delivery: QueryDelivery, Credentials: []string{},
	},
	{
		ID: "public.calendars.list", ContractVersion: "1",
		Title: "查询校历索引", Category: "learning", CategoryLabel: "学习与教学", Service: "教务处",
		TaskGroup: "schedule", TaskGroupLabel: "教学安排与校历", Operation: Read, Delivery: QueryDelivery, Credentials: []string{},
		Aliases:     []string{"校历", "学年校历", "学期校历", "教学日历"},
		Keywords:    []string{"calendar", "academic", "semester"},
		Description: "查询教务处公开校历索引的标题、日期和官方链接。不解析假期日期或个人课表；每次仅返回一页，需按 next_cursor 继续查询。",
		DataClass:   PublicData, ExecutionRule: PublicExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Provider: "publicinfo",
	},
	{
		ID: "public.notices.list", ContractVersion: "1",
		Title: "查询教务公告", Category: "learning", CategoryLabel: "学习与教学", Service: "教务处",
		TaskGroup: "notices", TaskGroupLabel: "教学通知", Operation: Read, Delivery: QueryDelivery, Credentials: []string{},
		Aliases:     []string{"教务公告", "教务通知", "教学通知", "教学公告"},
		Keywords:    []string{"notices", "announcement", "teaching", "news"},
		Description: "查询教务处公开通知的标题、日期和官方链接。不包含正文或附件；每次仅返回一页，不能将一页解释为完整公告列表。",
		DataClass:   PublicData, ExecutionRule: PublicExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Provider: "publicinfo",
	},
	{
		ID: AssignmentsCapability, ContractVersion: "1",
		Title: "查询作业与截止时间", Category: "learning", CategoryLabel: "学习与教学", Service: "Canvas",
		TaskGroup: "courses", TaskGroupLabel: "日常课程学习", Operation: Read, Delivery: QueryDelivery, Credentials: []string{"CanvasToken"},
		Aliases:     []string{"作业", "查作业", "作业截止时间", "截止时间", "作业期限"},
		Keywords:    []string{"assignments", "homework", "deadline", "deadlines", "due", "DDL"},
		Description: "查询已授权 Canvas 课程的作业及个性化截止时间，仅返回获准的固定字段视图。每次查询一门课程的一页；不包含作业正文、成绩、提交状态或附件，不提供提交或修改操作。",
		DataClass:   PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Provider: "canvas",
	},
	navigation("learning.practice.navigate", "查询第二课堂修业核对入口", "learning", "practice", "第二课堂修业", []string{"劳动教育", "劳动学时", "二课达标", "第二课堂修业", "学时核对"}, []string{"labor", "recognition", "qualification"}, []TaskPath{{"development", "activities"}, {"development", "evaluation"}}),
	navigation("library.loans.navigate", "定位个人借阅记录入口", "library", "borrowing", "我的图书馆", []string{"我的借阅", "借阅到期", "个人借阅记录"}, []string{"loans", "due", "primo"}, []TaskPath{}),
	navigation("library.spaces.navigate", "查询图书馆自习空间使用入口", "library", "spaces", "图书馆空间", []string{"图书馆座位", "自习", "研讨室", "小组学习室"}, []string{"seat", "study", "room", "opening"}, []TaskPath{{"learning", "courses"}}),
	navigation("life.sports.navigate", "查询体育场馆预约入口", "life", "sports", "体育场馆", []string{"场馆预约", "运动场地", "体育馆", "订场"}, []string{"sports", "venue", "reservation"}, []TaskPath{{"learning", "practice"}}),
	navigation("development.activities.navigate", "查询第二课堂活动参与入口", "development", "activities", "第二课堂活动", []string{"第二课堂", "二课", "活动报名", "取消报名", "讲座报名", "志愿活动"}, []string{"activity", "participation", "lecture", "volunteer"}, []TaskPath{{"learning", "practice"}}),
	navigation("research.computing.navigate", "定位科研计算账号入口", "research", "computing", "交我算 HPC", []string{"超算", "科研计算", "交我算", "HPC", "计算子账号"}, []string{"cluster", "computing", "studio"}, []TaskPath{{"digital", "network"}}),
	navigation("administration.payments.navigate", "查询学生缴费与票据办理入口", "administration", "payments", "学生财务", []string{"学费", "住宿费", "缴费", "票据", "退费"}, []string{"tuition", "payment", "receipt", "refund"}, nil),
	navigation("digital.network.navigate", "查询校外访问与网络排障入口", "digital", "network", "校园网络", []string{"VPN", "校外访问", "宿舍网络", "网络故障", "NIMO"}, []string{"network", "wifi", "connectivity"}, []TaskPath{{"library", "resources"}}),
	navigation("campus.maps.navigate", "查询校园地图与地点查找入口", "campus", "maps", "校园地图", []string{"校园地图", "找楼", "校区位置", "地点导览", "无障碍路线"}, []string{"map", "location", "building", "accessibility"}, nil),
	publicQuery("library.borrowing.list", "查询学生借阅规则", "library", "borrowing", "图书馆", []string{"借阅规则", "借书期限", "续借次数"}, "从图书馆公开表格读取读者类型、借阅册数、外借和续借周期；不查询个人借阅记录。"),
	publicQuery("life.health.list", "查询校医院公告", "life", "health", "校医院", []string{"校医院公告", "新生体检", "医保通知"}, "读取校医院首页公开公告的标题、发布日期和链接；仅覆盖首页公告，不提供诊疗建议或个人健康数据。"),
	publicQuery("development.notices.list", "查询团委公告", "development", "activities", "交大青年", []string{"团委公告", "学生竞赛", "挑战杯"}, "读取团委通知公告第一页的标题、日期和链接；不等同于第二课堂活动库或可报名名额。"),
	publicQuery("research.hardware.list", "查询超算队列硬件配置", "research", "computing", "交我算 HPC", []string{"超算硬件", "HPC配置", "GPU型号", "计算队列"}, "从官方计算系统表格读取队列、架构、参数和节点数量；是文档发布的配置，不代表实时空闲容量或排队状态。"),
	publicQuery("administration.fees.list", "查询收费公示索引", "administration", "payments", "财务计划处", []string{"收费公示", "学费公示", "收费标准"}, "读取财务首页信息公开栏中的收费及学费公示标题和链接；不提取收费金额、正文或个人账单。"),
	publicQuery("digital.support.list", "查询宿舍网络支持联系方式", "digital", "network", "NIMO", []string{"宿舍网络电话", "网管值班", "NIMO联系方式"}, "读取 NIMO 联系表中的校区、值班时间、地点和电话；仅是网页公布的安排，不代表当前有人值班。"),
	publicQuery("campus.organizations.list", "查询学校机构目录", "campus", "directory", "学校官网", []string{"机构目录", "部门官网", "找部门"}, "读取学校官网机构设置页的名称、机构分组、合署说明与官方链接；保留源站 HTTP 链接，不自动访问各部门网站。"),
}

func feedQuery(id, title, category, group, service string, aliases []string, description string) Capability {
	c := publicQuery(id, title, category, group, service, aliases, description)
	c.Provider = "campusfeed"
	return c
}

func navigation(id, title, category, group, service string, aliases, keywords []string, cross []TaskPath) Capability {
	return Capability{
		ID: id, ContractVersion: "1", Title: title, Category: category, CategoryLabel: categoryLabels[category],
		TaskGroup: group, TaskGroupLabel: taskGroupLabels[TaskPath{category, group}], Service: service,
		Aliases: aliases, Keywords: keywords, CrossListings: cross,
		Description: title + "，返回官方目的地、任务归属、已接入查询及尚未接入原因；不保存操作教程，不探测本次链接状态，不查询个人记录或办理业务。",
		DataClass:   PublicData, ExecutionRule: PublicExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Provider: "servicenav", Operation: Read, Delivery: NavigationDelivery, Credentials: []string{},
	}
}

func publicQuery(id, title, category, group, service string, aliases []string, description string) Capability {
	return Capability{ID: id, ContractVersion: "1", Title: title, Category: category, CategoryLabel: categoryLabels[category], TaskGroup: group, TaskGroupLabel: taskGroupLabels[TaskPath{category, group}], Service: service, Aliases: aliases, Keywords: []string{}, Description: description, DataClass: PublicData, ExecutionRule: PublicExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{}, Provider: "campuspublic"}
}

func cloneCapability(c Capability) Capability {
	c.CLICommand = "sjtu " + strings.ReplaceAll(c.ID, ".", " ")
	c.MCPTool = strings.ReplaceAll(c.ID, ".", "_")
	c.Aliases = append([]string(nil), c.Aliases...)
	c.Keywords = append([]string(nil), c.Keywords...)
	c.Effects = append([]Effect(nil), c.Effects...)
	if c.Credentials != nil {
		c.Credentials = append([]string{}, c.Credentials...)
	}
	c.CrossListings = append([]TaskPath{}, c.CrossListings...)
	c.InputSchema = append(c.InputSchema[:0:0], c.InputSchema...)
	c.OutputSchema = append(c.OutputSchema[:0:0], c.OutputSchema...)
	return c
}

// AssignmentMetadata returns the fixed definition without a grant or resource schema.
func AssignmentMetadata() Capability {
	for _, c := range catalog {
		if c.ID == AssignmentsCapability {
			return cloneCapability(c)
		}
	}
	panic("missing assignment metadata")
}

// PublicCapabilities returns definitions; the public Engine supplies schemas
// and authorization. Private definitions are never added to public discovery.
func PublicCapabilities() []Capability {
	result := []Capability{}
	for _, c := range catalog {
		if c.DataClass == PublicData {
			result = append(result, cloneCapability(c))
		}
	}
	return result
}

type CapabilityFilter struct {
	Query     string
	Category  string
	TaskGroup string
	Service   string
}

// FilterCapabilities only searches the caller's already authorized catalog.
// Terms are case-insensitive substrings and all terms must match; ordering is
// stable by capability ID. No network, database, or semantic model is involved.
func FilterCapabilities(caps []Capability, filter CapabilityFilter) ([]Capability, error) {
	if !utf8.ValidString(filter.Query) || !utf8.ValidString(filter.Category) || !utf8.ValidString(filter.TaskGroup) || !utf8.ValidString(filter.Service) || utf8.RuneCountInString(filter.Query) > 256 || len(filter.Category) > 128 || len(filter.TaskGroup) > 128 || len(filter.Service) > 128 {
		return nil, Fail("INVALID_ARGUMENT", "Search query must be valid UTF-8 and at most 256 characters; filters at most 128 bytes")
	}
	terms := strings.Fields(strings.ToLower(filter.Query))
	category, service := strings.TrimSpace(filter.Category), strings.TrimSpace(filter.Service)
	group := strings.TrimSpace(filter.TaskGroup)
	result := []Capability{}
	for _, c := range caps {
		if !c.Authorized || (service != "" && !strings.EqualFold(service, c.Service)) {
			continue
		}
		paths := append([]TaskPath{{c.Category, c.TaskGroup}}, c.CrossListings...)
		pathMatch := false
		for _, path := range paths {
			if (category == "" || strings.EqualFold(category, path.Category) || strings.EqualFold(category, categoryLabels[path.Category]) || (path.Category == c.Category && strings.EqualFold(category, c.CategoryLabel))) &&
				(group == "" || strings.EqualFold(group, path.TaskGroup) || strings.EqualFold(group, taskGroupLabels[path]) || (path.Category == c.Category && path.TaskGroup == c.TaskGroup && strings.EqualFold(group, c.TaskGroupLabel))) {
				pathMatch = true
				break
			}
		}
		if !pathMatch {
			continue
		}
		fields := []string{c.ID, c.Title, c.Category, c.CategoryLabel, c.TaskGroup, c.TaskGroupLabel, c.Service, c.Provider}
		for _, path := range c.CrossListings {
			fields = append(fields, pathFields(path)...)
		}
		fields = append(fields, c.Aliases...)
		fields = append(fields, c.Keywords...)
		haystack := strings.ToLower(strings.Join(fields, "\n"))
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, cloneCapability(c))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// CapabilitySummaries omits potentially large schemas; describe and SDK/MCP
// discovery retain full contracts including the granted resource constraints.
func CapabilitySummaries(caps []Capability) []Capability {
	result := make([]Capability, 0, len(caps))
	for _, c := range caps {
		c = cloneCapability(c)
		c.InputSchema, c.OutputSchema = nil, nil
		result = append(result, c)
	}
	return result
}
