package pan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestObservedUploadAndDownloadNodeIsExact(t *testing.T) {
	s, f, flow, _ := setupWrite(t)
	previous := flow.override
	const host = "s3pan3.jcloud.sjtu.edu.cn"
	f.storage.url = "https://" + host + "/tced-private-synthetic/lib/synthetic-object"
	flow.override = func(r request) (transport.Reply, error, bool) {
		reply, err, handled := previous(r)
		if handled && r.path == "/api/v1/file/lib/self/new.txt" && r.method == http.MethodPut {
			var init uploadInit
			if json.Unmarshal(reply.Body, &init) != nil {
				t.Fatal("synthetic init invalid")
			}
			init.Domain = host
			reply.Body, _ = json.Marshal(init)
		}
		return reply, err, handled
	}
	result, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "upload", Path: "new.txt", DataBase64: "ZGF0YQ=="})
	if e != nil || result.Status != "created" || f.storage.calls != 1 || string(f.storage.data) != "data" {
		t.Fatal("observed upload node did not complete exactly once", e)
	}
	initCalls, confirmCalls := 0, 0
	for _, r := range f.requests {
		if r.path == "/api/v1/file/lib/self/new.txt" && r.method == http.MethodPut {
			initCalls++
		}
		if r.path == "/api/v1/file/lib/self/SYNTHETIC_CONFIRM" && r.method == http.MethodPost {
			confirmCalls++
		}
	}
	if initCalls != 1 || confirmCalls != 1 {
		t.Fatal("upload stages repeated")
	}
	for _, domain := range []string{"s3pan4.jcloud.sjtu.edu.cn", "s3pan3.jcloud.sjtu.edu.cn.evil.invalid", "s3pan3.jcloud.sjtu.edu.cn:443", "https://s3pan3.jcloud.sjtu.edu.cn", "S3PAN3.jcloud.sjtu.edu.cn"} {
		if _, e := storageURL(domain, "/synthetic/object"); e == nil {
			t.Fatal("unreviewed upload domain accepted")
		}
	}
	u, _ := url.Parse("https://" + host + "/bucket/object?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=SYNTHETIC&X-Amz-Date=20261027T000000Z&X-Amz-Expires=7200&X-Amz-Signature=SYNTHETIC&X-Amz-SignedHeaders=host")
	if !validStorageDownload(u) {
		t.Fatal("independently observed authenticated download node rejected")
	}
	u.Host = "s3pan4.jcloud.sjtu.edu.cn"
	if validStorageDownload(u) {
		t.Fatal("unobserved numbered download node accepted")
	}
}
