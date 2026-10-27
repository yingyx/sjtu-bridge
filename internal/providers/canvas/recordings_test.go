package canvas

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type recordingClientFunc struct {
	get  func(*url.URL, http.Header) (transport.Reply, error)
	post func(*url.URL, http.Header, []byte) (transport.Reply, error)
}

func (f recordingClientFunc) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f.get(u, h)
}
func (f recordingClientFunc) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	return f.post(u, h, b)
}

func recordingFixture(t *testing.T) Service {
	t.Helper()
	return Service{Client: getterFunc(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if !Allowed(u) {
			t.Fatal("unreviewed Canvas recording URL", u.Path)
		}
		if strings.HasPrefix(u.Path, "/api/") && h.Get("Authorization") != "Bearer SYNTHETIC_CANVAS" {
			t.Fatal("missing API bearer")
		}
		switch u.Path {
		case "/api/v1/users/self/profile":
			return reply(`{"id":123}`), nil
		case "/api/v1/courses/7":
			return reply(`{"id":7,"enrollments":[{"user_id":123,"type":"student","enrollment_state":"active"}]}`), nil
		case "/api/v1/courses/7/external_tools/sessionless_launch":
			return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?launch_token=SYNTHETIC_LAUNCH"}`), nil
		case "/courses/7/external_tools/8329":
			if h.Get("Authorization") != "" {
				t.Fatal("bearer forwarded into LTI launch")
			}
			return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(`<form method="post" action="https://v.sjtu.edu.cn/jy-application-resourcemanage/lms/launch"><input type="hidden" name="oauth_signature" value="SYNTHETIC_SIGNATURE"></form>`)}, nil
		default:
			t.Fatal("unexpected Canvas recording request", u.Path)
			return transport.Reply{}, nil
		}
	}), RecordingClient: recordingClientFunc{post: func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		if !RecordingAllowed(u) || h.Get("Authorization") != "" || !strings.Contains(string(b), "SYNTHETIC_SIGNATURE") {
			t.Fatal("unsafe LTI form exchange")
		}
		return transport.Reply{Status: 302, Header: http.Header{"Location": {"https://v.sjtu.edu.cn/jy-application-resourcemanage-ui/#/lms/launch?jwt_token=SYNTHETIC_RECORDING"}}}, nil
	}, get: func(u *url.URL, h http.Header) (transport.Reply, error) {
		if !RecordingAllowed(u) || h.Get("jwt-token") != "SYNTHETIC_RECORDING" || h.Get("Authorization") != "" || h.Get("Cookie") != "" {
			t.Fatal("unreviewed recording request", u.Path)
		}
		switch u.Path {
		case recordingBase + "/lms/launch-context":
			return reply(`{"status":200,"data":{"launchType":"CANVAS_RECORD","canvasRecord":{"teachingClassId":44}}}`), nil
		case recordingBase + "/v1/subject_vod_list_new":
			if u.Query().Get("teclIds") != "44" {
				t.Fatal("teaching class widened")
			}
			return reply(`{"status":200,"data":{"rowCount":1,"records":[{"id":9,"subjName":"合成录播","courBeginTime":"2030-01-01 08:00:00","courEndTime":"2030-01-01 09:00:00","vodStatus":5}]}}`), nil
		case recordingBase + "/v1/course/ai/translate/9":
			return reply(`{"status":200,"data":{"beforeAssemblyList":[{"bg":0,"ed":1000,"res":"原始字幕 <x>"}],"afterAssemblyList":[]}}`), nil
		case recordingBase + "/v1/course/ai/ppt":
			return reply(`{"status":200,"data":{"docList":[{"ocrText":"合成投影片文字","imageUrl":"https://sv.sjtu.edu.cn/media/slide.png?token=SYNTHETIC_ASSET"}]}}`), nil
		case recordingBase + "/v1/course_vod_urls_new":
			return reply(`{"status":200,"data":{"id":9,"courseVodViewList":[{"url":"https://v.sjtu.edu.cn/media/recording.mp4?token=SYNTHETIC_ASSET"}]}}`), nil
		default:
			t.Fatal("unexpected recording request", u.Path)
			return transport.Reply{}, nil
		}
	}}}
}

func TestRecordingScopedSessionAndMaterials(t *testing.T) {
	s := recordingFixture(t)
	for _, id := range []string{"canvas.recordings.list", "canvas.recordings.transcript", "canvas.recordings.slides"} {
		q := canvasTestQuery(id)
		page, e := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, id, q, []string{q.CourseRef}, "")
		if e != nil || len(page.Rows) != 1 {
			t.Fatal(id, page, e)
		}
		if id == "canvas.recordings.transcript" && (page.Rows[0].Body == nil || !strings.Contains(*page.Rows[0].Body, "<x>")) {
			t.Fatal("transcript was treated as markup")
		}
		if id == "canvas.recordings.slides" && (page.Rows[0].Body == nil || !strings.Contains(*page.Rows[0].Body, "合成投影片文字")) {
			t.Fatal("slide OCR missing")
		}
	}
}

func TestRecordingTimesDoNotRequireZoneinfo(t *testing.T) {
	// Match a standalone deployment with no GOROOT zoneinfo archive and no
	// user-provided zoneinfo, regardless of the machine's local timezone.
	emptyRoot := t.TempDir()
	t.Setenv("ZONEINFO", filepath.Join(emptyRoot, "missing-zoneinfo.zip"))
	t.Setenv("GOROOT", emptyRoot)
	t.Setenv("TZ", "UTC")
	s := recordingFixture(t)
	q := canvasTestQuery("canvas.recordings.list")
	page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
	if err != nil || len(page.Rows) != 1 {
		t.Fatal("recording query failed without zoneinfo", err)
	}
	row := page.Rows[0]
	if row.StartAt == nil || *row.StartAt != "2030-01-01T08:00:00+08:00" || row.EndAt == nil || *row.EndAt != "2030-01-01T09:00:00+08:00" {
		t.Fatal("recording timestamps did not retain the school's UTC+8 offset")
	}
}

func TestRecordingModernAvailabilityAndTeachingClassBinding(t *testing.T) {
	for _, class := range []string{"44", "45"} {
		s := recordingFixture(t)
		base := s.RecordingClient
		s.RecordingClient = recordingClientFunc{post: func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
			return base.Post(context.Background(), u, h, b)
		}, get: func(u *url.URL, h http.Header) (transport.Reply, error) {
			if u.Path == recordingBase+"/v1/subject_vod_list_new" {
				return reply(`{"status":200,"data":{"rowCount":1,"records":[{"id":9,"teclId":` + class + `,"courName":"合成课次","clroName":"合成教室","vodDisplayStatus":5,"vodClickEnable":true}]}}`), nil
			}
			return base.Get(context.Background(), u, h)
		}}
		q := canvasTestQuery("canvas.recordings.list")
		page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
		if class == "44" && (err != nil || len(page.Rows) != 1 || page.Rows[0].State != "ready" || page.Rows[0].Location != "合成教室" || page.Rows[0].Title != "合成课次") {
			t.Fatal("modern recording availability missing", err)
		}
		if class != "44" && err == nil {
			t.Fatal("foreign teaching class accepted")
		}
	}
}

func TestRecordingOfficialSessionlessVerifierLaunch(t *testing.T) {
	s := recordingFixture(t)
	original := s.Client
	s.Client = getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if u.Path == "/api/v1/courses/7/external_tools/sessionless_launch" {
			return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/7/external_tools/sessionless_launch?verifier=SYNTHETIC_VERIFIER&session_token=SYNTHETIC_SESSION"}`), nil
		}
		if u.Path == "/courses/7/external_tools/sessionless_launch" {
			if !Allowed(u) || h.Get("Authorization") != "" || h.Get("Cookie") != "" {
				t.Fatal("sessionless launch inherited credentials or bypassed destination checks")
			}
			legacy, _ := url.Parse("https://oc.sjtu.edu.cn/courses/7/external_tools/8329?launch_token=SYNTHETIC_LAUNCH")
			return original.Get(ctx, legacy, h)
		}
		return original.Get(ctx, u, h)
	})
	q := canvasTestQuery("canvas.recordings.list")
	page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
	if err != nil || len(page.Rows) != 1 {
		t.Fatal(page, err)
	}
	for _, raw := range []string{
		"https://oc.sjtu.edu.cn/courses/7/external_tools/sessionless_launch?verifier=one&verifier=two",
		"https://oc.sjtu.edu.cn/courses/7/external_tools/sessionless_launch?verifier=one&redirect=https://evil.test",
		"https://oc.sjtu.edu.cn/courses/7/external_tools/sessionless_launch?verifier=one&session_token=",
		"https://oc.sjtu.edu.cn/courses/7/external_tools/sessionless_launch?session_token=one",
	} {
		u, _ := url.Parse(raw)
		if recordingCanvasAllowed(u) {
			t.Fatal("unreviewed verifier query accepted")
		}
	}
	foreign := recordingFixture(t)
	base := foreign.Client
	foreign.Client = getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if u.Path == "/api/v1/courses/7/external_tools/sessionless_launch" {
			return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/8/external_tools/sessionless_launch?verifier=SYNTHETIC_FOREIGN"}`), nil
		}
		if strings.HasPrefix(u.Path, "/courses/") {
			t.Fatal("foreign course launch reached transport")
		}
		return base.Get(ctx, u, h)
	})
	if _, err := foreign.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, ""); err == nil {
		t.Fatal("foreign course launch accepted")
	}
}

func TestRecordingOfficialSessionTokenLaunch(t *testing.T) {
	s := recordingFixture(t)
	base := s.Client
	s.Client = getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if u.Path == "/api/v1/courses/7/external_tools/sessionless_launch" {
			return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless&session_token=SYNTHETIC_SESSION"}`), nil
		}
		if u.Path == "/courses/7/external_tools/8329" {
			if !Allowed(u) || h.Get("Authorization") != "" || h.Get("Cookie") != "" {
				t.Fatal("session-token launch inherited root credentials")
			}
			legacy, _ := url.Parse("https://oc.sjtu.edu.cn/courses/7/external_tools/8329?launch_token=SYNTHETIC_LAUNCH")
			return base.Get(ctx, legacy, h)
		}
		return base.Get(ctx, u, h)
	})
	q := canvasTestQuery("canvas.recordings.list")
	page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
	if err != nil || len(page.Rows) != 1 {
		t.Fatal(page, err)
	}
	for _, query := range []string{
		"display=borderless&session_token=",
		"display=borderless&session_token=one&session_token=two",
		"display=unreviewed&session_token=one",
		"display=borderless&session_token=one&redirect=elsewhere",
		"session_token=one",
	} {
		u, _ := url.Parse("https://oc.sjtu.edu.cn/courses/7/external_tools/8329?" + query)
		if recordingCanvasAllowed(u) {
			t.Fatal("unreviewed session-token query accepted")
		}
	}
}

func TestRecordingSessionTokenCookieContinuationIsLocalAndCourseBound(t *testing.T) {
	for _, destination := range []string{
		"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless",
		"https://oc.sjtu.edu.cn/courses/8/external_tools/8329?display=borderless",
		"https://oc.sjtu.edu.cn/courses/7/external_tools/8330?display=borderless",
		"https://v.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless",
		"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless&redirect=elsewhere",
	} {
		s := recordingFixture(t)
		base := s.Client
		launches, continuations := 0, 0
		s.Client = getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
			if strings.HasPrefix(u.Path, "/api/") && h.Get("Cookie") != "" {
				t.Fatal("recording session cookie escaped to metadata client")
			}
			if u.Path == "/api/v1/courses/7/external_tools/sessionless_launch" {
				return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless&session_token=SYNTHETIC_SESSION"}`), nil
			}
			if u.Path == "/courses/7/external_tools/8329" && u.Query().Has("session_token") {
				launches++
				if h.Get("Authorization") != "" || h.Get("Cookie") != "" {
					t.Fatal("another call's cookie or bearer entered a new launch")
				}
				return transport.Reply{Status: http.StatusFound, Header: http.Header{"Location": {destination}, "Set-Cookie": {"CanvasSession=SYNTHETIC_LOCAL; Secure; HttpOnly; Path=/"}}}, nil
			}
			if !strings.HasPrefix(u.Path, "/api/") {
				continuations++
				if !Allowed(u) || u.String() != destination || h.Get("Authorization") != "" || h.Get("Cookie") != "CanvasSession=SYNTHETIC_LOCAL" {
					t.Fatal("unreviewed session continuation")
				}
				legacy, _ := url.Parse("https://oc.sjtu.edu.cn/courses/7/external_tools/8329?launch_token=SYNTHETIC_LAUNCH")
				return base.Get(ctx, legacy, h)
			}
			return base.Get(ctx, u, h)
		})
		originalRecording := s.RecordingClient
		s.RecordingClient = recordingClientFunc{get: func(u *url.URL, h http.Header) (transport.Reply, error) {
			if h.Get("Cookie") != "" {
				t.Fatal("Canvas cookie entered video request")
			}
			return originalRecording.Get(context.Background(), u, h)
		}, post: func(u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
			if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
				t.Fatal("Canvas credentials entered video form exchange")
			}
			return originalRecording.Post(context.Background(), u, h, body)
		}}
		q := canvasTestQuery("canvas.recordings.list")
		allowed := destination == "https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless"
		for attempt := 0; attempt < 2; attempt++ {
			page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
			if allowed && (err != nil || len(page.Rows) != 1) {
				t.Fatal("reviewed cookie continuation failed", err)
			}
			if !allowed && err == nil {
				t.Fatal("foreign or unreviewed continuation accepted")
			}
		}
		if launches != 2 || (allowed && continuations != 2) || (!allowed && continuations != 0) {
			t.Fatal("unexpected continuation count")
		}
	}
}

func TestRecordingOIDCLaunchBindsHintsStateAndSeparateCookieJars(t *testing.T) {
	for _, change := range []string{"", "client", "hint", "state", "redirect", "extra", "redirect_target", "redirect_nonce", "retry", "retry_changed_nonce", "retry_again"} {
		t.Run("change="+change, func(t *testing.T) {
			s := recordingFixture(t)
			canvas, video := s.Client, s.RecordingClient
			query := url.Values{"scope": {"openid"}, "response_type": {"id_token"}, "response_mode": {"form_post"}, "prompt": {"none"}, "client_id": {"1001"}, "redirect_uri": {"https://" + RecordingHost + recordingLaunchPath}, "login_hint": {"SYNTHETIC_HINT"}, "lti_message_hint": {"SYNTHETIC_MESSAGE"}, "state": {"SYNTHETIC_STATE"}, "nonce": {"SYNTHETIC_NONCE"}}
			switch change {
			case "client":
				query.Set("client_id", "1002")
			case "hint":
				query.Set("login_hint", "SYNTHETIC_OTHER")
			case "redirect":
				query.Set("redirect_uri", "https://evil.test/launch")
			case "extra":
				query.Set("extra", "unreviewed")
			}
			auth := &url.URL{Scheme: "https", Host: Host, Path: "/api/lti/authorize", RawQuery: query.Encode()}
			authReads, finalPosts := 0, 0
			s.Client = getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
				if strings.HasPrefix(u.Path, "/api/v1/") && h.Get("Cookie") != "" {
					t.Fatal("Canvas API inherited launch cookie")
				}
				switch u.Path {
				case "/api/v1/courses/7/external_tools/sessionless_launch":
					return reply(`{"id":8329,"url":"https://oc.sjtu.edu.cn/courses/7/external_tools/8329?display=borderless&session_token=SYNTHETIC_SESSION"}`), nil
				case "/courses/7/external_tools/8329":
					if h.Get("Authorization") != "" {
						t.Fatal("web launch inherited bearer")
					}
					if u.Query().Has("session_token") {
						if h.Get("Cookie") != "" {
							t.Fatal("new launch inherited cookie")
						}
						return transport.Reply{Status: 302, Header: http.Header{"Location": {"/courses/7/external_tools/8329?display=borderless"}, "Set-Cookie": {"CanvasSession=SYNTHETIC_CANVAS; Secure; Path=/"}}}, nil
					}
					if h.Get("Cookie") != "CanvasSession=SYNTHETIC_CANVAS" {
						t.Fatal("launch continuation lost session")
					}
					body := `<form method="post" action="https://v.sjtu.edu.cn` + recordingInitiationPath + `"><input type="hidden" name="iss" value="https://canvas.instructure.com"><input type="hidden" name="client_id" value="1001"><input type="hidden" name="login_hint" value="SYNTHETIC_HINT"><input type="hidden" name="lti_message_hint" value="SYNTHETIC_MESSAGE"><input type="hidden" name="target_link_uri" value="https://v.sjtu.edu.cn` + recordingInitiationPath + `"><input type="hidden" name="canvas_region" value="unknown"><input type="hidden" name="lti_storage_target" value="post_message_forwarding"></form>`
					return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(body)}, nil
				case "/api/lti/authorize":
					authReads++
					if h.Get("Authorization") != "" || h.Get("Cookie") != "CanvasSession=SYNTHETIC_CANVAS" || !Allowed(u) {
						t.Fatal("OIDC authorize credentials or route escaped")
					}
					if !u.Query().Has("target_link_uri") && !u.Query().Has("retried") {
						next := *u
						values := next.Query()
						values.Set("target_link_uri", "https://"+RecordingHost+recordingInitiationPath)
						if change == "redirect_target" {
							values.Set("target_link_uri", "https://evil.test/target")
						}
						if change == "redirect_nonce" {
							values.Set("nonce", "SYNTHETIC_CHANGED")
						}
						next.RawQuery = values.Encode()
						return transport.Reply{Status: 302, Header: http.Header{"Location": {next.String()}}}, nil
					}
					if strings.HasPrefix(change, "retry") && (!u.Query().Has("retried") || change == "retry_again") {
						var form strings.Builder
						form.WriteString(`<form id="retry_login" method="get" action="/api/lti/authorize"><input type="hidden" name="retried" value="true">`)
						for key, values := range u.Query() {
							if key == "target_link_uri" || key == "retried" {
								continue
							}
							value := values[0]
							if key == "nonce" && change == "retry_changed_nonce" {
								value = "SYNTHETIC_CHANGED"
							}
							form.WriteString(`<input type="hidden" name="` + key + `" value="` + value + `">`)
						}
						form.WriteString(`</form>`)
						return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(form.String())}, nil
					}
					state := "SYNTHETIC_STATE"
					if change == "state" {
						state = "SYNTHETIC_OTHER"
					}
					body := `<form method="post" action="https://v.sjtu.edu.cn` + recordingLaunchPath + `"><input type="hidden" name="utf8" value="&#x2713;"><input type="hidden" name="authenticity_token" value="SYNTHETIC_CANVAS_CSRF"><input type="hidden" name="id_token" value="SYNTHETIC_SIGNED_LTI"><input type="hidden" name="state" value="` + state + `"><input type="hidden" name="lti_storage_target" value="post_message_forwarding"></form>`
					return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(body)}, nil
				default:
					return canvas.Get(ctx, u, h)
				}
			})
			s.RecordingClient = recordingClientFunc{get: func(u *url.URL, h http.Header) (transport.Reply, error) { return video.Get(context.Background(), u, h) }, post: func(u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
				if !RecordingAllowed(u) || h.Get("Authorization") != "" || strings.Contains(h.Get("Cookie"), "CanvasSession") {
					t.Fatal("Canvas credential forwarded to video host")
				}
				values, err := url.ParseQuery(string(body))
				if err != nil {
					t.Fatal("invalid form")
				}
				if u.Path == recordingInitiationPath {
					if h.Get("Cookie") != "" || !validRecordingInitiation(values) {
						t.Fatal("unreviewed OIDC initiation")
					}
					return transport.Reply{Status: 302, Header: http.Header{"Location": {auth.String()}, "Set-Cookie": {"VideoNonce=SYNTHETIC_VIDEO; Secure; Path=/"}}}, nil
				}
				if u.Path != recordingLaunchPath || h.Get("Cookie") != "VideoNonce=SYNTHETIC_VIDEO" || values.Get("state") != "SYNTHETIC_STATE" || values.Get("id_token") != "SYNTHETIC_SIGNED_LTI" || values.Has("authenticity_token") || values.Has("utf8") {
					t.Fatal("unbound final OIDC launch")
				}
				finalPosts++
				return transport.Reply{Status: 302, Header: http.Header{"Location": {"https://v.sjtu.edu.cn/jy-application-resourcemanage-ui/#/lms/launch?jwt_token=SYNTHETIC_RECORDING"}}}, nil
			}}
			q := canvasTestQuery("canvas.recordings.list")
			page, err := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_CANVAS"), q.AccountRef, "canvas.recordings.list", q, []string{q.CourseRef}, "")
			wantReads := 2
			if change == "retry" {
				wantReads = 3
			}
			if (change == "" || change == "retry") && (err != nil || len(page.Rows) != 1 || authReads != wantReads || finalPosts != 1) {
				t.Fatal("OIDC launch failed", err)
			}
			if change != "" && change != "retry" && (err == nil || finalPosts != 0) {
				t.Fatal("unbound OIDC launch reached video POST")
			}
		})
	}
}

func TestRecordingContentVersionAndSourceURLBinding(t *testing.T) {
	for _, kind := range []string{"video", "slide"} {
		t.Run(kind, func(t *testing.T) {
			s := recordingFixture(t)
			s.MediaClient = contentGetter{bounded: func(u *url.URL, h http.Header, n int) (transport.Reply, error) {
				if !RecordingMediaAllowed(u) || h.Get("Authorization") != "" || h.Get("Cookie") != "" || h.Get("jwt-token") != "" {
					t.Fatal("media inherited root credentials")
				}
				mt := "video/mp4"
				if kind == "slide" {
					mt = "image/png"
				}
				head := http.Header{"Content-Type": {mt}, "Etag": {`"SYNTHETIC_IMMUTABLE"`}}
				if h.Get("Range") == "bytes=0-0" && n == 1 {
					head.Set("Content-Range", "bytes 0-0/4")
					return transport.Reply{Status: 206, Header: head, Body: []byte("d")}, nil
				}
				if h.Get("Range") != "bytes=1-2" || n != 2 || h.Get("If-Match") != `"SYNTHETIC_IMMUTABLE"` {
					t.Fatal("unversioned media range")
				}
				head.Set("Content-Range", "bytes 1-2/4")
				return transport.Reply{Status: 206, Header: head, Body: []byte("at")}, nil
			}}
			scope := contracts.ReadScope{Capability: contracts.CanvasRecordingContent, AccountRef: "canvas:user:123", ResourceRef: "canvas:course:7", View: "content_v1", Limit: 1}
			object := `{"recording_id":"9","media_kind":"` + kind + `","index":1}`
			meta, e := s.DescribeContent(context.Background(), []byte("SYNTHETIC_CANVAS"), scope, object)
			if e != nil || meta.Size != 4 {
				t.Fatal(meta, e)
			}
			chunk, e := s.ReadContent(context.Background(), []byte("SYNTHETIC_CANVAS"), scope, object, meta.Version, 1, 2)
			if e != nil || string(chunk.Data) != "at" {
				t.Fatal(chunk, e)
			}
			if strings.Contains(meta.Name, "SYNTHETIC_ASSET") || strings.Contains(meta.Version, "SYNTHETIC") {
				t.Fatal("asset ticket exposed")
			}
		})
	}
}

func TestRecordingLaunchAndMediaRejectUnreviewedDestinations(t *testing.T) {
	base, _ := url.Parse("https://oc.sjtu.edu.cn/courses/7/external_tools/8329?launch_token=SYNTHETIC")
	for _, body := range []string{`<form method="post" action="https://evil.test/lms/launch"><input type="hidden" name="x" value="y"></form>`, `<form method="post" action="https://v.sjtu.edu.cn/jy-application-resourcemanage/lms/launch"><input type="password" name="x" value="y"></form>`} {
		if _, _, e := launchForm([]byte(body), base); e == nil {
			t.Fatal("unreviewed LTI form accepted")
		}
	}
	for _, raw := range []string{"https://evil.test/file.mp4", "http://sv.sjtu.edu.cn/file.png", "https://sv.sjtu.edu.cn/file.m3u8", "https://sv.sjtu.edu.cn/login", "https://sv.sjtu.edu.cn/file.png?x=a&x=b"} {
		u, _ := url.Parse(raw)
		if RecordingMediaAllowed(u) {
			t.Fatal("unreviewed media accepted", raw)
		}
	}
	for _, raw := range []string{"https://live.sjtu.edu.cn/video.webm", "https://live.sjtu.edu.cn/live/a.m3u8", "https://live.sjtu.edu.cn.evil.test/file.jpg", "https://live.sjtu.edu.cn:8443/file.jpg", "https://live.sjtu.edu.cn/a/../file.jpg", "https://live.sjtu.edu.cn/a/./file.jpg", "https://live.sjtu.edu.cn/a/%2e%2e/file.jpg", "https://live.sjtu.edu.cn/a/%252e%252e/file.jpg"} {
		u, _ := url.Parse(raw)
		if RecordingMediaAllowed(u) {
			t.Fatal("reviewed media host, format or path boundary broadened")
		}
	}
	image, _ := url.Parse("https://live.sjtu.edu.cn/course/image.jpg?token=SYNTHETIC")
	if !RecordingMediaAllowed(image) {
		t.Fatal("reviewed school image host rejected")
	}
	image.Host += ":443"
	if !RecordingMediaAllowed(image) || image.Host != "live.sjtu.edu.cn:443" {
		t.Fatal("explicit default TLS port changed the signed URL or was rejected")
	}
	image, _ = url.Parse("https://live.sjtu.edu.cn//course///image.jpg?token=SYNTHETIC")
	if !RecordingMediaAllowed(image) || image.String() != "https://live.sjtu.edu.cn//course///image.jpg?token=SYNTHETIC" {
		t.Fatal("repeated slashes changed the API-issued URL or were rejected")
	}
	video, _ := url.Parse("https://live.sjtu.edu.cn:443/course/video.mp4?token=SYNTHETIC")
	if !RecordingMediaAllowed(video) || video.String() != "https://live.sjtu.edu.cn:443/course/video.mp4?token=SYNTHETIC" {
		t.Fatal("reviewed direct MP4 track changed the API-issued URL or was rejected")
	}
}

func TestRecordingResponseFormRejectsUnreviewedRailsFields(t *testing.T) {
	base, _ := url.Parse("https://oc.sjtu.edu.cn/api/lti/authorize")
	for _, input := range []string{
		`<input type="hidden" name="utf8" value="wrong">`,
		`<input type="hidden" name="authenticity_token" value="">`,
		`<input type="hidden" name="password" value="SYNTHETIC">`,
		`<input type="hidden" name="_method" value="delete">`,
	} {
		form := `<form method="post" action="https://v.sjtu.edu.cn` + recordingLaunchPath + `"><input type="hidden" name="id_token" value="SYNTHETIC_ID"><input type="hidden" name="state" value="SYNTHETIC_STATE">` + input + `</form>`
		if _, _, err := launchForm([]byte(form), base); err == nil {
			t.Fatal("unreviewed response form field accepted")
		}
	}
}

func TestRecordingResponseAndPageCompleteness(t *testing.T) {
	for _, raw := range []string{
		`{"data":{"rowCount":0,"records":[]}}`,
		`{"status":200,"ok":false,"data":{"rowCount":0,"records":[]}}`,
		`{"status":200,"dataOk":false,"data":{"rowCount":0,"records":[]}}`,
		`{"status":200,"data":{"records":[]}}`,
		`{"status":200,"data":{"rowCount":0,"pageIndex":2,"records":[]}}`,
	} {
		s := recordingFixture(t)
		s.RecordingClient = recordingClientFunc{get: func(_ *url.URL, _ http.Header) (transport.Reply, error) {
			return reply(raw), nil
		}}
		if _, _, err := s.recordingRows(context.Background(), "SYNTHETIC_RECORDING", "44", 1, 20); err == nil {
			t.Fatal("incomplete or failed recording response became empty success")
		}
	}
	for _, raw := range []string{`["one","two"]`, `[{"word":"one"},{"text":"two"}]`, `"[\"one\",\"two\"]"`} {
		words, err := recordingKeywords([]byte(raw))
		if err != nil || strings.Join(words, " ") != "one two" {
			t.Fatal("documented slide keyword shape rejected", err)
		}
	}
}

func TestRecordingStreamingMediaHasExplicitUnsupportedBoundary(t *testing.T) {
	for _, location := range []string{"https://v.sjtu.edu.cn/media/video.m3u8?token=SYNTHETIC", "https://mss2.sjtu.edu.cn/media/video.m3u8?token=SYNTHETIC", "https://evil.test/video.m3u8"} {
		s := recordingFixture(t)
		base := s.RecordingClient
		s.RecordingClient = recordingClientFunc{post: func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
			return base.Post(context.Background(), u, h, b)
		}, get: func(u *url.URL, h http.Header) (transport.Reply, error) {
			if u.Path == recordingBase+"/v1/course_vod_urls_new" {
				return reply(`{"status":200,"data":{"id":9,"courseVodViewList":[{"url":"` + location + `"}]}}`), nil
			}
			return base.Get(context.Background(), u, h)
		}}
		s.MediaClient = contentGetter{bounded: func(_ *url.URL, _ http.Header, _ int) (transport.Reply, error) {
			t.Fatal("unsupported streaming URL reached media transport")
			return transport.Reply{}, nil
		}}
		scope := contracts.ReadScope{Capability: contracts.CanvasRecordingContent, AccountRef: "canvas:user:123", ResourceRef: "canvas:course:7", View: "content_v1", Limit: 1}
		_, err := s.DescribeContent(context.Background(), []byte("SYNTHETIC_CANVAS"), scope, `{"recording_id":"9","media_kind":"video","index":1}`)
		want := "UNSUPPORTED"
		if strings.Contains(location, "evil.test") {
			want = "UPSTREAM_CHANGED"
		}
		if err == nil || err.(*contracts.Error).Code != want {
			t.Fatal("streaming boundary returned wrong result", err)
		}
	}
}
