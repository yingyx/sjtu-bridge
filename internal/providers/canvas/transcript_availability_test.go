package canvas

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestTranscriptSuccessfulNullDataIsExplicitlyUnavailable(t *testing.T) {
	for _, tc := range []struct{ name, path, body, code string }{
		{"no_original", recordingBase + "/v1/course/ai/translate/9", `{"status":200,"ok":true,"dataOk":false,"code":null,"data":null}`, "RESOURCE_UNAVAILABLE"},
		{"error_code", recordingBase + "/v1/course/ai/translate/9", `{"status":200,"ok":true,"dataOk":false,"code":"DENIED","data":null}`, "UPSTREAM_CHANGED"},
		{"failure", recordingBase + "/v1/course/ai/translate/9", `{"status":200,"ok":false,"dataOk":false,"data":null}`, "UPSTREAM_CHANGED"},
		{"missing_data", recordingBase + "/v1/course/ai/translate/9", `{"status":200,"ok":true,"dataOk":false}`, "UPSTREAM_CHANGED"},
		{"other_endpoint", recordingBase + "/lms/launch-context", `{"status":200,"ok":true,"dataOk":false,"data":null}`, "UPSTREAM_CHANGED"},
		{"missing_status", recordingBase + "/v1/course/ai/translate/9", `{"ok":true,"dataOk":false,"data":null}`, "UPSTREAM_CHANGED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Service{RecordingClient: recordingClientFunc{get: func(*url.URL, http.Header) (transport.Reply, error) { return reply(tc.body), nil }}}
			q := url.Values(nil)
			if tc.path != recordingBase+"/lms/launch-context" {
				q = url.Values{"useOriginal": {"true"}}
			}
			data, e := s.recordingRead(context.Background(), "SYNTHETIC_RECORDING", tc.path, q)
			failure, ok := e.(*contracts.Error)
			if !ok || failure.Code != tc.code || data != nil {
				t.Fatal("unexpected unavailable/error projection", e)
			}
		})
	}
}
