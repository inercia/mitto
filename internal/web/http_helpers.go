package web

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response with the given status code.
// It sets the Content-Type header to application/json.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
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

// writeError writes an error response with the given status code.
// For simple text errors, use http.Error directly.
// This function is for JSON error responses with structured data.
func writeErrorJSON(w http.ResponseWriter, status int, errorCode, message string) {
	writeJSON(w, status, map[string]string{
		"error":   errorCode,
		"message": message,
	})
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

// methodNotAllowed writes a 405 Method Not Allowed response.
func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
