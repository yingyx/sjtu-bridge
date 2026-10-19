package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func itoa(n int) string { return strconv.Itoa(n) }

// OwnerCall is used only by explicit terminal management commands. The owner
// secret is distinct from the vault master key and is never returned to CLI.
func OwnerCall(ctx context.Context, q AdminRequest, out any) error {
	token, e := storage.Owner(storage.SystemKeyring{})
	if e != nil {
		return e
	}
	endpoint, e := local.Endpoint()
	if e != nil {
		return storageError()
	}
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return local.Dial(ctx, endpoint) }, MaxResponseHeaderBytes: 8192}
	defer tr.CloseIdleConnections()
	b, e := json.Marshal(q)
	defer clear(b)
	if e != nil || len(b) > contracts.MaxJSONBytes {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid owner request")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", "http://bridge.local/owner/v1/manage", bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	res, e := (&http.Client{Transport: tr, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	if e != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Local Broker unavailable; run sjtu broker run")
	}
	defer res.Body.Close()
	v, e := io.ReadAll(io.LimitReader(res.Body, contracts.MaxJSONBytes+1))
	if e != nil || len(v) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Invalid owner response")
	}
	if res.StatusCode != 200 {
		var er struct {
			Error *contracts.Error `json:"error"`
		}
		if json.Unmarshal(v, &er) != nil || er.Error == nil {
			return contracts.Fail("BROKER_UNAVAILABLE", "Invalid Broker response")
		}
		return er.Error
	}
	if json.Unmarshal(v, out) != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Invalid owner response")
	}
	return nil
}
