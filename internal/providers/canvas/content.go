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
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type fileMetadata struct {
	ID          json.Number `json:"id"`
	DisplayName string      `json:"display_name"`
	Filename    string      `json:"filename"`
	ContentType string      `json:"content-type"`
	Size        int64       `json:"size"`
	UpdatedAt   string      `json:"updated_at"`
	ModifiedAt  string      `json:"modified_at"`
	UUID        string      `json:"uuid"`
	URL         string      `json:"url"`
	Locked      bool        `json:"locked_for_user"`
	Hidden      bool        `json:"hidden_for_user"`
}

type contentObject struct {
	FileID       string `json:"file_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
}

// CanvasContentObject binds a caller's reviewed query to the internal content
// port. It carries no bearer or download location.
func CanvasContentObject(q contracts.CanvasContentQuery) string {
	raw, _ := json.Marshal(contentObject{FileID: q.ItemID, AssignmentID: q.AssignmentID})
	return string(raw)
}

var contentFilePath = regexp.MustCompile(`^/api/v1/files/[1-9][0-9]{0,17}$`)
var contentDownloadPath = regexp.MustCompile(`^/files/[1-9][0-9]{0,17}/download$`)

const contentStorageHost = "s3.jcloud.sjtu.edu.cn"

// This storage location is issued by the school's authenticated download
// redirect. A caller cannot provide it, and the actual transport below is
// additionally bound to that one complete signed URL, not this predicate alone.
func contentStorageAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, contentStorageHost) || u.ForceQuery || u.Path == "/" || !strings.HasPrefix(u.Path, "/") || path.Clean(u.Path) != u.Path || strings.ContainsAny(u.Path, "\\\x00\r\n") {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) < 6 || len(q) > 7 {
		return false
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" || len(values[0]) > 2048 || strings.ContainsAny(values[0], "\x00\r\n") {
			return false
		}
		switch key {
		case "X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-Signature", "X-Amz-SignedHeaders", "response-content-disposition":
		default:
			return false
		}
	}
	expires, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	_, dateErr := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
	return err == nil && expires >= 1 && expires <= 604800 && dateErr == nil && q.Get("X-Amz-Algorithm") == "AWS4-HMAC-SHA256" && q.Get("X-Amz-SignedHeaders") == "host" && regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}/[0-9]{8}/[A-Za-z0-9-]{1,64}/s3/aws4_request$`).MatchString(q.Get("X-Amz-Credential")) && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(q.Get("X-Amz-Signature"))
}

func (s Service) readStorageContent(ctx context.Context, u *url.URL, rangeValue string, length int) (transport.Reply, error) {
	var client boundedMedia = s.FileClient
	if client == nil {
		fixed := u.String()
		c := transport.NewSecure(contentStorageHost, func(next *url.URL) bool { return contentStorageAllowed(next) && next.String() == fixed })
		defer c.Close()
		client = c
	}
	// The signed request authenticates itself. Canvas bearer, cookies and LTI
	// headers must never be copied to storage, even on another school host.
	r, err := client.GetBounded(ctx, u, http.Header{"Accept": {"*/*"}, "Accept-Encoding": {"identity"}, "Range": {rangeValue}}, length)
	if err == nil && r.Status >= 300 && r.Status < 400 {
		return transport.Reply{}, contracts.Fail("UPSTREAM_CHANGED", "Canvas storage destination redirected again")
	}
	return r, err
}

func contentAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.ForceQuery {
		return false
	}
	if contentFilePath.MatchString(u.Path) {
		return u.RawQuery == ""
	}
	if !contentDownloadPath.MatchString(u.Path) {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for k, values := range q {
		if len(values) != 1 || len(values[0]) > 512 {
			return false
		}
		switch k {
		case "download_frd":
			if values[0] != "1" && values[0] != "true" {
				return false
			}
		case "verifier":
			if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`).MatchString(values[0]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (s Service) scopedContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (*Provider, fileMetadata, domain.ContentObject, error) {
	var key contentObject
	var info domain.ContentObject
	var f fileMetadata
	if scope.Capability != contracts.CanvasFileContent || scope.View != "content_v1" || !contracts.CanvasCourseRef.MatchString(scope.ResourceRef) || strictjson.Decode([]byte(object), &key) != nil || !numeric.MatchString(key.FileID) || (key.AssignmentID != "" && !numeric.MatchString(key.AssignmentID)) {
		return nil, f, info, contracts.Fail("PERMISSION_DENIED", "Invalid Canvas content scope")
	}
	p := s.provider(secret)
	fail := func(e error) (*Provider, fileMetadata, domain.ContentObject, error) {
		p.Close()
		return nil, f, info, e
	}
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
	var c course
	if strictjson.Decode(r.Body, &c) != nil || string(c.ID) != cid || !owned(c, uid) {
		return fail(contracts.Fail("PERMISSION_DENIED", "Active student course ownership could not be verified"))
	}
	u := &url.URL{Scheme: "https", Host: Host, Path: cu.Path + "/files/" + key.FileID}
	if key.AssignmentID != "" {
		su := &url.URL{Scheme: "https", Host: Host, Path: cu.Path + "/assignments/" + key.AssignmentID + "/submissions/self"}
		r, e = p.read(ctx, su)
		if e != nil {
			return fail(e)
		}
		var submission struct {
			AssignmentID json.Number    `json:"assignment_id"`
			UserID       json.Number    `json:"user_id"`
			Attachments  []fileMetadata `json:"attachments"`
		}
		if strictjson.Decode(r.Body, &submission) != nil || string(submission.AssignmentID) != key.AssignmentID || string(submission.UserID) != uid || len(submission.Attachments) > 100 {
			return fail(contracts.Fail("PERMISSION_DENIED", "Submission attachment ownership could not be verified"))
		}
		found := false
		for _, a := range submission.Attachments {
			if string(a.ID) == key.FileID {
				found = true
			}
		}
		if !found {
			return fail(contracts.Fail("PERMISSION_DENIED", "File is not an attachment of this user's submission"))
		}
		u.Path = "/api/v1/files/" + key.FileID
	}
	r, e = p.read(ctx, u)
	if e != nil {
		return fail(e)
	}
	if strictjson.Decode(r.Body, &f) != nil || string(f.ID) != key.FileID || f.Locked || f.Hidden || f.Size < 0 || f.Size > contracts.MaxContentBytes {
		return fail(contracts.Fail("UPSTREAM_CHANGED", "Canvas file content metadata is invalid or locked"))
	}
	if _, e = time.Parse(time.RFC3339, f.UpdatedAt); e != nil {
		return fail(contracts.Fail("UPSTREAM_CHANGED", "Canvas file version is unavailable"))
	}
	mt, params, e := mime.ParseMediaType(f.ContentType)
	if e != nil || len(params) != 0 {
		return fail(contracts.Fail("UPSTREAM_CHANGED", "Canvas file MIME type is invalid"))
	}
	name := f.DisplayName
	if name == "" {
		name = f.Filename
	}
	name = cleanCanvasText(name)
	if len(name) == 0 || len(name) > 255 || name != path.Base(name) || strings.ContainsAny(name, `/\`) {
		return fail(contracts.Fail("UPSTREAM_CHANGED", "Canvas file name is invalid"))
	}
	info = domain.ContentObject{Name: name, MIMEType: mt, Size: f.Size, Version: fileVersion(f)}
	return p, f, info, nil
}

func (s Service) DescribeContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (domain.ContentObject, error) {
	if scope.Capability == contracts.CanvasRecordingContent {
		return s.describeRecordingContent(ctx, secret, scope, object)
	}
	p, _, info, e := s.scopedContent(ctx, secret, scope, object)
	if p != nil {
		defer p.Close()
	}
	if e != nil {
		return info, e
	}
	after, e := p.identity(ctx)
	if e == nil && scope.AccountRef != "canvas:user:"+after {
		e = contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during content read")
	}
	return info, e
}

func (s Service) ReadContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object, version string, offset int64, length int) (domain.ContentChunk, error) {
	if scope.Capability == contracts.CanvasRecordingContent {
		return s.readRecordingContent(ctx, secret, scope, object, version, offset, length)
	}
	var out domain.ContentChunk
	if offset < 0 || length < 0 || length > contracts.ContentChunkSize {
		return out, contracts.Fail("INVALID_ARGUMENT", "Invalid Canvas content range")
	}
	p, f, info, e := s.scopedContent(ctx, secret, scope, object)
	if e != nil {
		return out, e
	}
	defer p.Close()
	if info.Version != version {
		return out, contracts.Fail("CONTENT_CHANGED", "Canvas file version changed")
	}
	if offset > info.Size || int64(length) > info.Size-offset {
		return out, contracts.Fail("INVALID_ARGUMENT", "Canvas range exceeds file size")
	}
	out = domain.ContentChunk{Version: info.Version, Size: info.Size, Offset: offset, Data: []byte{}}
	if length == 0 {
		return out, nil
	}
	u, e := url.Parse(f.URL)
	if e != nil || !contentAllowed(u) || u.Path != "/files/"+string(f.ID)+"/download" {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas download destination requires review")
	}
	h := http.Header{"Authorization": {"Bearer " + string(p.token)}, "Accept": {"*/*"}, "Accept-Encoding": {"identity"}, "Range": {fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1)}}
	bounded, ok := s.Client.(interface {
		GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
	})
	if !ok {
		return out, contracts.Fail("UPSTREAM_UNAVAILABLE", "Bounded Canvas content transport is unavailable")
	}
	r, e := bounded.GetBounded(ctx, u, h, length)
	for hop := 0; e == nil && r.Status >= 300 && r.Status < 400 && hop < 3; hop++ {
		next, err := u.Parse(r.Header.Get("Location"))
		if err == nil && contentStorageAllowed(next) {
			r, e = s.readStorageContent(ctx, next, h.Get("Range"), length)
			break
		}
		if err != nil || r.Header.Get("Location") == "" || !contentAllowed(next) || next.Path != "/files/"+string(f.ID)+"/download" {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas storage redirect requires review")
		}
		u = next
		r, e = bounded.GetBounded(ctx, u, h, length)
	}
	if e != nil {
		return out, e
	}
	if r.Status == http.StatusPartialContent {
		want := fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(length)-1, info.Size)
		if r.Header.Get("Content-Range") != want {
			return out, contracts.Fail("CONTENT_CHANGED", "Canvas returned a different file range or size")
		}
	} else if r.Status != http.StatusOK || offset != 0 || int64(length) != info.Size {
		if e := transport.Status(r.Status); e != nil {
			return out, e
		}
		return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas does not support bounded range transfer")
	}
	if len(r.Body) != length || r.Header.Get("Content-Encoding") != "" {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas file range is truncated or encoded")
	}
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || (media != info.MIMEType && media != "application/octet-stream") {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas content MIME type changed")
	}
	if n := r.Header.Get("Content-Length"); n != "" && n != strconv.Itoa(length) {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Canvas file length changed")
	}
	// Re-read trusted file metadata after the range to detect a mutation
	// between version resolution and byte delivery, including same-size edits.
	var key contentObject
	_ = json.Unmarshal([]byte(object), &key)
	verifyURL := &url.URL{Scheme: "https", Host: Host, Path: "/api/v1/courses/" + strings.TrimPrefix(scope.ResourceRef, "canvas:course:") + "/files/" + key.FileID}
	if key.AssignmentID != "" {
		verifyURL.Path = "/api/v1/files/" + key.FileID
	}
	verify, e := p.read(ctx, verifyURL)
	if e != nil {
		return out, e
	}
	var current fileMetadata
	if strictjson.Decode(verify.Body, &current) != nil || current.Locked || current.Hidden || fileVersion(current) != info.Version {
		return out, contracts.Fail("CONTENT_CHANGED", "Canvas file changed during transfer")
	}
	after, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if scope.AccountRef != "canvas:user:"+after {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during transfer")
	}
	out.Data = append([]byte{}, r.Body...)
	return out, nil
}

func fileVersion(f fileMetadata) string {
	version := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s\n%s", string(f.ID), f.UUID, f.Size, f.UpdatedAt, f.ModifiedAt)))
	return hex.EncodeToString(version[:])
}
