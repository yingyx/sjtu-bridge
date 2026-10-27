package campuscard

import (
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestOnlyKnownHistoryQueryFormIsSubmitted(t *testing.T) {
	payload, e := queryForm([]byte(historyFormFixture), "2026-09-28", "2026-10-27")
	if e != nil {
		t.Fatal(e)
	}
	q, e := url.ParseQuery(string(payload))
	if e != nil || len(q) != 3 || q.Get("bDate") != "2026-09-28" || q.Get("eDate") != "2026-10-27" || len(q["searchbill"]) != 1 || q.Get("searchbill") != "" {
		t.Fatal("fixed query form or button default value changed")
	}
	for _, equivalent := range []string{"ecardbill.php", "./ecardbill.php", historyPath} {
		form := strings.Replace(historyFormFixture, origin+historyPath, equivalent, 1)
		if _, e := queryForm([]byte(form), "2026-09-28", "2026-10-27"); e != nil {
			t.Fatal("equivalent relative query form rejected")
		}
	}
	for _, raw := range []string{
		strings.Replace(historyFormFixture, `method="post"`, `method="get"`, 1),
		strings.Replace(historyFormFixture, origin+historyPath, origin+"/xxzx/sjtu-net/ecard/recharge.php", 1),
		strings.Replace(historyFormFixture, `name="eDate"`, `name="bDate"`, 1),
		strings.Replace(historyFormFixture, `type="date"`, `type="hidden"`, 1),
		strings.Replace(historyFormFixture, `</form>`, `<input type="hidden" name="card" value="OTHER"></form>`, 1),
		strings.Replace(historyFormFixture, `name="searchbill"`, `name="recharge"`, 1),
		strings.Replace(historyFormFixture, `name="searchbill"`, `name="searchbill" formaction="https://other.invalid/write"`, 1),
		historyFormFixture + historyFormFixture,
	} {
		if _, e := queryForm([]byte(raw), "2026-09-28", "2026-10-27"); e == nil {
			t.Fatal("unreviewed card form accepted")
		}
	}
	for _, target := range []string{"", "recharge.php", "../ecardbalance.php", "//other.invalid/ecardbill.php", "ecardbill.php?card=OTHER", "ecardbill.php?", "ecardbill.php#other", "https://other@weixin.sjtu.edu.cn/xxzx/sjtu-net/ecard/ecardbill.php", "http://weixin.sjtu.edu.cn/xxzx/sjtu-net/ecard/ecardbill.php"} {
		form := strings.Replace(historyFormFixture, origin+historyPath, target, 1)
		if _, e := queryForm([]byte(form), "2026-09-28", "2026-10-27"); e == nil {
			t.Fatal("unreviewed relative or authenticated target accepted")
		}
	}
}

func TestHistoryTableRequiresReviewedHeader(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(historyFixture, `<th>金额</th>`, `<th>其他</th>`, 1),
		strings.Replace(historyFixture, `<thead><tr><th>日期</th><th>金额</th><th>余额</th></tr></thead>`, "", 1),
		strings.Replace(historyFixture, `<th>金额</th>`, "", 1),
	} {
		if _, e := parseHistory([]byte(raw), "card:self"); e == nil {
			t.Fatal("unreviewed history table accepted")
		}
	}
}

func TestHistoryBannerAndDateEchoAreNotEmptyResults(t *testing.T) {
	// Synthetic banner and numeric echoes intentionally contain no result
	// table or verified empty marker. Their presence cannot imply no spending.
	body := []byte(`<html><body><table><tr><td><img src="card.png"></td><td><strong>校园卡账单查询</strong></td></tr></table><p>2026-01-01 2026-01-30</p><p>0</p></body></html>`)
	rows, e := parseHistory(body, "card:self")
	failure, ok := e.(*contracts.Error)
	if !ok || failure.Code != "UPSTREAM_CHANGED" || rows != nil || !strings.Contains(failure.Message, "query completed but result table or explicit empty marker unavailable") {
		t.Fatal("unverified result page was reported as an empty query")
	}
}
