package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

// collaborationExpansionRecords describes reviewed official destinations. None
// of these records registers an executable upstream operation.
func collaborationExpansionRecords() []record {
	makeRecord := func(id, title, purpose, audience, owner, category, group string, aliases, limits []string, destinations []contracts.ServiceDestination, sources []contracts.NavigationSource) record {
		return record{entry: contracts.ServiceEntry{
			ID: id, Title: title, Purpose: purpose, Service: title, Audience: audience, Owner: owner,
			Primary: path(category, group), Aliases: aliases, Keywords: aliases,
			VerifiedAt: "2026-10-23", ManualLimitations: limits, Destinations: destinations, Sources: sources,
		}}
	}
	destination := func(title, url, purpose, authentication, reason string) contracts.ServiceDestination {
		return contracts.ServiceDestination{Title: title, URL: url, Kind: "portal", Purpose: purpose,
			Upstream: &contracts.Handoff{Status: "link_only", ReasonCode: "manual_operation_only", Reason: reason, Authentication: authentication,
				Operation: contracts.Read, Effects: []contracts.Effect{contracts.NoEffect}, Reversibility: contracts.NotApplicable, ExecutionRule: contracts.ManualExecution}}
	}
	questionnaire := makeRecord("questionnaire", "智能问卷与本人填报入口", "定位问卷平台，在官网核对具体问卷对象、填写要求及本人已填记录入口。", "具体问卷指定对象；管理权限由平台判断", "网络信息中心", "digital", "communication",
		[]string{"交大问卷", "智能问卷", "我填写的问卷", "填报", "撤回问卷"},
		[]string{"平台首页与公开配置已核；未接入本人答卷、统计或管理接口。", "问卷填写、撤回、重提和发布可能产生正式业务效果，均在官网人工处理；不按每份问卷新增事项。"},
		[]contracts.ServiceDestination{destination("智能问卷服务平台", "https://wj.sjtu.edu.cn/", "由平台进入具体问卷或本人填写记录；具体资格由问卷要求确定。", "平台以jAccount标识账号；具体问卷认证要求以官网为准", "不读取答卷或他人统计，不创建、分发、填写、撤回或重新提交问卷。")},
		[]contracts.NavigationSource{{Title: "智能问卷平台公开配置：名称、用途与归口", URL: "https://wj.sjtu.edu.cn/config/frontendSettings.js"}, {Title: "智能问卷服务平台首页", URL: "https://wj.sjtu.edu.cn/"}})
	questionnaire.portals = []portalBinding{{"wj", 0}}
	meeting := makeRecord("video-meetings", "校园云视频会议", "定位学校腾讯会议服务说明、预约入口及主持人与普通参会者手册。", "教职工、学生；参会角色由会议组织者及平台判断", "网络信息中心", "digital", "communication",
		[]string{"腾讯会议", "云视频会议", "SSO会议", "视频开会", "会议预定"},
		[]string{"官网说明已核；未登录、未读取会议、参会名单或录制，不承诺当前容量与可用时段。", "预约会占用共享资源，按需在官网人工办理；不自动预约、变更、主持或扫描余量。"},
		[]contracts.ServiceDestination{destination("云视频会议预定系统", "https://meeting.sjtu.edu.cn/", "在官网按需核对本人会议和人工预约；手册由网信服务页定位。", "jAccount；客户端按官网说明使用SSO", "会议、录制和参会名单未接入；资源预约与变更仅官网人工处理。"), {Title: "云视频会议服务说明与使用手册", URL: "https://net.sjtu.edu.cn/xxfw/ysphy.htm", Kind: "official_documentation"}},
		[]contracts.NavigationSource{{Title: "网信中心云视频会议现行服务栏目", URL: "https://net.sjtu.edu.cn/xxfw/ysphy.htm"}})
	meeting.portals = []portalBinding{{"meeting", 0}}
	software := makeRecord("engineering-software", "工程软件许可服务指引", "查找学生创新中心软件授权平台的公开说明与各产品许可获取入口。", "全校师生；具体产品资格、用途和网络要求以官网为准", "学生创新中心", "digital", "software",
		[]string{"工程软件", "软件授权中心", "lic", "Solidworks", "LabVIEW", "Origin", "EndNote"},
		[]string{"2026-10-23已核lic.sjtu.edu.cn首页的软件说明、公告及学生创新中心归口；不推定与正版软件中心或旧lic.si同许可。", "只提供官网定位，不判断个人许可，不下载、安装、申请激活或读取授权密钥。"},
		[]contracts.ServiceDestination{destination("学生创新中心软件授权平台", "https://lic.sjtu.edu.cn/", "从公开软件与公告栏目核对产品当前说明，再在官网处理本人许可。", "公开首页无需登录；产品授权依各自要求", "未接入目录查询或个人许可接口；下载、安装和激活由用户在官网人工处理。")},
		[]contracts.NavigationSource{{Title: "软件授权中心当前首页与归口", URL: "https://lic.sjtu.edu.cn/"}})
	notifications := makeRecord("meeting-notifications", "通知与参会反馈平台", "定位学校通知服务平台，在官网核对会议通知、信息送达与参会反馈入口。", "具有通知平台权限的校内用户；群组与会议角色由官网判断", "网络信息中心", "digital", "communication",
		[]string{"通知服务", "会议通知", "参会反馈", "出席反馈", "notice"},
		[]string{"网信当前服务页仍列通知服务平台，目标站匿名访问进入统一认证；2020介绍仅证明历史用途，不保证现行全部功能。", "不读取私人通知、群组或联系人，不发送消息、导入名单或确认出席；与邮箱和视频会议分别处理。"},
		[]contracts.ServiceDestination{destination("通知服务平台", "https://notice.sjtu.edu.cn/", "经学校统一身份认证后在官网核对当前可用通知与反馈功能。", "匿名访问进入jAccount统一认证；角色另核", "私人通知及角色接口未接入；消息发送与反馈由官网人工处理。")},
		[]contracts.NavigationSource{{Title: "网信中心现行服务页中的通知服务平台链接", URL: "https://net.sjtu.edu.cn/xxfw/ysphy.htm"}, {Title: "2020年通知服务上线介绍（历史用途）", URL: "https://net.sjtu.edu.cn/info/1009/2094.htm"}})
	identity := makeRecord("visual-identity", "学校视觉规范与应用模板", "定位学校校徽、标准字、色彩、校标组合等规范与行政办公、多媒体应用模板。", "使用学校视觉标识的单位和个人；须遵守官网规范", "上海交通大学", "campus", "directory",
		[]string{"VI", "视觉识别", "校徽", "校标", "标志规范", "标准色", "官方模板"},
		[]string{"已核官网基础规范与应用栏目；公告和模板存在历史版本，使用时应核对官网当前文件及管理要求。", "不自动下载或生成官方身份材料；视觉规范不授予摄影图片的使用许可。"},
		[]contracts.ServiceDestination{{Title: "上海交通大学视觉形象识别系统", URL: "https://vi.sjtu.edu.cn/", Kind: "official_documentation"}},
		[]contracts.NavigationSource{{Title: "视觉形象识别系统当前首页", URL: "https://vi.sjtu.edu.cn/"}, {Title: "学校发布视觉形象识别系统的通知", URL: "https://gk.sjtu.edu.cn/Data/View/172"}})
	photos := makeRecord("campus-photos", "视觉交大图片素材", "定位学校图片库及公开主题，按官网要求核对具体图片授权与下载条件。", "有校园图片素材需求的用户；下载、供稿及使用角色另核", "上海交通大学", "campus", "directory",
		[]string{"视觉交大", "图片库", "校园照片", "摄影素材", "壁纸"},
		[]string{"已核首页公开搜索与主题栏目；未核每张图片的权利人、用途限制或下载资格。", "摄影素材与VI规范分别授权，不因可浏览而声称可自由转载；不自动下载、供稿或上传。"},
		[]contracts.ServiceDestination{{Title: "视觉交大", URL: "https://vs.sjtu.edu.cn/", Kind: "official_directory"}},
		[]contracts.NavigationSource{{Title: "视觉交大当前公开主题与图片搜索首页", URL: "https://vs.sjtu.edu.cn/"}})
	faculty := makeRecord("faculty-homepages", "教师公开主页发现", "通过教师主页平台按院系、学科或姓名定位教师公开研究与教学介绍。", "校内外访客及希望了解导师的考生；主页编辑仅教师本人按官网权限处理", "教师个人主页平台", "campus", "directory",
		[]string{"教师主页", "导师主页", "教师索引", "院系教师", "faculty", "导师研究方向"},
		[]string{"已核平台介绍及公开首页；仅导航，教师主页覆盖不等于全校在岗教师名册。", "公开脚本存在分页检索端点，但合成不存在姓名的单条请求未正确筛选且响应字段过宽，尚不能作为可靠查询；不读取非公开联系方式或代编辑主页。"},
		[]contracts.ServiceDestination{{Title: "教师主页：姓名、院系与学科入口", URL: "https://faculty.sjtu.edu.cn/", Kind: "official_directory"}, {Title: "教师主页平台介绍", URL: "https://faculty.sjtu.edu.cn/zwy.jsp?urltype=tree.TreeTempUrl&wbtreeid=1047", Kind: "official_documentation"}},
		[]contracts.NavigationSource{{Title: "教师主页平台公开索引", URL: "https://faculty.sjtu.edu.cn/"}, {Title: "平台介绍：校内外检索与主页发布用途", URL: "https://faculty.sjtu.edu.cn/zwy.jsp?urltype=tree.TreeTempUrl&wbtreeid=1047"}})
	cloud := makeRecord("cloud-storage", "交大云盘 Pan", "查询本人个人空间的开通状态与精确配额，或定位官网和独立客户端继续处理文件。", "Pan教职工、学生用户；具体账号状态由官网判断", "网络信息中心", "digital", "communication",
		[]string{"Pan", "交大云盘", "jBox", "云盘配额", "文件目录", "TboxWebdav"},
		[]string{"旧jBox已停服，当前使用Pan；官网默认容量不代表本人实时配额。", "通过第一方SSO的同一响应绑定用户ID与临时Token；可由Broker复用已有校园凭据，仍须独立核验Pan账号。", "仅查询个人空间配额；未开通不自动初始化。文件目录所需personal签发的初始化副作用尚未核清，目录和文件正文未接入。"},
		[]contracts.ServiceDestination{destination("交大云盘 Pan", "https://pan.sjtu.edu.cn/", "在官网查看本人文件，Bridge支持查询个人空间开通状态和配额。", "本人jAccount；由Pan核验账号状态", "文件操作由用户人工处理；不代上传、分享、移动、删除或初始化空间。"), {Title: "交大云盘官方使用手册", URL: "https://pan.sjtu.edu.cn/pandoc/install.html", Kind: "official_documentation"}},
		[]contracts.NavigationSource{{Title: "网信现行云盘说明：Pan与旧jBox停服", URL: "https://net.sjtu.edu.cn/xxfw/jBoxjdyp.htm"}, {Title: "Pan当前公开前端", URL: "https://pan.sjtu.edu.cn/"}})
	cloud.entry.SupportedCapabilityIDs = []string{contracts.PanQuotaCapability}
	cloud.entry.Destinations[0].RelatedCapabilityIDs = []string{contracts.PanQuotaCapability}
	cloud.entry.ExternalClients = []contracts.ExternalClientGuide{{
		ID: "tbox-webdav", Title: "TboxWebdav（独立第三方，只读模式）", InstallURL: "https://github.com/1357310795/TboxWebdav/releases", DocumentationURL: "https://github.com/1357310795/TboxWebdav",
		Setup:          []string{"先审阅上游版本与发布说明，在个人工具目录选择适用便携版本；Bridge不安装或启动服务。", "仅在本人电脑使用：TboxWebdav.Server.AspNetCore --host localhost --auth UserToken --access ReadOnly。", "WebDAV客户端连接本机localhost:65472，使用交互式凭据输入提供本人主动取得的Pan UserToken，不放入命令参数或公共配置。"},
		Authentication: "外部工具独立处理用户主动提供的Pan UserToken；不使用Bridge凭据或授权。", Scope: "独立WebDAV服务可读取目录及文件内容并写本地缓存；ReadOnly限制上游文件修改，不等于Bridge最小配额视图。",
		Verification: "2026-10-23审阅4537e22源码与README；未安装、未执行或登录真实账号。",
		Limitations:  []string{"其个人空间Token签发仍使用POST personal，首次创建副作用未核；严格要求无初始化写入时应继续使用官网。", "只读模式须显式设置；上游默认Full，禁止使用匿名认证或监听外网地址。", "独立服务、凭据保管、缓存及退出须由用户管理，不受Bridge撤销控制。"},
	}}
	return []record{questionnaire, meeting, software, notifications, identity, photos, faculty, cloud}
}
