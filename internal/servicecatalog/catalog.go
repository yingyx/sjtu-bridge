// Package servicecatalog owns the bundled vetted task directory. It performs
// no network access and never authorizes a capability or upstream operation.
package servicecatalog

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

const Revision = "bundled:2026-10-23-tasks-v4"
const Scope = "curated_bundled_tasks"

type navigationBinding struct {
	capability  string
	sourceID    string
	destination int
}
type portalBinding struct {
	sourceID    string
	destination int
}

type record struct {
	entry      contracts.ServiceEntry
	navigation []navigationBinding
	portals    []portalBinding
}

var records = buildRecords()

// Search searches static positive task metadata; authorization affects only the
// available action projection, never whether a vetted navigation task exists.
func Search(caps []contracts.Capability, filter contracts.ServiceFilter) (contracts.ServicePage, error) {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 1000000 {
		return contracts.ServicePage{}, contracts.Fail("INVALID_ARGUMENT", "Service limit must be 1-100; offset must be 0-1000000")
	}
	metadata := make([]contracts.Capability, 0, len(records))
	for _, r := range records {
		e := r.entry
		metadata = append(metadata, contracts.Capability{ID: e.ID, Title: e.Title, Service: e.Service, Category: e.Primary.Category, CategoryLabel: contracts.CategoryLabel(e.Primary.Category), TaskGroup: e.Primary.TaskGroup, TaskGroupLabel: contracts.TaskGroupLabel(e.Primary), CrossListings: e.CrossListings, Aliases: e.Aliases, Keywords: e.Keywords, Authorized: true})
	}
	// Reuse the existing bounded UTF-8, all-terms, alias and cross-path search.
	matched, err := contracts.FilterCapabilities(metadata, contracts.CapabilityFilter{Query: filter.Query, Category: filter.Category, TaskGroup: filter.TaskGroup, Service: filter.Service})
	if err != nil {
		return contracts.ServicePage{}, err
	}
	page := contracts.ServicePage{Items: []contracts.ServiceEntry{}, Total: len(matched), Offset: filter.Offset, Limit: filter.Limit, Scope: Scope, Revision: Revision}
	start := min(filter.Offset, len(matched))
	end := min(start+filter.Limit, len(matched))
	available := authorizedDefinitions(caps)
	for _, c := range matched[start:end] {
		r := find(c.ID)
		page.Items = append(page.Items, project(r.entry, available))
	}
	if end < len(matched) {
		next := end
		page.NextOffset = &next
	}
	if raw, err := json.Marshal(page); err != nil || len(raw) > contracts.MaxJSONBytes {
		return contracts.ServicePage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Service page exceeds 256 KiB; request a smaller limit")
	}
	return page, nil
}

func Describe(caps []contracts.Capability, id string) (contracts.ServiceEntry, error) {
	if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n") {
		return contracts.ServiceEntry{}, contracts.Fail("INVALID_ARGUMENT", "Service ID must be nonempty valid UTF-8 and at most 128 bytes")
	}
	if r := find(id); r != nil {
		return project(r.entry, authorizedDefinitions(caps)), nil
	}
	return contracts.ServiceEntry{}, contracts.Fail("NOT_FOUND", "Unknown vetted service task")
}

func find(id string) *record {
	for i := range records {
		if records[i].entry.ID == id {
			return &records[i]
		}
	}
	return nil
}

// Trusted definitions supply presentation and security metadata. A supplied ID,
// Authorized flag or read annotation cannot resurrect a removed/unknown action.
func definitions() map[string]contracts.Capability {
	result := map[string]contracts.Capability{}
	for _, c := range contracts.PublicCapabilities() {
		result[c.ID] = c
	}
	ids := append(contracts.CampusCapabilityIDs(), contracts.CanvasCapabilityIDs()...)
	ids = append(ids, contracts.AssignmentsCapability)
	for _, id := range ids {
		if c, ok := contracts.PersonalMetadata(id); ok {
			result[id] = c
		}
	}
	return result
}

func authorizedDefinitions(caps []contracts.Capability) map[string]contracts.Capability {
	trusted := definitions()
	result := map[string]contracts.Capability{}
	for _, c := range caps {
		t, ok := trusted[c.ID]
		if !ok || !c.Authorized || !policy.ExecutableRead(c) || !policy.ExecutableRead(t) {
			continue
		}
		if c.ContractVersion != t.ContractVersion || c.Provider != t.Provider || c.DataClass != t.DataClass || c.Operation != t.Operation || c.Delivery != t.Delivery || c.ExecutionRule != t.ExecutionRule || c.Reversibility != t.Reversibility || !slices.Equal(c.Effects, t.Effects) || !slices.Equal(c.Credentials, t.Credentials) {
			continue
		}
		t.Authorized = true
		result[c.ID] = t
	}
	return result
}

func project(e contracts.ServiceEntry, available map[string]contracts.Capability) contracts.ServiceEntry {
	e = cloneEntry(e)
	caps := []contracts.Capability{}
	for _, id := range e.SupportedCapabilityIDs {
		if c, ok := available[id]; ok {
			caps = append(caps, c)
		}
	}
	e.AvailableCapabilities = contracts.CapabilitySummaries(caps)
	return e
}

func cloneEntry(e contracts.ServiceEntry) contracts.ServiceEntry {
	e.ExternalClients = append([]contracts.ExternalClientGuide{}, e.ExternalClients...)
	for i := range e.ExternalClients {
		e.ExternalClients[i].Setup = append([]string{}, e.ExternalClients[i].Setup...)
		e.ExternalClients[i].Limitations = append([]string{}, e.ExternalClients[i].Limitations...)
	}
	e.CrossListings = append([]contracts.TaskPath{}, e.CrossListings...)
	e.Aliases = append([]string{}, e.Aliases...)
	e.Keywords = append([]string{}, e.Keywords...)
	e.SupportedCapabilityIDs = append([]string{}, e.SupportedCapabilityIDs...)
	e.AvailableCapabilities = append([]contracts.CapabilitySummary{}, e.AvailableCapabilities...)
	e.ManualLimitations = append([]string{}, e.ManualLimitations...)
	e.Sources = append([]contracts.NavigationSource{}, e.Sources...)
	e.Destinations = append([]contracts.ServiceDestination{}, e.Destinations...)
	for i := range e.Destinations {
		e.Destinations[i].RelatedCapabilityIDs = append([]string{}, e.Destinations[i].RelatedCapabilityIDs...)
		if h := e.Destinations[i].Upstream; h != nil {
			copy := *h
			copy.Effects = append([]contracts.Effect{}, h.Effects...)
			e.Destinations[i].Upstream = &copy
		}
	}
	return e
}

// NavigationItems and PortalItems are legacy delivery projections of the same
// records used by Search/Describe. Returned metadata is independently owned.
func NavigationItems(capability string) []contracts.Item {
	items := []contracts.Item{}
	for _, r := range records {
		for _, b := range r.navigation {
			if b.capability != capability {
				continue
			}
			e := cloneEntry(r.entry)
			d := e.Destinations[b.destination]
			n := contracts.Navigation{Purpose: d.Purpose, Primary: e.Primary, CrossListings: e.CrossListings, DestinationKind: d.Kind, RelatedCapabilities: d.RelatedCapabilityIDs, Sources: e.Sources, VerifiedAt: e.VerifiedAt}
			if n.Purpose == "" {
				n.Purpose = e.Purpose
			}
			if d.Upstream != nil {
				n.Upstream = *d.Upstream
			}
			items = append(items, contracts.Item{Provider: "servicenav", SourceID: b.sourceID, Title: d.Title, URL: d.URL, Navigation: &n, Fields: []contracts.PublicField{{Key: "service_task_id", Label: "稳定任务", Value: e.ID}}})
		}
	}
	return items
}

func PortalItems() []contracts.Item {
	items := []contracts.Item{}
	for _, r := range records {
		for _, b := range r.portals {
			e := cloneEntry(r.entry)
			d := e.Destinations[b.destination]
			fields := []contracts.PublicField{{Key: "task_path", Label: "任务归属", Value: e.Primary.Category + "." + e.Primary.TaskGroup}, {Key: "service_task_id", Label: "稳定任务", Value: e.ID}}
			if len(d.RelatedCapabilityIDs) > 0 {
				fields = append(fields, contracts.PublicField{Key: "related_capabilities", Label: "相关能力（个人能力须另行授权）", Value: strings.Join(d.RelatedCapabilityIDs, ",")})
			}
			items = append(items, contracts.Item{Provider: "publicinfo", SourceID: b.sourceID, Title: d.Title, URL: d.URL, Fields: fields})
		}
	}
	return items
}

func appendUnique(list []string, values ...string) []string {
	for _, v := range values {
		if v != "" && !slices.Contains(list, v) {
			list = append(list, v)
		}
	}
	return list
}

// Merge a generic portal locator with its exact task destination. Different
// downstream actions at the same URL retain separate manual handoff metadata.
func addDestination(e *contracts.ServiceEntry, d contracts.ServiceDestination) int {
	for i := range e.Destinations {
		prior := &e.Destinations[i]
		if prior.URL == d.URL && prior.Upstream == nil {
			related := appendUnique(prior.RelatedCapabilityIDs, d.RelatedCapabilityIDs...)
			*prior = d
			prior.RelatedCapabilityIDs = related
			return i
		}
	}
	e.Destinations = append(e.Destinations, d)
	return len(e.Destinations) - 1
}
func path(category, group string) contracts.TaskPath {
	return contracts.TaskPath{Category: category, TaskGroup: group}
}

// capabilityTasks is an explicit reviewed mapping, independent of credentials,
// providers or future capabilities. Related actions share a task record.
func capabilityTasks() map[string][]string {
	m := map[string][]string{}
	add := func(task string, ids ...string) {
		for _, id := range ids {
			m[id] = appendUnique(m[id], task)
		}
	}
	add("canvas", append([]string{contracts.AssignmentsCapability}, contracts.CanvasCapabilityIDs()...)...)
	for _, op := range []string{"courses", "filters", "hot", "course", "reviews", "review_filters", "review_trend", "teachers", "teacher_filters", "teacher", "teacher_courses", "enrolled", "followed", "ignored"} {
		add("jcourse", contracts.JCoursePrefix+op)
	}
	add("teaching-timetable", contracts.TimetableCapability)
	add("mail", contracts.MailCapability)
	add("campus-activities", contracts.ActivityCapability, contracts.ActivityTypesCapability, contracts.ActivityCalendarCapability, contracts.ActivityCountsCapability, contracts.ActivityRegistrationsCapability, contracts.ActivityCollectionsCapability, "development.activities.navigate")
	add("second-class-records", contracts.SecondClassHonorsCapability, contracts.SecondClassPracticeCapability, contracts.SecondClassEducationCapability)
	add("dorm-electricity", contracts.ElectricityBalanceCapability, contracts.ElectricityHistoryCapability)
	add("dorm-bath", contracts.BathBindingCapability, contracts.BathDevicesCapability, contracts.BathUsageCapability)
	add("teaching-information", "public.calendars.list", "public.notices.list", "public.notices.search")
	add("dining-occupancy", "life.dining.list", "life.dining.areas", "life.dining.series")
	add("campus-websites", "public.services.list")
	add("ids", "learning.semester.current", "learning.semester.periods", "learning.classrooms.types", "learning.classrooms.filters", "learning.classrooms.list", "learning.classrooms.details", "learning.classrooms.schedule", "learning.classrooms.available", "learning.courses.search", "campus.buildings.list", "campus.buildings.details", "campus.notices.list", "campus.notices.get", "campus.contacts.list", "campus.contacts.details", "learning.classrooms.navigate")
	add("ids-lost-found", "life.lostfound.list", "life.lostfound.navigate")
	add("licensed-software", "digital.software.list", "digital.software.navigate")
	add("library-borrowing", "library.borrowing.list")
	add("hospital-notices", "life.health.list", "life.health.get")
	add("youth-notices", "development.notices.list", "development.notices.get")
	add("hpc", "research.hardware.list", "research.computing.navigate")
	add("fee-notices", "administration.fees.list", "administration.fees.get")
	add("dorm-network-support", "digital.support.list")
	add("campus-network", "digital.network.navigate")
	add("campus-organizations", "campus.organizations.list")
	add("academic-practice", "learning.practice.navigate")
	add("library-loans", "library.loans.navigate")
	add("library-spaces", "library.spaces.navigate")
	add("sports-venues", "life.sports.navigate")
	add("campus-payments", "administration.payments.navigate")
	add("campus-map", "campus.maps.navigate")
	add("career-opportunities", contracts.CareerListCapability)
	add("campus-news", contracts.CampusNewsCapability)
	add("campus-ai", contracts.AIStatusCapability, contracts.AIUsageCapability)
	add("cloud-storage", contracts.PanQuotaCapability)
	add("shuiyuan-community", contracts.ShuiyuanSearchCapability)
	return m
}

func buildRecords() []record {
	result := []record{}
	indices := map[string]int{}
	ensure := func(id, title, purpose, service, category, group string) *record {
		if i, ok := indices[id]; ok {
			return &result[i]
		}
		indices[id] = len(result)
		result = append(result, record{entry: contracts.ServiceEntry{ID: id, Title: title, Purpose: purpose, Service: service, Primary: path(category, group), ManualLimitations: []string{"仅描述选定任务及官方入口，不代表全校服务全集；本次未检查链接。", "官网个人记录、资格与正式办理须另行认证和人工处理。"}}})
		return &result[len(result)-1]
	}
	// Vetted portal/task seeds are migrated here with original delivery IDs.
	portalTask := map[string]string{"jwc": "teaching-information", "teaching": "teaching-timetable", "canvas": "canvas", "jcourse": "jcourse", "library": "library", "ids": "ids", "activity": "campus-activities", "dekt": "second-class-records", "electric": "dorm-electricity", "bath": "dorm-bath", "software": "licensed-software", "mail": "mail", "ssc": "ssc", "feedback": "campus-feedback", "notes": "notes", "latex": "latex", "git": "gitlab", "chat": "campus-ai", "wj": "questionnaire", "meeting": "video-meetings"}
	for _, item := range portalSeeds() {
		task := strings.Split(item.Fields[0].Value, ".")
		r := ensure(portalTask[item.SourceID], item.Title, "定位"+item.Title+"的官方任务入口；具体查询和办理范围分别核对。", item.Title, task[0], task[1])
		r.entry.VerifiedAt = "2026-10-21" // Existing bundled portal snapshot, not a fresh check.
		related := []string{}
		for _, f := range item.Fields {
			if f.Key == "related_capabilities" {
				related = strings.Split(f.Value, ",")
			}
		}
		destination := addDestination(&r.entry, contracts.ServiceDestination{Title: item.Title, URL: item.URL, Kind: "portal", RelatedCapabilityIDs: related})
		r.portals = append(r.portals, portalBinding{item.SourceID, destination})
		r.entry.Sources = append(r.entry.Sources, contracts.NavigationSource{Title: "既有官方入口记录（2026-10-21）", URL: item.URL})
	}
	navigationSeeds := reviewedNavigation()
	ids := make([]string, 0, len(navigationSeeds))
	for id := range navigationSeeds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bindings := map[string]string{"ids": "ids", "ids-3d": "ids", "ids-dynamic": "ids", "ids-supervision": "ids", "ids-lostfound": "ids-lost-found", "loans": "library-loans", "practice": "academic-practice", "teaching": "academic-practice", "seat": "library-spaces", "rooms": "library-spaces", "sports": "sports-venues", "fitness": "fitness-assessment", "activities": "campus-activities", "registration": "campus-activities", "secondclass": "second-class-records", "electric": "dorm-electricity", "bath": "dorm-bath", "software": "licensed-software", "license": "licensed-software", "accounts": "hpc", "studio": "hpc", "payments": "campus-payments", "receipts": "campus-payments", "vpn": "campus-network", "wireless": "campus-network", "map": "campus-map", "accessibility": "accessibility-support"}
	for _, cap := range ids {
		for _, item := range navigationSeeds[cap] {
			n := item.Navigation
			r := ensure(bindings[item.SourceID], item.Title, n.Purpose, item.Title, n.Primary.Category, n.Primary.TaskGroup)
			r.entry.Aliases = appendUnique(r.entry.Aliases, item.Title)
			r.entry.CrossListings = append(r.entry.CrossListings, n.Primary)
			r.entry.CrossListings = append(r.entry.CrossListings, n.CrossListings...)
			if r.entry.VerifiedAt < n.VerifiedAt {
				r.entry.VerifiedAt = n.VerifiedAt
			}
			h := n.Upstream
			h.Effects = append([]contracts.Effect{}, h.Effects...)
			destination := addDestination(&r.entry, contracts.ServiceDestination{Title: item.Title, URL: item.URL, Kind: n.DestinationKind, Purpose: n.Purpose, RelatedCapabilityIDs: append([]string{}, n.RelatedCapabilities...), Upstream: &h})
			r.navigation = append(r.navigation, navigationBinding{cap, item.SourceID, destination})
			r.entry.Sources = append(r.entry.Sources, n.Sources...)
			r.entry.ManualLimitations = appendUnique(r.entry.ManualLimitations, n.Upstream.Reason)
		}
	}
	mapping := capabilityTasks()
	defs := definitions()
	caps := make([]string, 0, len(defs))
	for id := range defs {
		caps = append(caps, id)
	}
	sort.Strings(caps)
	for _, id := range caps {
		c := defs[id]
		for _, task := range mapping[id] {
			r := ensure(task, c.Title, c.Description, c.Service, c.Category, c.TaskGroup)
			if len(r.entry.SupportedCapabilityIDs) == 0 {
				r.entry.Service = c.Service
			}
			r.entry.SupportedCapabilityIDs = appendUnique(r.entry.SupportedCapabilityIDs, id)
			r.entry.Aliases = appendUnique(r.entry.Aliases, c.Title)
			r.entry.Aliases = appendUnique(r.entry.Aliases, c.Aliases...)
			r.entry.Keywords = appendUnique(r.entry.Keywords, c.Keywords...)
			for _, p := range append([]contracts.TaskPath{path(c.Category, c.TaskGroup)}, c.CrossListings...) {
				if p != r.entry.Primary && !slices.Contains(r.entry.CrossListings, p) {
					r.entry.CrossListings = append(r.entry.CrossListings, p)
				}
			}
		}
	}
	completeTaskMetadata(result)
	// Two locally reviewed long-tail tasks. These are deliberately navigation-only.
	failed := ensure("failed-grade-record", "不及格课程如实记录成绩申请", "查找不及格课程成绩如实记录申请的官方说明与人工入口。", "教务个人学业事务", "learning", "curriculum")
	failed.entry.Audience = "涉及不及格课程成绩的本科生；具体年级和当前资格未核"
	failed.entry.Owner = "教务处"
	failed.entry.VerifiedAt = "2026-10-22"
	failed.entry.Aliases = []string{"如实记录成绩", "不及格成绩记载", "挂科", "挂科成绩"}
	failed.entry.Sources = []contracts.NavigationSource{{Title: "教务处重修通知：仅支持成绩记载背景，未直接说明该申请", URL: "https://jwc.sjtu.edu.cn/info/1222/127661.htm"}}
	failed.entry.ManualLimitations = []string{"用途依据截至2026-10-22；当期申请条件、年级、作用和是否仍办理尚未核实。", "在交我办人工搜索完整事项名；不代申请，不声称已有成绩查询覆盖。"}
	deviation := ensure("ethics-protocol-deviation", "涉人伦理方案偏离", "定位涉人研究伦理方案违背或偏离的报告指南。", "涉人研究伦理审查", "research", "ethics")
	deviation.entry.Audience = "涉人研究项目负责人和伦理管理人员；具体项目资格由官网核验"
	deviation.entry.Owner = "上海交通大学伦理委员会"
	deviation.entry.VerifiedAt = "2026-10-22"
	deviation.entry.Aliases = []string{"伦理方案偏离", "方案违背", "涉人研究方案偏离"}
	deviation.entry.Sources = []contracts.NavigationSource{{Title: "伦理委员会审查流程与方案违背、偏离报告说明", URL: "https://irb.sjtu.edu.cn/info/1292/2291.htm"}}
	deviation.entry.ManualLimitations = []string{"用途依据截至2026-10-22；当前表单版本和个人业务权限未实测。", "在交我办人工搜索完整事项名；不读取受试者、研究材料或代提交报告，不判定伦理批准。"}
	for _, id := range []string{"failed-grade-record", "ethics-protocol-deviation"} {
		r := &result[indices[id]]
		r.entry.Destinations = []contracts.ServiceDestination{{Title: "交我办：人工搜索“" + r.entry.Title + "”", URL: "https://my.sjtu.edu.cn/", Kind: "official_directory"}}
	}
	result = mergeReviewedRecords(result, communityExpansionRecords())
	result = mergeReviewedRecords(result, externalExpansionRecords())
	result = mergeReviewedRecords(result, collaborationExpansionRecords())
	result = mergeReviewedRecords(result, aiExpansionRecords())
	// Coalesce exact locators, keeping distinct downstream action descriptions.
	for i := range result {
		r := &result[i]
		sources := []contracts.NavigationSource{}
		seen := map[string]bool{}
		for _, s := range r.entry.Sources {
			if !seen[s.URL] {
				sources = append(sources, s)
				seen[s.URL] = true
			}
		}
		r.entry.Sources = sources
		cross := []contracts.TaskPath{}
		for _, p := range r.entry.CrossListings {
			if p != r.entry.Primary && !slices.Contains(cross, p) {
				cross = append(cross, p)
			}
		}
		r.entry.CrossListings = cross
		// Reviewed destinations and navigation bindings form explicit many-to-many
		// support associations. A shared navigation capability need not create a
		// synthetic task, and none of these associations grants execution rights.
		for j := range r.entry.Destinations {
			d := &r.entry.Destinations[j]
			known := []string{}
			for _, id := range d.RelatedCapabilityIDs {
				if _, ok := defs[id]; ok {
					known = appendUnique(known, id)
					r.entry.SupportedCapabilityIDs = appendUnique(r.entry.SupportedCapabilityIDs, id)
				}
			}
			d.RelatedCapabilityIDs = known
		}
		for _, b := range r.navigation {
			if _, ok := defs[b.capability]; ok {
				r.entry.SupportedCapabilityIDs = appendUnique(r.entry.SupportedCapabilityIDs, b.capability)
			}
		}
		sort.Strings(r.entry.SupportedCapabilityIDs)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].entry.ID < result[j].entry.ID })
	return result
}
