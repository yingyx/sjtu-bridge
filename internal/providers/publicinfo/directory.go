package publicinfo

import "github.com/yingyx/sjtu-bridge/contracts"

const directoryRevision = "bundled:2026-10-20-v2"

// These reviewed locators are navigation metadata, never execution grants.
// Keep independently operated portals separate even when a task crosses them.
func directory() []contracts.Item {
	rows := []struct{ id, title, url, task, related string }{
		{"jwc", "教务处", "https://www.jwc.sjtu.edu.cn/", "learning.notices", "public.notices.list,public.notices.search,public.calendars.list"},
		{"teaching", "教学信息服务网", "https://i.sjtu.edu.cn/", "learning.courses", "learning.timetable.list"},
		{"canvas", "在线教学平台 Canvas", "https://oc.sjtu.edu.cn/", "learning.courses", "canvas.assignments.list"},
		{"library", "学校图书馆", "https://www.lib.sjtu.edu.cn/", "library.resources", "library.borrowing.list,library.loans.navigate,library.spaces.navigate"},
		{"ids", "数字化教学服务平台 IDS", "https://ids.sjtu.edu.cn/", "campus.maps", "campus.buildings.list,learning.semester.current"},
		{"activity", "校园活动平台", "https://activity.sjtu.edu.cn/", "development.activities", "development.activities.list,development.activities.navigate"},
		{"electric", "宿舍电费", "https://elec.sjtu.edu.cn/", "life.utilities", "life.electricity.balance,life.utilities.navigate"},
		{"bath", "宿舍洗浴", "https://bath.sjtu.edu.cn/ui/index", "life.utilities", "life.utilities.navigate"},
		{"software", "正版软件中心", "https://software.sjtu.edu.cn/List/rjlb", "digital.software", "digital.software.list,digital.software.navigate"},
		{"mail", "交大邮箱", "https://mail.sjtu.edu.cn/", "digital.communication", "digital.mail.list"},
		{"ssc", "学生事务中心", "https://ssc.sjtu.edu.cn/", "administration.payments", ""},
		{"feedback", "校园生活服务反馈平台", "https://feedback.sjtu.edu.cn/", "life.utilities", ""},
		{"notes", "水源文档", "https://notes.sjtu.edu.cn/", "digital.communication", ""},
		{"latex", "LaTeX 协作平台", "https://latex.sjtu.edu.cn/", "digital.software", ""},
		{"git", "校内 GitLab", "https://git.sjtu.edu.cn/", "digital.software", ""},
		{"chat", "校内大模型平台", "https://chat.sjtu.edu.cn/", "digital.software", ""},
		{"wj", "交大问卷", "https://wj.sjtu.edu.cn/", "digital.communication", ""},
		{"meeting", "云视频会议", "https://meeting.sjtu.edu.cn/", "digital.communication", ""},
	}
	items := make([]contracts.Item, 0, len(rows))
	for _, row := range rows {
		fields := []contracts.PublicField{{Key: "task_path", Label: "任务归属", Value: row.task}}
		if row.related != "" {
			fields = append(fields, contracts.PublicField{Key: "related_capabilities", Label: "相关能力（个人能力须另行授权）", Value: row.related})
		}
		items = append(items, contracts.Item{Provider: "publicinfo", SourceID: row.id, Title: row.title, URL: row.url, Fields: fields})
	}
	return items
}
