package transcribe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const MaxUploadBytes = 16 << 20

// UploadHTTP is an optional bounded infrastructure port. Ordinary form POST's
// 64 KiB ceiling is not raised to enable uploads, nor bypassed using raw HTTP.
type UploadHTTP interface {
	PostUpload(context.Context, *url.URL, http.Header, []byte, int) (transport.Reply, error)
}

// Execute is not an authorization entry point. Only a trusted executor that
// has admitted the exact resource-consuming operation may call it. No automatic
// retry follows a POST: timeout, cancellation and failed verification all leave
// the outcome unknown. Cancellation is intentionally unsupported: the reviewed
// frontend exposes deletion, not a documented cancellation protocol.
func (s *Service) Execute(ctx context.Context, secret []byte, account string, in contracts.TranscriptionMutation) (contracts.TranscriptionMutationResult, error) {
	bad := func() (contracts.TranscriptionMutationResult, error) {
		return contracts.TranscriptionMutationResult{}, contracts.Fail("INVALID_ARGUMENT", "Expected one bounded transcription upload or optimization")
	}
	unknown := func() (contracts.TranscriptionMutationResult, error) {
		return contracts.TranscriptionMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Transcription write was attempted; inspect own tasks before any new request")
	}
	if !contracts.ValidCollaborationAccount("transcribe", account) {
		return bad()
	}
	var upload UploadHTTP
	switch in.Action {
	case "upload":
		var ok bool
		upload, ok = s.Portal.(UploadHTTP)
		if !ok {
			return contracts.TranscriptionMutationResult{}, contracts.Fail("NOT_READY", "Bounded transcription upload transport is unavailable")
		}
		if in.TaskRef != "" || len(in.Data) == 0 || len(in.Data) > MaxUploadBytes || !validFilename(in.Filename, in.MIME) {
			return bad()
		}
	case "optimize":
		if !contracts.ValidCollaborationResource(contracts.TranscriptionGetCapability, in.TaskRef) || in.Filename != "" || in.MIME != "" || len(in.Data) != 0 {
			return bad()
		}
	default:
		return bad()
	}
	x, a, e := s.login(ctx, secret)
	if e != nil {
		return contracts.TranscriptionMutationResult{}, e
	}
	if a != account {
		return contracts.TranscriptionMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Transcription mutation account changed")
	}
	before, e := tasks(ctx, x)
	if e != nil {
		return contracts.TranscriptionMutationResult{}, e
	}
	previous := map[string]task{}
	for _, t := range before {
		previous[t.ID] = t
	}
	taskID := strings.TrimPrefix(in.TaskRef, "transcribe:task:")
	if in.Action == "upload" {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": in.Filename}))
		header.Set("Content-Type", in.MIME)
		part, e := writer.CreatePart(header)
		if e != nil {
			return bad()
		}
		if _, e = part.Write(in.Data); e != nil {
			return bad()
		}
		if e = writer.Close(); e != nil {
			return bad()
		}
		u, _ := url.Parse(root + "/ai/file/audioVideo/upload")
		h := x.Header(u)
		h.Set("Content-Type", writer.FormDataContentType())
		h.Set("X-Requested-With", "XMLHttpRequest")
		r, e := upload.PostUpload(ctx, u, h, buf.Bytes(), MaxUploadBytes+4096)
		if e != nil {
			return unknown()
		}
		raw, e := payload(r)
		if e != nil {
			return unknown()
		}
		var name string
		if json.Unmarshal(raw, &name) != nil || len(name) > 200 || strings.ContainsAny(name, "/\\\r\n") {
			return unknown()
		}
		taskID = strings.TrimSuffix(name, path.Ext(name))
		if !objectID.MatchString(taskID) {
			return unknown()
		}
		if _, exists := previous[taskID]; exists {
			return unknown()
		}
	} else {
		t, ok := previous[taskID]
		if !ok || t.Deleted != "N" {
			return contracts.TranscriptionMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Selected task is absent from own visible tasks")
		}
		if t.Status != "2" || t.Original == "" || t.Original == "转录失败" || t.Optimized != "" {
			return contracts.TranscriptionMutationResult{}, contracts.Fail("RESOURCE_UNAVAILABLE", "Optimization requires a completed task without an existing optimized result")
		}
		body, _ := json.Marshal(map[string]string{"id": taskID, "response_mode": "streaming"})
		r, e := x.Post(ctx, root+"/ai/v2/chat-messages", "application/json", body)
		if e != nil {
			return unknown()
		}
		if !validStream(r) {
			return unknown()
		}
	}
	after, e := tasks(ctx, x)
	if e != nil {
		return unknown()
	}
	identityAfter, e := identity(ctx, x)
	if e != nil || identityAfter != account {
		return unknown()
	}
	for _, t := range after {
		if t.ID != taskID || t.Deleted != "N" {
			continue
		}
		if _, ok := statuses[t.Status]; !ok {
			return unknown()
		}
		if in.Action == "optimize" && t.Optimized == "" {
			return unknown()
		}
		return contracts.TranscriptionMutationResult{Action: in.Action, TaskRef: "transcribe:task:" + taskID, Status: statuses[t.Status]}, nil
	}
	return unknown()
}
func validFilename(name, media string) bool {
	if name == "" || len(name) > 160 || !text(name) || strings.ContainsAny(name, "/\\\r\n\t\x00") || name == "." || name == ".." {
		return false
	}
	ext := strings.ToLower(path.Ext(name))
	allowed := map[string]string{".mp3": "audio/", ".wav": "audio/", ".m4a": "audio/", ".aac": "audio/", ".mp4": "video/", ".m4v": "video/", ".mov": "video/", ".avi": "video/"}
	prefix, ok := allowed[ext]
	if !ok {
		return false
	}
	parsed, params, e := mime.ParseMediaType(media)
	return e == nil && len(params) == 0 && parsed == media && strings.HasPrefix(media, prefix)
}
func validStream(r transport.Reply) bool {
	if r.Status != 200 || len(r.Body) > 2<<20 || !text(string(r.Body)) {
		return false
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "text/event-stream" {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(r.Body))
	scanner.Buffer(make([]byte, 4096), MaxTextBytes)
	message := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var event struct {
			Event  string `json:"event"`
			Answer string `json:"answer"`
		}
		if strictjson.Decode([]byte(data), &event) != nil {
			return false
		}
		switch event.Event {
		case "message":
			if !text(event.Answer) {
				return false
			}
			message = true
		case "workflow_started", "workflow_finished", "node_started", "node_finished", "message_end", "ping":
		case "error":
			return false
		default:
			return false
		}
	}
	return scanner.Err() == nil && message
}
