package ids

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

const (
	Classrooms   = "learning.classrooms.list"
	RoomTypes    = "learning.classrooms.types"
	RoomFilters  = "learning.classrooms.filters"
	RoomDetails  = "learning.classrooms.details"
	RoomSchedule = "learning.classrooms.schedule"
	Available    = "learning.classrooms.available"
	Periods      = "learning.semester.periods"
	CourseSearch = "learning.courses.search"
)

type teachingPeriod struct {
	Index int    `json:"sectionIndex"`
	Start string `json:"startTime"`
	End   string `json:"endTime"`
}

type teachingCourse struct {
	Name    string `json:"courseName"`
	Teacher string `json:"teacherName"`
	RoomID  int    `json:"roomId"`
	Start   int    `json:"startSection"`
	End     int    `json:"endSection"`
}

type teachingRoom struct {
	ID         int              `json:"id"`
	Name       string           `json:"name"`
	BuildingID int              `json:"buildId"`
	Code       string           `json:"roomCode"`
	FreeRoom   *string          `json:"freeRoom"`
	Courses    []teachingCourse `json:"roomCourseList"`
}

func teachingItem(id, title string, fields ...contracts.PublicField) contracts.Item {
	return contracts.Item{Provider: "ids", SourceID: id, Title: title, URL: base + "/", Fields: fields}
}

func (p *Provider) teaching(ctx context.Context, id string, in contracts.ListInput) (dataset, error) {
	switch id {
	case Periods:
		periods, current, err := p.teachingPeriods(ctx)
		if err != nil {
			return dataset{}, err
		}
		items := []contracts.Item{}
		for _, row := range periods {
			items = append(items, teachingItem(strconv.Itoa(row.Index), fmt.Sprintf("第 %d 节", row.Index), field("section", "节次", strconv.Itoa(row.Index)), field("start_time", "开始时间", row.Start), field("end_time", "结束时间", row.End), field("current_section_raw", "当前节次原值", strconv.Itoa(current))))
		}
		return dataset{items: items, sources: []contracts.Source{source("/course/findSection", "published_teaching_periods")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "教学节次来自 IDS；当前节次原值可为课间哨兵值，不据此推断正在授课。"}}}, nil
	case RoomTypes:
		return p.teachingRoomTypes(ctx)
	case RoomFilters:
		return p.teachingFilters(ctx)
	case Classrooms:
		return p.teachingClassrooms(ctx, in)
	case RoomDetails:
		return p.teachingRoomDetails(ctx, in)
	case RoomSchedule, Available:
		return p.teachingSchedule(ctx, id, in)
	case CourseSearch:
		return p.teachingSearch(ctx, in)
	default:
		return dataset{}, contracts.Fail("UNSUPPORTED", "Unknown IDS teaching query")
	}
}

func (p *Provider) teachingPeriods(ctx context.Context) ([]teachingPeriod, int, error) {
	raw, err := p.fetch(ctx, "/course/findSection", url.Values{})
	if err != nil {
		return nil, 0, err
	}
	var data struct {
		Periods []teachingPeriod `json:"section"`
		Current *int             `json:"curSection"`
	}
	if decode(raw, &data) != nil || len(data.Periods) == 0 || len(data.Periods) > 24 || data.Current == nil || *data.Current < -100 || *data.Current > 100 {
		return nil, 0, changed()
	}
	seen := map[int]bool{}
	for _, row := range data.Periods {
		start, e1 := time.Parse("15:04", row.Start)
		end, e2 := time.Parse("15:04", row.End)
		if row.Index < 1 || row.Index > 24 || seen[row.Index] || e1 != nil || e2 != nil || !start.Before(end) || start.Format("15:04") != row.Start || end.Format("15:04") != row.End {
			return nil, 0, changed()
		}
		seen[row.Index] = true
	}
	sort.Slice(data.Periods, func(i, j int) bool { return data.Periods[i].Index < data.Periods[j].Index })
	for i, row := range data.Periods {
		if row.Index != i+1 {
			return nil, 0, changed()
		}
	}
	return data.Periods, *data.Current, nil
}

func (p *Provider) teachingRoomTypes(ctx context.Context) (dataset, error) {
	raw, err := p.fetch(ctx, "/build/findRoomTypeList", url.Values{})
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		Rows []struct {
			Code string `json:"dicCode"`
			Name string `json:"dicName"`
		} `json:"roomTypeList"`
	}
	if decode(raw, &data) != nil || data.Rows == nil || len(data.Rows) > 100 {
		return dataset{}, changed()
	}
	items := []contracts.Item{}
	seen := map[string]bool{}
	for _, row := range data.Rows {
		if !text(row.Code) || !text(row.Name) || seen[row.Code] {
			return dataset{}, changed()
		}
		seen[row.Code] = true
		items = append(items, teachingItem(row.Code, row.Name, field("room_type", "教室类型代码", row.Code)))
	}
	return dataset{items: items, sources: []contracts.Source{source("/build/findRoomTypeList", "published_classroom_types")}}, nil
}

type teachingFilterOption struct {
	ID   int    `json:"id"`
	Text string `json:"optionText"`
}

type teachingFilter struct {
	Code     string                 `json:"selectCode"`
	Name     string                 `json:"selectName"`
	Multiple *int                   `json:"multiSelect"`
	Options  []teachingFilterOption `json:"optionList"`
}

func teachingFilterCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			if c < '0' || c > '9' {
				if c != '-' && c != '_' {
					return false
				}
			}
		}
	}
	return true
}

func (p *Provider) teachingFilterDefinitions(ctx context.Context) ([]teachingFilter, error) {
	raw, err := p.fetch(ctx, "/build/findSearchRoomSearchForm", url.Values{})
	if err != nil {
		return nil, err
	}
	var data struct {
		Filters []teachingFilter `json:"selectList"`
	}
	if decode(raw, &data) != nil || data.Filters == nil || len(data.Filters) > 32 {
		return nil, changed()
	}
	seen := map[string]bool{}
	for _, f := range data.Filters {
		if !teachingFilterCode(f.Code) || !text(f.Name) || seen[f.Code] || f.Multiple == nil || (*f.Multiple != 0 && *f.Multiple != 1) || len(f.Options) == 0 || len(f.Options) > 100 {
			return nil, changed()
		}
		seen[f.Code] = true
		options := map[int]bool{}
		for _, o := range f.Options {
			if o.ID < 1 || !text(o.Text) || options[o.ID] {
				return nil, changed()
			}
			options[o.ID] = true
		}
	}
	return data.Filters, nil
}

func (p *Provider) teachingFilters(ctx context.Context) (dataset, error) {
	definitions, err := p.teachingFilterDefinitions(ctx)
	if err != nil {
		return dataset{}, err
	}
	items := []contracts.Item{}
	for _, f := range definitions {
		for _, o := range f.Options {
			oid := strconv.Itoa(o.ID)
			items = append(items, teachingItem(f.Code+":"+oid, f.Name+" / "+o.Text, field("filter_kind", "筛选类别", "room_feature"), field("feature_code", "设施筛选代码", f.Code), field("feature_name", "设施筛选名称", f.Name), field("multi_select", "允许多选", strconv.FormatBool(*f.Multiple == 1)), field("option_id", "选项编号", oid), field("option_text", "选项名称", o.Text)))
		}
	}
	for _, value := range []string{"0-20", "21-50", "51-100", "101-9999"} {
		items = append(items, teachingItem("seat_range:"+value, "座位数 / "+value, field("filter_kind", "筛选类别", "seat_range"), field("seat_range", "座位范围", value)))
	}
	return dataset{items: items, sources: []contracts.Source{source("/build/findSearchRoomSearchForm", "published_classroom_feature_filter_options"), contracts.Source{Provider: "ids", URL: base + "/", Status: "bundled", Coverage: "reviewed_classroom_seat_range_options"}}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "设施选项来自当前 IDS 查询表单；座位范围为官网已核对的四档，不表示实时余座。教室类型与教学楼另查对应目录。"}}}, nil
}

func teachingSeatRange(value string) bool {
	switch value {
	case "", "0-20", "21-50", "51-100", "101-9999":
		return true
	}
	return false
}

func (p *Provider) teachingFeatureParameters(ctx context.Context, requested map[string][]string) (string, error) {
	if len(requested) > 8 {
		return "", contracts.Fail("INVALID_ARGUMENT", "At most eight classroom feature filters are accepted")
	}
	definitions, err := p.teachingFilterDefinitions(ctx)
	if err != nil {
		return "", err
	}
	known := map[string]teachingFilter{}
	for _, f := range definitions {
		known[f.Code] = f
	}
	keys := make([]string, 0, len(requested))
	for code := range requested {
		keys = append(keys, code)
	}
	sort.Strings(keys)
	var result strings.Builder
	for _, code := range keys {
		def, ok := known[code]
		values := requested[code]
		if !ok || len(values) == 0 || len(values) > 8 || (*def.Multiple == 0 && len(values) > 1) {
			return "", contracts.Fail("INVALID_ARGUMENT", "Choose existing classroom feature options within their selection limits")
		}
		options := map[string]bool{}
		for _, o := range def.Options {
			options[strconv.Itoa(o.ID)] = true
		}
		values = append([]string{}, values...)
		sort.Strings(values)
		seen := map[string]bool{}
		for _, value := range values {
			if !options[value] || seen[value] {
				return "", contracts.Fail("INVALID_ARGUMENT", "Unknown or duplicate classroom feature option")
			}
			seen[value] = true
		}
		// The upstream expression is assembled only from matched source definitions.
		result.WriteString(def.Code)
		result.WriteByte('=')
		result.WriteString(strings.Join(values, ","))
		result.WriteString(",#")
	}
	return result.String(), nil
}

func (p *Provider) teachingClassrooms(ctx context.Context, in contracts.ListInput) (dataset, error) {
	if !numericID(in.BuildingID) {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "A building ID is required")
	}
	if _, err := p.building(ctx, in.BuildingID); err != nil {
		return dataset{}, err
	}
	form := url.Values{"buildId": {in.BuildingID + ","}, "area": {"0"}}
	if !teachingSeatRange(in.SeatRange) {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Choose a published classroom seat range")
	}
	if in.SeatRange != "" {
		form.Set("zws", in.SeatRange)
	}
	if len(in.RoomFeatures) > 0 {
		features, err := p.teachingFeatureParameters(ctx, in.RoomFeatures)
		if err != nil {
			return dataset{}, err
		}
		form.Set("selectParamArrStr", features)
	}
	if in.RoomType != "" {
		types, err := p.teachingRoomTypes(ctx)
		if err != nil {
			return dataset{}, err
		}
		found := false
		for _, item := range types.items {
			if item.SourceID == in.RoomType {
				found = true
			}
		}
		if !found {
			return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Unknown published classroom type")
		}
		form.Set("roomType", in.RoomType+",")
	}
	raw, err := p.fetch(ctx, "/build/findRoomList", form)
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		Rows []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"roomList"`
	}
	if decode(raw, &data) != nil || data.Rows == nil || len(data.Rows) > 500 {
		return dataset{}, changed()
	}
	items := []contracts.Item{}
	seen := map[int]bool{}
	for _, row := range data.Rows {
		if row.ID < 1 || seen[row.ID] || !text(row.Name) {
			return dataset{}, changed()
		}
		seen[row.ID] = true
		rid := strconv.Itoa(row.ID)
		items = append(items, teachingItem(rid, row.Name, field("room_id", "教室编号", rid), field("building_id", "查询建筑编号", in.BuildingID)))
	}
	sources := []contracts.Source{source("/build/findAreaBuild", "published_building_directory"), source("/build/findRoomList", "single_building_classroom_directory")}
	if in.RoomType != "" {
		sources = append(sources, source("/build/findRoomTypeList", "published_classroom_types"))
	}
	if len(in.RoomFeatures) > 0 {
		sources = append(sources, source("/build/findSearchRoomSearchForm", "published_classroom_feature_filter_options"))
	}
	if in.SeatRange != "" {
		sources = append(sources, contracts.Source{Provider: "ids", URL: base + "/", Status: "bundled", Coverage: "reviewed_classroom_seat_range_options"})
	}
	return dataset{items: items, sources: sources, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅为选定教学楼和可选类型的教室目录；设施详情需单独查询，不表示当前空闲、开放或可借用。"}}}, nil
}

func (p *Provider) teachingRoomDetails(ctx context.Context, in contracts.ListInput) (dataset, error) {
	if !numericID(in.RoomID) {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "A classroom ID is required")
	}
	raw, err := p.fetch(ctx, "/roomAttr/findRoomDetail", url.Values{"roomId": {in.RoomID}})
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		Build struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			FullName string `json:"fullName"`
			Types    string `json:"roomTypeNames"`
		} `json:"build"`
		Attrs []struct {
			Name  string  `json:"name"`
			Code  string  `json:"code"`
			Value *string `json:"value"`
		} `json:"roomAttrList"`
		Description []struct {
			Value string `json:"value"`
		} `json:"JSMS"`
	}
	if decode(raw, &data) != nil || strconv.Itoa(data.Build.ID) != in.RoomID || !text(data.Build.Name) || len(data.Attrs) > 100 || len(data.Description) > 20 {
		return dataset{}, changed()
	}
	fields := []contracts.PublicField{field("room_id", "教室编号", in.RoomID)}
	if data.Build.FullName != "" {
		if !text(data.Build.FullName) {
			return dataset{}, changed()
		}
		fields = append(fields, field("location", "位置", data.Build.FullName))
	}
	if data.Build.Types != "" {
		if !text(data.Build.Types) {
			return dataset{}, changed()
		}
		fields = append(fields, field("room_types", "教室类型", data.Build.Types))
	}
	seen := map[string]bool{}
	for _, a := range data.Attrs {
		if !teachingFacility(a.Code) {
			continue
		}
		if !text(a.Name) || seen[a.Code] || (a.Value != nil && *a.Value != "" && !text(*a.Value)) {
			return dataset{}, changed()
		}
		seen[a.Code] = true
		value := "未提供"
		if a.Value != nil && *a.Value != "" {
			value = *a.Value
		}
		fields = append(fields, field("facility:"+a.Code, a.Name, value))
	}
	item := teachingItem(in.RoomID, data.Build.Name, fields...)
	if len(data.Description) > 0 {
		fragments := make([]string, 0, len(data.Description))
		for _, d := range data.Description {
			fragments = append(fragments, facilityMarkup(d.Value))
		}
		item.Content = facilityContent(strings.Join(fragments, "\n"))
		links := []contracts.PublicLink{}
		for _, link := range item.Content.Links {
			u, err := url.Parse(link.URL)
			if err != nil {
				continue
			}
			switch strings.ToLower(path.Ext(u.Path)) {
			case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".pdf":
				links = append(links, link)
			}
		}
		item.Content.Links = links
	}
	// Only source-provided static image/document references in the description
	// are preserved. Do not synthesize image paths or expose dynamic panoramas.
	return dataset{items: []contracts.Item{item}, sources: []contracts.Source{source("/roomAttr/findRoomDetail", "classroom_location_description_and_reviewed_facilities")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "返回公开位置、教室说明与已核对的座位、桌椅、讲台黑板、白板及扩声设施属性；静态图片仅保留说明中明确给出的链接，不猜测图片地址。不返回人员统计、环境传感或视频内容，设施存在不保证当时可用。"}}}, nil
}

func teachingFacility(code string) bool {
	switch code {
	case "INFO-JS-ZWS", "INFO-JS-KWS", "JS-TYPE-ZY", "JS-TYPE-KZ", "JS-TYPE-JT", "INFO-JS-DDBB", "INFO-JS-JTHB", "INFO-JS-KSXT":
		return true
	}
	return false
}

type teachingClosure struct {
	RoomCode string                     `json:"roomDm"`
	Sections map[string]json.RawMessage `json:"sectionMap"`
}

func (p *Provider) teachingSchedule(ctx context.Context, id string, in contracts.ListInput) (dataset, error) {
	if !numericID(in.BuildingID) || !validDate(in.Date) {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "A building ID and ISO date are required")
	}
	if in.RoomType != "" {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Classroom availability does not accept a room type filter")
	}
	if _, err := p.building(ctx, in.BuildingID); err != nil {
		return dataset{}, err
	}
	periods, _, err := p.teachingPeriods(ctx)
	if err != nil {
		return dataset{}, err
	}
	if id == Available && (in.StartSection < 1 || in.EndSection < in.StartSection || in.EndSection > len(periods)) {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Requested section range does not match published periods")
	}
	raw, err := p.fetch(ctx, "/build/findBuildRoomType", url.Values{"buildId": {in.BuildingID}, "courseDate": {in.Date}})
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		Floors []struct {
			ID    int            `json:"id"`
			Name  string         `json:"name"`
			Rooms []teachingRoom `json:"children"`
		} `json:"floorList"`
	}
	if decode(raw, &data) != nil || data.Floors == nil || len(data.Floors) > 50 {
		return dataset{}, changed()
	}
	closuresRaw, err := p.fetch(ctx, "/roomCloseState/findRoomAttrValueByCode", url.Values{"buildId": {in.BuildingID}})
	if err != nil {
		return dataset{}, err
	}
	closures := []teachingClosure(nil)
	if decode(closuresRaw, &closures) != nil || len(closures) > 500 {
		return dataset{}, changed()
	}
	closed := map[string]map[int]bool{}
	for _, row := range closures {
		if !text(row.RoomCode) || closed[row.RoomCode] != nil || len(row.Sections) > 24 {
			return dataset{}, changed()
		}
		closed[row.RoomCode] = map[int]bool{}
		for key, value := range row.Sections {
			n, e := strconv.Atoi(key)
			var state int
			if e != nil || n < 1 || n > len(periods) || decode(value, &state) != nil || (state != 0 && state != 1) {
				return dataset{}, changed()
			}
			closed[row.RoomCode][n] = state == 0
		}
	}
	items := []contracts.Item{}
	seen := map[int]bool{}
	for _, floor := range data.Floors {
		if floor.ID < 1 || !text(floor.Name) || floor.Rooms == nil || len(floor.Rooms) > 500 {
			return dataset{}, changed()
		}
		for _, room := range floor.Rooms {
			if room.ID < 1 || seen[room.ID] || !text(room.Name) || strconv.Itoa(room.BuildingID) != in.BuildingID || !text(room.Code) || room.Courses == nil || len(room.Courses) > 200 || room.FreeRoom == nil || (*room.FreeRoom != "0" && *room.FreeRoom != "1") {
				return dataset{}, changed()
			}
			seen[room.ID] = true
			occupied := make([][]teachingCourse, len(periods)+1)
			for _, course := range room.Courses {
				if course.Start < 1 || course.End < course.Start || course.End > len(periods) || !text(course.Name) || (course.Teacher != "" && !text(course.Teacher)) || course.RoomID != room.ID {
					return dataset{}, changed()
				}
				for section := course.Start; section <= course.End; section++ {
					occupied[section] = append(occupied[section], course)
				}
			}
			rid := strconv.Itoa(room.ID)
			baseFields := []contracts.PublicField{field("room_id", "教室编号", rid), field("building_id", "建筑编号", in.BuildingID), field("floor", "楼层", floor.Name), field("date", "查询日期", in.Date), field("self_study_designated", "标记为自修教室", strconv.FormatBool(*room.FreeRoom == "1"))}
			if id == Available {
				available := true
				currentClosure := "unknown"
				for section := in.StartSection; section <= in.EndSection; section++ {
					if len(occupied[section]) > 0 {
						available = false
					}
					if closed[room.Code][section] {
						currentClosure = "closed_marker_present"
					}
				}
				if !available {
					continue
				}
				fs := append([]contracts.PublicField{}, baseFields...)
				fs = append(fs, field("start_section", "起始节次", strconv.Itoa(in.StartSection)), field("end_section", "结束节次", strconv.Itoa(in.EndSection)), field("availability", "课表状态", "no_scheduled_course"), field("opening_status", "实际开放状态", "unknown"), field("current_closure_marker", "源站当前关闭标记", currentClosure))
				items = append(items, teachingItem(rid+":"+in.Date, room.Name, fs...))
			} else {
				for _, period := range periods {
					fs := append([]contracts.PublicField{}, baseFields...)
					status := "unscheduled"
					if *room.FreeRoom == "1" {
						status = "self_study"
					}
					courses := occupied[period.Index]
					if len(courses) > 0 {
						status = "scheduled"
					}
					closure := "unknown"
					if closed[room.Code][period.Index] {
						closure = "closed_marker_present"
					}
					fs = append(fs, field("section", "节次", strconv.Itoa(period.Index)), field("start_time", "开始时间", period.Start), field("end_time", "结束时间", period.End), field("schedule_status", "课表状态", status), field("current_closure_marker", "源站当前关闭标记", closure))
					for i, c := range courses {
						prefix := fmt.Sprintf("course_%d_", i+1)
						fs = append(fs, field(prefix+"name", "课程名称", c.Name))
						if c.Teacher != "" {
							fs = append(fs, field(prefix+"teacher", "任课教师", c.Teacher))
						}
					}
					items = append(items, teachingItem(fmt.Sprintf("%s:%s:%d", rid, in.Date, period.Index), fmt.Sprintf("%s / 第 %d 节", room.Name, period.Index), fs...))
				}
			}
			if len(items) > 5000 || len(seen) > 500 {
				return dataset{}, contracts.Fail("RESPONSE_TOO_LARGE", "IDS teaching result exceeds bounded output")
			}
		}
	}
	warnings := []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "按楼、日期与节次的课程安排查询；没有排课不代表教室开放、没有人员、可预约或允许进入。不进行高频监测。"}}
	if closures == nil {
		warnings = append(warnings, contracts.Warning{Code: "OPENING_UNKNOWN", Message: "源站没有提供教室关闭信息；不能据此确认开放。"})
	}
	if closures != nil {
		warnings = append(warnings, contracts.Warning{Code: "CLOSURE_DATE_UNKNOWN", Message: "关闭标记接口没有日期参数，仅单独展示当前公布标记；不将它当作所选日期的开放安排，也不用于推断历史或未来关闭。"})
	}
	return dataset{items: items, sources: []contracts.Source{source("/build/findAreaBuild", "published_building_directory"), source("/course/findSection", "published_teaching_periods"), source("/build/findBuildRoomType", "single_building_single_date_schedule"), source("/roomCloseState/findRoomAttrValueByCode", "published_current_closure_markers_without_date")}, warnings: warnings}, nil
}

func (p *Provider) teachingSearch(ctx context.Context, in contracts.ListInput) (dataset, error) {
	if !validDate(in.Date) || !text(in.Query) || len([]rune(in.Query)) > 64 || (in.CampusID != "3" && in.CampusID != "662") {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "A date, published campus ID and bounded search keyword are required")
	}
	raw, err := p.fetch(ctx, "/course/findCourseByBuild", url.Values{"searchKey": {in.Query}, "courseDate": {in.Date}, "area": {in.CampusID}})
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		Rooms map[string][]teachingCourse `json:"buildList"`
	}
	if decode(raw, &data) != nil || data.Rooms == nil || len(data.Rooms) > 500 {
		return dataset{}, changed()
	}
	names := make([]string, 0, len(data.Rooms))
	for name := range data.Rooms {
		names = append(names, name)
	}
	sort.Strings(names)
	items := []contracts.Item{}
	for _, name := range names {
		if !text(name) || data.Rooms[name] == nil || len(data.Rooms[name]) > 200 {
			return dataset{}, changed()
		}
		for i, c := range data.Rooms[name] {
			if !text(c.Name) || c.RoomID < 1 || c.Start < 1 || c.End < c.Start || c.End > 24 || (c.Teacher != "" && !text(c.Teacher)) {
				return dataset{}, changed()
			}
			fs := []contracts.PublicField{field("room_id", "教室编号", strconv.Itoa(c.RoomID)), field("room_name", "教室名称", name), field("campus_id", "校区编号", in.CampusID), field("date", "查询日期", in.Date), field("start_section", "起始节次", strconv.Itoa(c.Start)), field("end_section", "结束节次", strconv.Itoa(c.End))}
			if c.Teacher != "" {
				fs = append(fs, field("teacher", "任课教师", c.Teacher))
			}
			items = append(items, teachingItem(fmt.Sprintf("%s:%d:%d", in.Date, c.RoomID, i), c.Name, fs...))
			if len(items) > 5000 {
				return dataset{}, contracts.Fail("RESPONSE_TOO_LARGE", "IDS course search exceeds bounded output")
			}
		}
	}
	return dataset{items: items, sources: []contracts.Source{source("/course/findCourseByBuild", "single_campus_single_date_keyword_course_search")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅检索所选校区、日期的 IDS 教师、课程或教室关键词；不包含课程大纲、学生名单、个人课表或选课资格。"}}}, nil
}
