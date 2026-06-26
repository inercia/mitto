package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response with the given status code.
// It sets the Content-Type header to application/json and disables caching.
// API responses should never be cached to ensure clients always get fresh data.
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

// writeJSONCreated writes a JSON response with status 201 Created.
func writeJSONCreated(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusCreated, data)
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

// writeErrorJSON writes a structured JSON error response using the canonical
// error envelope: {"error":{"code":...,"message":...}}.
// For simple text errors, use http.Error directly.
func writeErrorJSON(w http.ResponseWriter, status int, errorCode, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: errorCode, Message: message}})
}

// writeNoContent writes a 204 No Content response.
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// parseJSONBody decodes the request body as JSON into the given value.
// Returns true if successful, false if there was an error (error response already sent).
func parseJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
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

// methodNotAllowed writes a 405 Method Not Allowed response.
func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
