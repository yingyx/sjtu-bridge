package pan

import (
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDownloadErrorContainsOnlyFixedStageAndSchoolInfrastructureHost(t *testing.T) {
	for _, raw := range []string{"https://s3pan4.jcloud.sjtu.edu.cn/private/SYNTHETIC_OBJECT?token=SYNTHETIC_SECRET", "https://SYNTHETIC_SECRET.evil.invalid/private?token=SYNTHETIC_SECRET", "https://SYNTHETIC_SECRET@s3pan4.jcloud.sjtu.edu.cn/private"} {
		u, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		err := storageRedirectDenied(u).(*contracts.Error)
		if err.Code != "PERMISSION_DENIED" || !strings.Contains(err.Message, "stage=download_storage_") || strings.Contains(err.Message, "SYNTHETIC_SECRET") || strings.Contains(err.Message, "SYNTHETIC_OBJECT") || strings.Contains(err.Message, "/private") {
			t.Fatal("storage denial leaked destination data")
		}
	}
}
