package canvas

// Protocol sources: Instructure canvas-lms/doc/api/lti_launch_overview.md;
// Uniseem/SJTU-CANVAS-DOWNLOADER engine/src/video/resource.rs (August 2026
// migration), and SJTU-Canvas-Helper client/video.rs. This adapter does not
// infer entitlement from a video ID or accept a caller-supplied launch URL.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

const RecordingHost = "v.sjtu.edu.cn"
const recordingBase = "/jy-application-resourcemanage"
const recordingTool = "8329"
const recordingInitiationPath = "/jy-lti-adapter/lti/canvas/oidc/login-initiation/canvas-record"
const recordingLaunchPath = "/jy-lti-adapter/lti/canvas/launch/canvas-record"

var recordingLaunchAPI = regexp.MustCompile(`^/api/v1/courses/[1-9][0-9]{0,17}/external_tools/sessionless_launch$`)
var recordingLaunchWeb = regexp.MustCompile(`^/courses/[1-9][0-9]{0,17}/external_tools/8329$`)
var recordingSessionlessWeb = regexp.MustCompile(`^/courses/[1-9][0-9]{0,17}/external_tools/sessionless_launch$`)
var transcriptPath = regexp.MustCompile(`^/jy-application-resourcemanage/v1/course/ai/translate/[1-9][0-9]{0,17}$`)

func recordingCanvasAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.ForceQuery {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	if u.Path == "/api/lti/authorize" || u.Path == "/api/lti/authorize_redirect" {
		return recordingAuthorizeQuery(q)
	}
	if recordingLaunchAPI.MatchString(u.Path) {
		return len(q) == 2 && q.Get("id") == recordingTool && q.Get("launch_type") == "course_navigation" && len(q["id"]) == 1 && len(q["launch_type"]) == 1
	}
	// LTI 1.1 sessionless launches use a one-time cached verifier, rather
	// than the regular tool URL's launch_token. The optional session_token
	// is issued by Canvas itself; neither value is accepted from a caller.
	if recordingSessionlessWeb.MatchString(u.Path) {
		if len(q) < 1 || len(q) > 2 || q.Get("verifier") == "" {
			return false
		}
		for key, values := range q {
			if (key != "verifier" && key != "session_token") || len(values) != 1 || values[0] == "" || len(values[0]) > 4096 || strings.ContainsAny(values[0], " \r\n\t") {
				return false
			}
		}
		return true
	}
	if !recordingLaunchWeb.MatchString(u.Path) {
		return false
	}
	// The token is consumed by Canvas before redirecting back to this exact
	// display-only route. recordingSession separately binds that continuation
	// to its original course, tool and short-lived local cookie jar.
	if len(q) == 1 && len(q["display"]) == 1 && q.Get("display") == "borderless" {
		return true
	}
	// Canvas LTI 1.3's course_or_account_launch_link initializes the web
	// session with an API-issued session_token and a borderless display.
	if q.Has("session_token") {
		value := q.Get("session_token")
		return len(q) == 2 && len(q["session_token"]) == 1 && len(q["display"]) == 1 && q.Get("display") == "borderless" && value != "" && len(value) <= 4096 && !strings.ContainsAny(value, " \r\n\t")
	}
	for k, v := range q {
		if len(v) != 1 || len(v[0]) > 4096 {
			return false
		}
		switch k {
		case "launch_token", "display", "borderless", "launch_type":
		default:
			return false
		}
	}
	return q.Get("launch_token") != ""
}

func recordingAuthorizeQuery(q url.Values) bool {
	for key, values := range q {
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 8192 || strings.ContainsAny(values[0], "\x00\r\n") {
			return false
		}
		switch key {
		case "scope", "response_type", "response_mode", "prompt", "client_id", "redirect_uri", "login_hint", "lti_message_hint", "state", "nonce", "target_link_uri", "retried":
		default:
			return false
		}
	}
	count := 10
	if q.Has("target_link_uri") {
		count++
	}
	if q.Has("retried") {
		count++
	}
	return len(q) == count && (!q.Has("retried") || q.Get("retried") == "true") && (!q.Has("target_link_uri") || q.Get("target_link_uri") == "https://"+RecordingHost+recordingInitiationPath) && q.Get("scope") == "openid" && q.Get("response_type") == "id_token" && q.Get("response_mode") == "form_post" && q.Get("prompt") == "none" && q.Get("redirect_uri") == "https://"+RecordingHost+recordingLaunchPath
}

// Canvas's missing_cookie_fix view submits only this named GET form. It may
// retry once with the original OIDC parameters; no consent or login form is
// accepted, and no credential or authentication response is put into a query.
func recordingAuthorizeRetry(raw []byte, base *url.URL, original url.Values) (*url.URL, bool, error) {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return nil, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas recording authorization page")
	}
	var selected *html.Node
	count := 0
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			for _, a := range n.Attr {
				if a.Key == "id" && a.Val == "retry_login" {
					selected = n
					count++
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if count == 0 {
		return nil, false, nil
	}
	fail := func() (*url.URL, bool, error) {
		return nil, true, contracts.Fail("UPSTREAM_CHANGED", "Unreviewed Canvas recording authorization retry")
	}
	if count != 1 || !transport.ExactHTTPS(base, Host) || base.Path != "/api/lti/authorize" {
		return fail()
	}
	action, method := "", "get"
	for _, a := range selected.Attr {
		if a.Key == "action" {
			action = a.Val
		}
		if a.Key == "method" {
			method = strings.ToLower(a.Val)
		}
	}
	target, err := base.Parse(action)
	if err != nil || action == "" || method != "get" || !transport.ExactHTTPS(target, Host) || target.Path != "/api/lti/authorize" || target.RawQuery != "" || target.ForceQuery {
		return fail()
	}
	values := url.Values{}
	valid := true
	var inspect func(*html.Node)
	inspect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, value, kind := "", "", ""
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "value":
					value = a.Val
				case "type":
					kind = strings.ToLower(a.Val)
				}
			}
			if name != "" {
				if kind != "hidden" || values.Has(name) {
					valid = false
				}
				values.Add(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			inspect(c)
		}
	}
	inspect(selected)
	if !valid || values.Get("retried") != "true" || !recordingAuthorizeQuery(values) {
		return fail()
	}
	for _, key := range []string{"scope", "response_type", "response_mode", "prompt", "client_id", "redirect_uri", "login_hint", "lti_message_hint", "state", "nonce"} {
		if values.Get(key) != original.Get(key) {
			return fail()
		}
	}
	target.RawQuery = values.Encode()
	if !recordingCanvasAllowed(target) {
		return fail()
	}
	return target, true, nil
}

// RecordingAllowed is used by the independent fixed-host transport. LTI
// bootstrap is restricted to the resource manager's launch endpoints; no
// Canvas bearer is forwarded here and API callers cannot choose URLs.
func RecordingAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, RecordingHost) || u.ForceQuery {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for _, v := range q {
		if len(v) != 1 {
			return false
		}
	}
	switch u.Path {
	case recordingInitiationPath, recordingLaunchPath:
		return len(q) == 0
	case recordingBase + "/lms/launch", recordingBase + "/lms/launch/", recordingBase + "/lms/launch-context":
		return len(q) == 0
	case recordingBase + "/v1/subject_vod_list_new":
		if len(q) != 6 || !numeric.MatchString(q.Get("teclIds")) || q.Get("page.orders[0].asc") != "false" || q.Get("page.orders[0].field") != "courBeginTime" || q.Get("schoolOpenStatusFlag") != "false" {
			return false
		}
		n, e := strconv.Atoi(q.Get("page.pageIndex"))
		size, f := strconv.Atoi(q.Get("page.pageSize"))
		return e == nil && f == nil && n >= 1 && n <= 10000 && size >= 1 && size <= 100
	case recordingBase + "/v1/course_vod_urls_new":
		return len(q) == 1 && numeric.MatchString(q.Get("courseId"))
	case recordingBase + "/v1/course/ai/ppt":
		return len(q) == 1 && numeric.MatchString(q.Get("courseId"))
	}
	return transcriptPath.MatchString(u.Path) && len(q) == 1 && q.Get("useOriginal") == "true"
}

func recordingJWT(u *url.URL) (string, bool) {
	if u.Scheme != "https" || u.Host != RecordingHost || u.User != nil || u.RawPath != "" || len(u.String()) > 8192 || !strings.HasPrefix(u.Path, recordingBase+"-ui/") {
		return "", false
	}
	q := u.Query()
	if u.Fragment != "" {
		_, query, found := strings.Cut(u.Fragment, "?")
		if found {
			v, e := url.ParseQuery(query)
			if e != nil {
				return "", false
			}
			q = v
		}
	}
	token := q.Get("jwt_token")
	return token, len(token) > 0 && len(token) <= 4096 && !strings.ContainsAny(token, " \r\n\t")
}

func launchForm(raw []byte, base *url.URL) (*url.URL, []byte, error) {
	doc, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		return nil, nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording launch form")
	}
	var action string
	values := url.Values{}
	forms := 0
	invalid := false
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, inForm bool) {
		if n.Type == html.ElementNode && n.Data == "form" {
			forms++
			inForm = true
			for _, a := range n.Attr {
				if a.Key == "action" {
					action = a.Val
				}
				if a.Key == "method" && !strings.EqualFold(a.Val, "post") {
					invalid = true
				}
			}
		}
		if inForm && n.Type == html.ElementNode && n.Data == "input" {
			name, value, kind := "", "", ""
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "value":
					value = a.Val
				case "type":
					kind = a.Val
				}
			}
			if name != "" {
				if kind != "hidden" || len(name) > 128 || len(value) > 8192 || values.Has(name) {
					invalid = true
				}
				values.Set(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, inForm)
		}
	}
	visit(doc, false)
	u, e := base.Parse(action)
	body := []byte(values.Encode())
	validDestination := e == nil && RecordingAllowed(u) && strings.HasPrefix(u.Path, recordingBase+"/lms/launch")
	if e == nil && RecordingAllowed(u) && u.Path == recordingInitiationPath {
		validDestination = base.Host == Host && recordingLaunchWeb.MatchString(base.Path) && validRecordingInitiation(values)
	}
	if e == nil && RecordingAllowed(u) && u.Path == recordingLaunchPath {
		validStorage := !values.Has("lti_storage_target") || (len(values["lti_storage_target"]) == 1 && values.Get("lti_storage_target") == "post_message_forwarding")
		validFields := true
		for key, field := range values {
			if len(field) != 1 || field[0] == "" || strings.ContainsAny(field[0], "\x00\r\n") {
				validFields = false
			}
			switch key {
			case "id_token", "state", "lti_storage_target", "authenticity_token":
			case "utf8":
				validFields = validFields && field[0] == "\u2713"
			default:
				validFields = false
			}
		}
		validDestination = base.Host == Host && (base.Path == "/api/lti/authorize" || base.Path == "/api/lti/authorize_redirect") && validFields && validStorage && values.Get("id_token") != "" && values.Get("state") != ""
		// Canvas uses Rails form_tag, which adds these form metadata fields.
		// They are not LTI response claims; keep the Canvas CSRF token on Canvas.
		values.Del("authenticity_token")
		values.Del("utf8")
		clear(body)
		body = []byte(values.Encode())
	}
	if e != nil || forms != 1 || action == "" || invalid || len(values) == 0 || len(values) > 100 || len(body) > 64<<10 || !validDestination {
		return nil, nil, contracts.Fail("UPSTREAM_CHANGED", "Unreviewed recording launch form")
	}
	return u, body, nil
}

func validRecordingInitiation(values url.Values) bool {
	for key, v := range values {
		if len(v) != 1 || len(v[0]) > 8192 || strings.ContainsAny(v[0], "\x00\r\n") {
			return false
		}
		switch key {
		case "iss", "login_hint", "target_link_uri", "lti_message_hint", "client_id", "canvas_region", "canvas_environment", "lti_deployment_id", "lti_storage_target":
		default:
			return false
		}
	}
	// This is Canvas's configured recommended resource hint, not the OIDC
	// redirect_uri. The actual authentication response route stays separate.
	return (values.Get("iss") == "https://canvas.instructure.com" || values.Get("iss") == "https://"+Host) && values.Get("login_hint") != "" && values.Get("lti_message_hint") != "" && numeric.MatchString(values.Get("client_id")) && values.Get("target_link_uri") == "https://"+RecordingHost+recordingInitiationPath
}

func (s Service) recordingSession(ctx context.Context, p *Provider, course string) (string, string, error) {
	if s.RecordingClient == nil {
		return "", "", contracts.Fail("UPSTREAM_UNAVAILABLE", "Recording transport is unavailable")
	}
	cid := strings.TrimPrefix(course, "canvas:course:")
	u := &url.URL{Scheme: "https", Host: Host, Path: "/api/v1/courses/" + cid + "/external_tools/sessionless_launch", RawQuery: url.Values{"id": {recordingTool}, "launch_type": {"course_navigation"}}.Encode()}
	r, e := p.read(ctx, u)
	if e != nil {
		return "", "", e
	}
	var launch struct {
		ID  json.Number `json:"id"`
		URL string      `json:"url"`
	}
	if strictjson.Decode(r.Body, &launch) != nil || string(launch.ID) != recordingTool {
		return "", "", contracts.Fail("UPSTREAM_CHANGED", "Recording tool launch could not be verified")
	}
	u, e = url.Parse(launch.URL)
	if e != nil || !recordingCanvasAllowed(u) || (u.Path != "/courses/"+cid+"/external_tools/"+recordingTool && u.Path != "/courses/"+cid+"/external_tools/sessionless_launch") || (u.Query().Get("session_token") == "" && u.Query().Get("launch_token") == "" && u.Query().Get("verifier") == "") {
		return "", "", contracts.Fail("UPSTREAM_CHANGED", "Unreviewed sessionless recording launch URL")
	}
	r, e = s.Client.Get(ctx, u, http.Header{"Accept": {"text/html"}})
	if e != nil {
		return "", "", e
	}
	var token string
	// This jar belongs to this one recording launch. It is never attached to
	// the shared metadata client, persisted, or used on the recording host.
	jar, _ := cookiejar.New(nil)
	videoJar, _ := cookiejar.New(nil)
	canContinueSession := u.Query().Get("session_token") != "" && u.Path == "/courses/"+cid+"/external_tools/"+recordingTool
	var oidcStart url.Values
	var oidcQuery url.Values
	oidcState := ""
	oidcFinished := false
	oidcRetried := false
	for step := 0; step < 8; step++ {
		if u.Host == Host {
			jar.SetCookies(u, (&http.Response{Header: r.Header}).Cookies())
		} else if u.Host == RecordingHost {
			videoJar.SetCookies(u, (&http.Response{Header: r.Header}).Cookies())
		}
		if r.Status >= 300 && r.Status < 400 {
			next, e := u.Parse(r.Header.Get("Location"))
			if e != nil {
				return "", "", contracts.Fail("UPSTREAM_CHANGED", "Invalid recording launch redirect")
			}
			if t, ok := recordingJWT(next); ok {
				if oidcStart != nil && !oidcFinished {
					return "", "", contracts.Fail("UPSTREAM_CHANGED", "Recording OIDC launch did not complete its state binding")
				}
				token = t
				break
			}
			if canContinueSession && r.Status == http.StatusFound && transport.ExactHTTPS(next, Host) && !next.ForceQuery && next.Path == u.Path && recordingCanvasAllowed(next) && len(next.Query()) == 1 && next.Query().Get("display") == "borderless" {
				jar.SetCookies(u, (&http.Response{Header: r.Header}).Cookies())
				header := http.Header{"Accept": {"text/html"}}
				request := &http.Request{Header: header}
				cookies := jar.Cookies(next)
				if len(cookies) == 0 || len(cookies) > 32 {
					return "", "", contracts.Fail("AUTH_REQUIRED", "Canvas recording session cookie is unavailable")
				}
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
				if len(header.Get("Cookie")) > 16<<10 {
					return "", "", contracts.Fail("UPSTREAM_CHANGED", "Canvas recording session cookie exceeds its limit")
				}
				canContinueSession = false
				u = next
				r, e = s.Client.Get(ctx, u, header)
				if e != nil {
					return "", "", e
				}
				continue
			}
			if oidcStart != nil && !oidcFinished && recordingCanvasAllowed(next) && (next.Path == "/api/lti/authorize" || next.Path == "/api/lti/authorize_redirect") {
				q := next.Query()
				changed := (q.Has("target_link_uri") && q.Get("target_link_uri") != oidcStart.Get("target_link_uri")) || (q.Has("retried") && !oidcRetried)
				if oidcQuery != nil {
					for key := range oidcQuery {
						if key != "target_link_uri" && q.Get(key) != oidcQuery.Get(key) {
							changed = true
						}
					}
				}
				if changed || q.Get("client_id") != oidcStart.Get("client_id") || q.Get("login_hint") != oidcStart.Get("login_hint") || q.Get("lti_message_hint") != oidcStart.Get("lti_message_hint") || (oidcState != "" && q.Get("state") != oidcState) {
					return "", "", contracts.Fail("PERMISSION_DENIED", "Recording OIDC authorization changed its launch binding")
				}
				if oidcQuery == nil {
					oidcQuery = q
				}
				oidcState = q.Get("state")
				header, err := recordingCookieHeader(jar, next)
				if err != nil {
					return "", "", err
				}
				u = next
				r, e = s.Client.Get(ctx, u, header)
				if e != nil {
					return "", "", e
				}
				continue
			}
			return "", "", contracts.Fail("UPSTREAM_CHANGED", "Recording launch redirect requires review")
		}
		if e = transport.Status(r.Status); e != nil {
			return "", "", e
		}
		if oidcQuery != nil && u.Host == Host && u.Path == "/api/lti/authorize" {
			next, found, err := recordingAuthorizeRetry(r.Body, u, oidcQuery)
			if err != nil {
				return "", "", err
			}
			if found {
				if oidcRetried {
					return "", "", contracts.Fail("UPSTREAM_CHANGED", "Canvas recording authorization retried more than once")
				}
				oidcRetried = true
				header, err := recordingCookieHeader(jar, next)
				if err != nil {
					return "", "", err
				}
				u = next
				r, e = s.Client.Get(ctx, u, header)
				if e != nil {
					return "", "", e
				}
				continue
			}
		}
		next, body, e := launchForm(r.Body, u)
		if e != nil {
			return "", "", e
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			clear(body)
			return "", "", contracts.Fail("UPSTREAM_CHANGED", "Invalid recording OIDC form encoding")
		}
		if next.Path == recordingInitiationPath {
			if oidcStart != nil {
				clear(body)
				return "", "", contracts.Fail("UPSTREAM_CHANGED", "Recording OIDC launch repeated initiation")
			}
			oidcStart = values
		}
		if next.Path == recordingLaunchPath {
			if oidcStart == nil || oidcState == "" || values.Get("state") != oidcState || (values.Has("lti_storage_target") && values.Get("lti_storage_target") != oidcStart.Get("lti_storage_target")) {
				clear(body)
				return "", "", contracts.Fail("PERMISSION_DENIED", "Recording OIDC launch state changed")
			}
			oidcFinished = true
		}
		u = next
		header, err := recordingCookieHeader(videoJar, u)
		if err != nil {
			clear(body)
			return "", "", err
		}
		header.Set("Content-Type", "application/x-www-form-urlencoded")
		r, e = s.RecordingClient.Post(ctx, u, header, body)
		clear(body)
		if e != nil {
			return "", "", e
		}
	}
	if token == "" {
		return "", "", contracts.Fail("AUTH_REQUIRED", "Recording launch did not establish a session")
	}
	data, e := s.recordingRead(ctx, token, recordingBase+"/lms/launch-context", nil)
	if e != nil {
		return "", "", e
	}
	var context struct {
		LaunchType   string `json:"launchType"`
		CanvasRecord struct {
			TeachingClass json.Number `json:"teachingClassId"`
		} `json:"canvasRecord"`
	}
	if strictjson.Decode(data, &context) != nil || context.LaunchType != "CANVAS_RECORD" || !numeric.MatchString(string(context.CanvasRecord.TeachingClass)) {
		return "", "", contracts.Fail("UPSTREAM_CHANGED", "Recording teaching-class binding is unavailable")
	}
	return token, string(context.CanvasRecord.TeachingClass), nil
}

func recordingCookieHeader(jar *cookiejar.Jar, target *url.URL) (http.Header, error) {
	header := http.Header{"Accept": {"text/html"}}
	request := &http.Request{Header: header}
	cookies := jar.Cookies(target)
	if len(cookies) > 32 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Recording session cookie count exceeds its limit")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if len(header.Get("Cookie")) > 16<<10 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Recording session cookie exceeds its limit")
	}
	return header, nil
}

func (s Service) recordingRead(ctx context.Context, token, path string, q url.Values) (json.RawMessage, error) {
	u := &url.URL{Scheme: "https", Host: RecordingHost, Path: path, RawQuery: q.Encode()}
	if !RecordingAllowed(u) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Invalid recording endpoint")
	}
	r, e := s.RecordingClient.Get(ctx, u, http.Header{"Jwt-Token": {token}, "Referer": {"https://" + RecordingHost + recordingBase + "-ui/"}, "Accept": {"application/json"}})
	if e != nil {
		return nil, e
	}
	if e = transport.Status(r.Status); e != nil {
		return nil, e
	}
	var v struct {
		Status json.Number     `json:"status"`
		OK     *bool           `json:"ok"`
		DataOK *bool           `json:"dataOk"`
		Code   string          `json:"code"`
		Data   json.RawMessage `json:"data"`
	}
	if strictjson.Decode(r.Body, &v) != nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording response")
	}
	if v.Code == "LMS_TEACHING_CLASS_NOT_FOUND" {
		return nil, contracts.Fail("NOT_FOUND", "This course has no recording teaching class")
	}
	// The first-party transcript viewer treats a successful null-data response
	// as unavailable text. Keep that case explicit rather than fabricating an
	// empty transcript, and do not generalize it to other APIs or error codes.
	if strings.HasPrefix(path, recordingBase+"/v1/course/ai/translate/") && string(v.Status) == "200" && v.OK != nil && *v.OK && v.DataOK != nil && !*v.DataOK && v.Code == "" && strings.TrimSpace(string(v.Data)) == "null" {
		return nil, contracts.Fail("RESOURCE_UNAVAILABLE", "No original transcript is available for this recording")
	}
	if string(v.Status) != "200" || (v.OK != nil && !*v.OK) || (v.DataOK != nil && !*v.DataOK) || len(v.Data) == 0 || string(v.Data) == "null" || !strings.HasPrefix(strings.TrimSpace(string(v.Data)), "{") {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording response")
	}
	return v.Data, nil
}

func recordingPageQuery(class string, page, limit int) url.Values {
	return url.Values{"page.pageIndex": {strconv.Itoa(page)}, "page.pageSize": {strconv.Itoa(limit)}, "page.orders[0].asc": {"false"}, "page.orders[0].field": {"courBeginTime"}, "teclIds": {class}, "schoolOpenStatusFlag": {"false"}}
}

type recordingRow struct {
	ID            json.Number     `json:"id"`
	ClassID       json.Number     `json:"teclId"`
	Title         string          `json:"subjName"`
	CourseTitle   string          `json:"courName"`
	At            string          `json:"courBeginTime"`
	End           string          `json:"courEndTime"`
	Room          string          `json:"classRoomName"`
	Classroom     string          `json:"clroName"`
	Status        int             `json:"vodStatus"`
	DisplayStatus *int            `json:"vodDisplayStatus"`
	RoomType      *int            `json:"clroType"`
	ClickEnabled  json.RawMessage `json:"vodClickEnable"`
}

func recordingState(row recordingRow) (string, error) {
	if row.DisplayStatus != nil || row.RoomType != nil || len(row.ClickEnabled) != 0 {
		click := string(row.ClickEnabled)
		if click != "" && click != "null" && click != "true" && click != "false" && click != "1" && click != "0" {
			return "", contracts.Fail("UPSTREAM_CHANGED", "Invalid recording playback availability")
		}
		ready := row.RoomType != nil && *row.RoomType == 3
		if row.DisplayStatus != nil {
			ready = *row.DisplayStatus == 3 || *row.DisplayStatus == 5 || *row.DisplayStatus == 6
		}
		if ready && (click == "true" || click == "1") {
			return "ready", nil
		}
		return "unavailable", nil
	}
	if row.Status == 5 {
		return "ready", nil
	}
	if row.Status == 4 {
		return "repairing", nil
	}
	return "unavailable", nil
}

func (s Service) recordingRows(ctx context.Context, token, class string, page, limit int) ([]recordingRow, int, error) {
	raw, e := s.recordingRead(ctx, token, recordingBase+"/v1/subject_vod_list_new", recordingPageQuery(class, page, limit))
	if e != nil {
		return nil, 0, e
	}
	var v struct {
		Records []recordingRow `json:"records"`
		Count   json.Number    `json:"rowCount"`
		Page    json.Number    `json:"pageIndex"`
	}
	if strictjson.Decode(raw, &v) != nil {
		return nil, 0, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording page")
	}
	count, countErr := strconv.Atoi(string(v.Count))
	if countErr != nil || v.Records == nil || len(v.Records) > limit || count < 0 || count > 1000000 || (len(v.Records) > 0 && count < (page-1)*limit+len(v.Records)) || (v.Page != "" && string(v.Page) != strconv.Itoa(page)) {
		return nil, 0, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording page")
	}
	seen := map[string]bool{}
	for _, r := range v.Records {
		if !numeric.MatchString(string(r.ID)) || seen[string(r.ID)] || len(r.Title) > 2048 || len(r.CourseTitle) > 2048 || len(r.Room) > 2048 || len(r.Classroom) > 2048 {
			return nil, 0, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording identity")
		}
		if r.ClassID != "" && string(r.ClassID) != class {
			return nil, 0, contracts.Fail("PERMISSION_DENIED", "Recording response changed the authorized teaching class")
		}
		if _, e := recordingState(r); e != nil {
			return nil, 0, e
		}
		seen[string(r.ID)] = true
	}
	return v.Records, count, nil
}

func (s Service) readRecordings(ctx context.Context, p *Provider, uid, id string, q contracts.CanvasQuery, next string) (domain.CanvasPage, error) {
	out := domain.CanvasPage{Rows: []contracts.CanvasRecord{}}
	token, class, e := s.recordingSession(ctx, p, q.CourseRef)
	if e != nil {
		return out, e
	}
	page := 1
	if next != "" {
		page, e = strconv.Atoi(next)
		if e != nil || page < 2 || page > 10000 || id != "canvas.recordings.list" {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording continuation")
		}
	}
	var rows []recordingRow
	var total int
	if id == "canvas.recordings.list" {
		rows, total, e = s.recordingRows(ctx, token, class, page, q.Limit)
	} else {
		for n := 1; n <= 8; n++ {
			var batch []recordingRow
			batch, total, e = s.recordingRows(ctx, token, class, n, 100)
			if e != nil {
				break
			}
			for _, r := range batch {
				if string(r.ID) == q.ItemID {
					rows = append(rows, r)
				}
			}
			if len(rows) > 0 || n*100 >= total {
				break
			}
			if n == 8 {
				e = contracts.Fail("PARTIAL_RESULT", "Recording ownership lookup exceeds eight pages")
			}
		}
		if e == nil && len(rows) == 0 {
			e = contracts.Fail("NOT_FOUND", "Recording is not in the authorized course")
		}
	}
	if e != nil {
		return out, e
	}
	// Campus recording timestamps use UTC+8; standalone binaries must not
	// depend on the host's IANA zoneinfo installation.
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, r := range rows {
		title, room := r.CourseTitle, r.Classroom
		if title == "" {
			title = r.Title
		}
		if room == "" {
			room = r.Room
		}
		row := contracts.CanvasRecord{CourseRef: q.CourseRef, Ref: "canvas:recording:" + string(r.ID), Title: cleanCanvasText(title), URL: "https://" + Host + "/courses/" + strings.TrimPrefix(q.CourseRef, "canvas:course:"), Location: cleanCanvasText(room)}
		if row.Title == "" {
			row.Title = "课堂录播"
		}
		row.State, _ = recordingState(r)
		for _, v := range []struct {
			raw string
			to  **string
		}{{r.At, &row.StartAt}, {r.End, &row.EndAt}} {
			if v.raw != "" {
				stamp, e := time.ParseInLocation("2006-01-02 15:04:05", v.raw, zone)
				if e != nil {
					return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording time")
				}
				text := stamp.Format(time.RFC3339)
				*v.to = &text
			}
		}
		if id != "canvas.recordings.list" {
			text, count, cut, e := s.recordingText(ctx, token, string(r.ID), id)
			if e != nil {
				return out, e
			}
			row.Body = &text
			row.Count = &count
			row.Truncated = &cut
		}
		out.Rows = append(out.Rows, row)
	}
	if id == "canvas.recordings.list" && page*q.Limit < total {
		if len(rows) == 0 {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Recording pagination made no progress")
		}
		out.Next = strconv.Itoa(page + 1)
	}
	after, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if after != uid {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during recording query")
	}
	return out, nil
}

func (s Service) recordingText(ctx context.Context, token, item, id string) (string, int, bool, error) {
	path := recordingBase + "/v1/course/ai/translate/" + item
	q := url.Values{"useOriginal": {"true"}}
	if id == "canvas.recordings.slides" {
		path = recordingBase + "/v1/course/ai/ppt"
		q = url.Values{"courseId": {item}}
	}
	raw, e := s.recordingRead(ctx, token, path, q)
	if e != nil {
		return "", 0, false, e
	}
	var text strings.Builder
	count := 0
	if id == "canvas.recordings.transcript" {
		var v struct {
			Before []struct {
				Start int64  `json:"bg"`
				End   int64  `json:"ed"`
				Text  string `json:"res"`
			} `json:"beforeAssemblyList"`
			After []struct {
				Text string `json:"res"`
			} `json:"afterAssemblyList"`
		}
		if strictjson.Decode(raw, &v) != nil || (v.Before == nil && v.After == nil) || len(v.Before) > 10000 {
			return "", 0, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording transcript")
		}
		for _, r := range v.Before {
			if r.Start < 0 || r.End < r.Start {
				return "", 0, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcript timing")
			}
			fmt.Fprintf(&text, "[%d–%d ms] %s\n", r.Start, r.End, cleanCanvasText(r.Text))
			count++
		}
		if count == 0 && len(v.After) > 0 {
			return "", 0, false, contracts.Fail("UPSTREAM_CHANGED", "Only alternate transcript is available; original view is unavailable")
		}
	} else {
		var v struct {
			Docs []struct {
				Text     string          `json:"ocrText"`
				Keywords json.RawMessage `json:"textKeywordList"`
			} `json:"docList"`
		}
		if strictjson.Decode(raw, &v) != nil || v.Docs == nil || len(v.Docs) > 5000 {
			return "", 0, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording slides")
		}
		for i, r := range v.Docs {
			count++
			fmt.Fprintf(&text, "[投影片 %d]\n", i+1)
			if r.Text != "" {
				text.WriteString(cleanCanvasText(r.Text))
			} else {
				keywords, err := recordingKeywords(r.Keywords)
				if err != nil {
					return "", 0, false, err
				}
				for _, word := range keywords {
					text.WriteString(cleanCanvasText(word))
					text.WriteByte(' ')
				}
			}
			text.WriteByte('\n')
		}
	}
	body := cleanCanvasText(text.String())
	cut := false
	if len(body) > 32768 {
		body = body[:32768]
		for !utf8.ValidString(body) {
			body = body[:len(body)-1]
		}
		cut = true
	}
	return body, count, cut, nil
}

// The public player exposes textKeywordList as strings, word/text objects,
// or a JSON-encoded list depending on the OCR service version.
func recordingKeywords(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		if !strings.HasPrefix(strings.TrimSpace(encoded), "[") {
			return []string{encoded}, nil
		}
		raw = json.RawMessage(encoded)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) > 10000 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording slide keywords")
	}
	words := []string{}
	for _, item := range items {
		var word string
		if json.Unmarshal(item, &word) == nil {
			words = append(words, word)
			continue
		}
		var object struct {
			Word string `json:"word"`
			Text string `json:"text"`
		}
		if json.Unmarshal(item, &object) != nil || !strings.HasPrefix(strings.TrimSpace(string(item)), "{") {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid recording slide keyword")
		}
		if object.Word != "" {
			words = append(words, object.Word)
		} else {
			words = append(words, object.Text)
		}
	}
	return words, nil
}
