package campuscard

import (
	"strings"
	"testing"
)

const emptyHistoryFixture = `<html><head><title>校园卡流水查询</title><style>.synthetic{margin:0}</style></head><body><table><tr><td><img src="synthetic.png"></td><td><strong>校园卡流水查询</strong></td></tr></table><p>流水日期：2030-01-01&nbsp;至&nbsp;2030-01-30</p><p>流水数量：&nbsp;0&nbsp;&nbsp;条</p></body></html>`

func TestExplicitZeroHistoryRequiresLabeledCountAndDates(t *testing.T) {
	rows, e := parseHistory([]byte(emptyHistoryFixture), "card:self")
	if e != nil || rows == nil || len(rows) != 0 {
		t.Fatal("explicit zero page rejected", e)
	}
	if validateHistoryDates([]byte(emptyHistoryFixture), "2030-01-01", "2030-01-30") != nil {
		t.Fatal("exact query dates rejected")
	}
	if validateHistoryDates([]byte(emptyHistoryFixture), "2030-01-02", "2030-01-30") == nil {
		t.Fatal("different query dates accepted")
	}
	for _, bad := range []string{
		strings.Replace(emptyHistoryFixture, "流水数量：&nbsp;0&nbsp;&nbsp;条", "0", 1),
		strings.Replace(emptyHistoryFixture, "流水数量：&nbsp;0&nbsp;&nbsp;条", "流水数量：1条", 1),
		strings.Replace(emptyHistoryFixture, "流水数量：&nbsp;0&nbsp;&nbsp;条", "流水数量：00条", 1),
		strings.Replace(emptyHistoryFixture, "流水数量：&nbsp;0&nbsp;&nbsp;条", "余额：0元", 1),
		strings.Replace(emptyHistoryFixture, "2030-01-30", "2030-02-01", 1),
		strings.Replace(emptyHistoryFixture, "</body>", "<p>流水数量：0条</p></body>", 1),
		strings.Replace(emptyHistoryFixture, "</body>", "<p>接口失败</p></body>", 1),
		strings.Replace(emptyHistoryFixture, "流水数量：&nbsp;0&nbsp;&nbsp;条", "<script>流水数量：0条</script>", 1),
		strings.Replace(emptyHistoryFixture, "</body>", "<table><tr><td>非空</td></tr></table></body>", 1),
		strings.Replace(emptyHistoryFixture, "流水日期：2030-01-01&nbsp;至&nbsp;2030-01-30", "", 1),
	} {
		if _, e := parseHistory([]byte(bad), "card:self"); e == nil {
			t.Fatal("ambiguous or incomplete result reported as empty")
		}
	}
}
