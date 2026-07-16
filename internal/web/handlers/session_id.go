package handlers

import (
	"net/http"
	"regexp"
)

// sessionIDRegex matches the session ID format: YYYYMMDD-HHMMSS-XXXXXXXX
// where YYYYMMDD is the date, HHMMSS is the time, and XXXXXXXX is 8 hex characters.
// Example: 20260127-113605-87080ed8
var sessionIDRegex = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-fA-F]{8}$`)

// uuidRegex matches standard UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
// Example: b7a07613-3d2b-47c4-9f50-1ffd710f3a49
// This supports legacy sessions created before the timestamp format was adopted.
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidSessionID checks if the given string is a valid session ID.
// Supports two formats:
//   - Timestamp format: YYYYMMDD-HHMMSS-XXXXXXXX (current standard)
//   - UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (legacy, for backward compatibility)
func IsValidSessionID(id string) bool {
	if id == "" {
		return false
	}
	return sessionIDRegex.MatchString(id) || uuidRegex.MatchString(id)
}

// sessionIDFromPath extracts the {id} path wildcard and validates it. On an
// invalid ID it writes a 400 and returns ok=false.
func sessionIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	sessionID := r.PathValue("id")
	if !IsValidSessionID(sessionID) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid session ID format")
		return "", false
	}
	return sessionID, true
}
