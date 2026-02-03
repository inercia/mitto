package web

import (
	"strings"
	"testing"
)

func TestIsValidSessionID(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		// Valid session IDs (format: YYYYMMDD-HHMMSS-XXXXXXXX)
		{"valid session ID", "20260127-113605-87080ed8", true},
		{"valid session ID uppercase hex", "20260127-113605-87080ED8", true},
		{"valid session ID mixed case hex", "20260127-113605-87080eD8", true},
		{"valid session ID midnight", "20260101-000000-00000000", true},
		{"valid session ID end of day", "20261231-235959-ffffffff", true},

		// Invalid session IDs
		{"empty string", "", false},
		{"too short", "20260127-113605", false},
		{"path traversal attempt", "../../../etc/passwd", false},
		{"path traversal with session ID", "20260127-113605-87080ed8/../../../etc/passwd", false},
		{"null bytes", "20260127-113605-8708\x00ed8", false},
		{"invalid date format", "2026-01-27-113605-87080ed8", false},
		{"invalid time format", "20260127-11:36:05-87080ed8", false},
		{"invalid hex characters", "20260127-113605-8708zzzz", false},
		{"no dashes", "2026012711360587080ed8", false},
		{"extra dashes", "20260127-113605-8708-0ed8", false},
		{"wrong segment lengths", "202601-27113605-87080ed8", false},
		{"UUID v4 format (not valid)", "550e8400-e29b-41d4-a716-446655440000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidSessionID(tt.id)
			if got != tt.valid {
				t.Errorf("IsValidSessionID(%q) = %v, want %v", tt.id, got, tt.valid)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
		errMsg   string
	}{
		// Valid usernames
		{"valid simple", "admin", false, ""},
		{"valid with numbers", "user123", false, ""},
		{"valid with dot", "john.doe", false, ""},
		{"valid with hyphen", "my-user", false, ""},
		{"valid with underscore", "my_user", false, ""},
		{"valid mixed case", "User123", false, ""},
		{"valid minimum length", "abc", false, ""},

		// Invalid usernames
		{"empty", "", true, "Username is required"},
		{"only spaces", "   ", true, "Username is required"},
		{"too short", "ab", true, "Username must be at least 3 characters"},
		{"single char", "a", true, "Username must be at least 3 characters"},
		{"too long", strings.Repeat("a", MaxUsernameLength+1), true, "Username must be at most 64 characters"},
		{"starts with underscore", "_user", true, "Username must start with a letter or number"},
		{"starts with hyphen", "-user", true, "Username must start with a letter or number"},
		{"starts with dot", ".user", true, "Username must start with a letter or number"},
		{"contains at", "user@name", true, "Username can only contain letters, numbers, underscore, hyphen, and dot"},
		{"contains space", "user name", true, "Username can only contain letters, numbers, underscore, hyphen, and dot"},
		{"contains exclamation", "user!123", true, "Username can only contain letters, numbers, underscore, hyphen, and dot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			hasErr := err != ""
			if hasErr != tt.wantErr {
				t.Errorf("ValidateUsername(%q) hasErr = %v, wantErr %v, got error: %q", tt.username, hasErr, tt.wantErr, err)
			}
			if tt.wantErr && err != tt.errMsg {
				t.Errorf("ValidateUsername(%q) error = %q, want %q", tt.username, err, tt.errMsg)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pwd     string
		wantErr bool
		errMsg  string
	}{
		// Valid passwords
		{"valid complex", "MyP@ssw0rd", false, ""},
		{"valid alphanumeric", "SecurePass123", false, ""},
		{"valid simple", "abcd1234", false, ""},
		{"valid with special chars", "Pass!@#$%", false, ""},
		{"valid minimum length", "a1b2c3d4", false, ""},
		{"valid special chars only", "Password!", false, ""},

		// Invalid passwords
		{"empty", "", true, "Password is required"},
		{"too short", "abc123", true, "Password must be at least 8 characters"},
		{"too short 2", "Pass1", true, "Password must be at least 8 characters"},
		{"too long", strings.Repeat("a1", 65), true, "Password must be at most 128 characters"},
		{"common password", "password", true, "Password is too common. Please choose a stronger password"},
		{"common password uppercase", "PASSWORD", true, "Password is too common. Please choose a stronger password"},
		{"common 12345678", "12345678", true, "Password is too common. Please choose a stronger password"},
		{"common qwerty123", "qwerty123", true, "Password is too common. Please choose a stronger password"},
		{"common admin123", "admin123", true, "Password is too common. Please choose a stronger password"},
		{"common changeme", "changeme", true, "Password is too common. Please choose a stronger password"},
		{"no letters", "12345678!", true, "Password must contain at least one letter and one number or special character"},
		{"no numbers or special", "abcdefgh", true, "Password must contain at least one letter and one number or special character"},
		{"no numbers or special 2", "PasswordOnly", true, "Password must contain at least one letter and one number or special character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.pwd)
			hasErr := err != ""
			if hasErr != tt.wantErr {
				t.Errorf("ValidatePassword(%q) hasErr = %v, wantErr %v, got error: %q", tt.pwd, hasErr, tt.wantErr, err)
			}
			if tt.wantErr && err != tt.errMsg {
				t.Errorf("ValidatePassword(%q) error = %q, want %q", tt.pwd, err, tt.errMsg)
			}
		})
	}
}
