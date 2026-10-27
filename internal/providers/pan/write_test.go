package pan

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type writeFake struct {
	*fake
	storage *storageFake
}

func (f *writeFake) PutBounded(ctx context.Context, u *url.URL, h http.Header, b []byte, n int) (transport.Reply, error) {
	return f.call(ctx, "PUT", u, h, b)
}
func (f *writeFake) StoragePut() putHTTP { return f.storage }

type storageFake struct {
	t     *testing.T
	calls int
	data  []byte
	fail  bool
	url   string
}

func (f *storageFake) PutBounded(_ context.Context, u *url.URL, h http.Header, b []byte, n int) (transport.Reply, error) {
	f.t.Helper()
	f.calls++
	wantURL := f.url
	if wantURL == "" {
		wantURL = "https://s3pan.jcloud.sjtu.edu.cn/tced-private-synthetic/lib/synthetic-object"
	}
	if u.String() != wantURL || h.Get("Cookie") != "" || strings.Contains(fmt.Sprint(h), syntheticToken) || h.Get("Authorization") != "AWS4-HMAC-SHA256 SYNTHETIC_STORAGE_SIGNATURE" || n > 64<<10 {
		f.t.Fatal("Unreviewed or credential-forwarding storage request")
	}
	f.data = append([]byte(nil), b...)
	if f.fail {
		return transport.Reply{}, fmt.Errorf("SYNTHETIC_IO_ERROR")
	}
	return transport.Reply{Status: 200}, nil
}

func setupWrite(t *testing.T) (*Service, *writeFake, *flow, *bool) {
	s, f, flow := setup(t, `{"hasPersonalSpace":true}`)
	w := &writeFake{fake: f, storage: &storageFake{t: t}}
	s.Portal = w
	exists := false
	flow.override = func(r request) (transport.Reply, error, bool) {
		switch {
		case r.path == "/user/v1/space/1/personal":
			return jsonReply(fmt.Sprintf(`{"libraryId":"lib","spaceId":"self","accessToken":%q,"expiresIn":3600}`, syntheticToken)), nil, true
		case r.path == "/api/v1/directory/lib/self/new" && r.method == "GET":
			if exists {
				return jsonReply(`{"name":"new","type":"dir"}`), nil, true
			}
			return transport.Reply{Status: 404}, nil, true
		case r.path == "/api/v1/directory/lib/self/new" && r.method == "PUT":
			exists = true
			return jsonReply(`{"path":["new"]}`), nil, true
		case r.path == "/api/v1/directory/lib/self/new.txt" && r.method == "GET":
			if exists {
				sum := md5.Sum([]byte("data"))
				return jsonReply(fmt.Sprintf(`{"name":"new.txt","type":"file","size":"4","eTag":%q}`, hex.EncodeToString(sum[:]))), nil, true
			}
			return transport.Reply{Status: 404}, nil, true
		case r.path == "/api/v1/file/lib/self/new.txt" && r.method == "PUT":
			return transport.Reply{Status: 201, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"confirmKey":"SYNTHETIC_CONFIRM","domain":"s3pan.jcloud.sjtu.edu.cn","path":"/tced-private-synthetic/lib/synthetic-object","headers":{"authorization":"AWS4-HMAC-SHA256 SYNTHETIC_STORAGE_SIGNATURE"}}`)}, nil, true
		case r.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" && r.method == "POST":
			exists = true
			return jsonReply(`{"path":["new.txt"]}`), nil, true
		}
		return transport.Reply{}, nil, false
	}
	return s, w, flow, &exists
}

func TestCreateDirectoryAndNewFileOnly(t *testing.T) {
	for _, action := range []string{"mkdir", "upload"} {
		t.Run(action, func(t *testing.T) {
			s, f, _, _ := setupWrite(t)
			q := contracts.PanMutation{Action: action, Path: "new"}
			if action == "upload" {
				q.Path = "new.txt"
				q.DataBase64 = base64.StdEncoding.EncodeToString([]byte("data"))
			}
			r, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", q)
			if e != nil || r.Path != q.Path || r.Status != "created" || action == "upload" && r.Size != 4 {
				t.Fatalf("mutation projection: %#v %v", r, e)
			}
			for _, request := range f.requests {
				if request.method == "PUT" || request.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" {
					if request.query.Get("conflict_resolution_strategy") != "ask" {
						t.Fatal("Write omitted conflict refusal")
					}
				}
			}
			if action == "upload" && (f.storage.calls != 1 || string(f.storage.data) != "data") {
				t.Fatal("Approved bytes not uploaded once")
			}
			_, e = s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", q)
			errCode(t, e, "CONFLICT")
		})
	}
}

func TestWriteUnknownAndNoAutomaticRetry(t *testing.T) {
	s, f, _, _ := setupWrite(t)
	f.storage.fail = true
	_, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "upload", Path: "new.txt", DataBase64: "ZGF0YQ=="})
	errCode(t, e, "WRITE_OUTCOME_UNKNOWN")
	if f.storage.calls != 1 {
		t.Fatal("Storage was retried")
	}
	for _, r := range f.requests {
		if r.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" {
			t.Fatal("Unverified upload was confirmed")
		}
	}
}

func TestDirectoryCreatedWithEmpty201StillRequiresExactMetadata(t *testing.T) {
	for _, test := range []string{"created", "missing", "wrong_kind", "wrong_name"} {
		t.Run(test, func(t *testing.T) {
			s, f, flow, exists := setupWrite(t)
			previous := flow.override
			flow.override = func(r request) (transport.Reply, error, bool) {
				if r.path == "/api/v1/directory/lib/self/new" && r.method == "PUT" {
					*exists = true
					return transport.Reply{Status: http.StatusCreated}, nil, true
				}
				if *exists && r.path == "/api/v1/directory/lib/self/new" && r.method == "GET" {
					switch test {
					case "missing":
						return transport.Reply{Status: http.StatusNotFound}, nil, true
					case "wrong_kind":
						return jsonReply(`{"name":"new","type":"file","size":"0"}`), nil, true
					case "wrong_name":
						return jsonReply(`{"name":"different","type":"dir"}`), nil, true
					}
				}
				return previous(r)
			}
			result, err := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "mkdir", Path: "new"})
			if test == "created" {
				if err != nil || result.Status != "created" || result.Kind != "dir" || result.Path != "new" {
					t.Fatalf("official empty creation result: %#v %v", result, err)
				}
			} else {
				errCode(t, err, "WRITE_OUTCOME_UNKNOWN")
			}
			writes := 0
			for _, request := range f.requests {
				if request.method == "PUT" {
					writes++
				}
			}
			if writes != 1 {
				t.Fatal("directory creation was retried")
			}
		})
	}
}

func TestUploadOfficialHeadersAndCompletionProjection(t *testing.T) {
	for _, overwritten := range []bool{false, true} {
		t.Run(fmt.Sprint(overwritten), func(t *testing.T) {
			s, f, flow, exists := setupWrite(t)
			previous := flow.override
			flow.override = func(r request) (transport.Reply, error, bool) {
				if r.path == "/api/v1/file/lib/self/new.txt" && r.method == "PUT" {
					var body struct {
						Size string `json:"size"`
					}
					if json.Unmarshal([]byte(r.body), &body) != nil || body.Size != "4" || r.query.Get("filesize") != "4" {
						t.Fatal("upload size differs from approved bytes or official string shape")
					}
					return transport.Reply{Status: 201, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"confirmKey":"SYNTHETIC_CONFIRM","domain":"s3pan.jcloud.sjtu.edu.cn","path":"/tced-private-synthetic/lib/synthetic-object","headers":{"Authorization":"AWS4-HMAC-SHA256 SYNTHETIC_STORAGE_SIGNATURE","Content-Type":"text/plain","Cache-Control":"max-age=31104000","x-cos-acl":"private","x-cos-storage-class":"STANDARD","x-cos-traffic-limit":"204800"}}`)}, nil, true
				}
				if r.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" && r.method == "POST" {
					*exists = true
					return jsonReply(fmt.Sprintf(`{"path":["new.txt"],"isOverwritten":%t}`, overwritten)), nil, true
				}
				return previous(r)
			}
			result, err := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "upload", Path: "new.txt", DataBase64: "ZGF0YQ=="})
			if overwritten {
				errCode(t, err, "WRITE_OUTCOME_UNKNOWN")
			} else if err != nil || result.Size != 4 || result.Status != "created" {
				t.Fatalf("official upload: %#v %v", result, err)
			}
			if f.storage.calls != 1 {
				t.Fatal("upload retried")
			}
		})
	}
}

func TestMutationRejectsTraversalOversizeAndWrongAccountBeforeWrite(t *testing.T) {
	for _, q := range []contracts.PanMutation{{Action: "mkdir", Path: "../x"}, {Action: "mkdir", Path: "%2e%2e/x"}, {Action: "upload", Path: "x", DataBase64: base64.StdEncoding.EncodeToString(make([]byte, contracts.PanUploadMaxBytes+1))}, {Action: "upload", Path: "x", DataBase64: "ZG\nF0YQ=="}, {Action: "delete", Path: "x"}} {
		s, f, _, _ := setupWrite(t)
		_, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", q)
		if e == nil {
			t.Fatal("Unsafe mutation accepted")
		}
		for _, r := range f.requests {
			if r.method == "PUT" {
				t.Fatal("Invalid input reached a write")
			}
		}
	}
	s, f, _, _ := setupWrite(t)
	_, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:99", contracts.PanMutation{Action: "mkdir", Path: "new"})
	errCode(t, e, "PERMISSION_DENIED")
	for _, r := range f.requests {
		if r.method == "PUT" {
			t.Fatal("Different account reached a write")
		}
	}
}

func TestStorageHeaderAndURLAllowlist(t *testing.T) {
	for _, d := range []string{"localhost", "s3.jcloud.sjtu.edu.cn", "s3pan4.jcloud.sjtu.edu.cn", "s3pan.jcloud.sjtu.edu.cn.evil.invalid", "s3pan.jcloud.sjtu.edu.cn:443", "s3pan.jcloud.sjtu.edu.cn@evil.invalid", "https://s3pan.jcloud.sjtu.edu.cn", "s3pan.jcloud.sjtu.edu.cn/"} {
		if _, e := storageURL(d, "/object"); e == nil {
			t.Fatal("Unreviewed storage host")
		}
	}
	for _, p := range []string{"//evil.invalid/x", "/../x", "/x?user_token=SYNTHETIC", "/%2e%2e/x", "/%2Fx", "/%5cx", "/%00x", "/x\ny", "/x\\y", "/x/", "/./x", "/x//y", "/" + strings.Repeat("x", 4096)} {
		if _, e := storageURL("s3pan.jcloud.sjtu.edu.cn", p); e == nil {
			t.Fatal("Unreviewed storage path")
		}
	}
	for _, key := range []string{"Cookie", "Host", "Proxy-Authorization", "X-Forwarded-For", "Location"} {
		if _, e := uploadHeaders(map[string]string{"Authorization": "AWS4-HMAC-SHA256 SYNTHETIC", key: "SYNTHETIC"}); e == nil {
			t.Fatal("Unsafe storage header")
		}
	}
	if _, e := uploadHeaders(map[string]string{"Authorization": "Bearer SYNTHETIC"}); e == nil {
		t.Fatal("Unknown signature type")
	}
	for key, value := range map[string]string{"x-cos-acl": "public-read", "x-amz-acl": "public-read-write", "Cache-Control": "public, max-age=3600", "x-cos-storage-class": "ARCHIVE", "x-cos-traffic-limit": "0"} {
		if _, err := uploadHeaders(map[string]string{"Authorization": "AWS4-HMAC-SHA256 SYNTHETIC", key: value}); err == nil {
			t.Fatal("unreviewed storage policy accepted")
		}
	}
	u, _ := url.Parse("https://s3pan.jcloud.sjtu.edu.cn/bucket/object?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=SYNTHETIC&X-Amz-Date=20261026T000000Z&X-Amz-Expires=7200&X-Amz-Signature=SYNTHETIC&X-Amz-SignedHeaders=host")
	if !validStorageDownload(u) {
		t.Fatal("Reviewed school storage download rejected")
	}
	u.RawQuery = "user_token=SYNTHETIC"
	if validStorageDownload(u) {
		t.Fatal("Storage token injection accepted")
	}
}

func TestObservedSchoolStorageHostAndSignedDownloadQuery(t *testing.T) {
	for _, host := range []string{"s3pan.jcloud.sjtu.edu.cn", "s3pan2.jcloud.sjtu.edu.cn", "s3pan3.jcloud.sjtu.edu.cn"} {
		u, err := storageURL(host, "/bucket/lib/object")
		if err != nil || u.Host != host {
			t.Fatal("reviewed storage upload host rejected")
		}
		u.RawQuery = "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Content-Sha256=UNSIGNED-PAYLOAD&X-Amz-Credential=SYNTHETIC&X-Amz-Date=20261027T000000Z&X-Amz-Expires=7200&X-Amz-Signature=SYNTHETIC&X-Amz-SignedHeaders=host&response-content-disposition=inline&share-id=SYNTHETIC&smh-space=library%3Aspace&traffic-owner=organization%3A1&traffic-usage=download"
		if !validStorageDownload(u) {
			t.Fatal("reviewed signed download rejected")
		}
		q := u.Query()
		q.Set("X-Amz-Content-Sha256", "SYNTHETIC_UNREVIEWED")
		u.RawQuery = q.Encode()
		if validStorageDownload(u) {
			t.Fatal("unreviewed payload hash accepted")
		}
	}
}

func TestSignedDownloadMeteringIsBoundedWithoutChangingURL(t *testing.T) {
	u, _ := url.Parse("https://s3pan2.jcloud.sjtu.edu.cn/bucket/lib/object?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=SYNTHETIC&X-Amz-Date=20261026T000000Z&X-Amz-Expires=7200&X-Amz-Signature=SYNTHETIC&X-Amz-SignedHeaders=host&smh-space=lib%3Aspace&traffic-usage=download")
	before := u.String()
	if !validStorageDownload(u) || u.String() != before {
		t.Fatal("first-party signed URL was rejected or rewritten")
	}
	for _, key := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-Signature", "X-Amz-SignedHeaders"} {
		q := u.Query()
		q.Del(key)
		v := *u
		v.RawQuery = q.Encode()
		if validStorageDownload(&v) {
			t.Fatal("incomplete signing fields accepted")
		}
	}
	for _, value := range []string{strings.Repeat("x", 513), "SYNTHETIC\nPRIVATE", "SYNTHETIC\x00", "合成", "value with spaces"} {
		q := u.Query()
		q.Set("traffic-owner", value)
		v := *u
		v.RawQuery = q.Encode()
		if validStorageDownload(&v) {
			t.Fatal("unbounded or non-token metering value accepted")
		}
	}
	q := u.Query()
	q.Set("redirect", "https://evil.invalid")
	u.RawQuery = q.Encode()
	if validStorageDownload(u) {
		t.Fatal("unreviewed URL parameter accepted")
	}
}

func TestStorageObjectURLMatchesFirstPartySegmentEncoding(t *testing.T) {
	for _, test := range []struct{ object, escaped string }{
		{"/bucket/lib/object:part@x", "/bucket/lib/object%3Apart%40x"},
		{"/bucket/lib/中文 name#tag.txt", "/bucket/lib/%E4%B8%AD%E6%96%87%20name%23tag.txt"},
		{"/bucket/lib/literal%20name+other&key=value", "/bucket/lib/literal%2520name%2Bother%26key%3Dvalue"},
		{"/bucket/lib/!f'()*", "/bucket/lib/!f'()*"},
		{"/bucket/" + strings.Repeat("x", 256), "/bucket/" + strings.Repeat("x", 256)},
	} {
		u, err := storageURL("s3pan.jcloud.sjtu.edu.cn", test.object)
		if err != nil || u.String() != "https://s3pan.jcloud.sjtu.edu.cn"+test.escaped || u.Path != test.object || u.RawQuery != "" || u.Fragment != "" {
			t.Fatalf("storage object was not encoded as a raw key: %v", err)
		}
	}
	for _, test := range []struct{ domain, object, reason string }{
		{"https://s3pan.jcloud.sjtu.edu.cn", "/object", "host"},
		{"s3pan.jcloud.sjtu.edu.cn", "relative", "path_shape"},
		{"s3pan.jcloud.sjtu.edu.cn", "/object?key=value", "path_syntax"},
		{"s3pan.jcloud.sjtu.edu.cn", "/../object", "path_segment"},
		{"s3pan.jcloud.sjtu.edu.cn", "/%2e%2e/object", "path_encoding"},
	} {
		_, reason := checkedStorageURL(test.domain, test.object)
		if reason != test.reason {
			t.Fatalf("safe diagnostic reason: got %q want %q", reason, test.reason)
		}
	}
}

func TestUploadRawStorageKeyUsesOneEncodedPinnedPUT(t *testing.T) {
	s, f, flow, _ := setupWrite(t)
	f.storage.url = "https://s3pan.jcloud.sjtu.edu.cn/bucket/lib/part%3Aone%23tag%2520%20name"
	previous := flow.override
	flow.override = func(r request) (transport.Reply, error, bool) {
		if r.path == "/api/v1/file/lib/self/new.txt" && r.method == "PUT" {
			return jsonReply(`{"confirmKey":"SYNTHETIC_CONFIRM","domain":"s3pan.jcloud.sjtu.edu.cn","path":"/bucket/lib/part:one#tag%20 name","headers":{"authorization":"AWS4-HMAC-SHA256 SYNTHETIC_STORAGE_SIGNATURE"}}`), nil, true
		}
		return previous(r)
	}
	result, err := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "upload", Path: "new.txt", DataBase64: "ZGF0YQ=="})
	if err != nil || result.Status != "created" || f.storage.calls != 1 || string(f.storage.data) != "data" {
		t.Fatalf("encoded storage upload: %v", err)
	}
}

func TestUploadStorageRefusalHasSafeStageWithoutLeakingUpstreamValues(t *testing.T) {
	for _, domain := range []string{
		"uploads.jcloud.sjtu.edu.cn", "SYNTHETIC_SECRET.invalid", "https://uploads.jcloud.sjtu.edu.cn",
		"uploads.jcloud.sjtu.edu.cn:443", "uploads.jcloud.sjtu.edu.cn.", "a.b.jcloud.sjtu.edu.cn",
		"0123.jcloud.sjtu.edu.cn", strings.Repeat("a", 17) + ".jcloud.sjtu.edu.cn",
		"uploads.jcloud.sjtu.edu.cn\nSYNTHETIC_SECRET",
	} {
		s, f, flow, _ := setupWrite(t)
		previous := flow.override
		flow.override = func(r request) (transport.Reply, error, bool) {
			if r.path == "/api/v1/file/lib/self/new.txt" && r.method == "PUT" {
				return jsonReply(fmt.Sprintf(`{"confirmKey":"SYNTHETIC_CONFIRM","domain":%q,"path":"/SYNTHETIC_PRIVATE_OBJECT","headers":{"authorization":"AWS4-HMAC-SHA256 SYNTHETIC_STORAGE_SIGNATURE"}}`, domain)), nil, true
			}
			return previous(r)
		}
		_, err := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "upload", Path: "new.txt", DataBase64: "ZGF0YQ=="})
		errCode(t, err, "WRITE_OUTCOME_UNKNOWN")
		if f.storage.calls != 0 || !strings.Contains(err.Error(), "stage=upload_storage_host") {
			t.Fatal("unreviewed host reached storage or lacked a fixed failure stage")
		}
		text := err.Error()
		if strings.Contains(text, "SYNTHETIC_") || strings.Contains(text, "://") || strings.Contains(text, "/SYNTHETIC_PRIVATE_OBJECT") {
			t.Fatal("upstream private value appeared in error")
		}
		if domain == "uploads.jcloud.sjtu.edu.cn" {
			if !strings.Contains(text, "infrastructure_host="+domain) || storageHost(domain) {
				t.Fatal("safe hostname projection changed the endpoint authorization")
			}
		} else if strings.Contains(text, "infrastructure_host=") || strings.Contains(text, domain) {
			t.Fatal("arbitrary upstream hostname appeared in error")
		}
		for _, request := range f.requests {
			if request.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" {
				t.Fatal("unreviewed storage upload was confirmed")
			}
		}
	}
	if text := unknownStorageWrite("SYNTHETIC_SECRET.invalid", "SYNTHETIC_UNTRUSTED_REASON").Error(); !strings.Contains(text, "stage=upload_storage_url") || strings.Contains(text, "SYNTHETIC_") {
		t.Fatal("untrusted stage appeared in error")
	}
}
