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
	{ID: ElectricityBalanceCapability, ContractVersion: "1", Title: "查询本人绑定宿舍剩余电量", Category: "life", CategoryLabel: "校园生活与健康", TaskGroup: "utilities", TaskGroupLabel: "住宿与生活设施", Service: "宿舍电费", Aliases: []string{"宿舍剩余电量", "宿舍电量", "剩余度数", "补贴电量"}, Keywords: []string{"electricity", "kWh", "balance"}, Description: "通过 Broker 查询本人当前绑定房间的自购剩余电量和补贴电量，分别保留源站度数。仅接受已验证的唯一房间；不查询其他房间，不计算金额、可用天数或历史，不充值、解绑或换绑。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "electric"},
	{ID: ActivityCapability, ContractVersion: "1", Title: "查询校园活动列表", Category: "development", CategoryLabel: "发展与校园参与", TaskGroup: "activities", TaskGroupLabel: "第二课堂活动与学生组织", CrossListings: []TaskPath{{"learning", "practice"}}, Service: "校园活动平台", Aliases: []string{"校园活动", "第二课堂活动列表", "讲座活动", "劳动教育活动"}, Keywords: []string{"activities", "lecture", "feed"}, Description: "通过 Broker 查询本人登录可见的校园活动列表，仅返回活动标题、时间及源站允许公开的地点。每次最多 20 条；不读取详情、参与历史、报名人数或名额，不报名、签到、收藏或订阅；活动参与不等同于修业认定。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "activity"},
	{ID: TimetableCapability, ContractVersion: "1", Title: "查询个人学期课表", Category: "learning", CategoryLabel: "学习与教学", TaskGroup: "courses", TaskGroupLabel: "日常课程学习", CrossListings: []TaskPath{{"learning", "schedule"}}, Service: "教学信息服务网", Aliases: []string{"个人课表", "教务课表", "上课地点", "课程周次"}, Keywords: []string{"timetable", "schedule"}, Description: "通过 Broker 查询获准学期的常规课程课表，返回课程、地点、星期、节次及源站周次；不推算当前教学周，不包含成绩、实践教学列表或选课操作。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "teaching"},
	{ID: MailCapability, ContractVersion: "1", Title: "查询交大收件箱邮件标题", Category: "digital", CategoryLabel: "账号、网络与数字工具", TaskGroup: "communication", TaskGroupLabel: "通信与协作", Service: "交大邮箱", Aliases: []string{"邮箱", "邮件列表", "邮件标题", "邮件搜索", "收件箱"}, Keywords: []string{"email", "mail", "inbox"}, Description: "通过 Broker 查询获准收件箱的邮件标题、发件地址和时间；query 仅按标题字面关键词检索，不读取正文、摘要、附件或联系人，不标记已读、不发信。正文阅读仍需官网人工处理。", DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JAAuthCookie"}, Provider: "sjtumail"},
	feedQuery("public.notices.search", "检索近期教务 RSS 摘要", "learning", "notices", "教务处", []string{"教务摘要", "教务搜索", "RSS", "近期教务通知"}, "读取教务处 RSS 的近期最多 50 条通知；可用 query 按空白分词、全部匹配标题与摘要。无匹配仅表示该 feed 未命中。与 public.notices.list 共享公告链接，后者用于归档索引翻页；不读取正文附件。"),
	feedQuery("life.dining.list", "查询食堂拥挤度概览", "life", "dining", "校园生活", []string{"食堂", "餐厅", "食堂拥挤度", "就餐"}, "读取校园生活食堂概览、食堂 ID 和源站指标。使用 ID 调用 life.dining.areas 查看分区；本调用不遍历分区。概览没有采样时间，零值或过期数据不能解释为空闲或营业保证。"),
	feedQuery("life.dining.areas", "查询食堂分区拥挤度", "life", "dining", "校园生活", []string{"食堂分区", "食堂楼层", "餐厅拥挤度"}, "用 life.dining.list 的 canteen_id 查询一座食堂的分区及最新采样；标注采样时间、缺失与过期状态。无采样、零值或关闭状态不能解释为空闲；不提供预测、人数或预约。"),
	feedQuery("life.dining.series", "查询食堂今日与昨日拥挤度曲线", "life", "dining", "校园生活", []string{"食堂曲线", "今日拥挤度", "昨日拥挤度", "拥挤度变化"}, "用 canteen_id 读取源站当前用餐时段窗口的今日、昨日或两组曲线，逐点分页保留真实日期和原始指标；day 默认 both。不宣称全天，不插值、不抽样、不积累后台历史。请按 next_cursor 读完该窗口。"),
	{
		ID: "public.services.list", ContractVersion: "1",
		Title: "查找校园网站入口", Category: "campus", CategoryLabel: "校情与导览", TaskGroup: "directory", TaskGroupLabel: "机构与官方入口", Service: "校园网站目录",
		Aliases:     []string{"校园服务", "校园网站", "门户入口", "办事入口"},
		Keywords:    []string{"services", "portal", "website", "links", "directory"},
		Description: "按校园任务查找教学、图书馆、住宿、活动及数字工具的官网入口和相关已接入能力。仅为内置网站目录，不代表网站业务全部接入，也不检查实时可用性。",
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
	navigation("life.utilities.navigate", "定位宿舍电费与洗浴服务", "life", "utilities", "住宿服务", []string{"宿舍电费", "剩余电量", "用电记录", "宿舍洗浴", "洗澡", "浴室"}, []string{"electricity", "bath", "dormitory"}, nil),
	navigation("digital.software.navigate", "定位正版软件授权与获取入口", "digital", "software", "正版软件中心", []string{"正版软件", "软件下载", "软件激活", "软件授权"}, []string{"software", "license", "activation"}, nil),
	idsQuery("campus.buildings.list", "查询教学建筑目录", "campus", "maps", []string{"教学楼", "IDS建筑", "校区教学建筑"}, "匿名读取 IDS 发布的教学建筑名称及所属校区；仅覆盖源站教学建筑目录，不代表全部校园建筑、教室空闲、开放或可预约状态。"),
	idsQuery("learning.semester.current", "查询 IDS 当前学期", "learning", "schedule", []string{"当前学期", "当前学年", "IDS周次"}, "匿名读取 IDS 当前学年、学期和源站原始周次；不计算节假日或教学周，不代表个人课表或完整校历。"),
	idsQuery("learning.semester.periods", "查询 IDS 上课节次与时间", "learning", "schedule", []string{"上课时间", "节次时间", "教学节次", "作息时间"}, "读取 IDS 发布的教学节次及起止时间，供教室课表与单日自习查询使用；不推断放假、调休或个人课程。"),
	idsQuery("learning.classrooms.types", "查询 IDS 教室类型", "learning", "schedule", []string{"教室类型", "教室分类", "多媒体教室"}, "读取 IDS 教室类型及源站编码，作为教室目录的过滤条件；类型不表示开放或借用资格。"),
	idsQuery("learning.classrooms.filters", "查询教室设施筛选项", "learning", "schedule", []string{"教室设施筛选", "教室筛选项", "教室设备筛选"}, "读取 IDS 公开教室设施筛选编码、可选值及单选/多选规则，用于 learning.classrooms.list 的 room_features；选项不代表设备实时可用状态。"),
	idsQuery("learning.classrooms.list", "查询教学楼教室目录", "learning", "schedule", []string{"教室目录", "找教室", "教室容量"}, "用 campus.buildings.list 返回的 building_id 查询一座教学楼的教室，可按 room_type、seat_range 和 room_features 过滤；类型与设施选项先查 types/filters。按 next_cursor 继续读取，目录不代表教室当前空闲、开放或可借用。"),
	idsQuery("learning.classrooms.details", "查询教室设施详情", "learning", "schedule", []string{"教室设施", "教室设备", "教室详情"}, "用教室目录返回的 room_id 读取该教室公开设施信息；仅保留公开字段，不控制设备、不访问实时监控。"),
	idsQuery("learning.classrooms.schedule", "查询教学楼单日教室课表", "learning", "schedule", []string{"教室课表", "教室占用", "教学楼课表"}, "按 building_id 和 date 查询一座教学楼单日课程安排，分页保留源站日期、节次与地点；不是本人课表，也不保证未排课教室开放或可预约。不提供批量全校扫描或后台监测。"),
	idsQuery("learning.classrooms.available", "查询单楼单日未排课教室", "learning", "schedule", []string{"空闲教室", "自习教室", "找自习室", "未排课教室"}, "按 building_id、date 及连续 start_section/end_section 查询一座教学楼的源站未排课教室。仅供按需查找自习场所，不保证实际开放、无临时占用或可借用；不提供轮询、余量告警或自动预约。"),
	idsQuery("learning.courses.search", "检索 IDS 单日公开课程", "learning", "schedule", []string{"课程地点", "公开课程查询", "IDS课程搜索"}, "用 query 在指定 campus_id 和 date 内检索源站公开课程安排；仅匹配该范围，不是教务课程目录或个人课表，不遍历全部校区或日期。"),
	idsQuery("campus.buildings.details", "查询教学建筑导览与设施", "campus", "maps", []string{"教学楼详情", "教学楼设施", "教学楼导览"}, "用教学建筑目录的 building_id 读取源站公开导览与设施信息；3D 展示与需要身份的实时动态由官网提供，不访问监控、不控制设备。"),
	idsQuery("campus.notices.list", "查询 IDS 教学楼服务公告", "learning", "notices", []string{"IDS公告", "教学楼服务公告", "教学楼通知"}, "分页读取 IDS 公告标题与官方详情入口；以 campus.notices.get 按需读取正文，不代表教务通知全集。"),
	idsQuery("campus.notices.get", "读取 IDS 服务公告正文", "learning", "notices", []string{"IDS公告正文", "教学楼公告详情"}, "用 campus.notices.list 返回的 item_id 读取一篇公开公告正文；附件和图片仅提供公开来源链接，不自动下载。"),
	idsQuery("campus.contacts.list", "查询 IDS 教学服务联系机构", "campus", "directory", []string{"教学服务联系机构", "IDS通讯录", "教学楼管理单位"}, "读取 IDS 公布的教学服务联系机构及 org_id；使用 campus.contacts.details 查询该机构公开办公联系方式，不查询个人通讯录。"),
	idsQuery("campus.contacts.details", "查询 IDS 机构办公联系方式", "campus", "directory", []string{"教学楼联系电话", "教学服务电话", "教学服务办公地点"}, "用 campus.contacts.list 返回的 org_id 读取源站公开办公联系方式；只投影公开办公信息，不扩展为个人资料查询。"),
	idsQuery("life.lostfound.list", "查询 IDS 失物招领公告", "life", "convenience", []string{"失物招领", "寻物启事", "招领启事", "丢失物品"}, "按 kind 区分寻物和招领，分页查询 IDS 公告摘要及官方入口；不返回联系人隐私，不读取认领信息，不发布、认领或联系他人。"),
	navigation("learning.classrooms.navigate", "定位 IDS 现场导览与教学服务", "learning", "schedule", "数字化教学服务平台 IDS", []string{"教学督导", "教学楼3D", "教学楼实时动态"}, []string{"IDS", "classroom", "supervision"}, []TaskPath{{"campus", "maps"}}),
	navigation("life.lostfound.navigate", "定位失物招领详情入口", "life", "convenience", "数字化教学服务平台 IDS", []string{"失物详情", "失物招领详情"}, []string{"lost", "found"}, nil),
	publicQuery("digital.software.list", "查询正版软件目录", "digital", "software", "正版软件中心", []string{"软件目录", "校园正版软件", "可用软件", "MATLAB", "Office", "Windows"}, "读取正版软件中心公开目录的软件名称、分类及官方详情入口；不下载、安装、激活或接受许可，不保证个人授权资格、最新版本及软件当前可用性。"),
	publicQuery("library.borrowing.list", "查询学生借阅规则", "library", "borrowing", "图书馆", []string{"借阅规则", "借书期限", "续借次数", "预约规则", "续借条件"}, "读取图书馆新生指南与固定正式借阅规则，返回读者额度及普通借阅、短借、续借和预约的来源条件；保留出处和适用限制，不查询个人记录或办理预约续借。"),
	publicQuery("life.health.list", "查询校医院公告", "life", "health", "校医院", []string{"校医院公告", "新生体检", "医保通知"}, "分页读取校医院通知公告归档的标题、日期、对象ID及链接；按 next_cursor 续页，以 life.health.get 按需读取正文。不提供诊疗建议或个人健康数据。"),
	publicQuery("life.health.get", "读取校医院公告正文", "life", "health", "校医院", []string{"校医院公告正文", "体检通知详情"}, "用校医院公告列表的 item_id 读取一篇公开正文及来源链接；附件和图片仅列链接，不自动下载，不作为诊疗建议。"),
	publicQuery("development.notices.list", "查询团委公告", "development", "activities", "交大青年", []string{"团委公告", "学生竞赛", "挑战杯"}, "分页读取团委通知公告归档的标题、日期、对象ID和链接；以 development.notices.get 按需读正文。不等同于第二课堂活动库或可报名名额。"),
	publicQuery("development.notices.get", "读取团委公告正文", "development", "activities", "交大青年", []string{"团委公告正文", "竞赛通知详情"}, "用团委公告列表的 item_id 读取一篇公开通知正文及来源链接；不自动下载附件，不报名、签到或读取个人参与记录。"),
	publicQuery("research.hardware.list", "查询超算队列硬件配置", "research", "computing", "交我算 HPC", []string{"超算硬件", "HPC配置", "GPU型号", "计算队列"}, "从官方计算系统表格读取队列、架构、参数和节点数量；是文档发布的配置，不代表实时空闲容量或排队状态。"),
	publicQuery("administration.fees.list", "查询收费公示索引", "administration", "payments", "财务计划处", []string{"收费公示", "学费公示", "收费标准"}, "分页读取财务信息公示归档中的收费及学费记录，返回对象ID和官方来源；以 administration.fees.get 按需读HTML正文。PDF记录只提供官方来源，不查询个人账单。"),
	publicQuery("administration.fees.get", "读取收费公示正文", "administration", "payments", "财务计划处", []string{"收费公示正文", "学费调整详情"}, "用收费公示列表的数字 item_id 读取HTML正文、表格文字及公开附件链接；PDF和图片仅提供来源，未解析其金额时明确提示，不推算个人应缴费用。"),
	publicQuery("digital.support.list", "查询宿舍网络支持联系方式", "digital", "network", "NIMO", []string{"宿舍网络电话", "网管值班", "NIMO联系方式"}, "读取 NIMO 联系表中的校区、值班时间、地点和电话；仅是网页公布的安排，不代表当前有人值班。"),
	publicQuery("campus.organizations.list", "查询学校机构目录", "campus", "directory", "学校官网", []string{"机构目录", "部门官网", "找部门"}, "读取学校官网机构设置页的名称、机构分组、合署说明与官方链接；保留源站 HTTP 链接，不自动访问各部门网站。"),
}

func idsQuery(id, title, category, group string, aliases []string, description string) Capability {
	c := publicQuery(id, title, category, group, "数字化教学服务平台 IDS", aliases, description)
	c.Provider = "ids"
	if id == "campus.notices.list" || id == "campus.notices.get" {
		c.CrossListings = []TaskPath{{"campus", "maps"}}
	}
	if id == "campus.contacts.list" || id == "campus.contacts.details" {
		c.CrossListings = []TaskPath{{"learning", "schedule"}}
	}
	return c
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
// Terms are case-insensitive substrings and all terms must match. Searches rank
// exact tasks before keywords and classification; lists remain ordered by ID.
// No network, database, or semantic model is involved.
func FilterCapabilities(caps []Capability, filter CapabilityFilter) ([]Capability, error) {
	if !utf8.ValidString(filter.Query) || !utf8.ValidString(filter.Category) || !utf8.ValidString(filter.TaskGroup) || !utf8.ValidString(filter.Service) || utf8.RuneCountInString(filter.Query) > 256 || len(filter.Category) > 128 || len(filter.TaskGroup) > 128 || len(filter.Service) > 128 {
		return nil, Fail("INVALID_ARGUMENT", "Search query must be valid UTF-8 and at most 256 characters; filters at most 128 bytes")
	}
	terms := strings.Fields(strings.ToLower(filter.Query))
	category, service := strings.TrimSpace(filter.Category), strings.TrimSpace(filter.Service)
	group := strings.TrimSpace(filter.TaskGroup)
	if category != "" {
		known := false
		allowed := make([]string, 0, len(categoryLabels))
		for id, label := range categoryLabels {
			known = known || strings.EqualFold(category, id) || strings.EqualFold(category, label)
			allowed = append(allowed, id)
		}
		if !known {
			sort.Strings(allowed)
			return nil, Fail("INVALID_ARGUMENT", "未知 --category 筛选值；可用分类 ID："+strings.Join(allowed, "、"))
		}
	}
	if group != "" {
		known := false
		allowedIDs := map[string]bool{}
		for path, label := range taskGroupLabels {
			known = known || strings.EqualFold(group, path.TaskGroup) || strings.EqualFold(group, label)
			allowedIDs[path.TaskGroup] = true
		}
		if !known {
			allowed := make([]string, 0, len(allowedIDs))
			for id := range allowedIDs {
				allowed = append(allowed, id)
			}
			sort.Strings(allowed)
			return nil, Fail("INVALID_ARGUMENT", "未知 --task-group 筛选值；可用任务组 ID："+strings.Join(allowed, "、"))
		}
	}
	result := []Capability{}
	scores := map[string]int{}
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
		score, matched := capabilitySearchScore(c, terms)
		if matched {
			result = append(result, cloneCapability(c))
			scores[c.ID] = score
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if scores[result[i].ID] != scores[result[j].ID] {
			return scores[result[i].ID] > scores[result[j].ID]
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func capabilitySearchScore(c Capability, terms []string) (int, bool) {
	primary := append([]string{c.ID, c.Title}, c.Aliases...)
	keywords := append([]string{c.Service, c.Provider}, c.Keywords...)
	classification := []string{c.Category, c.CategoryLabel, c.TaskGroup, c.TaskGroupLabel}
	for _, path := range c.CrossListings {
		classification = append(classification, pathFields(path)...)
	}
	contains := func(fields []string, term string) bool {
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), term) {
				return true
			}
		}
		return false
	}
	score := 0
	for _, term := range terms {
		switch {
		case contains(primary, term):
			score += 1000
		case contains(keywords, term):
			score += 100
		case contains(classification, term):
			score++
		default:
			return 0, false
		}
	}
	query := strings.Join(terms, " ")
	if query != "" {
		for _, field := range primary {
			if strings.EqualFold(strings.Join(strings.Fields(field), " "), query) {
				score += 1000000
				break
			}
		}
	}
	return score, true
}

// CapabilitySummary is the task-oriented discovery view. Execution policy and
// full contracts remain available separately and are never inferred from it.
type CapabilitySummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	CLICommand  string   `json:"cli_command"`
	MCPTool     string   `json:"mcp_tool"`
	Delivery    Delivery `json:"delivery"`
	Category    string   `json:"category"`
	Service     string   `json:"service"`
}

// CapabilitySummaries returns only task selection and invocation information.
// SDK/MCP discovery retains full contracts and granted resource constraints.
func CapabilitySummaries(caps []Capability) []CapabilitySummary {
	result := make([]CapabilitySummary, 0, len(caps))
	for _, c := range caps {
		c = cloneCapability(c)
		description := strings.TrimSpace(c.Description)
		if end := strings.IndexAny(description, "。；\n"); end >= 0 {
			description = description[:end] + "。"
		}
		result = append(result, CapabilitySummary{
			ID: c.ID, Title: c.Title, Description: description,
			CLICommand: c.CLICommand, MCPTool: c.MCPTool, Delivery: c.Delivery,
			Category: c.Category, Service: c.Service,
		})
	}
	return result
}
