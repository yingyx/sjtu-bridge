package contracts

// Categories are stable business domains; providers and identities are facets.
var categoryLabels = map[string]string{
	"learning": "学习与教学", "library": "图书与文献", "life": "校园生活与健康",
	"development": "发展与校园参与", "research": "科研与实验",
	"administration": "行政与财务", "digital": "账号、网络与数字工具", "campus": "校情与导览",
}

// Selected task groups are implemented first; the full design is documented in
// docs/service-taxonomy.md. Merely registering a path does not authorize it.
var taskGroupLabels = map[TaskPath]string{
	{"digital", "communication"}:   "通信与协作",
	{"life", "dining"}:             "餐饮与校园支付",
	{"library", "borrowing"}:       "借阅与续借",
	{"life", "health"}:             "健康与医疗",
	{"learning", "practice"}:       "实践修业与达标",
	{"learning", "schedule"}:       "教学安排与校历",
	{"learning", "courses"}:        "日常课程学习",
	{"learning", "notices"}:        "教学通知",
	{"library", "spaces"}:          "图书馆空间",
	{"library", "resources"}:       "文献发现与获取",
	{"life", "sports"}:             "运动与文化休闲",
	{"development", "activities"}:  "第二课堂活动与学生组织",
	{"development", "evaluation"}:  "综合评价与荣誉",
	{"research", "computing"}:      "科研计算与数据",
	{"administration", "payments"}: "缴费、结算与票据",
	{"digital", "network"}:         "网络接入与故障",
	{"campus", "maps"}:             "地图与地点导览",
	{"campus", "directory"}:        "机构与官方入口",
}

func pathFields(p TaskPath) []string {
	return []string{p.Category, categoryLabels[p.Category], p.TaskGroup, taskGroupLabels[p]}
}
