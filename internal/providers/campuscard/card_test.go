package campuscard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const cardFixture = `<table class="table table-condensed"><caption><strong>校园卡余额：&nbsp;12.30 元</strong></caption><tbody><tr><td>卡账号：</td><td>101010</td></tr><tr><td>过渡余额：</td><td>0.00 元</td></tr><tr><td>挂失状态：</td><td>正常</td></tr><tr><td>冻结状态：</td><td>正常</td></tr><tr><td>姓名：</td><td>DO_NOT_RETURN</td></tr><tr><td>银行卡：</td><td>DO_NOT_RETURN</td></tr></tbody></table>`
const historyFixture = `<table class="table table-condensed"><thead><tr><th>日期</th><th>金额</th><th>余额</th></tr></thead><tbody><td><strong>2026-10-25 12:00:00</strong><br>合成系统<br>合成商户</td><td>-2.50</td><td>12.30</td><td><strong>2026-10-24 12:00:00</strong><br>转账</td><td><font>+20</font></td><td>14.80</td><tr><td colspan="2">充值收入：</td><td>20 元</td></tr><tr><td colspan="2">消费支出：</td><td>-2.50 元</td></tr></tbody></table>`
const historyFormFixture = `<form method="post" action="https://weixin.sjtu.edu.cn/xxzx/sjtu-net/ecard/ecardbill.php"><input type="date" name="bDate"><input type="date" name="eDate"><button type="submit" name="searchbill">查询消费流水</button></form>`

type cardStub struct {
	t                   *testing.T
	authed              bool
	historyAuthed       bool
	balance             string
	callbackOnly        bool
	cleanBalanceCalls   int
	cleanHistoryCalls   int
	callbackPath        string
	wrongHistoryPath    bool
	historyQueries      int
	historyContinuation bool
}

func (f *cardStub) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if strings.Contains(h.Get("Cookie"), "JAAuthCookie") && u.Path != "/jaccount/jalogin" {
		f.t.Fatal("root cookie leaked")
	}
	r := transport.Reply{Status: 302, Header: http.Header{}}
	location := ""
	switch {
	case u.Host == Host && (u.Path == balancePath || u.Path == historyPath || u.Path == historyResultPath) && u.Query().Get("code") != "":
		if f.callbackOnly {
			r.Status = http.StatusOK
			r.Header.Set("Content-Type", "text/html; charset=UTF-8")
			r.Body = []byte(f.balance)
			if u.Path == historyPath || u.Path == historyResultPath {
				r.Body = []byte(historyFormFixture)
				if f.historyQueries > 0 {
					r.Body = []byte(historyFixture)
				}
			}
			return r, nil
		}
		if u.Path == balancePath {
			f.authed = true
		} else {
			f.historyAuthed = true
		}
		r.Header.Add("Set-Cookie", "CARDSESSION=SYNTHETIC; Path=/; Secure")
		location = origin + u.Path
	case u.Host == Host && ((u.Path == balancePath && !f.authed) || (u.Path == historyPath && !f.historyAuthed)):
		path := u.Path
		if path == balancePath {
			f.cleanBalanceCalls++
		} else {
			f.cleanHistoryCalls++
			if f.wrongHistoryPath {
				path = balancePath
			}
		}
		location = "https://" + sso.Host + "/oauth2/authorize?" + url.Values{"client_id": {"janicweixin20150709"}, "redirect_uri": {"http://" + Host + path}, "response_type": {"code"}, "scope": {cardScope}, "state": {"4"}}.Encode()
	case u.Host == sso.Host && u.Path == "/oauth2/authorize":
		if u.Query().Get("client_id") != "" {
			callback, _ := url.Parse(u.Query().Get("redirect_uri"))
			f.callbackPath = callback.Path
			location = "https://" + sso.Host + "/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC"
		} else {
			location = "http://" + Host + f.callbackPath + "?code=SYNTHETIC&state=4"
		}
	case u.Host == sso.Host && u.Path == "/jaccount/jalogin":
		location = "https://" + sso.Host + "/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC"
	case u.Host == Host && f.authed:
		if !strings.Contains(h.Get("Cookie"), "CARDSESSION=SYNTHETIC") {
			f.t.Fatal("portal session missing")
		}
		r.Status = 200
		r.Header.Set("Content-Type", "text/html; charset=UTF-8")
		r.Body = []byte(f.balance)
		if u.Path == historyPath {
			r.Body = []byte(historyFormFixture)
		}
		return r, nil
	default:
		f.t.Fatal("unexpected endpoint")
	}
	r.Header.Set("Location", location)
	return r, nil
}
func TestCardConsumesBoundCallbackWithoutCookieSession(t *testing.T) {
	stub := &cardStub{t: t, balance: cardFixture, callbackOnly: true}
	s := &Service{Portal: stub, Auth: stub}
	account, e := s.Identity(context.Background(), []byte("SYNTHETIC_COOKIE"))
	if e != nil || account != "card:account:101010" {
		t.Fatalf("bound callback not consumed: %v", e)
	}
	if stub.cleanBalanceCalls != 1 {
		t.Fatal("one-time callback was discarded and balance requested again")
	}
}

func TestHistoryUsesIndependentBoundCallback(t *testing.T) {
	stub := &cardStub{t: t, balance: cardFixture, callbackOnly: true}
	s := &Service{Portal: stub, Auth: stub}
	q := contracts.CampusQuery{AccountRef: "card:account:101010", ResourceRef: "card:self", View: "card_transactions_v1", Limit: 3}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(page.Rows) != 2 {
		t.Fatalf("independent history callback failed: %v", e)
	}
	if stub.cleanBalanceCalls != 1 || stub.cleanHistoryCalls != 1 {
		t.Fatal("a one-time callback was discarded or history was not independent")
	}
	for _, wrongOwner := range []bool{false, true} {
		stub := &cardStub{t: t, balance: cardFixture, callbackOnly: true, wrongHistoryPath: !wrongOwner}
		s := &Service{Portal: stub, Auth: stub}
		account := q.AccountRef
		if wrongOwner {
			account = "card:account:202020"
		}
		q.AccountRef = account
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), account, q, ""); e == nil {
			t.Fatal("wrong owner or history callback path accepted")
		}
		if wrongOwner && stub.cleanHistoryCalls != 0 {
			t.Fatal("history requested before card owner was verified")
		}
	}
}

func TestCardCallbackBodyMustBeValidHTML(t *testing.T) {
	for _, reply := range []transport.Reply{
		{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(cardFixture)},
		{Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=GBK"}}, Body: []byte(cardFixture)},
		{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte{0xff}},
		{Status: 302, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(cardFixture)},
	} {
		if _, e := htmlReply(reply); e == nil {
			t.Fatal("invalid callback HTML accepted")
		}
	}
}
func (f *cardStub) Post(_ context.Context, u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
	if u.String() != origin+historyPath || h.Get("Content-Type") != "application/x-www-form-urlencoded" || h.Get("Origin") != origin || h.Get("Referer") != origin+historyPath || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("unreviewed card POST or credential destination")
	}
	values, e := url.ParseQuery(string(body))
	begin, beginErr := time.Parse("2006-01-02", values.Get("bDate"))
	end, endErr := time.Parse("2006-01-02", values.Get("eDate"))
	if e != nil || len(values) != 3 || len(values["searchbill"]) != 1 || beginErr != nil || endErr != nil || end.Sub(begin) != 29*24*time.Hour {
		f.t.Fatal("card query is not the fixed 30-date form")
	}
	f.historyQueries++
	if f.historyContinuation {
		return transport.Reply{Status: 302, Header: http.Header{"Location": {"https://" + sso.Host + "/oauth2/authorize?" + url.Values{"client_id": {"janicweixin20150709"}, "redirect_uri": {"http://" + Host + historyResultPath}, "response_type": {"code"}, "scope": {cardScope}, "state": {"4"}}.Encode()}}}, nil
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html; charset=UTF-8"}}, Body: []byte(historyFixture)}, nil
}

func TestHistoryQueryFollowsOneBoundOAuthContinuation(t *testing.T) {
	stub := &cardStub{t: t, balance: cardFixture, callbackOnly: true, historyContinuation: true}
	s := &Service{Portal: stub, Auth: stub}
	q := contracts.CampusQuery{AccountRef: "card:account:101010", ResourceRef: "card:self", View: "card_transactions_v1", Limit: 3}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 2 || stub.historyQueries != 1 || stub.cleanHistoryCalls != 1 {
		t.Fatalf("history continuation failed or query repeated: %v", e)
	}
}
func TestCardReadProjectionAndIdentity(t *testing.T) {
	for _, id := range []string{contracts.CardBalanceCapability, contracts.CardTransactionsCapability} {
		t.Run(id, func(t *testing.T) {
			stub := &cardStub{t: t, balance: cardFixture}
			s := &Service{Portal: stub, Auth: stub}
			rules, _ := contracts.StudentRecordRules(id)
			q := contracts.CampusQuery{AccountRef: "card:account:101010", ResourceRef: "card:self", View: rules.View, Limit: rules.MaxLimit}
			p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
			if e != nil {
				t.Fatal(e)
			}
			if p.AccountRef != q.AccountRef || len(p.Rows) < 1 {
				t.Fatal("missing card records")
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "DO_NOT_RETURN") {
				t.Fatal("bank/name leaked")
			}
			if id == contracts.CardTransactionsCapability && (len(p.Rows) != 2 || p.Rows[0].Student.AmountCNY != "-2.50" || p.Rows[0].Student.OccurredAt != "2026-10-25T12:00:00+08:00") {
				t.Fatal("transaction parsing changed")
			}
		})
	}
}
func TestCardParsingFailuresAndNoWriteURLs(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, strings.Replace(cardFixture, "101010", "OTHER", 1), strings.Replace(cardFixture, "12.30 元", "unknown", 1), cardFixture + cardFixture} {
		if _, _, e := parseBalance([]byte(body)); e == nil {
			t.Fatal("invalid balance accepted")
		}
	}
	for _, body := range []string{`<html>login</html>`, strings.Replace(historyFixture, "2026-10-25 12:00:00", "bad-date", 1), strings.Replace(historyFixture, "-2.50", "oops", 1), strings.Replace(historyFixture, `<td>12.30</td>`, "", 1)} {
		if _, e := parseHistory([]byte(body), "card:self"); e == nil {
			t.Fatal("invalid history accepted")
		}
	}
	for _, raw := range []string{origin + "/xxzx/sjtu-net/ecard/recharge.php", origin + historyPath + "?student=OTHER", strings.Replace(origin+balancePath, "https:", "http:", 1)} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed card URL allowed")
		}
	}
	stub := &cardStub{t: t, balance: cardFixture}
	s := &Service{Portal: stub, Auth: stub}
	q := contracts.CampusQuery{AccountRef: "card:account:OTHER", ResourceRef: "card:self", View: "card_balance_v1", Limit: 1}
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); e == nil {
		t.Fatal("changed card identity accepted")
	}
}
