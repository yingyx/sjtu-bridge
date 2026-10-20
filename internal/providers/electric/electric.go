package electric

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

var accountID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var roomID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var bindingCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var decimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,9})?$`)

type envelope struct {
	Errno    *int              `json:"errno"`
	Total    *int              `json:"total"`
	Entities []json.RawMessage `json:"entities"`
}

func decode(r transport.Reply) ([]json.RawMessage, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	d := json.NewDecoder(bytes.NewReader(r.Body))
	d.UseNumber()
	var v envelope
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) || !uniqueJSON(d, 0) || json.Unmarshal(r.Body, &v) != nil || v.Errno == nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected electricity JSON envelope")
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected one electricity JSON envelope")
	}
	if *v.Errno != 0 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Electricity portal rejected the query")
	}
	// These are singleton endpoints. The frontend consumes entities[0]; total
	// is not required by that contract, but a conflicting count is rejected.
	if (v.Total != nil && *v.Total != 1) || len(v.Entities) != 1 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected exactly one electricity identity or balance record")
	}
	return v.Entities, nil
}

// Reject ambiguous duplicate keys before decoding identity, binding or units.
// Fold case because encoding/json also matches struct fields without case.
func uniqueJSON(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, e := d.Token()
	if e != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			s, ok := key.(string)
			s = strings.ToLower(s)
			if e != nil || !ok || seen[s] || !uniqueJSON(d, depth+1) {
				return false
			}
			seen[s] = true
		}
	case '[':
		for d.More() {
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, e := d.Token()
	return e == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}

func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func bound(raw json.RawMessage) (bool, bool) {
	switch string(raw) {
	case "true", "1", `"1"`:
		return true, true
	case "false", "0", `"0"`:
		return false, true
	default:
		return false, false
	}
}

type binding struct{ account, room, code, campus, building string }

func (b binding) accountRef() string  { return "electric:user:" + b.account }
func (b binding) resourceRef() string { return "electric:room:" + b.room }

func (x *session) binding(ctx context.Context) (binding, error) {
	var b binding
	r, e := x.get(ctx, infoURL)
	if e != nil {
		return b, e
	}
	rows, e := decode(r)
	if e != nil {
		return b, e
	}
	var v struct {
		Account  string          `json:"account"`
		IsBind   json.RawMessage `json:"isbind"`
		RoomID   json.RawMessage `json:"roomid"`
		RoomCode json.RawMessage `json:"roomdm"`
		Campus   json.RawMessage `json:"xqdm"`
		Building json.RawMessage `json:"lddm"`
	}
	if json.Unmarshal(rows[0], &v) != nil || !accountID.MatchString(v.Account) {
		return b, contracts.Fail("UPSTREAM_CHANGED", "Electricity account identity is missing or changed")
	}
	active, valid := bound(v.IsBind)
	if !valid {
		return b, contracts.Fail("UPSTREAM_CHANGED", "Electricity room binding state is missing or changed")
	}
	if !active {
		return b, contracts.Fail("NOT_READY", "Bind your own dormitory room manually in the official electricity portal first")
	}
	b = binding{account: v.Account, room: scalar(v.RoomID), code: scalar(v.RoomCode), campus: scalar(v.Campus), building: scalar(v.Building)}
	if !roomID.MatchString(b.room) || !bindingCode.MatchString(b.code) || !bindingCode.MatchString(b.campus) || !bindingCode.MatchString(b.building) {
		return binding{}, contracts.Fail("UPSTREAM_CHANGED", "Electricity bound-room identity is missing or changed")
	}
	return b, nil
}

// Binding is for Broker owner resource discovery. It proves the stable account
// and its server-bound room; it does not enumerate rooms or grant an application.
func (s *Service) Binding(ctx context.Context, secret []byte) (string, string, error) {
	x, e := s.login(ctx, secret)
	if e != nil {
		return "", "", e
	}
	b, e := x.binding(ctx)
	if e != nil {
		return "", "", e
	}
	return b.accountRef(), b.resourceRef(), nil
}

func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	account, _, e := s.Binding(ctx, secret)
	return account, e
}

// Read sends no room selector. The balance endpoint implicitly refers to the
// authenticated user's binding, which is rechecked before and after the read.
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.AccountRef != account || !strings.HasPrefix(account, "electric:user:") || !accountID.MatchString(strings.TrimPrefix(account, "electric:user:")) || !strings.HasPrefix(q.ResourceRef, "electric:room:") || !roomID.MatchString(strings.TrimPrefix(q.ResourceRef, "electric:room:")) || q.View != View || q.Query != "" || q.Limit != 1 || q.Cursor != "" || next != "" {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected one fixed-view bound-room electricity balance")
	}
	x, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	before, e := x.binding(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if before.accountRef() != account || before.resourceRef() != q.ResourceRef {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Electricity account or bound room changed")
	}
	r, e := x.get(ctx, balanceURL)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rows, e := decode(r)
	if e != nil {
		return domain.CampusPage{}, e
	}
	var v struct {
		Remaining json.RawMessage `json:"SYL"`
		Subsidy   json.RawMessage `json:"SYBZ"`
	}
	if json.Unmarshal(rows[0], &v) != nil {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Electricity balance structure changed")
	}
	remaining, subsidy := scalar(v.Remaining), scalar(v.Subsidy)
	if !decimal.MatchString(remaining) || !decimal.MatchString(subsidy) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Electricity degree values are missing or invalid")
	}
	after, e := x.binding(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if before != after {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Electricity account or bound room changed during the read")
	}
	return domain.CampusPage{AccountRef: account, Coverage: "bound_room_balance_only", Rows: []contracts.CampusRecord{{ResourceRef: q.ResourceRef, Ref: "electric:balance:" + before.room, Title: "本人绑定宿舍剩余电量", RemainingKWh: remaining, SubsidyKWh: subsidy}}}, nil
}
