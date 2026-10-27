package contracts

import (
	"encoding/base64"
	"strings"
)

const (
	PanMkdirCapability  = "digital.pan.directories.create"
	PanUploadCapability = "digital.pan.files.upload"
	PanUploadMaxBytes   = 32 << 10
)

// PanMutation describes only a new direct child of an independently authorized
// parent directory. Existing objects are never replaced or automatically renamed.
type PanMutation struct {
	Action     string `json:"action"`
	Path       string `json:"path"`
	DataBase64 string `json:"data_base64,omitempty"`
}
type PanMutationResult struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

func ValidatePanMutation(q PanMutation) error {
	if !ValidPanPath(q.Path, false) || strings.Contains(q.Path, "%") {
		return Fail("INVALID_ARGUMENT", "Expected a new relative Pan object path")
	}
	switch q.Action {
	case "mkdir":
		if q.DataBase64 != "" {
			return Fail("INVALID_ARGUMENT", "Directory creation cannot contain file bytes")
		}
	case "upload":
		if len(q.DataBase64) > base64.StdEncoding.EncodedLen(PanUploadMaxBytes) {
			return Fail("INVALID_ARGUMENT", "Pan upload exceeds 32 KiB")
		}
		b, e := base64.StdEncoding.Strict().DecodeString(q.DataBase64)
		defer clear(b)
		if e != nil || len(b) > PanUploadMaxBytes || base64.StdEncoding.EncodeToString(b) != q.DataBase64 {
			return Fail("INVALID_ARGUMENT", "Expected canonical base64 file bytes within 32 KiB")
		}
	default:
		return Fail("INVALID_ARGUMENT", "Unsupported Pan mutation")
	}
	return nil
}
