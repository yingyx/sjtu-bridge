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
	{
		ID: "public.services.list", ContractVersion: "1",
		Title: "查找校园网站入口", Category: "campus", CategoryLabel: "校园服务导航", Service: "校园网站目录",
		Aliases:     []string{"校园服务", "校园网站", "门户入口", "办事入口"},
		Keywords:    []string{"services", "portal", "website", "links", "directory"},
		Description: "查找教务处、教学信息服务网和 Canvas 的官方链接。仅为内置网站目录，不代表网站业务已接入，也不检查实时可用性。",
		DataClass:   "public", Effects: []string{"none"}, Provider: "publicinfo",
	},
	{
		ID: "public.calendars.list", ContractVersion: "1",
		Title: "查询校历索引", Category: "learning", CategoryLabel: "学习与教学", Service: "教务处",
		Aliases:     []string{"校历", "学年校历", "学期校历", "教学日历"},
		Keywords:    []string{"calendar", "academic", "semester"},
		Description: "查询教务处公开校历索引的标题、日期和官方链接。不解析假期日期或个人课表；每次仅返回一页，需按 next_cursor 继续查询。",
		DataClass:   "public", Effects: []string{"none"}, Provider: "publicinfo",
	},
	{
		ID: "public.notices.list", ContractVersion: "1",
		Title: "查询教务公告", Category: "learning", CategoryLabel: "学习与教学", Service: "教务处",
		Aliases:     []string{"教务公告", "教务通知", "教学通知", "教学公告"},
		Keywords:    []string{"notices", "announcement", "teaching", "news"},
		Description: "查询教务处公开通知的标题、日期和官方链接。不包含正文或附件；每次仅返回一页，不能将一页解释为完整公告列表。",
		DataClass:   "public", Effects: []string{"none"}, Provider: "publicinfo",
	},
	{
		ID: AssignmentsCapability, ContractVersion: "1",
		Title: "查询作业与截止时间", Category: "learning", CategoryLabel: "学习与教学", Service: "Canvas",
		Aliases:     []string{"作业", "查作业", "作业截止时间", "截止时间", "作业期限"},
		Keywords:    []string{"assignments", "homework", "deadline", "deadlines", "due", "DDL"},
		Description: "查询已授权 Canvas 课程的作业及个性化截止时间，仅返回获准的固定字段视图。每次查询一门课程的一页；不包含作业正文、成绩、提交状态或附件，不提供提交或修改操作。",
		DataClass:   "personal", Effects: []string{"none"}, Provider: "canvas",
	},
}

func cloneCapability(c Capability) Capability {
	c.CLICommand = "sjtu " + strings.ReplaceAll(c.ID, ".", " ")
	c.MCPTool = strings.ReplaceAll(c.ID, ".", "_")
	c.Aliases = append([]string(nil), c.Aliases...)
	c.Keywords = append([]string(nil), c.Keywords...)
	c.Effects = append([]string(nil), c.Effects...)
	c.InputSchema = append(c.InputSchema[:0:0], c.InputSchema...)
	c.OutputSchema = append(c.OutputSchema[:0:0], c.OutputSchema...)
	return c
}

func assignmentMetadata() Capability {
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
		if c.DataClass == "public" {
			result = append(result, cloneCapability(c))
		}
	}
	return result
}

type CapabilityFilter struct {
	Query    string
	Category string
	Service  string
}

// FilterCapabilities only searches the caller's already authorized catalog.
// Terms are case-insensitive substrings and all terms must match; ordering is
// stable by capability ID. No network, database, or semantic model is involved.
func FilterCapabilities(caps []Capability, filter CapabilityFilter) ([]Capability, error) {
	if !utf8.ValidString(filter.Query) || !utf8.ValidString(filter.Category) || !utf8.ValidString(filter.Service) || utf8.RuneCountInString(filter.Query) > 256 || len(filter.Category) > 128 || len(filter.Service) > 128 {
		return nil, Fail("INVALID_ARGUMENT", "Search query must be valid UTF-8 and at most 256 characters; filters at most 128 bytes")
	}
	terms := strings.Fields(strings.ToLower(filter.Query))
	category, service := strings.TrimSpace(filter.Category), strings.TrimSpace(filter.Service)
	result := []Capability{}
	for _, c := range caps {
		if !c.Authorized || (category != "" && !strings.EqualFold(category, c.Category) && !strings.EqualFold(category, c.CategoryLabel)) || (service != "" && !strings.EqualFold(service, c.Service)) {
			continue
		}
		fields := []string{c.ID, c.Title, c.Category, c.CategoryLabel, c.Service, c.Provider}
		fields = append(fields, c.Aliases...)
		fields = append(fields, c.Keywords...)
		haystack := strings.ToLower(strings.Join(fields, "\n"))
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, cloneCapability(c))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// CapabilitySummaries omits potentially large schemas; describe and SDK/MCP
// discovery retain full contracts including the granted resource constraints.
func CapabilitySummaries(caps []Capability) []Capability {
	result := make([]Capability, 0, len(caps))
	for _, c := range caps {
		c = cloneCapability(c)
		c.InputSchema, c.OutputSchema = nil, nil
		result = append(result, c)
	}
	return result
}
