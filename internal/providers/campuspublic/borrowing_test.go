package campuspublic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func borrowingGuideFixture(readers ...string) string {
	rows := make([][]string, len(readers))
	for i, reader := range readers {
		rows[i] = []string{reader, "99", "21天", "5", "14天", "3", "借阅"}
	}
	return `<section id="section-4"><h3>借阅规则</h3>` + table([]string{"读者类型", "借阅册数", "外借周期", "续借次数", "续借周期", "预约册数", "权限"}, rows...) + `<div><h4><strong> 如何预约图书 </strong></h4><p>示例分馆取书，不含示例专业分馆。<br>处理时间为示例工作日数。<br>假期暂停特定图书预约。<br>到架保留示例期限，逾期取消。</p></div></section><section><p>无关资源内容</p></section>`
}

func borrowingFormalFixture() string {
	return `<html><body><div><p>第一条 <span>教职工及计划内学生</span>凭有效本人证件享有权限。</p><p>证件限本人使用，离校须归还。</p><p>第二条 借阅、预约权限：</p>` + table([]string{"类别", "外借 册数", "外借周期", "续借次数", "续借 周期", "预约册数", "权限", ""}, []string{"教职工 计划内学生", "99", "21天", "5", "14天", "3", "借阅", ""}, []string{"访问人员", "须另行开通相应权限", "借阅", ""}) + `<p>第三条 借阅规定：</p><p>1. 借书：</p><p>普通图书须本人借阅；<span>短期外借</span>另有限制，不能预约。</p><p>2. 还书：</p><p>到期前归还，院系分馆图书不支持异馆还书。</p><p>3. <span>续借</span>：</p><p>到期前办理，借期从续借日计算。</p><p>逾期、已被预约、短期外借不能续借。</p><p>4. 预约：</p><p>需要有效交大邮箱和手机号，收到通知后取书。</p><p>到架保留示例期限（假期除外），逾期自动取消。</p><p>5. 催还：</p><p>收到催还应按期归还，未收到提醒亦须履行义务。</p></div><script>不应返回的脚本</script></body></html>`
}

func TestBorrowingPreservesConditionsAndReaderScope(t *testing.T) {
	items, err := parseBorrowingRules([]byte(borrowingGuideFixture("计划内学生")), []byte(borrowingFormalFixture()))
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	fields := map[string]string{}
	for _, f := range items[0].Fields {
		fields[f.Key] = f.Value
	}
	for key, want := range map[string]string{
		"loan_limit": "99", "loan_period": "21天", "renewal_limit": "5", "renewal_period": "14天",
		"applicability": "限本人使用", "reader_categories": "访问人员", "loan_conditions": "短期外借",
		"return_conditions": "院系分馆", "renewal_conditions": "逾期、已被预约、短期外借不能续借",
		"reservation_conditions": "假期除外", "recall_conditions": "未收到提醒",
		"guide_reservation_conditions": "假期暂停特定图书预约", "rules_source": borrowingRulesURL,
	} {
		if !strings.Contains(fields[key], want) {
			t.Fatalf("%s omitted %q: %q", key, want, fields[key])
		}
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "不应返回的脚本") || strings.Contains(string(raw), "无关资源内容") || strings.Contains(string(raw), "短期外借 另") {
		t.Fatal("unrelated content or broken inline text", string(raw))
	}
}

func TestBorrowingRejectsMissingConditionsAndConflictingPublications(t *testing.T) {
	guide, rules := borrowingGuideFixture("计划内学生"), borrowingFormalFixture()
	for name, pair := range map[string][2]string{
		"changed section":       {strings.ReplaceAll(guide, `id="section-4"`, `id="other"`), rules},
		"missing reservation":   {strings.ReplaceAll(guide, "如何预约图书", "新栏目"), rules},
		"quota mismatch":        {strings.ReplaceAll(guide, "99", "98"), rules},
		"missing renewal":       {guide, strings.ReplaceAll(rules, "3. <span>续借</span>：", "新增说明：")},
		"duplicate group":       {guide, strings.ReplaceAll(rules, "5. 催还：", "3. 续借：")},
		"changed formal quota":  {guide, strings.ReplaceAll(rules, "外借 册数", "新额度标题")},
		"login":                 {guide, rules + `<input type="password">`},
		"other reader category": {borrowingGuideFixture("非学生读者"), rules},
		"duplicate rows":        {borrowingGuideFixture("计划内学生", "计划内学生"), rules},
		"script shell":          {guide, `<html><script>load()</script></html>`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseBorrowingRules([]byte(pair[0]), []byte(pair[1]))
			checkCode(t, err, "UPSTREAM_CHANGED")
		})
	}
}

type borrowingFetch struct {
	pages map[string]transport.Reply
	calls []string
}

func (f *borrowingFetch) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if len(h) != 0 {
		panic("public borrowing query sent headers")
	}
	if err := ctx.Err(); err != nil {
		return transport.Reply{}, err
	}
	f.calls = append(f.calls, u.String())
	return f.pages[u.String()], nil
}

func borrowingProvider(readers ...string) (*Provider, *borrowingFetch) {
	f := &borrowingFetch{pages: map[string]transport.Reply{
		sources["library.borrowing.list"].URL: {Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: []byte(borrowingGuideFixture(readers...))},
		borrowingRulesURL:                     {Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: []byte(borrowingFormalFixture())},
	}}
	return &Provider{clients: map[string]Fetcher{"library.borrowing.list": f, "library.borrowing.rules": f}}, f
}

func TestBorrowingFetchesBothSourcesAndBindsCursorToBoth(t *testing.T) {
	p, f := borrowingProvider("计划内学生本科", "计划内学生研究生")
	first, err := p.listBorrowing(context.Background(), contracts.ListInput{Limit: 1})
	if err != nil || !first.Meta.HasMore || first.Meta.NextCursor == nil || len(first.Meta.Sources) != 2 || len(first.Warnings) != 2 || len(f.calls) != 2 {
		t.Fatal(first, err, f.calls)
	}
	second, err := p.listBorrowing(context.Background(), contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil || second.Meta.HasMore || len(f.calls) != 4 || second.Data.Items[0].Title == first.Data.Items[0].Title {
		t.Fatal(second, err)
	}
	reply := f.pages[borrowingRulesURL]
	reply.Body = []byte(strings.ReplaceAll(string(reply.Body), "未收到提醒", "没有收到提醒"))
	f.pages[borrowingRulesURL] = reply
	_, err = p.listBorrowing(context.Background(), contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	checkCode(t, err, "UPSTREAM_CHANGED")
	before := len(f.calls)
	_, err = p.listBorrowing(context.Background(), contracts.ListInput{Limit: 2, Cursor: *first.Meta.NextCursor})
	checkCode(t, err, "INVALID_ARGUMENT")
	if len(f.calls) != before {
		t.Fatal("mismatched cursor performed network request")
	}
}

func TestBorrowingRequiresBothPublications(t *testing.T) {
	for _, source := range []string{sources["library.borrowing.list"].URL, borrowingRulesURL} {
		for _, status := range []int{302, 403, 429, 500} {
			p, f := borrowingProvider("计划内学生")
			r := f.pages[source]
			r.Status = status
			f.pages[source] = r
			response, err := p.listBorrowing(context.Background(), contracts.ListInput{Limit: 10})
			want := "UPSTREAM_UNAVAILABLE"
			if status == 429 {
				want = "RATE_LIMITED"
			}
			checkCode(t, err, want)
			if len(response.Data.Items) != 0 {
				t.Fatal("partial rules substituted after a source failure")
			}
		}
	}
}

func TestBorrowingRejectsUnsupportedInputsBeforeFetch(t *testing.T) {
	p, f := borrowingProvider("计划内学生")
	for _, in := range []contracts.ListInput{
		{Limit: 0}, {Limit: 101}, {Limit: 1, Query: "query"}, {Limit: 1, CanteenID: "100"},
		{Limit: 1, Day: "today"}, {Limit: 1, ItemID: "arbitrary"},
		{Limit: 1, Cursor: "invalid"}, {Limit: 1, Cursor: strings.Repeat("x", 4097)},
	} {
		_, err := p.listBorrowing(context.Background(), in)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if len(f.calls) != 0 {
		t.Fatal("invalid borrowing input performed a request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.listBorrowing(ctx, contracts.ListInput{Limit: 1})
	if err != context.Canceled || len(f.calls) != 0 {
		t.Fatal(err, f.calls)
	}
}
