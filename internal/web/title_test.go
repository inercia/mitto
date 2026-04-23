package web

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestSessionNeedsTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session without a name
	sessionID := "test-session-1"
	err = store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // No name
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Session without name should need title
	if !SessionNeedsTitle(store, sessionID) {
		t.Error("SessionNeedsTitle should return true for session without name")
	}

	// Update session with a name
	err = store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "My Session"
	})
	if err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// Session with name should not need title
	if SessionNeedsTitle(store, sessionID) {
		t.Error("SessionNeedsTitle should return false for session with name")
	}
}

func TestSessionNeedsTitle_NilStore(t *testing.T) {
	if SessionNeedsTitle(nil, "some-id") {
		t.Error("SessionNeedsTitle should return false for nil store")
	}
}

func TestSessionNeedsTitle_EmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if SessionNeedsTitle(store, "") {
		t.Error("SessionNeedsTitle should return false for empty session ID")
	}
}

func TestSessionNeedsTitle_NonExistentSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if SessionNeedsTitle(store, "non-existent") {
		t.Error("SessionNeedsTitle should return false for non-existent session")
	}
}

func TestGenerateAndSetTitle_NilStore(t *testing.T) {
	// Should not panic with nil store
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:     nil,
		SessionID: "test",
		Message:   "Hello",
		OnTitleGenerated: func(sessionID, title string) {
			// This won't be called since auxiliary isn't initialized
		},
	})

	// Give goroutine time to run (it should exit early due to auxiliary not being initialized)
	// This test mainly verifies no panic occurs
}

func TestGenerateQuickTitle(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string // "" means we expect empty (no title)
	}{
		{
			name:    "simple message",
			message: "How do I fix the login bug in the auth service",
			want:    "How do I fix the login", // 6 words
		},
		{
			name:    "markdown bold",
			message: "**Fix the login bug** in the auth service now",
			want:    "Fix the login bug in the",
		},
		{
			name:    "markdown heading",
			message: "## Fix the login bug\nMore details here",
			want:    "Fix the login bug More details",
		},
		{
			name:    "markdown link",
			message: "Please review [the auth PR](https://github.com/org/repo/pull/123) soon",
			want:    "Please review the auth PR soon",
		},
		{
			name:    "bare URL stripped",
			message: "https://example.com/page is the reference for this fix",
			want:    "Is the reference for this fix",
		},
		{
			name:    "inline code stripped",
			message: "Call `getUserById` to fetch the user record",
			want:    "Call to fetch the user record",
		},
		{
			name:    "fenced code block stripped",
			message: "```go\nfunc foo() {}\n```\nThis implements the feature",
			want:    "This implements the feature",
		},
		{
			name:    "very short message returns empty",
			message: "ok",
			want:    "",
		},
		{
			name:    "empty message returns empty",
			message: "",
			want:    "",
		},
		{
			name:    "single char returns empty",
			message: "x",
			want:    "",
		},
		{
			name:    "all URL returns empty",
			message: "https://example.com/very/long/url/that/has/no/text",
			want:    "",
		},
		{
			name:    "all code block returns empty",
			message: "```\nsome code here\n```",
			want:    "",
		},
		{
			name:    "very long message capped at 50 chars",
			message: "Implement a comprehensive authentication system with OAuth2 support and MFA",
			want:    "Implement a comprehensive authentication system...", // 47 chars + "..."
		},
		{
			name:    "first letter capitalized",
			message: "fix the broken test in the CI pipeline",
			want:    "Fix the broken test in the",
		},
		{
			name:    "leading punctuation stripped",
			message: "...fix the broken test here",
			want:    "Fix the broken test here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateQuickTitle(tt.message)
			if got != tt.want {
				t.Errorf("GenerateQuickTitle(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

func TestTitleGenerationConfig_Fields(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	cfg := TitleGenerationConfig{
		Store:     store,
		SessionID: "test-session",
		Message:   "Test message",
		Logger:    nil,
		OnTitleGenerated: func(sessionID, title string) {
			// callback
		},
	}

	if cfg.Store != store {
		t.Error("Store field not set correctly")
	}
	if cfg.SessionID != "test-session" {
		t.Error("SessionID field not set correctly")
	}
	if cfg.Message != "Test message" {
		t.Error("Message field not set correctly")
	}
	if cfg.OnTitleGenerated == nil {
		t.Error("OnTitleGenerated callback not set")
	}
}
