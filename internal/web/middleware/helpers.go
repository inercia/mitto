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
