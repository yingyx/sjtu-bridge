package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

// communityExpansionRecords contains reviewed task boundaries, not permission
// grants. Anonymous-source evidence was checked on 2026-10-23.
func communityExpansionRecords() []record {
	makeRecord := func(id, title, purpose, service, category, group, url string, capabilities []string, limitations []string) record {
		return record{entry: contracts.ServiceEntry{
			ID: id, Title: title, Purpose: purpose, Service: service, Audience: "校园服务使用者", Primary: contracts.TaskPath{Category: category, TaskGroup: group},
			CrossListings: []contracts.TaskPath{}, Aliases: []string{title}, Keywords: []string{id}, SupportedCapabilityIDs: capabilities,
			Destinations: []contracts.ServiceDestination{{Title: title + "官网", URL: url, Kind: "official_directory", RelatedCapabilityIDs: capabilities}},
			Sources:      []contracts.NavigationSource{{Title: "官方入口与匿名只读边界核验（2026-10-23）", URL: url}}, VerifiedAt: "2026-10-23", ManualLimitations: limitations,
		}, portals: []portalBinding{{sourceID: id, destination: 0}}}
	}
	community := makeRecord("shuiyuan-community", "水源校园社区", "用本人授权的User API Key，在应用获准的具体板块中按关键词检索帖子摘要和链接；社区内容为用户生成内容。", "水源社区", "development", "community", "https://shuiyuan.sjtu.edu.cn/", []string{contracts.ShuiyuanSearchCapability}, []string{
		"通过sjtu auth authorize --provider shuiyuan在官网申请read授权；不导入论坛Cookie。匿名访问受限，不提供匿名搜索回退。",
		"每次搜索限定一个明确获准的板块，不包含子板块；最多10个源页，超限须缩小关键词，不返回伪完整结果。板块发现仅为当前可见快照。",
		"不读取私信、用户资料或正文，不执行发帖、回复、点赞和已读追踪。源站仍可能记录访问、搜索日志及Key使用时间。",
	})
	community.entry.Aliases = append(community.entry.Aliases, "水源", "校园论坛", "校园社区交流")
	career := makeRecord("career-opportunities", "就业职位实习与宣讲发现", "按类型查询就业网首屏招聘、实习与专场宣讲卡片，或定位官网继续查找。", "学生就业服务和职业发展中心", "development", "career", "https://www.job.sjtu.edu.cn/career/index", []string{contracts.CareerListCapability}, []string{
		"仅首页对应类型卡片；query只匹配当次首屏标题，不覆盖职位库、归档或全站检索。",
		"宣讲时间保留源站未给年份的文字，不猜测年份或将活动日期当作发布日期。",
		"投递、网签、去向登记、简历、咨询预约及个人信息留在官网人工办理；未代理咨询或抓取下游详情。",
	})
	career.entry.Aliases = append(career.entry.Aliases, "就业", "招聘", "实习", "宣讲会", "生涯")
	news := makeRecord("campus-news", "校级新闻资讯", "查询新闻学术网交大要闻首屏标题、可见日期与官方链接。", "上海交通大学新闻学术网", "campus", "news", "https://news.sjtu.edu.cn/jdyw/index.html", []string{contracts.CampusNewsCapability}, []string{
		"仅交大要闻首屏，不覆盖校级通知通告、其他栏目、归档、正文及全站检索。",
		"保留源站顺序，首位可能是旧专题；未验证置顶标志，不把路径日期或抓取时间当作发布日期。新闻不等同于办事规则。",
	})
	news.entry.Aliases = append(news.entry.Aliases, "交大要闻", "新闻学术网", "校级资讯")
	return []record{community, career, news}
}
