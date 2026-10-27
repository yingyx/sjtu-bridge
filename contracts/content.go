package contracts

import (
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxContentBytes  int64 = 1 << 30
	ContentChunkSize       = 256 << 10
)

var contentRefPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ContentDescriptor describes an immutable version through a short-lived,
// application-bound reference. It contains no upstream or local file location.
type ContentDescriptor struct {
	Ref       string    `json:"ref"`
	Name      string    `json:"name"`
	MIMEType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	Version   string    `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
	ChunkSize int       `json:"chunk_size"`
}

type ContentRequest struct {
	Ref string `json:"ref"`
}

type ContentReadRequest struct {
	Ref     string `json:"ref"`
	Version string `json:"version"`
	Offset  int64  `json:"offset"`
	Length  int    `json:"length"`
}

func ValidContentRef(ref string) bool { return contentRefPattern.MatchString(ref) }

func ValidContentVersion(version string) bool {
	if len(version) == 0 || len(version) > 128 {
		return false
	}
	for _, c := range version {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func (q ContentRequest) Valid() bool { return ValidContentRef(q.Ref) }

func (q ContentReadRequest) Valid() bool {
	return ValidContentRef(q.Ref) && ValidContentVersion(q.Version) && q.Offset >= 0 && q.Offset <= MaxContentBytes && q.Length > 0 && q.Length <= ContentChunkSize
}

func (d ContentDescriptor) Valid() bool {
	if !ValidContentRef(d.Ref) || !ValidContentVersion(d.Version) || d.Size < 0 || d.Size > MaxContentBytes || d.ChunkSize != ContentChunkSize || d.ExpiresAt.IsZero() {
		return false
	}
	if len(d.Name) == 0 || len(d.Name) > 255 || !utf8.ValidString(d.Name) || strings.TrimSpace(d.Name) != d.Name || d.Name == "." || d.Name == ".." || strings.ContainsAny(d.Name, `/\`) {
		return false
	}
	for _, c := range d.Name {
		if unicode.IsControl(c) {
			return false
		}
	}
	mediaType, params, err := mime.ParseMediaType(d.MIMEType)
	return err == nil && len(d.MIMEType) <= 128 && len(params) == 0 && strings.Contains(mediaType, "/") && mediaType == d.MIMEType
}

// ContentDescriptorSchema is shared by explicit content-producing capabilities.
// A descriptor contains only an application-bound reference, never a URL.
func ContentDescriptorSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"ref":        map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
		"name":       map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
		"mime_type":  map[string]any{"type": "string", "maxLength": 128},
		"size":       map[string]any{"type": "integer", "minimum": 0, "maximum": MaxContentBytes},
		"version":    map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"expires_at": map[string]any{"type": "string"},
		"chunk_size": map[string]any{"type": "integer", "const": ContentChunkSize},
	}, "required": []string{"ref", "name", "mime_type", "size", "version", "expires_at", "chunk_size"}}
}
