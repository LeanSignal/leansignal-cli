package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/leansignal/leansignal-cli/internal/config"
)

// Exit codes. Scripts branch on these, so they are part of the CLI's contract
// and must not be renumbered.
const (
	ExitOK         = 0
	ExitError      = 1 // generic failure
	ExitUsage      = 2 // bad flags or arguments
	ExitAuth       = 3 // 401 / 403
	ExitNotFound   = 4 // 404
	ExitValidation = 5 // 422
	ExitServer     = 6 // 5xx
	ExitNetwork    = 7 // could not reach the API
)

// APIError is a non-2xx response from lean-api, decoded from its standard
// {error, message, details} envelope.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details any
	// LoginURL is set when the API reports an expired browser session. It is
	// never actionable for a token, and is only surfaced for cookie auth.
	LoginURL string
	Method   string
	Path     string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}

	if e.Code != "" && !strings.EqualFold(e.Code, msg) {
		return fmt.Sprintf("%s (%s)", msg, e.Code)
	}

	return msg
}

// Hint returns an actionable next step for the errors where one exists.
func (e *APIError) Hint() string {
	switch {
	// session_expired is the API's *cookie* rejection: it means the server
	// evaluated the request as a browser one and found no session. Against a
	// bearer token that is not a stale credential, it is a server that does not
	// accept tokens on this endpoint — so telling the user to log in again would
	// send them round a loop that cannot terminate.
	case e.Code == "session_expired":
		return "this API answered as if no browser session was present, which means it does not yet" +
			" accept personal access tokens — the token auth middleware is not deployed on this tenant." +
			" Re-running 'auth login' will not help"
	case e.Code == "session_required":
		return "that endpoint needs a browser session and is not part of leanctl — use the web app"
	case e.Code == "insufficient_scope":
		return "this token lacks the scope for that action — mint one with 'write'" +
			" (or 'write:delete' to delete) via 'leanctl auth tokens create'"
	case e.Code == "invalid_token", e.Status == http.StatusUnauthorized:
		return "the token was rejected — mint a new one in the web app, then" +
			" 'leanctl auth login --tenant <tenant>'"
	case e.Status == http.StatusForbidden:
		return "your role does not allow this action"
	default:
		return ""
	}
}

// ExitCode maps an error to the process exit status.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	// A missing credential is an auth problem from a script's point of view:
	// the fix is the same as for a rejected token.
	if errors.Is(err, config.ErrNoContext) {
		return ExitAuth
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusUnauthorized, apiErr.Status == http.StatusForbidden:
			return ExitAuth
		case apiErr.Status == http.StatusNotFound:
			return ExitNotFound
		case apiErr.Status == http.StatusUnprocessableEntity:
			return ExitValidation
		case apiErr.Status >= 500:
			return ExitServer
		default:
			return ExitError
		}
	}

	var netErr *NetworkError
	if errors.As(err, &netErr) {
		return ExitNetwork
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return ExitUsage
	}

	return ExitError
}

// NetworkError wraps a transport failure — the API was never reached.
type NetworkError struct {
	Op  string
	Err error
}

func (e *NetworkError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *NetworkError) Unwrap() error { return e.Err }

// UsageError is a caller mistake: bad flags, missing arguments, unreadable file.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usage builds a UsageError.
func Usage(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// parseAPIError decodes lean-api's error envelope. Bodies that are not the
// expected shape (a proxy's HTML error page, say) still produce a usable
// message rather than an empty one.
func parseAPIError(status int, method, path string, body []byte) *APIError {
	e := &APIError{Status: status, Method: method, Path: path}

	var env struct {
		Error    string `json:"error"`
		Message  string `json:"message"`
		Details  any    `json:"details"`
		LoginURL string `json:"login_url"`
	}

	if err := json.Unmarshal(body, &env); err == nil {
		e.Code, e.Message, e.Details, e.LoginURL = env.Error, env.Message, env.Details, env.LoginURL
	}

	// Some envelopes carry a code and no message — session_expired is
	// {error, login_url}. Rendering the raw JSON at the user was worse than
	// saying nothing, so derive a sentence from the code instead.
	if e.Message == "" && e.Code != "" {
		e.Message = strings.ReplaceAll(e.Code, "_", " ")
	}

	if e.Message == "" {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}

		if snippet != "" && !strings.HasPrefix(snippet, "<") {
			e.Message = snippet
		} else {
			e.Message = fmt.Sprintf("HTTP %d from %s %s", status, method, path)
		}
	}

	return e
}
