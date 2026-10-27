package pan

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// This pattern only limits safe diagnostic output. Storage destinations are
// independently authorized by the exact endpoint list below.
var publicInfrastructureHost = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}\.jcloud\.sjtu\.edu\.cn$`)

func storageRedirectDenied(dest *url.URL) error {
	message := "Pan storage redirect denied"
	if dest != nil {
		message += " (stage=download_storage_" + storageDownloadReason(dest)
		if publicInfrastructureHost.MatchString(dest.Host) {
			message += "; infrastructure_host=" + dest.Host
		}
		message += ")"
	}
	return contracts.Fail("PERMISSION_DENIED", message)
}
func storageHost(host string) bool {
	// Nodes 2 and 3 were observed on authenticated first-party download
	// redirects. Node 3 was also signed by simple-upload initialization and
	// independently checked over HTTPS. Keep exact endpoints, never infer
	// domain suffixes or further numbered nodes.
	return host == "s3pan.jcloud.sjtu.edu.cn" || host == "s3pan2.jcloud.sjtu.edu.cn" || host == "s3pan3.jcloud.sjtu.edu.cn" || cosHost.MatchString(host)
}

// Download redirects can carry only the reviewed storage service's signature
// parameters. Each actual request is additionally pinned to the exact URL.
func validStorageDownload(u *url.URL) bool {
	return storageDownloadReason(u) == ""
}

func storageDownloadReason(u *url.URL) string {
	if u == nil || !storageHost(u.Host) {
		return "host"
	}
	if !transport.ExactHTTPS(u, u.Host) {
		return "url_shape"
	}
	if !contracts.ValidPanPath(strings.TrimPrefix(u.Path, "/"), false) || !strings.HasPrefix(u.Path, "/") {
		return "path"
	}
	q, ok := sso.Values(u, 16)
	if !ok {
		return "query_shape"
	}
	if u.Host == "s3pan.jcloud.sjtu.edu.cn" || u.Host == "s3pan2.jcloud.sjtu.edu.cn" || u.Host == "s3pan3.jcloud.sjtu.edu.cn" {
		if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
			return "signature_fields"
		}
		for _, key := range []string{"X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-Signature", "X-Amz-SignedHeaders"} {
			if q.Get(key) == "" {
				return "signature_fields"
			}
		}
	}
	for k, v := range q {
		if len(v) != 1 || len(v[0]) > 4096 {
			return "query_value"
		}
		switch strings.ToLower(k) {
		case "x-amz-algorithm", "x-amz-credential", "x-amz-date", "x-amz-expires", "x-amz-signedheaders", "x-amz-signature", "x-amz-security-token", "q-sign-algorithm", "q-ak", "q-sign-time", "q-key-time", "q-header-list", "q-url-param-list", "q-signature", "x-cos-security-token", "response-content-disposition", "response-content-type":
		case "x-amz-content-sha256":
			if v[0] != "UNSIGNED-PAYLOAD" {
				return "payload_hash"
			}
		case "share-id", "smh-space", "traffic-owner", "traffic-usage":
			// These four opaque accounting fields are present on this school's
			// first-party signed download redirects. Like the deployed SDK's
			// info.cosUrl, preserve the entire signed URL without rewriting any
			// field. They never select another destination or an API operation.
			if len(v[0]) > 512 || strings.IndexFunc(v[0], func(c rune) bool { return c < 0x21 || c > 0x7e }) >= 0 {
				return "metering_value"
			}
		default:
			return "query_key"
		}
	}
	return ""
}
