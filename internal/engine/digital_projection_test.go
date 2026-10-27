package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDigitalForumViewsBindRefURLAndExcludePostBody(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "shuiyuan:category:7", ItemID: "21"}
	fresh := func() contracts.CampusRecord {
		return contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:post:12", Title: "合成主题", Digital: &contracts.DigitalRecord{Forum: &contracts.ForumItem{TopicID: "21", PostID: "12", PostNumber: 2, CategoryRef: q.ResourceRef, URL: "https://shuiyuan.sjtu.edu.cn/t/21/2", CreatedAt: "2026-10-25T00:00:00Z", Body: "SYNTHETIC_PRIVATE_BODY"}}}
	}
	row, e := projectDigital(contracts.ShuiyuanPostsCapability, q, fresh())
	if e != nil || row.Digital.Forum.Body != "SYNTHETIC_PRIVATE_BODY" {
		t.Fatal("body view", e)
	}
	for _, mutate := range []func(*contracts.CampusRecord){
		func(r *contracts.CampusRecord) { r.Ref = "shuiyuan:post:99" },
		func(r *contracts.CampusRecord) { r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/t/22/2" },
		func(r *contracts.CampusRecord) { r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/admin" },
		func(r *contracts.CampusRecord) { r.Digital.Forum.CategoryRef = "shuiyuan:category:8" },
		func(r *contracts.CampusRecord) {
			r.Digital.Forum.TopicID = "22"
			r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/t/22/2"
		},
	} {
		r := fresh()
		mutate(&r)
		if _, e := projectDigital(contracts.ShuiyuanPostsCapability, q, r); e == nil {
			t.Fatal("foreign post metadata accepted")
		}
	}
	r := fresh()
	r.Ref = "shuiyuan:topic:21"
	r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/t/21"
	for _, id := range []string{contracts.ShuiyuanTopicCapability, contracts.ShuiyuanTopicsCapability} {
		row, e := projectDigital(id, q, r)
		raw, _ := json.Marshal(row)
		if e != nil || strings.Contains(string(raw), "SYNTHETIC_PRIVATE_BODY") || row.Digital.Forum.PostID != "" || row.Digital.Forum.PostNumber != 0 {
			t.Fatal("body leaked through topic metadata", id, e)
		}
	}
}
func TestDigitalPrivateNotificationCannotCarryTopicSummary(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "shuiyuan:notifications:self"}
	for _, kind := range []int{6, 7, 16, 29, 30, 10000} {
		r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:notification:31", Title: "SYNTHETIC_PRIVATE_TITLE", Digital: &contracts.DigitalRecord{Forum: &contracts.ForumItem{Kind: kind, Read: true, TopicID: "21", URL: "https://shuiyuan.sjtu.edu.cn/t/21", Body: "SYNTHETIC_PRIVATE_BODY", PostID: "12", PostNumber: 1}}}
		out, e := projectDigital(contracts.ShuiyuanNotificationsCapability, q, r)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "SYNTHETIC_PRIVATE") || out.Digital.Forum.TopicID != "" || out.Digital.Forum.URL != "" || out.Digital.Forum.PostID != "" || out.Digital.Forum.Kind != kind || !out.Digital.Forum.Read {
			t.Fatal("private notification content escaped", kind, string(raw))
		}
	}
	r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:notification:31", Title: "合成常规摘要", Digital: &contracts.DigitalRecord{Forum: &contracts.ForumItem{Kind: 2, TopicID: "21", URL: "https://shuiyuan.sjtu.edu.cn/t/21"}}}
	out, e := projectDigital(contracts.ShuiyuanNotificationsCapability, q, r)
	if e != nil || out.Title != "合成常规摘要" || out.Digital.Forum.TopicID != "21" {
		t.Fatal("ordinary summary lost", e)
	}
	r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/t/22"
	if _, e := projectDigital(contracts.ShuiyuanNotificationsCapability, q, r); e == nil {
		t.Fatal("forged notification URL")
	}
	r.Digital.Forum.URL = "https://shuiyuan.sjtu.edu.cn/t/21"
	r.Ref = "shuiyuan:topic:31"
	if _, e := projectDigital(contracts.ShuiyuanNotificationsCapability, q, r); e == nil {
		t.Fatal("forged notification ref")
	}
}
func TestDigitalMailHeadersCannotCarryBodyOrAttachments(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "mail:folder:2", ItemID: "12"}
	fresh := func() contracts.CampusRecord {
		return contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "sjtumail:message:12", Title: "合成邮件", Digital: &contracts.DigitalRecord{Mail: &contracts.MailItem{ID: "12", FolderID: "2", Sender: "synthetic@example.invalid", SentAt: "2026-10-25T00:00:00Z", Body: "SYNTHETIC_PRIVATE_BODY", Truncated: true, Attachments: []contracts.MailAttachment{{Part: "2", Name: "SYNTHETIC_ATTACHMENT", MIMEType: "text/plain", Size: 1}}}}}
	}
	header, e := projectDigital(contracts.MailSearchCapability, q, fresh())
	raw, _ := json.Marshal(header)
	if e != nil || strings.Contains(string(raw), "SYNTHETIC_PRIVATE") || strings.Contains(string(raw), "SYNTHETIC_ATTACHMENT") || header.Digital.Mail.Truncated {
		t.Fatal("body escaped mail headers", e, string(raw))
	}
	body, e := projectDigital(contracts.MailMessageCapability, q, fresh())
	if e != nil || body.Digital.Mail.Body != "SYNTHETIC_PRIVATE_BODY" || len(body.Digital.Mail.Attachments) != 1 {
		t.Fatal("authorized body lost", e)
	}
	for _, mutate := range []func(*contracts.CampusRecord){
		func(r *contracts.CampusRecord) { r.Ref = "sjtumail:message:99" },
		func(r *contracts.CampusRecord) { r.Digital.Mail.FolderID = "3" },
		func(r *contracts.CampusRecord) { r.Digital.Mail.Attachments[0].Part = "2../1" },
		func(r *contracts.CampusRecord) { r.Digital.Mail.Attachments[0].MIMEType = "invalid" },
		func(r *contracts.CampusRecord) { r.Digital.Mail.Attachments[0].Size = -1 },
	} {
		r := fresh()
		mutate(&r)
		if _, e := projectDigital(contracts.MailMessageCapability, q, r); e == nil {
			t.Fatal("forged mail object/attachment accepted")
		}
	}
}
