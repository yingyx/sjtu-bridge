package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

// These dates cite the existing provider and local service-inventory evidence;
// constructing or searching the directory does not reverify a remote service.
func completeTaskMetadata(records []record) {
	type metadata struct{ title, purpose, url, date string }
	data := map[string]metadata{
		"library-borrowing":    {"学生借阅规则", "查询公开借阅额度、期限、续借和预约条件；不查询个人借阅记录。", "https://www.lib.sjtu.edu.cn/fresh/xinsheng.html", "2026-10-20"},
		"hospital-notices":     {"校医院公告", "查找校医院公开通知与按需正文；不读取健康记录或代挂号。", "https://hospital.sjtu.edu.cn/web/news/xxfbtzgg", "2026-10-20"},
		"youth-notices":        {"团委公告", "查找团委公开通知及正文；不等同校园活动库或可报名名额。", "https://youth.sjtu.edu.cn/tzgg.html", "2026-10-20"},
		"fee-notices":          {"收费公示", "查找收费公示索引与HTML正文；PDF仅作来源，不推算个人应缴额。", "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm", "2026-10-20"},
		"dorm-network-support": {"宿舍网络支持", "查询NIMO公布的宿舍网络值班联系安排；不保证当前在岗。", "https://nimo.sjtu.edu.cn/wiki/user-manual-2024/contact-us/", "2026-10-20"},
		"campus-organizations": {"学校机构目录", "查询学校公开机构及部门官网入口；不展开个人通讯录。", "https://www.sjtu.edu.cn/jgsz/index.html", "2026-10-20"},
		"dining-occupancy":     {"食堂拥挤度", "查询食堂概览、分区与源站今日或昨日窗口的采样曲线；不表示实时人数或座位保证。", "https://campuslife.sjtu.edu.cn/", "2026-10-20"},
		"campus-websites":      {"校园网站任务目录", "定位选定校园任务的官方入口；目录收录不代表网站业务已接入。", "https://www.sjtu.edu.cn/xs/", "2026-10-21"},
	}
	for i := range records {
		e := &records[i].entry
		if m, ok := data[e.ID]; ok {
			e.Title, e.Purpose = m.title, m.purpose
			if e.VerifiedAt == "" {
				e.VerifiedAt = m.date
			}
			e.Sources = append(e.Sources, contracts.NavigationSource{Title: "官方来源依据（" + m.date + "）", URL: m.url})
			if len(e.Destinations) == 0 {
				e.Destinations = []contracts.ServiceDestination{{Title: m.title, URL: m.url, Kind: "official_directory"}}
			}
		}
		switch e.ID {
		case "ssc":
			e.Purpose = "定位师生服务中心的综合办事与文书服务；具体事项的资格和办理要求在官网核对。"
		case "academic-practice":
			e.Title = "劳动教育与教务修业核对"
			e.Purpose = "定位劳动教育认定和教务培养修业要求；参与记录不代替正式达标认定。"
		case "canvas":
			e.Title = "Canvas课程学习"
			e.Purpose = "查询获准课程、公告、学习模块、作业要求、本人提交状态、文件元数据和课程安排；具体读取按能力和课程授权。"
			e.Audience = "当前有效选课且获得相应应用授权的本人"
		case "teaching-timetable":
			e.Title = "教务个人课表"
			e.Purpose = "查询获准学期常规课表；课表不包含成绩、选退课或毕业达标判定。"
		case "jcourse":
			e.Title = "jCourse课程与教师信息"
			e.Purpose = "查找课程社区的课程、教师、点评及本人已有列表；社区数据不是教务官方成绩或选课结果。"
		case "ids":
			e.Title = "IDS教学楼与教室服务"
			e.Purpose = "查询教学楼、教室设施、单日课表及教学服务公开信息；3D、实名动态与督导保留官网入口。"
		case "campus-activities":
			e.Title = "校园活动发现与参与入口"
			e.Purpose = "查找本人可见校园活动、日历、报名履历和收藏；报名、取消及签到由官网人工办理。"
		case "second-class-records":
			e.Title = "第二课堂成长记录"
			e.Purpose = "查询本人获准的荣誉、实践与教育固定记录；不推断学分认定、课程通过或毕业资格。"
		case "mail":
			e.Purpose = "查询本人获准收件箱的邮件标题、发件地址和时间；不读取正文附件或标记已读。"
		case "licensed-software":
			e.Purpose = "查找正版软件中心公开目录与官方获取入口；产品目录不证明个人许可，下载激活由官网处理。"
		}
	}
}
