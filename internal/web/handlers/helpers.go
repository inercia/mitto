package handlers

import (
	"encoding/json"
	"net/http"
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

// methodNotAllowed writes a 405 Method Not Allowed response.
func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// writeNoContent writes a 204 No Content response.
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// writeErrorJSON writes a structured JSON error response with the given status
// code, error code, and message.
func writeErrorJSON(w http.ResponseWriter, status int, errorCode, message string) {
	writeJSON(w, status, map[string]string{
		"error":   errorCode,
		"message": message,
	})
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
