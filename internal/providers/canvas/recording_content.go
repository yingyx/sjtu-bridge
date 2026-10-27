package canvas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type recordingContentObject struct {
	RecordingID string `json:"recording_id"`
	Kind        string `json:"media_kind"`
	Index       int    `json:"index"`
}
type boundedMedia interface {
	GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
}

// sv is the public frontend's IMAGE_SERVICE_URL (global-production.json).
// The course PPT API also issues live.sjtu.edu.cn images, as confirmed by
// the school's response and public Canvas integrations. Its API-issued MP4
// tracks are accepted too; HLS and live stream endpoints stay unsupported.
// Additional storage hosts require explicit review; a URL embedded in content
// or supplied by an application is never a media destination.
func RecordingMediaAllowed(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := u.Hostname()
	if (host != RecordingHost && host != "sv.sjtu.edu.cn" && host != "live.sjtu.edu.cn") || (u.Host != host && u.Host != host+":443") || !transport.ExactHTTPS(u, u.Host) || u.ForceQuery || !strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\%\x00\r\n") {
		return false
	}
	// The PPT API issues image paths with repeated slashes. Preserve those
	// exact signed bytes, while refusing traversal or encoded path variants.
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	case ".mp4", ".webm", ".mp3", ".m4a", ".aac":
		if host == "live.sjtu.edu.cn" && strings.ToLower(path.Ext(u.Path)) != ".mp4" {
			return false
		}
	default:
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) > 20 {
		return false
	}
	for _, values := range q {
		if len(values) != 1 || len(values[0]) > 4096 {
			return false
		}
	}
	return true
}

func (s Service) mediaTransport(u *url.URL) (boundedMedia, func()) {
	if s.MediaClient != nil {
		return s.MediaClient, func() {}
	}
	target := u.String()
	c := transport.NewSecure(u.Hostname(), func(v *url.URL) bool { return RecordingMediaAllowed(v) && v.String() == target })
	return c, c.Close
}

func (s Service) recordingContentTarget(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (*Provider, *url.URL, recordingContentObject, error) {
	var key recordingContentObject
	bad := func(e error) (*Provider, *url.URL, recordingContentObject, error) { return nil, nil, key, e }
	if scope.Capability != contracts.CanvasRecordingContent || scope.View != "content_v1" || !contracts.CanvasCourseRef.MatchString(scope.ResourceRef) || strictjson.Decode([]byte(object), &key) != nil || !numeric.MatchString(key.RecordingID) || (key.Kind != "video" && key.Kind != "slide") || key.Index < 1 || key.Index > 5000 {
		return bad(contracts.Fail("PERMISSION_DENIED", "Invalid recording content scope"))
	}
	p := s.provider(secret)
	fail := func(e error) (*Provider, *url.URL, recordingContentObject, error) { p.Close(); return bad(e) }
	uid, e := p.identity(ctx)
	if e != nil {
		return fail(e)
	}
	if scope.AccountRef != "canvas:user:"+uid {
		return fail(contracts.Fail("PERMISSION_DENIED", "Canvas identity changed"))
	}
	cid := strings.TrimPrefix(scope.ResourceRef, "canvas:course:")
	cu := &url.URL{Scheme: "https", Host: Host, Path: "/api/v1/courses/" + cid}
	r, e := p.read(ctx, cu)
	if e != nil {
		return fail(e)
	}
	var course course
	if strictjson.Decode(r.Body, &course) != nil || string(course.ID) != cid || !owned(course, uid) {
		return fail(contracts.Fail("PERMISSION_DENIED", "Recording course ownership could not be verified"))
	}
	token, class, e := s.recordingSession(ctx, p, scope.ResourceRef)
	if e != nil {
		return fail(e)
	}
	found := false
	for page := 1; page <= 8; page++ {
		rows, total, e := s.recordingRows(ctx, token, class, page, 100)
		if e != nil {
			return fail(e)
		}
		for _, row := range rows {
			if string(row.ID) == key.RecordingID {
				found = true
			}
		}
		if found || page*100 >= total {
			break
		}
		if page == 8 {
			return fail(contracts.Fail("PARTIAL_RESULT", "Recording ownership lookup exceeds eight pages"))
		}
	}
	if !found {
		return fail(contracts.Fail("PERMISSION_DENIED", "Recording is not in the authorized course"))
	}
	var raw json.RawMessage
	var location string
	if key.Kind == "video" {
		raw, e = s.recordingRead(ctx, token, recordingBase+"/v1/course_vod_urls_new", url.Values{"courseId": {key.RecordingID}})
		if e != nil {
			return fail(e)
		}
		var data struct {
			ID    json.Number `json:"id"`
			Views []struct {
				URL     string `json:"url"`
				PlayURL string `json:"playUrl"`
				HDV     string `json:"rtmpUrlHdv"`
			} `json:"courseVodViewList"`
		}
		if strictjson.Decode(raw, &data) != nil || string(data.ID) != key.RecordingID || len(data.Views) > 100 || key.Index > len(data.Views) {
			return fail(contracts.Fail("NOT_FOUND", "Recording video track is unavailable"))
		}
		v := data.Views[key.Index-1]
		location = v.URL
		if location == "" {
			location = v.PlayURL
		}
		if location == "" {
			location = v.HDV
		}
	} else {
		raw, e = s.recordingRead(ctx, token, recordingBase+"/v1/course/ai/ppt", url.Values{"courseId": {key.RecordingID}})
		if e != nil {
			return fail(e)
		}
		var data struct {
			Docs []struct {
				ImageURL string `json:"imageUrl"`
				PPTURL   string `json:"pptImgUrl"`
				URL      string `json:"url"`
			} `json:"docList"`
		}
		if strictjson.Decode(raw, &data) != nil || len(data.Docs) > 5000 || key.Index > len(data.Docs) {
			return fail(contracts.Fail("NOT_FOUND", "Recording slide image is unavailable"))
		}
		v := data.Docs[key.Index-1]
		location = v.ImageURL
		if location == "" {
			location = v.PPTURL
		}
		if location == "" {
			location = v.URL
		}
	}
	u, e := url.Parse(location)
	if e != nil || !RecordingMediaAllowed(u) {
		if e == nil && key.Kind == "video" && u.Scheme == "https" && regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}\.sjtu\.edu\.cn$`).MatchString(u.Hostname()) {
			switch strings.ToLower(path.Ext(u.Path)) {
			case ".m3u8", ".mpd", ".flv":
				return fail(contracts.Fail("UNSUPPORTED", "This recording uses streaming media; bounded direct-file content does not support HLS, DASH or live streams"))
			}
		}
		return fail(contracts.Fail("UPSTREAM_CHANGED", "Recording media destination or format requires review"))
	}
	return p, u, key, nil
}

type mediaInfo struct {
	Object domain.ContentObject
	ETag   string
}

func recordingMediaHeaders(u *url.URL, byteRange string) http.Header {
	h := http.Header{"Range": {byteRange}, "Accept-Encoding": {"identity"}}
	if u.Hostname() == "live.sjtu.edu.cn" {
		h.Set("Accept", "image/jpeg,image/png,image/gif,image/webp")
		if strings.EqualFold(path.Ext(u.Path), ".mp4") {
			h.Set("Accept", "video/mp4")
		}
		h.Set("Referer", "https://v.sjtu.edu.cn/jy-application-resourcemanage-ui/")
	}
	return h
}

func probeMedia(ctx context.Context, c boundedMedia, u *url.URL, key recordingContentObject) (mediaInfo, error) {
	var out mediaInfo
	r, e := c.GetBounded(ctx, u, recordingMediaHeaders(u, "bytes=0-0"), 1)
	if e != nil {
		return out, e
	}
	if r.Status != http.StatusPartialContent || len(r.Body) != 1 || r.Header.Get("Content-Encoding") != "" {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording media does not support bounded ranges")
	}
	var size int64
	if _, e = fmt.Sscanf(r.Header.Get("Content-Range"), "bytes 0-0/%d", &size); e != nil || r.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-0/%d", size) || size < 1 || size > contracts.MaxContentBytes {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording media size is unavailable or exceeds limit")
	}
	etag := r.Header.Get("ETag")
	if len(etag) < 3 || len(etag) > 256 || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || strings.HasPrefix(etag, "W/") || strings.ContainsAny(etag, "\r\n") {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording media has no immutable transfer version")
	}
	mt, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || len(params) != 0 {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording media MIME type is unavailable")
	}
	if (key.Kind == "slide" && !strings.HasPrefix(mt, "image/") && mt != "application/octet-stream") || (key.Kind == "video" && !strings.HasPrefix(mt, "video/") && !strings.HasPrefix(mt, "audio/") && mt != "application/octet-stream") {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording media format differs from its selected kind")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%s:%d", key.RecordingID, key.Kind, key.Index, etag, size)))
	out = mediaInfo{Object: domain.ContentObject{Name: fmt.Sprintf("recording-%s-%s-%d%s", key.RecordingID, key.Kind, key.Index, strings.ToLower(path.Ext(u.Path))), MIMEType: mt, Size: size, Version: hex.EncodeToString(sum[:])}, ETag: etag}
	return out, nil
}

func (s Service) describeRecordingContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (domain.ContentObject, error) {
	p, u, key, e := s.recordingContentTarget(ctx, secret, scope, object)
	if e != nil {
		return domain.ContentObject{}, e
	}
	defer p.Close()
	c, close := s.mediaTransport(u)
	defer close()
	meta, e := probeMedia(ctx, c, u, key)
	if e != nil {
		return domain.ContentObject{}, e
	}
	after, e := p.identity(ctx)
	if e != nil {
		return domain.ContentObject{}, e
	}
	if scope.AccountRef != "canvas:user:"+after {
		return domain.ContentObject{}, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during media read")
	}
	return meta.Object, nil
}

func (s Service) readRecordingContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object, version string, offset int64, length int) (domain.ContentChunk, error) {
	var out domain.ContentChunk
	if offset < 0 || length < 0 || length > contracts.ContentChunkSize {
		return out, contracts.Fail("INVALID_ARGUMENT", "Invalid recording content range")
	}
	p, u, key, e := s.recordingContentTarget(ctx, secret, scope, object)
	if e != nil {
		return out, e
	}
	defer p.Close()
	c, close := s.mediaTransport(u)
	defer close()
	meta, e := probeMedia(ctx, c, u, key)
	if e != nil {
		return out, e
	}
	info := meta.Object
	if info.Version != version {
		return out, contracts.Fail("CONTENT_CHANGED", "Recording media version changed")
	}
	if offset > info.Size || int64(length) > info.Size-offset {
		return out, contracts.Fail("INVALID_ARGUMENT", "Recording range exceeds object size")
	}
	out = domain.ContentChunk{Version: version, Offset: offset, Size: info.Size, Data: []byte{}}
	if length == 0 {
		return out, nil
	}
	header := recordingMediaHeaders(u, fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1))
	header.Set("If-Match", meta.ETag)
	r, e := c.GetBounded(ctx, u, header, length)
	if e != nil {
		return out, e
	}
	if r.Status == http.StatusPreconditionFailed {
		return out, contracts.Fail("CONTENT_CHANGED", "Recording media changed during transfer")
	}
	want := fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(length)-1, info.Size)
	if r.Status != http.StatusPartialContent || r.Header.Get("Content-Range") != want || r.Header.Get("ETag") != meta.ETag || r.Header.Get("Content-Encoding") != "" || len(r.Body) != length {
		return out, contracts.Fail("CONTENT_CHANGED", "Recording media returned a different version or range")
	}
	if n := r.Header.Get("Content-Length"); n != "" && n != strconv.Itoa(length) {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Recording range was truncated")
	}
	after, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if scope.AccountRef != "canvas:user:"+after {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during media transfer")
	}
	out.Data = append([]byte{}, r.Body...)
	return out, nil
}
