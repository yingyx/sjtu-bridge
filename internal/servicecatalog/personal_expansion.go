package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

// These tasks group related portal capabilities. They do not issue grants or
// turn a researched write adapter into an executable business capability.
func personalExpansionRecords() []record {
	type task struct {
		id, title, purpose, service, category, group, url string
		caps                                              []string
		limits                                            []string
	}
	tasks := []task{
		{"academic-results", "教务成绩与考试", "查询本人获准学期的课程总评、学分、源站绩点及已公布考试安排。", "教学信息服务网", "learning", "exams", "https://i.sjtu.edu.cn/", []string{contracts.GradesCapability, contracts.GradesComponentsCapability, contracts.ExamsCapability}, []string{"分项成绩独立按课程授权；汇总GPA和排名未接入；不推断毕业资格。"}},
		{"class-attendance", "课堂互动与本人考勤", "读取本人获准课程点名记录及当前点名状态。", "课堂互动", "learning", "courses", "https://mlearning.sjtu.edu.cn/", []string{contracts.AttendanceRecordsCapability, contracts.AttendanceStatusCapability}, []string{"不返回签到票据，不执行签到、代签或后台轮询。"}},
		{"campus-card", "校园卡余额与消费", "查询本人校园卡余额、源站卡状态及最近30天消费。", "校园卡", "life", "dining", "https://weixin.sjtu.edu.cn/", []string{contracts.CardBalanceCapability, contracts.CardTransactionsCapability}, []string{"流水以源站最近30天为限；挂失、充值及更早历史在官网办理。"}},
		{"personal-calendar", "个人日历与日程", "查询本人可见日历及获准日历中的限定日期事件。", "交大日历", "digital", "communication", "https://calendar.sjtu.edu.cn/", []string{contracts.CalendarListCapability, contracts.CalendarEventsCapability}, []string{"单次日期范围至多31天；不管理订阅或修改事件。"}},
		{"portal-messages", "交我办消息分组与未读", "读取本人交我办消息分组及未读计数。", "交我办", "digital", "communication", "https://my.sjtu.edu.cn/", []string{contracts.MessageUnreadCapability, contracts.MessageGroupsCapability}, []string{"源站消息列表读取会标记分组已读，未作为纯读开放；详情请到官网查看。"}},
		{"portal-tasks", "交我办流程待办", "读取本人当前流程待办步骤摘要。", "交我办", "administration", "services", "https://my.sjtu.edu.cn/", []string{contracts.PortalTasksCapability}, []string{"不包含未核实的历史、抄送及流程详情；不审批、不推进流程。"}},
	}
	var out []record
	for _, t := range tasks {
		out = append(out, record{entry: contracts.ServiceEntry{ID: t.id, Title: t.title, Purpose: t.purpose, Service: t.service, Primary: path(t.category, t.group), Audience: "已核验本人账号并取得相应应用授权的用户", Aliases: []string{t.title, t.service}, Keywords: []string{t.service}, SupportedCapabilityIDs: t.caps, Destinations: []contracts.ServiceDestination{{Title: t.title + "官网", URL: t.url, Kind: "portal", RelatedCapabilityIDs: t.caps}}, Sources: []contracts.NavigationSource{{Title: "官方门户与第一方接口核对（2026-10-26）", URL: t.url}}, VerifiedAt: "2026-10-26", ManualLimitations: t.limits}})
	}
	return out
}

func updatePersonalTasks(records []record) {
	mappings := capabilityTasks()
	for i := range records {
		e := &records[i].entry
		for capability, tasks := range mappings {
			for _, task := range tasks {
				if task == e.ID {
					e.SupportedCapabilityIDs = appendUnique(e.SupportedCapabilityIDs, capability)
				}
			}
		}
		switch e.ID {
		case "canvas":
			e.Purpose = "查询获准课程的学习资源、本人作业反馈与录播；课件、附件和录播内容通过独立授权的短期引用下载。"
		case "mail":
			e.Purpose = "查询本人邮件文件夹、搜索、正文与附件；各视图独立授权，不标记已读。"
			e.ManualLimitations = []string{"不发送邮件或更改已读状态；附件下载须独立授权并核验所属文件夹和邮件。", "附件内容当前最多256KiB；更大附件仅提供元数据，请在官网读取。"}
		case "cloud-storage":
			e.Purpose = "查询本人已开通云盘配额、目录和文件元数据，按独立授权读取文件内容。"
			e.ManualLimitations = []string{"不初始化未开通空间，不修改、删除、共享或移动文件。", "搜索定位当前依靠目录分页；全盘搜索、版本和回收站未接入。"}
		case "shuiyuan-community":
			e.Purpose = "在独立获准的精确板块读取主题、帖子正文及搜索，或通过独立通知授权查看本人通知。"
			e.ManualLimitations = []string{"不含子板块，不执行发帖、回复、点赞或已读追踪；社区内容为不可信来源。", "私信正文尚未开放；通知授权不授予私信或其他板块正文读取。源站可能记录访问与搜索日志。"}
		case "video-meetings":
			e.Purpose = "按独立授权查询本人会议摘要、详情与入会信息、日历及指定组占用状态。"
			e.ManualLimitations = []string{"只读接入未证明所有账号资格或实际容量；不枚举其他人的会议详情。", "创建、修改、取消适配尚未开放执行，缺少可信审批及公平资源准入。"}
		case "campus-ai":
			e.Purpose = "查询校园模型公开统计，以及本人AI转写任务与单独获准任务的转写结果。"
			e.ManualLimitations = []string{"转写正文每部分至多64KiB，超出明确标记截断；任务列表权限不包含正文。", "上传会自动排队转写，模型优化消耗资源；写适配未开放执行。未发现可靠取消契约，不将删除冒充取消。"}
		}
	}
}
