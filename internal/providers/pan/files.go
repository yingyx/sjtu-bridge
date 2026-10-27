package pan

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
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

var spaceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var cosHost = regexp.MustCompile(`^[a-z0-9-]+-[0-9]+\.cos\.[a-z0-9-]+\.myqcloud\.com$`)

type fileSession struct {
	*session
	library, space string
	access         []byte
}

func (x *fileSession) close() { clear(x.access); x.session.close() }

// openFiles exchanges a short-lived data token only for a space that the
// read-only state endpoint has already proved exists. An unopened account is
// never implicitly initialized by a file read.
func (s *Service) openFiles(ctx context.Context, secret []byte, account string) (*fileSession, error) {
	x, e := s.login(ctx, secret)
	if e != nil {
		return nil, e
	}
	failed := true
	defer func() {
		if failed {
			x.close()
		}
	}()
	if x.account != account {
		return nil, contracts.Fail("PERMISSION_DENIED", "Pan account changed")
	}
	var state struct {
		HasPersonalSpace *bool `json:"hasPersonalSpace"`
	}
	if e = x.get(ctx, "/user/v1/space/1", &state); e != nil {
		return nil, e
	}
	if state.HasPersonalSpace == nil {
		return nil, changed()
	}
	if !*state.HasPersonalSpace {
		return nil, contracts.Fail("RESOURCE_UNAVAILABLE", "Open your personal space on the official Pan website first")
	}
	u, _ := url.Parse(root + "/user/v1/space/1/personal")
	u.RawQuery = url.Values{"user_token": {string(x.token)}}.Encode()
	r, e := s.Portal.Post(ctx, u, http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}, []byte(`{}`))
	u.RawQuery = ""
	if e != nil {
		return nil, requestError(ctx, e)
	}
	defer clear(r.Body)
	var token struct {
		Library string `json:"libraryId"`
		Space   string `json:"spaceId"`
		Access  string `json:"accessToken"`
		Expires int    `json:"expiresIn"`
	}
	if e = decode(r, &token); e != nil {
		return nil, e
	}
	if !spaceID.MatchString(token.Library) || !spaceID.MatchString(token.Space) || !tokenPattern.MatchString(token.Access) || token.Expires < 1 || token.Expires > 366*86400 {
		return nil, changed()
	}
	failed = false
	return &fileSession{session: x, library: token.Library, space: token.Space, access: []byte(token.Access)}, nil
}

func fileAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 6)
	if !ok || !tokenPattern.MatchString(q.Get("access_token")) {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segments) < 6 || segments[0] != "api" || segments[1] != "v1" || (segments[2] != "directory" && segments[2] != "file") || !spaceID.MatchString(segments[3]) || !spaceID.MatchString(segments[4]) || !contracts.ValidPanPath(strings.Join(segments[5:], "/"), true) {
		return false
	}
	for k := range q {
		switch k {
		case "access_token", "info", "page", "page_size", "order_by", "order_by_type":
		default:
			return false
		}
	}
	if q.Has("info") {
		return len(q) == 2 && (q.Get("info") == "" || q.Get("info") == "1")
	}
	if segments[2] == "file" {
		return len(q) == 1
	}
	if len(q) != 5 || q.Get("order_by") != "name" || q.Get("order_by_type") != "asc" {
		return false
	}
	p, e := strconv.Atoi(q.Get("page"))
	n, en := strconv.Atoi(q.Get("page_size"))
	return e == nil && en == nil && p >= 1 && p <= 100 && n >= 1 && n <= 100 && strconv.Itoa(p) == q.Get("page") && strconv.Itoa(n) == q.Get("page_size")
}

func (x *fileSession) dataURL(kind, p string, q url.Values) *url.URL {
	u := &url.URL{Scheme: "https", Host: Host, Path: "/api/v1/" + kind + "/" + x.library + "/" + x.space + "/" + p}
	if q == nil {
		q = url.Values{}
	}
	q.Set("access_token", string(x.access))
	u.RawQuery = q.Encode()
	return u
}

func (x *fileSession) json(ctx context.Context, kind, p string, q url.Values, out any) error {
	u := x.dataURL(kind, p, q)
	if !fileAllowed(u) {
		return contracts.Fail("PERMISSION_DENIED", "Pan file destination denied")
	}
	r, e := x.service.Portal.Get(ctx, u, http.Header{"Accept": {"application/json"}})
	u.RawQuery = ""
	if e != nil {
		return requestError(ctx, e)
	}
	defer clear(r.Body)
	if r.Status == 404 {
		return contracts.Fail("RESOURCE_UNAVAILABLE", "Pan object does not exist")
	}
	return decode(r, out)
}

type fileWire struct {
	Name        string          `json:"name"`
	Kind        string          `json:"type"`
	Size        json.RawMessage `json:"size"`
	ETag        string          `json:"eTag"`
	Modified    string          `json:"modificationTime"`
	ContentType string          `json:"contentType"`
	Path        []string        `json:"path"`
}

func fileProjection(w fileWire, p string) (*contracts.PanFile, error) {
	if w.Kind != "file" && w.Kind != "dir" || !contracts.ValidPanPath(p, false) || w.Name != path.Base(p) || !utf8.ValidString(w.Name) || len(w.Modified) > 64 || len(w.ETag) > 256 {
		return nil, changed()
	}
	var size *int64
	if w.Kind == "file" {
		var n int64
		var str string
		if json.Unmarshal(w.Size, &str) == nil {
			var e error
			n, e = strconv.ParseInt(str, 10, 64)
			if e != nil {
				return nil, changed()
			}
		} else if json.Unmarshal(w.Size, &n) != nil {
			return nil, changed()
		}
		if n < 0 {
			return nil, changed()
		}
		size = &n
	}
	ct := w.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if len(ct) > 256 {
		return nil, changed()
	}
	media, _, mediaErr := mime.ParseMediaType(ct)
	if mediaErr != nil {
		return nil, changed()
	}
	return &contracts.PanFile{Path: p, Kind: w.Kind, Size: size, Version: w.ETag, ModifiedAt: w.Modified, MIMEType: media}, nil
}

func (s *Service) readFiles(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := contracts.PanFilesCapability
	if q.View == "file_v1" {
		id = contracts.PanInfoCapability
	}
	rules, _ := contracts.CampusRules(id)
	if account != q.AccountRef || !rules.ValidQuery(q) {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid Pan file query")
	}
	x, e := s.openFiles(ctx, secret, account)
	if e != nil {
		return domain.CampusPage{}, e
	}
	defer x.close()
	page := domain.CampusPage{AccountRef: account, Coverage: rules.Coverage, Rows: []contracts.CampusRecord{}}
	if id == contracts.PanInfoCapability {
		if next != "" {
			return page, contracts.Fail("CURSOR_INVALID", "Object metadata is not paginated")
		}
		var w fileWire
		if e = x.json(ctx, "directory", q.Path, url.Values{"info": {"1"}}, &w); e != nil {
			return page, e
		}
		f, e := fileProjection(w, q.Path)
		if e != nil {
			return page, e
		}
		page.Rows = append(page.Rows, fileRow(q.ResourceRef, f))
		return page, nil
	}
	n := 1
	if next != "" {
		n, e = strconv.Atoi(next)
		if e != nil || n < 2 || n > 100 {
			return page, contracts.Fail("CURSOR_INVALID", "Invalid Pan directory continuation")
		}
	}
	var response struct {
		Path     []string   `json:"path"`
		Contents []fileWire `json:"contents"`
		Total    *int       `json:"totalNum"`
	}
	params := url.Values{"page": {strconv.Itoa(n)}, "page_size": {strconv.Itoa(q.Limit)}, "order_by": {"name"}, "order_by_type": {"asc"}}
	if e = x.json(ctx, "directory", q.Path, params, &response); e != nil {
		return page, e
	}
	if response.Total == nil || *response.Total < 0 || len(response.Contents) > q.Limit || strings.Join(response.Path, "/") != q.Path {
		return page, changed()
	}
	remaining := *response.Total - (n-1)*q.Limit
	if remaining < 0 {
		return page, changed()
	}
	want := min(q.Limit, remaining)
	if len(response.Contents) != want {
		return page, changed()
	}
	seen := map[string]bool{}
	for _, w := range response.Contents {
		p := w.Name
		if q.Path != "" {
			p = q.Path + "/" + w.Name
		}
		f, e := fileProjection(w, p)
		if e != nil || seen[p] {
			return page, changed()
		}
		seen[p] = true
		page.Rows = append(page.Rows, fileRow(q.ResourceRef, f))
	}
	if n*q.Limit < *response.Total {
		if len(response.Contents) == 0 {
			return page, changed()
		}
		if n >= 100 {
			return page, contracts.Fail("RESULT_LIMIT_EXCEEDED", "Narrow the Pan directory; page bound reached")
		}
		page.Next = strconv.Itoa(n + 1)
	}
	return page, nil
}
func fileRow(resource string, f *contracts.PanFile) contracts.CampusRecord {
	h := sha256.Sum256([]byte(f.Path))
	return contracts.CampusRecord{ResourceRef: resource, Ref: "pan:file:" + hex.EncodeToString(h[:]), Title: path.Base(f.Path), Digital: &contracts.DigitalRecord{File: f}}
}

func (s *Service) fileInfo(ctx context.Context, x *fileSession, p string) (*contracts.PanFile, error) {
	var w fileWire
	if e := x.json(ctx, "directory", p, url.Values{"info": {"1"}}, &w); e != nil {
		return nil, e
	}
	f, e := fileProjection(w, p)
	if e != nil {
		return nil, e
	}
	if f.Kind != "file" || f.Version == "" || f.Size == nil {
		return nil, contracts.Fail("RESOURCE_UNAVAILABLE", "A versioned file is required")
	}
	return f, nil
}
func (s *Service) DescribeContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (domain.ContentObject, error) {
	if scope.Capability != contracts.PanContentCapability || scope.ResourceRef != "pan:files:self" || !contracts.ValidPanPath(object, false) {
		return domain.ContentObject{}, contracts.Fail("PERMISSION_DENIED", "Pan content scope denied")
	}
	x, e := s.openFiles(ctx, secret, scope.AccountRef)
	if e != nil {
		return domain.ContentObject{}, e
	}
	defer x.close()
	f, e := s.fileInfo(ctx, x, object)
	if e != nil {
		return domain.ContentObject{}, e
	}
	if *f.Size > contracts.MaxContentBytes {
		return domain.ContentObject{}, contracts.Fail("RESPONSE_TOO_LARGE", "Pan content exceeds the 1 GiB content limit")
	}
	return domain.ContentObject{Name: path.Base(object), MIMEType: f.MIMEType, Size: *f.Size, Version: contentVersion(f)}, nil
}
func contentVersion(f *contracts.PanFile) string {
	h := sha256.Sum256([]byte(f.Path + "\x00" + f.Version + "\x00" + strconv.FormatInt(*f.Size, 10)))
	return hex.EncodeToString(h[:])
}

type boundedHTTP interface {
	GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
}

// SMH metadata can expose either the opaque ETag or its quoted HTTP form.
// If-Match requires a strong quoted entity-tag (RFC 9110, 8.8.3/13.1.1).
// Never replace an invalid validator with '*' or silently omit the condition.
func matchETag(version string) (string, error) {
	if version == "" || len(version) > 256 || strings.HasPrefix(version, "W/") {
		return "", changed()
	}
	opaque := version
	quoted := strings.HasPrefix(version, `"`) || strings.HasSuffix(version, `"`)
	if quoted {
		if len(version) < 2 || !strings.HasPrefix(version, `"`) || !strings.HasSuffix(version, `"`) {
			return "", changed()
		}
		opaque = version[1 : len(version)-1]
	}
	for _, c := range opaque {
		if c < 0x21 || c > 0x7e || c == '"' {
			return "", changed()
		}
	}
	if quoted {
		return version, nil
	}
	return `"` + version + `"`, nil
}

func (s *Service) ReadContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object, version string, offset int64, length int) (domain.ContentChunk, error) {
	if scope.Capability != contracts.PanContentCapability || scope.ResourceRef != "pan:files:self" || !contracts.ValidPanPath(object, false) || offset < 0 || length < 0 || length > contracts.ContentChunkSize {
		return domain.ContentChunk{}, contracts.Fail("PERMISSION_DENIED", "Pan content range denied")
	}
	x, e := s.openFiles(ctx, secret, scope.AccountRef)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	defer x.close()
	f, e := s.fileInfo(ctx, x, object)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	if *f.Size > contracts.MaxContentBytes {
		return domain.ContentChunk{}, contracts.Fail("RESPONSE_TOO_LARGE", "Pan content exceeds the 1 GiB content limit")
	}
	if contentVersion(f) != version {
		return domain.ContentChunk{}, contracts.Fail("CONTENT_CHANGED", "Pan file changed")
	}
	if offset > *f.Size || int64(length) > *f.Size-offset {
		return domain.ContentChunk{}, contracts.Fail("INVALID_ARGUMENT", "Pan range exceeds file")
	}
	if length == 0 {
		return domain.ContentChunk{Version: version, Offset: offset, Size: *f.Size, Data: []byte{}}, nil
	}
	etag, e := matchETag(f.Version)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	u := x.dataURL("file", object, nil)
	h := http.Header{"Range": {fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1)}, "If-Match": {etag}, "Accept-Encoding": {"identity"}}
	client, ok := s.Portal.(boundedHTTP)
	if !ok {
		return domain.ContentChunk{}, contracts.Fail("UNSUPPORTED", "Bounded Pan content transport unavailable")
	}
	r, e := client.GetBounded(ctx, u, h, length)
	u.RawQuery = ""
	if e != nil {
		return domain.ContentChunk{}, requestError(ctx, e)
	}
	// Some deployments hand out a signed COS URL. Only the exact first-party
	// response destination is followed; no Cookie or access token is forwarded.
	if r.Status == 302 || r.Status == 307 {
		dest, err := url.Parse(r.Header.Get("Location"))
		if err != nil || !validStorageDownload(dest) {
			return domain.ContentChunk{}, storageRedirectDenied(dest)
		}
		fixed := dest.String()
		c := transport.NewSecure(dest.Host, func(v *url.URL) bool { return v != nil && v.String() == fixed })
		defer c.Close()
		r, e = c.GetBounded(ctx, dest, h, length)
		if e != nil {
			return domain.ContentChunk{}, requestError(ctx, e)
		}
	}
	if r.Status == 412 {
		return domain.ContentChunk{}, contracts.Fail("CONTENT_CHANGED", "Pan version precondition failed")
	}
	want := fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(length)-1, *f.Size)
	if r.Status != 206 || r.Header.Get("Content-Range") != want || len(r.Body) != length {
		return domain.ContentChunk{}, contracts.Fail("UPSTREAM_CHANGED", "Pan ignored or truncated the exact range")
	}
	// Recheck the metadata version after reading, including deployments that do
	// not honor If-Match. Never publish mixed chunks from changed files.
	after, e := s.fileInfo(ctx, x, object)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	if contentVersion(after) != version {
		clear(r.Body)
		return domain.ContentChunk{}, contracts.Fail("CONTENT_CHANGED", "Pan file changed during read")
	}
	return domain.ContentChunk{Version: version, Offset: offset, Size: *f.Size, Data: r.Body}, nil
}

var _ domain.Content = (*Service)(nil)
