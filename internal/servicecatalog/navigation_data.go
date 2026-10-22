package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

func entry(id, title, url, purpose, kind string, operation contracts.Operation, effects []contracts.Effect, reversibility contracts.Reversibility, reasonCode, reason, authentication, source string, related []string, capability string) contracts.Item {
	var meta contracts.Capability
	for _, c := range contracts.PublicCapabilities() {
		if c.ID == capability {
			meta = c
			break
		}
	}
	primary := contracts.TaskPath{Category: meta.Category, TaskGroup: meta.TaskGroup}
	cross := meta.CrossListings
	if id == "fitness" {
		primary = contracts.TaskPath{Category: "learning", TaskGroup: "practice"}
		cross = []contracts.TaskPath{{Category: "life", TaskGroup: "sports"}}
	}
	if id == "accessibility" {
		primary = contracts.TaskPath{Category: "campus", TaskGroup: "directory"}
		cross = []contracts.TaskPath{{Category: "campus", TaskGroup: "maps"}}
	}
	verifiedAt := "2026-10-19"
	if id == "loans" || id == "activities" || id == "registration" || id == "electric" || id == "bath" || id == "software" || id == "license" || id == "ids" {
		verifiedAt = "2026-10-20"
	}
	if id == "electric" || id == "bath" || id == "ids" || id == "ids-3d" || id == "ids-dynamic" || id == "ids-supervision" || id == "ids-lostfound" {
		verifiedAt = "2026-10-21"
	}
	return contracts.Item{Provider: "servicenav", SourceID: id, Title: title, URL: url, Navigation: &contracts.Navigation{Purpose: purpose, Primary: primary, CrossListings: cross, DestinationKind: kind, RelatedCapabilities: related, Sources: []contracts.NavigationSource{{Title: "官方入口依据", URL: source}}, VerifiedAt: verifiedAt, Upstream: contracts.Handoff{Status: "link_only", ReasonCode: reasonCode, Reason: reason, Authentication: authentication, Operation: operation, Effects: effects, Reversibility: reversibility, ExecutionRule: contracts.ManualExecution}}}
}

// reviewedNavigation supplies the original dated locator metadata once, during
// construction of the canonical task records. It is not a second registry.
func reviewedNavigation() map[string][]contracts.Item {
	return map[string][]contracts.Item{
		"learning.classrooms.navigate": {
			entry("ids-3d", "打开 IDS 教学楼 3D 导览", "https://ids.sjtu.edu.cn/", "从 IDS 当前教学楼栏目进入 3D 导览；已接入的建筑与教室设施数据可先按相关能力查询。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interactive_view_not_supported", "3D 页面依赖官网生成的加密参数与交互界面，Bridge 只定位官网入口，不生成裸深链或运行 3D 场景。", "以官网当前要求为准", "https://ids.sjtu.edu.cn/", []string{"campus.buildings.list", "campus.buildings.details", "learning.classrooms.details"}, "learning.classrooms.navigate"),
			entry("ids-dynamic", "打开 IDS 教学楼实时动态", "https://ids.sjtu.edu.cn/", "在 IDS 选择教学楼后进入官网实时动态栏目；源课表查询与实时现场状态分别提供。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "authenticated_realtime_not_supported", "实时动态需要官网身份和动态页面参数，Bridge 不读取实时画面、人员状态或监控数据。", "在官网按要求实名认证", "https://ids.sjtu.edu.cn/", []string{"learning.classrooms.schedule", "learning.classrooms.available"}, "learning.classrooms.navigate"),
			entry("ids-supervision", "定位 IDS 教学督导栏目", "https://ids.sjtu.edu.cn/", "由 IDS 当前官网进入教学督导栏目，由官网判断角色与可用功能。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "role_bound_service_not_supported", "督导角色、业务字段和操作契约未接入；不从普通查询权限继承督导权限或执行提交。", "以官网角色和认证要求为准", "https://ids.sjtu.edu.cn/", []string{"campus.contacts.list"}, "learning.classrooms.navigate"),
		},
		"life.lostfound.navigate": {
			entry("ids-lostfound", "打开 IDS 失物招领详情", "https://ids.sjtu.edu.cn/", "在 IDS 失物招领栏目按公告摘要查找详情；先用 life.lostfound.list 区分寻物与招领。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "private_contact_detail_not_supported", "详情可能包含个人联系方式或认领信息，Bridge 仅提供公开摘要；登记、认领动作尚未核实，若官网提供则由用户人工办理。", "以官网详情页当前要求为准", "https://ids.sjtu.edu.cn/", []string{"life.lostfound.list"}, "life.lostfound.navigate"),
		},
		"library.loans.navigate": {
			entry("loans", "查看我的借阅与到期时间", "https://86sjt-primo.hosted.exlibrisgroup.com.cn/primo-explore/account?vid=book&section=loans", "直达学校图书馆链接的个人借阅页；借阅规则可单独查询，不代表个人借阅状态。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "secure_authentication_unavailable", "2026-10-20 匿名核对的 Primo/PDS 登录链指向 HTTP 账号入口，其 HTTPS 连接被拒绝；Bridge 未接通符合 TLS 要求的认证链，不返回借阅数据。", "按图书馆官网要求在浏览器认证", "https://www.lib.sjtu.edu.cn/", []string{"library.borrowing.list"}, "library.loans.navigate"),
		},
		"learning.practice.navigate": {
			entry("practice", "核对劳动教育认定", "https://my.sjtu.edu.cn/", "劳动学时与修业达标的核对入口；活动参与另见发展领域。", "portal", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "identity_binding_missing", "个人修业查询尚无已验证的账号绑定与授权接口。", "jAccount（仅官网）", "https://sais.sjtu.edu.cn/tzgg/1328.html", []string{}, "learning.practice.navigate"),
			entry("teaching", "核对教务修业要求", "https://i.sjtu.edu.cn/", "教务培养与修业结果的权威归属；不能用活动报名或签到替代。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "identity_binding_missing", "个人修业与达标记录尚未接入。", "jAccount（仅官网）", "https://www.jwc.sjtu.edu.cn/", []string{}, "learning.practice.navigate"),
		},
		"library.spaces.navigate": {
			entry("seat", "定位图书馆座位系统", "https://libseat.sjtu.edu.cn/", "座位占用及预约归此系统；借阅期限可直接调用相关查询。", "application", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect}, contracts.UnknownReversibility, "authenticated_interface_unverified", "未验证匿名余座接口，预约写操作未开放。", "以官网认证要求为准", "https://www.lib.sjtu.edu.cn/fresh/xinsheng.html", []string{"library.borrowing.list"}, "library.spaces.navigate"),
			entry("rooms", "定位小组学习室服务", "https://www.lib.sjtu.edu.cn/fresh/xinsheng.html", "房间服务由图书馆常用服务栏目定位，与普通阅览座位区分。", "official_directory", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect}, contracts.UnknownReversibility, "interface_unverified", "房间预约端点及授权契约未验证。", "以目标系统要求为准", "https://www.lib.sjtu.edu.cn/fresh/xinsheng.html", []string{"library.borrowing.list"}, "library.spaces.navigate"),
		},
		"life.sports.navigate": {
			entry("sports", "定位体育场馆预约", "https://sports.sjtu.edu.cn/pc/", "日常运动订场入口；体测修业归学习领域。", "application", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect}, contracts.UnknownReversibility, "authenticated_interface_unverified", "未验证公开余量接口，预约业务未开放。", "以官网认证要求为准", "https://tiyuxi.sjtu.edu.cn/info/1077/3451.htm", []string{}, "life.sports.navigate"),
			entry("fitness", "定位体测官方通知", "https://www.jwc.sjtu.edu.cn/xwtg/tztg.htm", "体测考试与日常订场是不同目的地，以当期教务通知为入口。", "official_directory", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "personal_interface_unverified", "个人体测成绩及预约未接入；此链接仅为通知来源。", "公开通知无需登录；个人系统以官网为准", "https://www.jwc.sjtu.edu.cn/", []string{"public.notices.list"}, "life.sports.navigate"),
		},
		"development.activities.navigate": {
			entry("activities", "定位第二课堂活动", "https://activity.sjtu.edu.cn/dekt", "校园活动平台的第二课堂入口；活动发现归发展领域，学时与达标核对归学习领域。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "authentication_required", "可独立授权查询分类、摘要、日历、本人履历和收藏；详情留在官网。参与状态不代表获得学分。", "jAccount；已接入个人能力须 Broker 另行授权", "https://activity.sjtu.edu.cn/", []string{contracts.ActivityCapability, contracts.ActivityTypesCapability, contracts.ActivityCalendarCapability, contracts.ActivityRegistrationsCapability, "learning.practice.navigate"}, "development.activities.navigate"),
			entry("secondclass", "核对第二课堂成长记录", "https://dektcjd.sjtu.edu.cn/ui/manage", "独立的第二课堂成绩单门户；核对劳动、讲座、实践及非敏感荣誉记录，编辑和完整证明导出由官网办理。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "authentication_required", "须单独核验 dekt 账号并按视图授权；不自动判断课程通过或毕业达标，不导出政治身份、党团荣誉、金额或照片。", "jAccount；与活动门户分别授权", "https://dektcjd.sjtu.edu.cn/ui/manage", []string{contracts.SecondClassHonorsCapability, contracts.SecondClassPracticeCapability, contracts.SecondClassEducationCapability, "learning.practice.navigate"}, "development.activities.navigate"),
			entry("registration", "定位校园活动报名管理", "https://activity.sjtu.edu.cn/", "报名、取消及签到由原系统管理；不能以列表查询代替报名成功或修业认定。", "application", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect, contracts.FormalSubmissionEffect}, contracts.UnknownReversibility, "write_not_supported", "报名、取消、签到、收藏修改和日历订阅均未开放。", "jAccount（仅官网）", "https://activity.sjtu.edu.cn/", []string{"learning.practice.navigate"}, "development.activities.navigate"),
		},
		"life.utilities.navigate": {
			entry("electric", "查询本人宿舍用电", "https://elec.sjtu.edu.cn/", "宿舍电量与历史用电的官网入口；已接入的本人绑定房间电量需独立登录授权，自购与补贴度数分开。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "manual_operation_only", "剩余电量与历史日用电分别由 Broker 授权查询；充值、解绑及换绑仍由官网人工办理。", "jAccount；个人查询须 Broker 独立账号和房间授权", "https://ourhome.sjtu.edu.cn/article/918", []string{contracts.ElectricityBalanceCapability, contracts.ElectricityHistoryCapability}, "life.utilities.navigate"),
			entry("bath", "定位宿舍洗浴门户", "https://bath.sjtu.edu.cn/ui/index", "宿舍洗浴官网入口；本人绑定、楼层设备计数和消费记录可使用相关查询能力，支付签约与水控密码在官网管理。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "manual_operation_only", "本人绑定、设备计数与消费明细已接入独立只读授权；密码、支付、设备控制与绑定变更由官网人工办理。", "jAccount；个人查询须 Broker 独立账号与视图授权", "https://bath.sjtu.edu.cn/ui/index", []string{contracts.BathBindingCapability, contracts.BathDevicesCapability, contracts.BathUsageCapability}, "life.utilities.navigate"),
		},
		"digital.software.navigate": {
			entry("software", "定位正版软件目录与详情", "https://software.sjtu.edu.cn/List/rjlb", "公开软件目录可直接查询；详情用于核对软件说明、版本及本人资格。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "detail_not_integrated", "Bridge 仅读取目录元数据，个人授权资格与详情未接入。", "公开目录无需登录；个人服务以官网为准", "https://software.sjtu.edu.cn/List/rjlb", []string{"digital.software.list"}, "digital.software.navigate"),
			entry("license", "获取与激活正版软件", "https://software.sjtu.edu.cn/", "下载、安装、激活及许可确认由用户在正版软件中心官网办理。", "application", contracts.Write, []contracts.Effect{contracts.FormalSubmissionEffect}, contracts.UnknownReversibility, "license_action_not_supported", "不下载软件、不读取激活密钥、不接受许可或修改设备；具体资格和授权条款以官网为准。", "以软件中心及具体产品要求为准", "https://software.sjtu.edu.cn/", []string{"digital.software.list"}, "digital.software.navigate"),
		},
		"research.computing.navigate": {
			entry("accounts", "定位交我算账号申请", "https://docs.hpc.sjtu.edu.cn/quickstart/index.html", "主账号和学生子账号的适用入口由平台当前文档定位。", "official_documentation", contracts.Write, []contracts.Effect{contracts.FormalSubmissionEffect}, contracts.UnknownReversibility, "write_not_supported", "账号申请与审批未接入；无需向 Bridge 导入 HPC 密码。", "申请在官网认证", "https://docs.hpc.sjtu.edu.cn/quickstart/index.html", []string{"research.hardware.list"}, "research.computing.navigate"),
			entry("studio", "定位 HPC Studio", "https://studio.hpc.sjtu.edu.cn/", "既有账号的可视化计算目的地；公开配置可先用相关查询。", "application", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect, contracts.FinancialEffect}, contracts.UnknownReversibility, "write_not_supported", "计算作业可能消耗配额或产生费用，当前未开放业务执行。", "HPC 账号（仅官网）", "https://docs.hpc.sjtu.edu.cn/quickstart/index.html", []string{"research.hardware.list"}, "research.computing.navigate"),
		},
		"administration.payments.navigate": {
			entry("payments", "定位学校缴费平台", "https://www.jdcw.sjtu.edu.cn/payment", "个人账单和缴费归此目的地；收费公示可先查询官方索引。", "application", contracts.Write, []contracts.Effect{contracts.FinancialEffect}, contracts.UnknownReversibility, "write_not_supported", "支付与个人账单接口未接入，不能由公示推算个人应缴额。", "以缴费平台要求为准", "https://www.jdcw.sjtu.edu.cn/", []string{"administration.fees.list"}, "administration.payments.navigate"),
			entry("receipts", "定位票据与退费受理部门", "https://www.jdcw.sjtu.edu.cn/", "票据及退费由财务计划处收费票据栏目定位。", "official_directory", contracts.Write, []contracts.Effect{contracts.FormalSubmissionEffect, contracts.FinancialEffect}, contracts.UnknownReversibility, "interface_unverified", "退费申请涉及财务事项；具体票据与退费接口、处理效果及可撤回性均未验证。", "以具体受理系统要求为准", "https://www.jdcw.sjtu.edu.cn/", []string{"administration.fees.list"}, "administration.payments.navigate"),
		},
		"digital.network.navigate": {
			entry("vpn", "定位校园 VPN 服务", "https://net.sjtu.edu.cn/xxfw/VPN.htm", "校外资源访问的官方客户端与服务入口；与宿舍有线网络支持区分。", "official_documentation", contracts.Write, []contracts.Effect{contracts.NoEffect}, contracts.UnknownReversibility, "local_action_not_supported", "此处仅指本人设备的连接配置；Bridge 不配置设备网络或建立 VPN 连接，恢复方式未验证。", "jAccount（仅官方客户端）", "https://net.sjtu.edu.cn/xxfw/VPN.htm", []string{}, "digital.network.navigate"),
			entry("wireless", "定位校园无线网支持", "https://net.sjtu.edu.cn/xxfw/wxwl.htm", "校园无线网由网络信息中心服务；宿舍网络联系方式可直接查询 NIMO。", "official_documentation", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interface_unverified", "无线网个人状态及报修接口未接入。", "公开说明无需登录；报修以官网为准", "https://net.sjtu.edu.cn/xxfw/wxwl.htm", []string{"digital.support.list"}, "digital.network.navigate"),
		},
		"campus.maps.navigate": {
			entry("ids", "查询 IDS 教学建筑与教室服务", "https://ids.sjtu.edu.cn/", "教学建筑目录和设施主归地图，教室目录、单日课表与未排课查询主归学习；使用相关能力直接读取。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interactive_views_not_supported", "地图数据不表示开放或借用资格；3D、实名实时动态与督导由官网栏目提供，不执行现场监控或业务提交。", "已接入公开查询无需登录；其他栏目以官网为准", "https://ids.sjtu.edu.cn/", []string{"campus.buildings.list", "campus.buildings.details", "learning.semester.current", "learning.semester.periods", "learning.classrooms.list", "learning.classrooms.schedule", "learning.classrooms.available", "learning.classrooms.navigate"}, "campus.maps.navigate"),
			entry("map", "定位校园电子地图", "https://map.sjtu.edu.cn/", "地图查楼与地点搜索的官方目的地，由学校学生入口链接确认。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interface_unverified", "地图数据接口与路线能力未验证。", "以地图系统要求为准", "https://www.sjtu.edu.cn/xs/", []string{"campus.organizations.list"}, "campus.maps.navigate"),
			entry("accessibility", "定位目的地管理单位", "https://www.sjtu.edu.cn/jgsz/index.html", "无障碍可达性或临时通行问题需向目的地管理单位核实，可先查机构目录。", "official_directory", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "data_unavailable", "尚无已验证的无障碍路线、施工或电梯状态数据源。", "公开机构目录无需登录", "https://www.sjtu.edu.cn/xs/", []string{"campus.organizations.list"}, "campus.maps.navigate"),
		},
	}

}
