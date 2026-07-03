package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// These are package-local copies of the HTTP helpers in internal/web, kept here
// to avoid importing internal/web (which would cause an import cycle).

// writeJSON writes a JSON response with the given status code.
// It sets the Content-Type header to application/json and disables caching.
// API responses should never be cached to ensure clients always get fresh data.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

// writeJSONOK writes a JSON response with status 200 OK.
func writeJSONOK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, data)
}

// writeJSONCreated writes a JSON response with status 201 Created.
func writeJSONCreated(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusCreated, data)
}

// methodNotAllowed writes a 405 Method Not Allowed response.
func methodNotAllowed(w http.ResponseWriter) {
	writeErrorJSON(w, http.StatusMethodNotAllowed, "", "Method not allowed")
}

// writeNoContent writes a 204 No Content response.
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// errorBody is the inner object of the canonical API error envelope.
type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// errorEnvelope is the canonical error response shape for all non-exception API
// responses. See docs/devel/rest-api-conventions.md §4.
type errorEnvelope struct {
	Error errorBody `json:"error"`
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
	errCodeUnavailable      = "unavailable"
)

// auxBackedRequestTimeout bounds aux/bd-backed handlers BELOW the 30s
// middleware cap (middleware.DefaultRequestTimeout) so they can write a
// clear, retryable 503 before http.TimeoutHandler emits its opaque one.
// It is a var only so tests can shorten it; treat it as constant in prod.
var auxBackedRequestTimeout = 25 * time.Second

// defaultCodeForStatus returns the canonical error code string for an HTTP
// status code, per the policy table in rest-api-conventions.md §4. Unmapped
// statuses fall back to server_error.
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
	case http.StatusServiceUnavailable:
		return errCodeUnavailable
	default:
		return errCodeServerError
	}
}

// writeRetryableUnavailable writes a 503 with a Retry-After header and the
// canonical "unavailable" error envelope, signalling the client to retry shortly.
func writeRetryableUnavailable(w http.ResponseWriter, message string, retryAfterSeconds int) {
	if retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	writeErrorJSON(w, http.StatusServiceUnavailable, errCodeUnavailable, message)
}

// writeErrorJSON writes a structured JSON error response using the canonical
// error envelope: {"error":{"code":...,"message":...}}.
// An empty errorCode derives the canonical code from the status.
func writeErrorJSON(w http.ResponseWriter, status int, errorCode, message string) {
	if errorCode == "" {
		errorCode = defaultCodeForStatus(status)
	}
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: errorCode, Message: message}})
}

// parseJSONBody decodes the request body as JSON into the given value.
// Returns true if successful, false if there was an error (error response already sent).
func parseJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body: "+err.Error())
		return false
	}
	return true
}

// writeJSONWithETag serializes data to JSON, computes an ETag from the response body,
// and returns 304 Not Modified if the client's If-None-Match header matches.
// This saves bandwidth for endpoints that are polled frequently with rarely-changing data.
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, data interface{}) {
	body, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// json.Encoder adds a trailing newline; match that for consistency
	body = append(body, '\n')

	hash := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(hash[:]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache") // Must revalidate, but can use ETag

	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}
