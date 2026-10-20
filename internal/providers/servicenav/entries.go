package servicenav

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
	return contracts.Item{Provider: "servicenav", SourceID: id, Title: title, URL: url, Navigation: &contracts.Navigation{Purpose: purpose, Primary: primary, CrossListings: cross, DestinationKind: kind, RelatedCapabilities: related, Sources: []contracts.NavigationSource{{Title: "官方入口依据", URL: source}}, VerifiedAt: verifiedAt, Upstream: contracts.Handoff{Status: "link_only", ReasonCode: reasonCode, Reason: reason, Authentication: authentication, Operation: operation, Effects: effects, Reversibility: reversibility, ExecutionRule: contracts.ManualExecution}}}
}

var entries = map[string][]contracts.Item{
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
		entry("activities", "定位第二课堂活动", "https://activity.sjtu.edu.cn/dekt", "校园活动平台的第二课堂入口；活动发现归发展领域，学时与达标核对归学习领域。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "authentication_required", "活动列表要求登录并独立授权；个人参与历史和修业认定尚未接入。公开团委通知可单独查询。", "jAccount；已接入个人能力须 Broker 另行授权", "https://activity.sjtu.edu.cn/", []string{"development.notices.list", "learning.practice.navigate"}, "development.activities.navigate"),
		entry("registration", "定位校园活动报名管理", "https://activity.sjtu.edu.cn/", "报名、取消及签到由原系统管理；不能以列表查询代替报名成功或修业认定。", "application", contracts.Write, []contracts.Effect{contracts.ResourceCommitmentEffect, contracts.FormalSubmissionEffect}, contracts.UnknownReversibility, "write_not_supported", "报名、取消、签到、收藏和日历订阅均未开放。", "jAccount（仅官网）", "https://activity.sjtu.edu.cn/", []string{"learning.practice.navigate"}, "development.activities.navigate"),
	},
	"life.utilities.navigate": {
		entry("electric", "查询本人宿舍用电", "https://elec.sjtu.edu.cn/", "宿舍电量与历史用电的官网入口；已接入的本人绑定房间电量需独立登录授权，自购与补贴度数分开。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "remaining_interfaces_unverified", "life.electricity.balance 已完成限定账号真实只读验收；历史明细、充值、解绑及换绑仍由官网办理，不查询任意房间。", "jAccount；个人查询须 Broker 独立账号和房间授权", "https://ourhome.sjtu.edu.cn/article/918", []string{}, "life.utilities.navigate"),
		entry("bath", "定位宿舍洗浴门户", "https://bath.sjtu.edu.cn/ui/index", "宿舍洗浴统一入口；服务记录与设备操作均以官网为准。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interface_unverified", "已核对入口需 jAccount；业务接口、设备状态与收费契约未验证，仅提供链接，不开关设备或支付。", "jAccount（仅官网）", "https://bath.sjtu.edu.cn/ui/index", []string{}, "life.utilities.navigate"),
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
		entry("ids", "查询教学建筑与当前学期", "https://ids.sjtu.edu.cn/", "IDS 已接入教学建筑目录与当前学期；与电子地图路线、教室占用和借用区分。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "remaining_interfaces_unverified", "教室课表、空闲或占用接口尚未接入；预约、借用与报修均由官网人工办理。", "已接入目录无需登录；业务以官网为准", "https://ids.sjtu.edu.cn/", []string{"campus.buildings.list", "learning.semester.current"}, "campus.maps.navigate"),
		entry("map", "定位校园电子地图", "https://map.sjtu.edu.cn/", "地图查楼与地点搜索的官方目的地，由学校学生入口链接确认。", "application", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "interface_unverified", "地图数据接口与路线能力未验证。", "以地图系统要求为准", "https://www.sjtu.edu.cn/xs/", []string{"campus.organizations.list"}, "campus.maps.navigate"),
		entry("accessibility", "定位目的地管理单位", "https://www.sjtu.edu.cn/jgsz/index.html", "无障碍可达性或临时通行问题需向目的地管理单位核实，可先查机构目录。", "official_directory", contracts.Read, []contracts.Effect{contracts.NoEffect}, contracts.NotApplicable, "data_unavailable", "尚无已验证的无障碍路线、施工或电梯状态数据源。", "公开机构目录无需登录", "https://www.sjtu.edu.cn/xs/", []string{"campus.organizations.list"}, "campus.maps.navigate"),
	},
}
