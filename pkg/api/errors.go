package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Canonical API error codes, mirrored 1:1 from docs/devel/rest-api-conventions.md
// §4 and internal/web/handlers/helpers.go's errCode* constants, so both the Go
// and JS (web/static/sdk/core/errors.js) SDKs agree on the taxonomy.
const (
	CodeBadRequest       = "bad_request"
	CodeUnauthenticated  = "unauthenticated"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeConflict         = "conflict"
	CodeTooLarge         = "too_large"
	CodeRateLimited      = "rate_limited"
	CodeUnavailable      = "unavailable"
	CodeServerError      = "server_error"
)

// Sentinel errors for errors.Is checks. (*APIError).Is matches by HTTP
// Status (not Code), so errors.Is(err, ErrConflict) is true for any 409
// response even when the server attaches an app-specific code such as
// "queue_full" (see internal/web/handlers/queue.go) instead of the
// canonical "conflict" — HTTP status is the more fundamental classifier
// and every canonical code maps to exactly one status.
var (
	ErrBadRequest   = &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest}
	ErrUnauthorized = &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthenticated}
	ErrForbidden    = &APIError{Status: http.StatusForbidden, Code: CodeForbidden}
	ErrNotFound     = &APIError{Status: http.StatusNotFound, Code: CodeNotFound}
	ErrConflict     = &APIError{Status: http.StatusConflict, Code: CodeConflict}
	ErrTooLarge     = &APIError{Status: http.StatusRequestEntityTooLarge, Code: CodeTooLarge}
	ErrRateLimited  = &APIError{Status: http.StatusTooManyRequests, Code: CodeRateLimited}
	ErrUnavailable  = &APIError{Status: http.StatusServiceUnavailable, Code: CodeUnavailable}
	ErrServerError  = &APIError{Status: http.StatusInternalServerError, Code: CodeServerError}
)

// APIError represents a non-2xx HTTP response from the Mitto REST API.
// It carries the parsed error envelope fields so callers can branch on
// Code (via errors.Is/errors.As) without string-matching Error().
//
// The raw response body is always preserved in Body, even when it could
// not be parsed as either error envelope shape, so callers needing custom
// parsing are never blocked by this type.
type APIError struct {
	Op      string         // short operation label, e.g. "get session"
	Status  int            // HTTP status code
	Code    string         // canonical or server-supplied error code
	Message string         // human-readable message
	Details map[string]any // optional structured context (canonical envelope only)
	Body    []byte         // raw response body
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("%s: status %d (%s): %s", e.Op, e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("status %d (%s): %s", e.Status, e.Code, e.Message)
}

// Is reports whether target is one of the sentinel *APIError values above,
// matching by HTTP Status. This lets a single concrete APIError value
// returned by the client satisfy errors.Is(err, ErrNotFound) regardless of
// Op/Message wording, which envelope shape produced it, or whether the
// server attached an app-specific Code instead of the canonical one.
func (e *APIError) Is(target error) bool {
	t, ok := target.(*APIError)
	if !ok || t == nil {
		return false
	}
	return e.Status != 0 && e.Status == t.Status
}

// errorCodeForStatus maps an HTTP status to the canonical error code,
// mirroring defaultCodeForStatus in internal/web/handlers/helpers.go and
// errorCodeForStatus in web/static/sdk/core/errors.js. Used when the
// response body doesn't carry an explicit code (e.g. a non-JSON body).
func errorCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeTooLarge
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	default:
		return CodeServerError
	}
}

// errorFromResponse builds an *APIError from a non-2xx HTTP response body.
// It never panics on a malformed/empty/non-JSON body. It parses both
// envelope shapes documented in docs/devel/rest-api-conventions.md §4:
//
//   - canonical nested: {"error":{"code":"...","message":"...","details":{}}}
//   - legacy flat: {"error":"code","message":"..."} (external-stable
//     endpoints, e.g. POST /api/callback/{token} via writeCallbackError)
//
// Message precedence deliberately mirrors errorFromResponse in
// web/static/sdk/core/errors.js: nested message, then a top-level message
// field, then the flat error-code string, then a generic fallback. Since
// the code is surfaced separately via Code, the sibling message is
// preferred over echoing the code.
func errorFromResponse(op string, status int, body []byte) *APIError {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw) // best-effort; raw stays nil on failure

	var flatCode string
	var nested struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	var topLevelMessage string

	if raw != nil {
		if errRaw, ok := raw["error"]; ok {
			if err := json.Unmarshal(errRaw, &nested); err != nil {
				nested = struct {
					Code    string         `json:"code"`
					Message string         `json:"message"`
					Details map[string]any `json:"details"`
				}{}
				_ = json.Unmarshal(errRaw, &flatCode)
			}
		}
		if msgRaw, ok := raw["message"]; ok {
			_ = json.Unmarshal(msgRaw, &topLevelMessage)
		}
	}

	code := nested.Code
	if code == "" {
		code = flatCode
	}
	if code == "" {
		code = errorCodeForStatus(status)
	}

	message := nested.Message
	if message == "" {
		message = topLevelMessage
	}
	if message == "" {
		message = flatCode
	}
	if message == "" {
		message = fmt.Sprintf("request failed with status %d", status)
	}

	return &APIError{
		Op:      op,
		Status:  status,
		Code:    code,
		Message: message,
		Details: nested.Details,
		Body:    body,
	}
}

// apiError is a convenience wrapper used by client.go call sites: it reads
// the response body once and builds the typed error from it.
func (c *Client) apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return errorFromResponse(op, resp.StatusCode, body)
}

// sessionNotFoundError builds a 404 *APIError for endpoints that short-circuit
// on a missing session before reading the body. sessionID is carried in
// Details so errors.As callers can recover it, while the rendered message
// keeps the historical "session not found: <id>" wording.
func sessionNotFoundError(op, sessionID string) error {
	return &APIError{
		Op:      op,
		Status:  http.StatusNotFound,
		Code:    CodeNotFound,
		Message: fmt.Sprintf("session not found: %s", sessionID),
		Details: map[string]any{"session_id": sessionID},
	}
}
