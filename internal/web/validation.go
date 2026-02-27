package web

import (
	"regexp"
	"strings"
	"unicode"
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

// GenericErrorMessages maps internal error types to user-friendly messages.
// This prevents leaking internal details to clients.
var GenericErrorMessages = map[string]string{
	"session_create": "Failed to create session",
	"session_resume": "Failed to resume session",
	"session_rename": "Failed to rename session",
	"session_load":   "Failed to load session",
	"session_store":  "Session storage unavailable",
	"prompt_send":    "Failed to send message",
	"prompt_failed":  "Request failed",
	"events_read":    "Failed to read session data",
	"metadata_read":  "Failed to read session information",
}

// Credential validation constants.
const (
	// MinUsernameLength is the minimum allowed length for usernames.
	MinUsernameLength = 3
	// MaxUsernameLength is the maximum allowed length for usernames.
	MaxUsernameLength = 64
	// MinPasswordLength is the minimum allowed length for passwords.
	MinPasswordLength = 8
	// MaxPasswordLength is the maximum allowed length for passwords.
	MaxPasswordLength = 128
)

// Common weak passwords that should be rejected.
var commonWeakPasswords = map[string]bool{
	"password":   true,
	"password1":  true,
	"password12": true,
	"12345678":   true,
	"123456789":  true,
	"qwerty123":  true,
	"admin123":   true,
	"letmein":    true,
	"welcome":    true,
	"monkey123":  true,
	"dragon123":  true,
	"master123":  true,
	"changeme":   true,
}

// ValidateUsername validates a username for external access authentication.
// Returns an error message if invalid, empty string if valid.
func ValidateUsername(username string) string {
	username = strings.TrimSpace(username)

	if username == "" {
		return "Username is required"
	}

	if len(username) < MinUsernameLength {
		return "Username must be at least 3 characters"
	}

	if len(username) > MaxUsernameLength {
		return "Username must be at most 64 characters"
	}

	// Check for control characters
	for _, r := range username {
		if unicode.IsControl(r) {
			return "Username cannot contain control characters"
		}
	}

	// Username should start with a letter or number
	if len(username) > 0 {
		first := rune(username[0])
		if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
			return "Username must start with a letter or number"
		}
	}

	// Check for valid characters (alphanumeric, underscore, hyphen, dot)
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return "Username can only contain letters, numbers, underscore, hyphen, and dot"
		}
	}

	return ""
}

// ValidatePassword validates a password for external access authentication.
// Returns an error message if invalid, empty string if valid.
func ValidatePassword(password string) string {
	if password == "" {
		return "Password is required"
	}

	if len(password) < MinPasswordLength {
		return "Password must be at least 8 characters"
	}

	if len(password) > MaxPasswordLength {
		return "Password must be at most 128 characters"
	}

	// Check for control characters (except common whitespace)
	for _, r := range password {
		if unicode.IsControl(r) && r != '\t' {
			return "Password cannot contain control characters"
		}
	}

	// Check against common weak passwords (case-insensitive)
	if commonWeakPasswords[strings.ToLower(password)] {
		return "Password is too common. Please choose a stronger password"
	}

	// Check for minimum complexity: at least one letter and one number or special char
	hasLetter := false
	hasNonLetter := false
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if !unicode.IsSpace(r) {
			hasNonLetter = true
		}
	}

	if !hasLetter || !hasNonLetter {
		return "Password must contain at least one letter and one number or special character"
	}

	return ""
}
