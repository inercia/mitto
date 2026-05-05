package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCallbackStore_GenerateToken(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	token, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Verify token format: cb_ + 64 hex chars
	if !strings.HasPrefix(token, callbackTokenPrefix) {
		t.Errorf("Token should start with %q, got %q", callbackTokenPrefix, token)
	}

	expectedLen := len(callbackTokenPrefix) + 64 // cb_ + 64 hex chars
	if len(token) != expectedLen {
		t.Errorf("Token length = %d, want %d", len(token), expectedLen)
	}

	if !ValidateCallbackToken(token) {
		t.Errorf("Token should be valid: %q", token)
	}

	// Verify callback.json was created
	callbackPath := filepath.Join(dir, callbackFileName)
	if _, err := os.Stat(callbackPath); os.IsNotExist(err) {
		t.Fatal("callback.json should exist after GenerateToken()")
	}

	// Verify stored config
	config, err := cs.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if config.Token != token {
		t.Errorf("Stored token = %q, want %q", config.Token, token)
	}

	if config.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	if config.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestCallbackStore_GenerateToken_Rotation(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Generate first token
	token1, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() first error = %v", err)
	}

	// Wait a bit to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Generate second token (rotation)
	token2, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() second error = %v", err)
	}

	// Tokens should differ
	if token1 == token2 {
		t.Error("Rotated token should differ from original")
	}

	// Verify only new token is stored
	config, err := cs.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if config.Token != token2 {
		t.Errorf("Stored token = %q, want %q (new token)", config.Token, token2)
	}

	if config.Token == token1 {
		t.Error("Old token should be gone from file")
	}
}

func TestCallbackStore_GenerateToken_PreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Generate initial token
	_, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() first error = %v", err)
	}

	config1, _ := cs.Get()
	originalCreatedAt := config1.CreatedAt
	originalUpdatedAt := config1.UpdatedAt

	// Wait to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Rotate token
	_, err = cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() second error = %v", err)
	}

	config2, _ := cs.Get()

	// CreatedAt should be preserved
	if !config2.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt changed: got %v, want %v", config2.CreatedAt, originalCreatedAt)
	}

	// UpdatedAt should be different
	if config2.UpdatedAt.Equal(originalUpdatedAt) {
		t.Error("UpdatedAt should have changed")
	}

	// UpdatedAt should be after CreatedAt
	if !config2.UpdatedAt.After(config2.CreatedAt) {
		t.Error("UpdatedAt should be after CreatedAt")
	}
}

func TestCallbackStore_Get(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Generate token
	token, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Get and verify
	config, err := cs.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if config.Token != token {
		t.Errorf("Token = %q, want %q", config.Token, token)
	}

	if config.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	if config.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestCallbackStore_Get_NotFound(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Get without generating token
	_, err := cs.Get()
	if err != ErrCallbackNotFound {
		t.Errorf("Get() error = %v, want ErrCallbackNotFound", err)
	}
}

func TestCallbackStore_Revoke(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Generate token
	_, err := cs.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	callbackPath := filepath.Join(dir, callbackFileName)
	if _, err := os.Stat(callbackPath); os.IsNotExist(err) {
		t.Fatal("callback.json should exist after GenerateToken()")
	}

	// Revoke
	if err := cs.Revoke(); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	// File should be gone
	if _, err := os.Stat(callbackPath); !os.IsNotExist(err) {
		t.Error("callback.json should not exist after Revoke()")
	}

	// Get should return not found
	_, err = cs.Get()
	if err != ErrCallbackNotFound {
		t.Errorf("Get() after Revoke() error = %v, want ErrCallbackNotFound", err)
	}
}

func TestCallbackStore_Revoke_NotFound(t *testing.T) {
	dir := t.TempDir()
	cs := NewCallbackStore(dir)

	// Revoke without generating token
	err := cs.Revoke()
	if err != ErrCallbackNotFound {
		t.Errorf("Revoke() error = %v, want ErrCallbackNotFound", err)
	}
}

func TestValidateCallbackToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{
			name:  "valid token",
			token: "cb_" + strings.Repeat("a", 64),
			want:  true,
		},
		{
			name:  "valid token with mixed hex",
			token: "cb_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  true,
		},
		{
			name:  "missing prefix",
			token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  false,
		},
		{
			name:  "wrong prefix",
			token: "xx_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  false,
		},
		{
			name:  "too short",
			token: "cb_" + strings.Repeat("a", 63),
			want:  false,
		},
		{
			name:  "too long",
			token: "cb_" + strings.Repeat("a", 65),
			want:  false,
		},
		{
			name:  "non-hex characters",
			token: "cb_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdez",
			want:  false,
		},
		{
			name:  "uppercase hex (valid)",
			token: "cb_0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
			want:  true,
		},
		{
			name:  "empty string",
			token: "",
			want:  false,
		},
		{
			name:  "just prefix",
			token: "cb_",
			want:  false,
		},
		{
			name:  "special characters in hex part",
			token: "cb_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abc!ef",
			want:  false,
		},
		{
			name:  "spaces in token",
			token: "cb_ 123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateCallbackToken(tt.token)
			if got != tt.want {
				t.Errorf("ValidateCallbackToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}
