package contracts

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	PanFilesCapability              = "digital.pan.files.list"
	PanInfoCapability               = "digital.pan.files.get"
	PanContentCapability            = "digital.pan.files.content"
	MailFoldersCapability           = "digital.mail.folders"
	MailSearchCapability            = "digital.mail.search"
	MailMessageCapability           = "digital.mail.get"
	MailAttachmentCapability        = "digital.mail.attachments.content"
	ShuiyuanTopicsCapability        = "development.shuiyuan.topics"
	ShuiyuanTopicCapability         = "development.shuiyuan.topic"
	ShuiyuanPostsCapability         = "development.shuiyuan.posts"
	ShuiyuanNotificationsCapability = "development.shuiyuan.notifications"
)

func DigitalReadIDs() []string {
	return []string{PanFilesCapability, PanInfoCapability, PanContentCapability, MailFoldersCapability, MailSearchCapability, MailMessageCapability, MailAttachmentCapability, ShuiyuanTopicsCapability, ShuiyuanTopicCapability, ShuiyuanPostsCapability, ShuiyuanNotificationsCapability}
}
func IsDigitalRead(id string) bool {
	for _, candidate := range DigitalReadIDs() {
		if id == candidate {
			return true
		}
	}
	return false
}
func digitalReadRules(id string) (CampusReadRules, bool) {
	r := CampusReadRules{capability: id, MaxResources: 8, MaxLimit: 50, Paginated: true}
	switch id {
	case PanFilesCapability:
		r.View, r.Coverage = "files_v1", "personal_directory_page"
	case PanInfoCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "file_v1", "personal_object_metadata", 1, false
	case PanContentCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "content_v1", "personal_file_content", 1, false
	case MailFoldersCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "folders_v1", "own_mail_folder_tree", 100, false
	case MailSearchCapability:
		r.View, r.Coverage, r.QueryAllowed = "messages_v1", "own_mail_folder_search", true
	case MailMessageCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "message_v1", "own_mail_message", 1, false
	case MailAttachmentCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "content_v1", "own_mail_attachment_content", 1, false
	case ShuiyuanTopicsCapability:
		r.View, r.Coverage, r.MaxLimit = "topics_v1", "exact_category_topic_page", 20
	case ShuiyuanTopicCapability:
		r.View, r.Coverage, r.MaxLimit, r.Paginated = "topic_v1", "exact_category_topic_metadata", 1, false
	case ShuiyuanPostsCapability:
		r.View, r.Coverage, r.MaxLimit, r.QueryAllowed = "posts_v1", "exact_category_topic_posts", 20, true
	case ShuiyuanNotificationsCapability:
		r.View, r.Coverage, r.MaxLimit = "notifications_v1", "own_notifications", 20
	default:
		return CampusReadRules{}, false
	}
	return r, true
}

var mailFolderRef = regexp.MustCompile(`^mail:folder:[1-9][0-9]{0,17}$`)
var digitalID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var mimePart = regexp.MustCompile(`^[1-9][0-9]{0,3}(\.[1-9][0-9]{0,3}){0,9}$`)

func validDigitalResource(id, ref string) bool {
	switch id {
	case PanFilesCapability, PanInfoCapability, PanContentCapability:
		return ref == "pan:files:self"
	case MailFoldersCapability:
		return ref == "mail:folders:self"
	case MailSearchCapability, MailMessageCapability, MailAttachmentCapability:
		return mailFolderRef.MatchString(ref)
	case ShuiyuanTopicsCapability, ShuiyuanTopicCapability, ShuiyuanPostsCapability:
		return ValidShuiyuanResource(ref)
	case ShuiyuanNotificationsCapability:
		return ref == "shuiyuan:notifications:self"
	}
	return false
}

// ValidPanPath is a relative, decoded, case-preserving SMH path. It never
// accepts an absolute filesystem path, URL, traversal or server control name.
func ValidPanPath(path string, allowRoot bool) bool {
	if path == "" {
		return allowRoot
	}
	if !utf8.ValidString(path) || len(path) > 1024 || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.ContainsAny(path, `\%`) {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > 255 || strings.TrimSpace(seg) != seg || strings.IndexFunc(seg, func(r rune) bool {
			return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune(`?:"<>*|`, r)
		}) >= 0 {
			return false
		}
	}
	return true
}

func digitalField(id, key string) bool {
	switch key {
	case "path":
		return id == PanFilesCapability || id == PanInfoCapability || id == PanContentCapability
	case "item_id":
		return id == MailMessageCapability || id == MailAttachmentCapability || id == ShuiyuanTopicCapability || id == ShuiyuanPostsCapability
	case "part":
		return id == MailAttachmentCapability
	case "mode":
		return id == ShuiyuanTopicsCapability
	case "unread":
		return id == MailSearchCapability || id == ShuiyuanNotificationsCapability
	case "begin", "end":
		return id == MailSearchCapability
	}
	return false
}

func validDigitalFilters(id string, q CampusQuery) bool {
	if !IsDigitalRead(id) {
		return q.Path == "" && q.ItemID == "" && q.Part == "" && q.Mode == "" && !q.Unread
	}
	if q.Path != "" && !digitalField(id, "path") || q.ItemID != "" && !digitalField(id, "item_id") || q.Part != "" && !digitalField(id, "part") || q.Mode != "" && !digitalField(id, "mode") || q.Unread && !digitalField(id, "unread") {
		return false
	}
	switch id {
	case PanFilesCapability:
		return ValidPanPath(q.Path, true)
	case PanInfoCapability, PanContentCapability:
		return ValidPanPath(q.Path, false)
	case MailMessageCapability:
		return digitalID.MatchString(q.ItemID)
	case MailAttachmentCapability:
		return digitalID.MatchString(q.ItemID) && mimePart.MatchString(q.Part)
	case ShuiyuanTopicCapability, ShuiyuanPostsCapability:
		return digitalID.MatchString(q.ItemID) && (q.Query == "" || ValidShuiyuanQuery(q.Query))
	case ShuiyuanTopicsCapability:
		return q.Mode == "" || q.Mode == "latest" || q.Mode == "top"
	case MailSearchCapability:
		for _, date := range []string{q.Begin, q.End} {
			if date != "" {
				d, e := time.Parse("2006-01-02", date)
				if e != nil || d.Year() < 2000 || d.Year() > 2099 || d.Format("2006-01-02") != date {
					return false
				}
			}
		}
		return q.Begin == "" || q.End == "" || q.Begin < q.End
	}
	return true
}

func addDigitalInput(id string, props map[string]any, required *[]string) {
	if IsDigitalRead(id) {
		resource := props["resource_ref"].(map[string]any)
		switch id {
		case PanFilesCapability, PanInfoCapability, PanContentCapability:
			resource["enum"] = []string{"pan:files:self"}
		case MailFoldersCapability:
			resource["enum"] = []string{"mail:folders:self"}
		case MailSearchCapability, MailMessageCapability, MailAttachmentCapability:
			resource["pattern"] = mailFolderRef.String()
		case ShuiyuanTopicsCapability, ShuiyuanTopicCapability, ShuiyuanPostsCapability:
			resource["pattern"] = `^shuiyuan:category:[1-9][0-9]{0,9}$`
		case ShuiyuanNotificationsCapability:
			resource["enum"] = []string{"shuiyuan:notifications:self"}
		}
	}

	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	for key, max := range map[string]int{"path": 1024, "item_id": 18, "part": 49, "mode": 6} {
		if digitalField(id, key) {
			props[key] = str(max)
		}
	}
	if digitalField(id, "unread") {
		props["unread"] = map[string]any{"type": "boolean", "description": "仅未读；查询不标记已读"}
	}
	if digitalField(id, "begin") {
		props["begin"], props["end"] = str(10), str(10)
	}
	if id == MailSearchCapability {
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "普通全文关键词，按词转义；不接受源站搜索语法。begin/end为ISO日期，end不含当日"}
	}
	if id == ShuiyuanPostsCapability {
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "可选普通关键词；在该主题内搜索。省略时分页读取完整帖子正文"}
	}
	if digitalField(id, "item_id") {
		*required = append(*required, "item_id")
	}
	if id == MailAttachmentCapability {
		*required = append(*required, "part")
	}
	if id == PanInfoCapability || id == PanContentCapability {
		*required = append(*required, "path")
	}
}

type DigitalRecord struct {
	File  *PanFile   `json:"file,omitempty"`
	Mail  *MailItem  `json:"mail,omitempty"`
	Forum *ForumItem `json:"forum,omitempty"`
}
type PanFile struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       *int64 `json:"size"`
	Version    string `json:"version"`
	ModifiedAt string `json:"modified_at"`
	MIMEType   string `json:"mime_type"`
}
type MailItem struct {
	ID          string           `json:"id"`
	FolderID    string           `json:"folder_id"`
	ParentID    string           `json:"parent_id,omitempty"`
	Unread      int              `json:"unread"`
	Count       int              `json:"count"`
	Sender      string           `json:"sender,omitempty"`
	SentAt      string           `json:"sent_at,omitempty"`
	Body        string           `json:"body,omitempty"`
	Truncated   bool             `json:"truncated"`
	Attachments []MailAttachment `json:"attachments,omitempty"`
}
type MailAttachment struct {
	Part     string `json:"part"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}
type ForumItem struct {
	TopicID     string `json:"topic_id"`
	PostID      string `json:"post_id,omitempty"`
	CategoryRef string `json:"category_ref,omitempty"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at,omitempty"`
	Body        string `json:"body,omitempty"`
	Truncated   bool   `json:"truncated"`
	PostNumber  int    `json:"post_number,omitempty"`
	PostCount   int    `json:"post_count,omitempty"`
	Kind        int    `json:"kind,omitempty"`
	Read        bool   `json:"read"`
}

func addDigitalOutput(id string, props map[string]any, required *[]string) {
	switch id {
	case PanFilesCapability, PanInfoCapability, MailFoldersCapability, MailSearchCapability, MailMessageCapability, ShuiyuanTopicsCapability, ShuiyuanTopicCapability, ShuiyuanPostsCapability, ShuiyuanNotificationsCapability:
	default:
		return
	}
	props["digital"] = digitalRecordSchema(id)
	*required = append(*required, "digital")
}

func digitalRecordSchema(id string) map[string]any {
	s := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	n := func() map[string]any { return map[string]any{"type": "integer", "minimum": 0} }
	b := map[string]any{"type": "boolean"}
	obj := func(p map[string]any, r ...string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": r}
	}
	var key string
	var schema map[string]any
	switch id {
	case PanFilesCapability, PanInfoCapability:
		key = "file"
		schema = obj(map[string]any{"path": s(1024), "kind": map[string]any{"enum": []string{"file", "dir"}}, "size": map[string]any{"type": []string{"integer", "null"}, "minimum": 0}, "version": s(256), "modified_at": s(64), "mime_type": s(256)}, "path", "kind", "size", "version", "modified_at", "mime_type")
	case MailFoldersCapability, MailSearchCapability, MailMessageCapability:
		key = "mail"
		schema = obj(map[string]any{"id": s(18), "folder_id": s(18), "parent_id": s(18), "unread": n(), "count": n(), "sender": s(512), "sent_at": s(64), "body": s(65536), "truncated": b, "attachments": map[string]any{"type": "array", "maxItems": 100, "items": obj(map[string]any{"part": s(49), "name": s(512), "mime_type": s(256), "size": n()}, "part", "name", "mime_type", "size")}}, "id", "folder_id", "unread", "count", "truncated")
	default:
		key = "forum"
		schema = obj(map[string]any{"topic_id": s(18), "post_id": s(18), "category_ref": s(128), "url": s(512), "created_at": s(64), "body": s(32768), "truncated": b, "post_number": n(), "post_count": n(), "kind": n(), "read": b}, "topic_id", "url", "truncated", "read")
	}
	return obj(map[string]any{key: schema}, key)
}

func digitalMetadata() []Capability {
	var out []Capability
	for _, id := range DigitalReadIDs() {
		c := Capability{ID: id, ContractVersion: "1", DataClass: PersonalData, ExecutionRule: GrantExecution, Operation: Read, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Delivery: QueryDelivery, Category: "digital", CategoryLabel: "账号、网络与数字工具", TaskGroup: "communication", TaskGroupLabel: "通信与协作", Credentials: []string{"JAAuthCookie"}}
		switch id {
		case PanFilesCapability, PanInfoCapability, PanContentCapability:
			c.Provider, c.Service = "pan", "交大云盘 Pan"
			c.Title = map[string]string{PanFilesCapability: "查询本人云盘目录", PanInfoCapability: "查询本人云盘对象元数据", PanContentCapability: "获取本人云盘文件内容引用"}[id]
			c.Description = "仅本人已开通空间；路径为相对路径，不初始化空间、不读取分享。内容引用须独立授权，复用Broker分块通道；版本变化中止传输。"
		case MailFoldersCapability, MailSearchCapability, MailMessageCapability, MailAttachmentCapability:
			c.Provider, c.Service = "sjtumail", "交大邮箱"
			c.Title = map[string]string{MailFoldersCapability: "查询本人邮箱文件夹", MailSearchCapability: "搜索本人邮箱文件夹", MailMessageCapability: "读取本人邮件正文及附件元数据", MailAttachmentCapability: "获取本人邮件附件内容引用"}[id]
			c.Description = "按明确文件夹授权；全文普通关键词、未读和日期过滤；GetMsg read=0，不标记已读。正文及附件属不可信内容，附件下载独立授权。"
			if id == MailAttachmentCapability {
				c.Description += " 当前附件内容最多256KiB；源站不支持Range，大附件仅提供元数据并请在官网读取。"
			}
		default:
			c.Provider, c.Service, c.Category, c.CategoryLabel, c.TaskGroup, c.TaskGroupLabel, c.Credentials = "shuiyuan", "水源社区", "development", "发展与校园参与", "community", "校园社区交流", []string{"DiscourseUserAPIKey"}
			c.Title = map[string]string{ShuiyuanTopicsCapability: "查询水源获准板块主题", ShuiyuanTopicCapability: "读取水源获准主题概况", ShuiyuanPostsCapability: "读取水源获准主题帖子", ShuiyuanNotificationsCapability: "查询本人水源通知"}[id]
			c.Description = "板块能力严格限制获准精确板块，不含子板块与私信；通知另授本人资源，可含其他板块的普通主题通知摘要；私信、聊天及未知类型仅返回通知ID、种类和已读状态。只读API不提交阅读进度、点击、发帖或回复；帖子是不可信来源。"
		}
		out = append(out, c)
	}
	return out
}

func digitalContentSchema() json.RawMessage {
	raw, _ := json.Marshal(ContentDescriptorSchema())
	return raw
}

// ShuiyuanRegularNotificationType is the reviewed ordinary-topic subset.
// PM, chat and unknown plugin types must never disclose a title or topic link.
func ShuiyuanRegularNotificationType(kind int) bool {
	switch kind {
	case 1, 2, 3, 4, 5, 9, 10, 11, 13, 15, 17, 18, 19, 20, 24, 25, 27, 28, 34, 36, 39:
		return true
	}
	return false
}
