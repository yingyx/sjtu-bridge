package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func addBusinessExample(command *cobra.Command, capability contracts.Capability) {
	schema, err := decodeInputSchema(capability.InputSchema)
	if err != nil {
		return
	}
	names := append([]string{}, schema.Required...)
	sort.Strings(names)
	parts := []string{capability.CLICommand}
	for _, name := range names {
		parts = append(parts, "--"+cliFlagName(name), usageExampleValue(name, schema.Properties[name], command))
	}
	command.Example = "  " + strings.Join(append(parts, "--json"), " ")
	command.Long = contracts.CapabilitySummaries([]contracts.Capability{capability})[0].Description
}

func completeCommandHelp(root *cobra.Command) {
	resourceDescriptions := map[string]string{
		"calendars": "校历索引", "notices": "公告索引与正文", "services": "校园网站目录", "classrooms": "教室目录、设施与单日排课",
		"courses": "课程查询", "practice": "实践修业与达标", "semester": "学期与上课节次", "timetable": "本人课表",
		"borrowing": "借阅规则与条件", "loans": "个人借阅入口", "spaces": "图书馆空间入口", "dining": "食堂与用餐时段数据",
		"health": "校医院公告", "lostfound": "失物招领", "sports": "体育场馆入口", "utilities": "宿舍生活入口", "electricity": "本人宿舍电量",
		"activities": "校园活动", "network": "校园网络入口", "software": "正版软件目录与入口", "support": "网络支持信息", "mail": "本人收件箱标题",
		"fees": "收费公示", "payments": "缴费入口", "buildings": "教学建筑与楼层", "contacts": "教学楼办公联系", "maps": "校园地图入口",
		"organizations": "学校机构目录", "computing": "科研计算入口", "hardware": "计算硬件信息", "assignments": "Canvas 作业与截止时间",
		"serve": "通过标准输入输出提供 MCP 服务",
	}
	for _, group := range root.Commands() {
		if group.Name() == "public" {
			group.Short = "公开校历、教务公告与校园网站目录"
		}
		group.Short = strings.ReplaceAll(group.Short, "公开查询与服务导航", "查询与服务导航")
		for _, child := range group.Commands() {
			if child.Short == "" {
				child.Short = resourceDescriptions[child.Name()]
			}
		}
	}
}
