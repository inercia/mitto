package middleware

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response with the given status code.
// This is a package-local helper to avoid importing internal/web (which would cause an import cycle).
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeJSONOK writes a JSON response with status 200 OK.
func writeJSONOK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, data)
}

// errorEnvelope is the canonical error response shape. See
// docs/devel/rest-api-conventions.md §4. Mirrors the handlers package.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Canonical API error codes, mapped 1:1 to HTTP status codes per
// docs/devel/rest-api-conventions.md §4.
const (
	errCodeBadRequest       = "bad_request"
	errCodeUnauthenticated  = "unauthenticated"
	errCodeForbidden        = "forbidden"
	errCodeNotFound         = "not_found"
	errCodeMethodNotAllowed = "method_not_allowed"
	errCodeConflict         = "conflict"
	errCodeTooLarge         = "too_large"
	errCodeRateLimited      = "rate_limited"
	errCodeServerError      = "server_error"
)

// defaultCodeForStatus returns the canonical error code for an HTTP status,
// per rest-api-conventions.md §4. Unmapped statuses fall back to server_error.
func defaultCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return errCodeBadRequest
	case http.StatusUnauthorized:
		return errCodeUnauthenticated
	case http.StatusForbidden:
		return errCodeForbidden
	case http.StatusNotFound:
		return errCodeNotFound
	case http.StatusMethodNotAllowed:
		return errCodeMethodNotAllowed
	case http.StatusConflict:
		return errCodeConflict
	case http.StatusRequestEntityTooLarge:
		return errCodeTooLarge
	case http.StatusTooManyRequests:
		return errCodeRateLimited
	default:
		return errCodeServerError
	}
}

// writeErrorJSON writes the canonical JSON error envelope:
// {"error":{"code":...,"message":...}}. An empty errorCode derives the
// canonical code from the status.
func writeErrorJSON(w http.ResponseWriter, status int, errorCode, message string) {
	if errorCode == "" {
		errorCode = defaultCodeForStatus(status)
	}
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: errorCode, Message: message}})
}
