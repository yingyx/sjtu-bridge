package qrlogin

import (
	"errors"
	"net/url"

	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// Diagnostic contains only implementation-defined stage/endpoint labels and
// bounded HTTP metadata. It cannot contain a URL, account, cookie or QR value.
type Diagnostic struct {
	Stage          string `json:"stage"`
	Endpoint       string `json:"endpoint,omitempty"`
	Hop            int    `json:"hop,omitempty"`
	Status         int    `json:"status,omitempty"`
	RedirectTarget string `json:"redirect_target,omitempty"`
	RedirectShape  string `json:"redirect_shape,omitempty"`
}

type stageError struct {
	diagnostic Diagnostic
	cause      error
}

func (e *stageError) Error() string { return e.cause.Error() }
func (e *stageError) Unwrap() error { return e.cause }

func diagnosticError(err error, d Diagnostic) error {
	if err == nil {
		return nil
	}
	var previous *stageError
	if errors.As(err, &previous) {
		return err
	}
	return &stageError{diagnostic: d, cause: err}
}

// Diagnose returns safe metadata, never the underlying error text.
func Diagnose(err error) Diagnostic {
	var e *stageError
	if errors.As(err, &e) {
		return e.diagnostic
	}
	return Diagnostic{Stage: "unknown"}
}

func endpoint(u *url.URL) string {
	if u == nil {
		return "unreviewed"
	}
	if u.Host == "my.sjtu.edu.cn" && u.Path == "/ui/appmyinfo" {
		return "my_callback"
	}
	if u.Host == host {
		switch u.Path {
		case "/jaccount/expresslogin":
			return "express"
		case "/oauth2/authorize":
			return "oauth"
		case "/jaccount/jalogin":
			return "sign_in"
		}
	}
	return "unreviewed"
}

// Describe rejected redirects using a closed vocabulary. Even a hostile target
// cannot insert a URL, unknown query key, ticket or account into diagnostics.
func describeRedirect(d *Diagnostic, u *url.URL) {
	d.RedirectTarget = endpoint(u)
	if u == nil {
		d.RedirectShape = "invalid_url"
		return
	}
	if u.Scheme != "https" {
		d.RedirectShape = "non_https"
		return
	}
	if !transport.ExactHTTPS(u, host) && !transport.ExactHTTPS(u, "my.sjtu.edu.cn") {
		d.RedirectShape = "unreviewed_url"
		return
	}
	q, ok := oneQuery(u)
	if !ok {
		d.RedirectShape = "invalid_query"
		return
	}
	switch {
	case len(q) == 0:
		d.RedirectShape = "no_query"
	case len(q) == 1 && q.Get("code") != "":
		d.RedirectShape = "code_only"
	case len(q) == 2 && q.Get("code") != "" && q.Get("state") != "":
		d.RedirectShape = "code_state"
	case q.Get("context") != "" && q.Get("jatkt") != "" && (len(q) == 2 || len(q) == 3 && q.Get("state") != ""):
		d.RedirectShape = "oauth_context_ticket"
	case q.Get("client_id") != "" && q.Get("redirect_uri") != "" && q.Get("response_type") != "":
		d.RedirectShape = "oauth_client"
	default:
		d.RedirectShape = "other_query"
	}
}
