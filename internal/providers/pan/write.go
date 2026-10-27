package pan

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type putHTTP interface {
	PutBounded(context.Context, *url.URL, http.Header, []byte, int) (transport.Reply, error)
}

var confirmKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// writeAllowed is included in the fixed Pan transport predicate. It admits no
// rename, overwrite, deletion, arbitrary body destination or query-controlled URL.
func writeAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 6 || parts[0] != "api" || parts[1] != "v1" || (parts[2] != "directory" && parts[2] != "file") || !spaceID.MatchString(parts[3]) || !spaceID.MatchString(parts[4]) {
		return false
	}
	p := strings.Join(parts[5:], "/")
	if !contracts.ValidPanPath(p, false) {
		return false
	}
	q, ok := sso.Values(u, 4)
	if !ok || !tokenPattern.MatchString(q.Get("access_token")) || q.Get("conflict_resolution_strategy") != "ask" {
		return false
	}
	if parts[2] == "directory" {
		return len(q) == 2
	}
	if q.Has("confirm") {
		return len(q) == 3 && (q.Get("confirm") == "" || q.Get("confirm") == "1") && confirmKeyPattern.MatchString(p)
	}
	if len(q) != 3 || !q.Has("filesize") {
		return false
	}
	n, e := strconv.Atoi(q.Get("filesize"))
	return e == nil && n >= 0 && n <= contracts.PanUploadMaxBytes && strconv.Itoa(n) == q.Get("filesize")
}

const unknownWriteMessage = "Pan write outcome could not be verified; inspect the exact path on the official website before retrying"

func unknownWrite() error { return contracts.Fail("WRITE_OUTCOME_UNKNOWN", unknownWriteMessage) }

// A rejected storage URL is diagnosable without exposing an object key, signed
// URL or arbitrary upstream string. This projection does not authorize a host;
// storageHost remains the independent, exact endpoint allowlist.
func unknownStorageWrite(domain, reason string) error {
	stage := "upload_storage_url"
	if reason == "host" {
		stage = "upload_storage_host"
	}
	message := unknownWriteMessage + " (stage=" + stage
	if publicInfrastructureHost.MatchString(domain) {
		message += "; infrastructure_host=" + domain
	}
	return contracts.Fail("WRITE_OUTCOME_UNKNOWN", message+")")
}

func writeStatus(r transport.Reply) error {
	if r.Status == 409 {
		return contracts.Fail("CONFLICT", "A Pan directory or file already exists; no overwrite was requested")
	}
	if r.Status == 401 || r.Status == 403 || r.Status == 429 {
		return transport.Status(r.Status)
	}
	if r.Status != 200 && r.Status != 201 {
		return unknownWrite()
	}
	return nil
}

// Mutate is called only by the Broker's concrete write executor. The caller
// must bind the exact parent, path, action and byte digest to its trusted intent.
// No upstream write is automatically retried, even for a request timeout.
func (s *Service) Mutate(ctx context.Context, secret []byte, account string, q contracts.PanMutation) (contracts.PanMutationResult, error) {
	if e := contracts.ValidatePanMutation(q); e != nil {
		return contracts.PanMutationResult{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	x, e := s.openFiles(ctx, secret, account)
	if e != nil {
		return contracts.PanMutationResult{}, e
	}
	defer x.close()
	if _, ok := s.Portal.(putHTTP); !ok {
		return contracts.PanMutationResult{}, contracts.Fail("UNSUPPORTED", "Bounded Pan write transport unavailable")
	}
	// SMH can create intermediate directories. Require every existing parent so
	// the reviewed action never silently creates additional paths.
	parent := path.Dir(q.Path)
	if parent != "." {
		var w fileWire
		if e = x.json(ctx, "directory", parent, url.Values{"info": {"1"}}, &w); e != nil {
			return contracts.PanMutationResult{}, e
		}
		if w.Kind != "dir" || w.Name != path.Base(parent) {
			return contracts.PanMutationResult{}, contracts.Fail("RESOURCE_UNAVAILABLE", "Pan parent must be an existing directory")
		}
	}
	if e = x.requireAbsent(ctx, q.Path); e != nil {
		return contracts.PanMutationResult{}, e
	}
	if q.Action == "mkdir" {
		return s.mkdir(ctx, x, q)
	}
	b, _ := base64.StdEncoding.Strict().DecodeString(q.DataBase64)
	defer clear(b)
	return s.upload(ctx, x, q, b)
}

func (x *fileSession) requireAbsent(ctx context.Context, p string) error {
	u := x.dataURL("directory", p, url.Values{"info": {"1"}})
	r, e := x.service.Portal.Get(ctx, u, http.Header{"Accept": {"application/json"}})
	u.RawQuery = ""
	defer clear(r.Body)
	if e != nil {
		return requestError(ctx, e)
	}
	if r.Status == 404 {
		return nil
	}
	if r.Status == 200 {
		return contracts.Fail("CONFLICT", "A Pan object already exists; no overwrite was requested")
	}
	return transport.Status(r.Status)
}

func (x *fileSession) put(ctx context.Context, kind, p string, q url.Values, b []byte) (transport.Reply, error) {
	u := x.dataURL(kind, p, q)
	if !writeAllowed(u) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Unreviewed Pan write endpoint")
	}
	c := x.service.Portal.(putHTTP)
	r, e := c.PutBounded(ctx, u, http.Header{"Accept": {"application/json"}, "Content-Type": {"application/json"}}, b, 1<<20)
	u.RawQuery = ""
	if e != nil {
		clear(r.Body)
		return transport.Reply{}, unknownWrite()
	}
	if e = writeStatus(r); e != nil {
		clear(r.Body)
		return transport.Reply{}, e
	}
	return r, nil
}

func (s *Service) mkdir(ctx context.Context, x *fileSession, q contracts.PanMutation) (contracts.PanMutationResult, error) {
	r, e := x.put(ctx, "directory", q.Path, url.Values{"conflict_resolution_strategy": {"ask"}}, []byte(`{}`))
	if e != nil {
		return contracts.PanMutationResult{}, e
	}
	defer clear(r.Body)
	// SMH returns 201 with no body for ask + default with_inode=0. The
	// response path is only documented for rename; never require it for the
	// non-overwriting operation. A body, when present, must still identify the
	// exact requested path. Independently verify the new directory below.
	// https://cloud.tencent.com/document/product/1339/71146
	if r.Status != http.StatusCreated || len(r.Body) != 0 {
		var result struct {
			Path []string `json:"path"`
		}
		if e = decodeSuccess(r, &result); e != nil || strings.Join(result.Path, "/") != q.Path {
			return contracts.PanMutationResult{}, unknownWrite()
		}
	}
	var w fileWire
	if e = x.json(ctx, "directory", q.Path, url.Values{"info": {"1"}}, &w); e != nil || w.Kind != "dir" || w.Name != path.Base(q.Path) {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	return contracts.PanMutationResult{Action: q.Action, Path: q.Path, Kind: "dir", Status: "created"}, nil
}

func decodeSuccess(r transport.Reply, out any) error {
	// Read decoder accepts only 200; successful creation can legitimately be 201.
	r.Status = 200
	return decode(r, out)
}

type uploadInit struct {
	ConfirmKey string            `json:"confirmKey"`
	Domain     string            `json:"domain"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Expiration string            `json:"expiration"`
}

func storageURL(domain, object string) (*url.URL, error) {
	u, reason := checkedStorageURL(domain, object)
	if reason != "" {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed Pan storage URL")
	}
	return u, nil
}

func checkedStorageURL(domain, object string) (*url.URL, string) {
	if !storageHost(domain) {
		return nil, "host"
	}
	if len(object) < 2 || len(object) > 4096 || !utf8.ValidString(object) || !strings.HasPrefix(object, "/") {
		return nil, "path_shape"
	}
	// This is an opaque storage object key, not a user's logical Pan path.
	// The deployed first-party SDK encodes each raw path segment with
	// encodeURIComponent before joining it to the bare domain. Parsing the raw
	// value as a URL would instead interpret '#' and '%' as URL syntax.
	// https://pan.sjtu.edu.cn/js/index.41e36dd0.js (simpleUpload, module 2c24)
	// Its encoder also supports query suffixes; no upload query parameters have
	// been reviewed here, so keep rejecting those rather than forwarding them.
	if strings.ContainsAny(object, "?\\") || strings.ContainsFunc(object, unicode.IsControl) {
		return nil, "path_syntax"
	}
	parts := strings.Split(strings.TrimPrefix(object, "/"), "/")
	encode := strings.NewReplacer("+", "%20", "%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, "path_segment"
		}
		// A percent sign is a literal key byte and will be escaped below. Still
		// reject encoded traversal/separators to avoid ambiguous backend decoding.
		if decoded, err := url.PathUnescape(part); err == nil && (decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") || strings.ContainsFunc(decoded, unicode.IsControl)) {
			return nil, "path_encoding"
		}
		parts[i] = encode.Replace(url.QueryEscape(part))
	}
	u, e := url.Parse("https://" + domain + "/" + strings.Join(parts, "/"))
	if e != nil {
		return nil, "url_shape"
	}
	// ExactHTTPS normally excludes caller-provided RawPath. Here RawPath is
	// generated exclusively by the encoder above and pinned for the one PUT.
	canonical := *u
	canonical.RawPath = ""
	if !transport.ExactHTTPS(&canonical, domain) || len(u.String()) > 8192 || u.RawQuery != "" || u.Path != object {
		return nil, "url_shape"
	}
	return u, ""
}

func uploadHeaders(raw map[string]string) (http.Header, error) {
	if len(raw) < 1 || len(raw) > 12 {
		return nil, changed()
	}
	h := make(http.Header)
	for k, v := range raw {
		if len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") || h.Get(k) != "" {
			return nil, changed()
		}
		switch strings.ToLower(k) {
		case "authorization", "content-type", "content-md5", "x-cos-security-token", "x-cos-meta-fileid", "x-cos-meta-smh-file-id", "x-amz-date", "x-amz-content-sha256", "x-amz-security-token":
		case "cache-control":
			// Official simple-upload responses include a storage cache duration;
			// do not accept public/shared-cache directives from the portal.
			if v != "no-cache" && v != "no-store" {
				n, err := strconv.Atoi(strings.TrimPrefix(v, "max-age="))
				if !strings.HasPrefix(v, "max-age=") || err != nil || n < 0 || n > 365*86400 || strconv.Itoa(n) != strings.TrimPrefix(v, "max-age=") {
					return nil, changed()
				}
			}
		case "x-cos-acl", "x-amz-acl":
			if v != "default" && v != "private" {
				return nil, contracts.Fail("PERMISSION_DENIED", "Pan storage ACL must remain private")
			}
		case "x-cos-storage-class":
			if v != "STANDARD" {
				return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed Pan storage class")
			}
		case "x-cos-traffic-limit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 100<<10 || n > 100<<20 || strconv.Itoa(n) != v {
				return nil, changed()
			}
		default:
			return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed Pan storage header")
		}
		h.Set(k, v)
	}
	if auth := h.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") && !strings.HasPrefix(auth, "q-sign-algorithm=sha1&") {
		return nil, changed()
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/octet-stream")
	}
	return h, nil
}

func (s *Service) upload(ctx context.Context, x *fileSession, q contracts.PanMutation, b []byte) (contracts.PanMutationResult, error) {
	body, _ := json.Marshal(struct {
		Size string `json:"size"`
	}{strconv.Itoa(len(b))})
	r, e := x.put(ctx, "file", q.Path, url.Values{"conflict_resolution_strategy": {"ask"}, "filesize": {strconv.Itoa(len(b))}}, body)
	if e != nil {
		return contracts.PanMutationResult{}, e
	}
	defer clear(r.Body)
	var init uploadInit
	if e = decodeSuccess(r, &init); e != nil || !confirmKeyPattern.MatchString(init.ConfirmKey) {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	defer func() { init.ConfirmKey = ""; clear(init.Headers) }()
	u, reason := checkedStorageURL(init.Domain, init.Path)
	if reason != "" {
		return contracts.PanMutationResult{}, unknownStorageWrite(init.Domain, reason)
	}
	h, e := uploadHeaders(init.Headers)
	if e != nil {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	defer clear(h)
	if init.Expiration != "" {
		deadline, e := time.Parse(time.RFC3339, init.Expiration)
		if e != nil || !deadline.After(time.Now()) || deadline.After(time.Now().Add(24*time.Hour)) {
			return contracts.PanMutationResult{}, unknownWrite()
		}
	}
	if signed := h.Get("Content-MD5"); signed != "" {
		sum := md5.Sum(b)
		if signed != base64.StdEncoding.EncodeToString(sum[:]) {
			return contracts.PanMutationResult{}, unknownWrite()
		}
	}
	fixed := u.String()
	client := transport.NewSecure(u.Host, func(v *url.URL) bool { return v != nil && v.String() == fixed })
	defer client.Close()
	storage := putHTTP(client)
	// Tests substitute a transport which still receives the fully pinned URL and
	// header projection; production never reuses a portal token/cookie client.
	if override, ok := s.Portal.(interface{ StoragePut() putHTTP }); ok {
		storage = override.StoragePut()
	}
	up, e := storage.PutBounded(ctx, u, h, b, 64<<10)
	clear(up.Body)
	if e != nil || up.Status != 200 {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	confirm := x.dataURL("file", init.ConfirmKey, url.Values{"confirm": {"1"}, "conflict_resolution_strategy": {"ask"}})
	if !writeAllowed(confirm) {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	done, e := s.Portal.Post(ctx, confirm, http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}, []byte(`{}`))
	confirm.RawQuery = ""
	defer clear(done.Body)
	if e != nil {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	if e = writeStatus(done); e != nil {
		return contracts.PanMutationResult{}, e
	}
	var completed struct {
		Path        []string `json:"path"`
		Overwritten *bool    `json:"isOverwritten"`
	}
	e = decodeSuccess(done, &completed)
	pathMatches := strings.Join(completed.Path, "/") == q.Path
	overwritten := completed.Overwritten != nil && *completed.Overwritten
	if e != nil || !pathMatches || overwritten {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	// Reconcile the final object with the approved bytes. Metadata hashes are
	// checked when the server returns an MD5 ETag, rather than assumed for multipart.
	f, e := s.fileInfo(ctx, x, q.Path)
	if e != nil || f == nil || f.Size == nil || *f.Size != int64(len(b)) {
		return contracts.PanMutationResult{}, unknownWrite()
	}
	if len(f.Version) == 32 {
		sum := md5.Sum(b)
		if !strings.EqualFold(f.Version, hex.EncodeToString(sum[:])) {
			return contracts.PanMutationResult{}, unknownWrite()
		}
	}
	return contracts.PanMutationResult{Action: q.Action, Path: q.Path, Kind: "file", Size: *f.Size, Version: f.Version, Status: "created"}, nil
}
