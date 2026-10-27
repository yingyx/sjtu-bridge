package campuscard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
)

var cardIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
var moneyPattern = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,2})?$`)
var historyCount = regexp.MustCompile(`^流水数量：(0|[1-9][0-9]{0,3})条$`)
var historyDates = regexp.MustCompile(`^流水日期：([0-9]{4}-[0-9]{2}-[0-9]{2})至([0-9]{4}-[0-9]{2}-[0-9]{2})$`)

// Summaries are individual rendered text nodes. Decode entities through the
// HTML parser and remove only Unicode spacing, never infer a count from an
// unlabelled number or a balance. A partial/duplicate summary is not trusted.
func historySummary(body []byte) (count int, begin, end string, present bool, err error) {
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		err = changed()
		return
	}
	counts, dates := 0, 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			v := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return -1
				}
				return r
			}, n.Data)
			if strings.HasPrefix(v, "流水数量") {
				present = true
				counts++
				m := historyCount.FindStringSubmatch(v)
				if m == nil {
					err = changed()
				} else {
					count, _ = strconv.Atoi(m[1])
				}
			}
			if strings.HasPrefix(v, "流水日期") {
				present = true
				dates++
				m := historyDates.FindStringSubmatch(v)
				if m == nil {
					err = changed()
				} else {
					begin, end = m[1], m[2]
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if !present {
		return
	}
	b, be := time.Parse("2006-01-02", begin)
	z, ze := time.Parse("2006-01-02", end)
	if counts != 1 || dates != 1 || be != nil || ze != nil || z.Before(b) || z.Sub(b) > 29*24*time.Hour || count > 2000 {
		err = changed()
	}
	return
}

func validateHistoryDates(body []byte, begin, end string) error {
	_, b, z, present, e := historySummary(body)
	if e != nil {
		return e
	}
	if present && (b != begin || z != end) {
		return changed()
	}
	return nil
}

func emptyHistory(body []byte) bool {
	count, _, _, present, e := historySummary(body)
	if e != nil || !present || count != 0 {
		return false
	}
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return false
	}
	// The reviewed zero-result page has only its two-cell header table. An
	// unrecognized data table, form, or embedded content is not empty. Scripts
	// and styles are not rendered evidence and are never executed.
	if len(elements(root, "table")) != 1 || len(elements(root, "tr")) != 1 || len(elements(root, "td")) != 2 || len(elements(root, "th")) != 0 || len(elements(root, "form")) != 0 || len(elements(root, "iframe")) != 0 {
		return false
	}
	titles := elements(root, "title")
	if len(titles) != 1 || text(titles[0]) != "校园卡流水查询" {
		return false
	}
	valid := true
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "style" || n.Data == "script" {
				return
			}
		}
		if n.Type == html.TextNode {
			v := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return -1
				}
				return r
			}, n.Data)
			if v != "" && v != "校园卡流水查询" && !historyCount.MatchString(v) && !historyDates.MatchString(v) {
				valid = false
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return valid
}

func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Campus card page structure changed") }
func elements(root *html.Node, tag string) []*html.Node {
	out := []*html.Node{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.ElementNode && n.Data == tag {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
func table(body []byte) (*html.Node, error) {
	root, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return nil, changed()
	}
	var chosen *html.Node
	for _, n := range elements(root, "table") {
		if strings.Contains(" "+attr(n, "class")+" ", " table-condensed ") {
			if chosen != nil {
				return nil, changed()
			}
			chosen = n
		}
	}
	if chosen == nil {
		return nil, changed()
	}
	return chosen, nil
}
func amount(raw string) (string, error) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " ")), "元"))
	s = strings.TrimPrefix(s, "+")
	if !moneyPattern.MatchString(s) {
		return "", changed()
	}
	return s, nil
}

func parseBalance(body []byte) (*contracts.StudentRecord, string, error) {
	t, e := table(body)
	if e != nil {
		return nil, "", e
	}
	captions := elements(t, "caption")
	if len(captions) != 1 {
		return nil, "", changed()
	}
	caption := text(captions[0])
	if !strings.HasPrefix(caption, "校园卡余额：") {
		return nil, "", changed()
	}
	balance, e := amount(strings.TrimPrefix(caption, "校园卡余额："))
	if e != nil {
		return nil, "", e
	}
	out := &contracts.StudentRecord{BalanceCNY: balance}
	cardID := ""
	seen := map[string]bool{}
	for _, row := range elements(t, "tr") {
		cells := elements(row, "td")
		if len(cells) != 2 {
			continue
		}
		label, value := text(cells[0]), text(cells[1])
		if !contracts.ValidStudentRecordText(value) {
			return nil, "", changed()
		}
		switch label {
		case "卡账号：", "过渡余额：", "挂失状态：", "冻结状态：":
			if seen[label] {
				return nil, "", changed()
			}
			seen[label] = true
		default:
			continue
		}
		switch label {
		case "卡账号：":
			cardID = value
		case "过渡余额：":
			out.TransitionBalanceCNY, e = amount(value)
			if e != nil {
				return nil, "", e
			}
		case "挂失状态：":
			out.LostStatus = value
		case "冻结状态：":
			out.FrozenStatus = value
		}
	}
	if !cardIDPattern.MatchString(cardID) {
		return nil, "", changed()
	}
	return out, cardID, nil
}

func parseHistory(body []byte, resource string) ([]contracts.CampusRecord, error) {
	t, e := table(body)
	if e != nil {
		if emptyHistory(body) {
			return []contracts.CampusRecord{}, nil
		}
		// Successful authentication and a completed date query do not prove
		// that an incomplete banner page represents an empty transaction list.
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Campus card query completed but result table or explicit empty marker unavailable")
	}
	heads := elements(t, "thead")
	if len(heads) != 1 {
		return nil, changed()
	}
	headers := elements(heads[0], "th")
	if len(headers) != 3 {
		return nil, changed()
	}
	dateLabel, amountLabel, balanceLabel := text(headers[0]), text(headers[1]), text(headers[2])
	if dateLabel != "日期" && dateLabel != "消费日期·地点" || amountLabel != "金额" && amountLabel != "交易额" || balanceLabel != "余额" && balanceLabel != "卡余额" {
		return nil, changed()
	}
	// x/net/html repairs the site's bare tbody td sequence into rows. Reading
	// the td stream in source order also handles that repaired structure.
	bodies := elements(t, "tbody")
	if len(bodies) != 1 {
		return nil, changed()
	}
	cells := elements(bodies[0], "td")
	out := []contracts.CampusRecord{}
	for i := 0; i < len(cells); {
		cell := cells[i]
		if attr(cell, "colspan") != "" {
			label := text(cell)
			if strings.HasPrefix(label, "充值收入") || strings.HasPrefix(label, "消费支出") {
				if i+1 >= len(cells) {
					return nil, changed()
				}
				if _, e := amount(text(cells[i+1])); e != nil {
					return nil, e
				}
				i += 2
			} else if label == "" {
				i++
			} else {
				return nil, changed()
			}
			continue
		}
		anchors := elements(cell, "strong")
		if len(anchors) == 0 {
			// Only the two known footer labels and their value are skipped.
			label := text(cell)
			if label == "" || strings.HasPrefix(label, "充值收入") || strings.HasPrefix(label, "消费支出") {
				i++
				if label != "" && i < len(cells) {
					i++
				}
				continue
			}
			return nil, changed()
		}
		if len(anchors) != 1 || i+2 >= len(cells) || attr(cells[i+1], "colspan") != "" || attr(cells[i+2], "colspan") != "" {
			return nil, changed()
		}
		at, e := time.ParseInLocation("2006-01-02 15:04:05", text(anchors[0]), time.FixedZone("Asia/Shanghai", 8*60*60))
		if e != nil {
			return nil, changed()
		}
		value, e := amount(text(cells[i+1]))
		if e != nil {
			return nil, e
		}
		balance, e := amount(text(cells[i+2]))
		if e != nil {
			return nil, e
		}
		parts := []string{}
		for _, part := range strings.Split(text(cell), "\n") {
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) < 1 || len(parts) > 3 {
			return nil, changed()
		}
		view := &contracts.StudentRecord{AmountCNY: value, BalanceCNY: balance, OccurredAt: at.Format(time.RFC3339)}
		if len(parts) > 1 {
			view.System = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			view.Merchant = strings.TrimSpace(parts[2])
		}
		if !contracts.ValidStudentRecordText(view.System) || !contracts.ValidStudentRecordText(view.Merchant) {
			return nil, changed()
		}
		row := contracts.CampusRecord{ResourceRef: resource, Title: "校园卡交易", Student: view}
		raw, _ := json.Marshal(row)
		sum := sha256.Sum256(append(raw, []byte(":"+strconv.Itoa(len(out)))...))
		row.Ref = "card:transaction:" + hex.EncodeToString(sum[:])
		out = append(out, row)
		if len(out) > 2000 {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Campus card history exceeds the reviewed bound")
		}
		i += 3
	}
	count, _, _, present, e := historySummary(body)
	if e != nil || present && count != len(out) {
		return nil, changed()
	}
	return out, nil
}
