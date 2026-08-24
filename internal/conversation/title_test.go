package conversation

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

// TestSessionNeedsTitle_FallbackTitleStillNeedsUpgrade reproduces mitto-ee3:
// after GenerateAndSetTitle's step 1 sets a quick fallback title, Metadata.Name is
// non-empty but the title is not the real LLM-generated one. The current
// SessionNeedsTitle only checks meta.Name == "", so it returns false and
// titleCoordinator.retryIfNeeded short-circuits forever — the fallback title
// permanently blocks any upgrade even after auxiliary health recovers.
//
// This test seeds a session with a fallback-only title (Name set,
// NameIsFallback=true) and asserts SessionNeedsTitle returns true so that the
// prompt_complete quiescence path can re-attempt LLM title generation.
//
// EXPECTED TO FAIL on current tree (SessionNeedsTitle returns false when Name
// is set, regardless of NameIsFallback). Will pass after the Fix phase widens
// the predicate to treat fallback-only titles as needing generation.
func TestSessionNeedsTitle_FallbackTitleStillNeedsUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-fallback"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Simulate GenerateAndSetTitle step 1: quick fallback populates Name and
	// marks it as a fallback (the marker is the mechanism the Fix phase will
	// use to distinguish fallback from real titles).
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "Fix the login bug"
		m.NameIsFallback = true
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	if !SessionNeedsTitle(store, sessionID) {
		t.Fatal("mitto-ee3: SessionNeedsTitle should return true for a fallback-only " +
			"title so retryIfNeeded can upgrade it to the real LLM-generated title. " +
			"Got false — fallback title permanently blocks the upgrade path.")
	}
}

// TestSessionNeedsTitle_ExplicitRenameSuppressesAutoTitle reproduces mitto-808:
// an explicit rename (human or MCP mitto_conversation_update) must permanently
// suppress auto-title generation, even over an existing fallback title.
//
// Sequence (mirrors internal/mcpserver/tools_conversation_lifecycle.go
// handleConversationUpdate and internal/web/handlers/session_update.go
// HandleUpdateSession, both of which set NameExplicit=true and clear
// NameIsFallback on a non-empty rename after the mitto-808 fix):
//  1. GenerateAndSetTitle step 1 sets a quick fallback title (Name=fallback,
//     NameIsFallback=true) — see title.go:210-215.
//  2. An explicit rename (human/MCP) sets Name to the user's chosen title and
//     marks NameExplicit=true (clearing NameIsFallback), exactly as the two
//     fixed rename handlers do.
//  3. SessionNeedsTitle must now return false — the conversation has an
//     explicit name and must never be auto-titled/clobbered again.
//
// Before the mitto-808 fix, the rename handlers only wrote meta.Name and
// never cleared meta.NameIsFallback, so SessionNeedsTitle (which only checked
// `meta.Name == "" || meta.NameIsFallback`) incorrectly kept reporting "needs
// title" after a rename — letting the post-turn safety net
// (title_coordinator.go:43-51) and the in-flight aux generator
// (title.go:389-391) overwrite the explicit rename.
func TestSessionNeedsTitle_ExplicitRenameSuppressesAutoTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-explicit-rename"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: GenerateAndSetTitle's quick fallback (title.go:210-215).
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "Fix the login bug"
		m.NameIsFallback = true
	}); err != nil {
		t.Fatalf("UpdateMetadata (fallback) failed: %v", err)
	}

	// Step 2: explicit rename, mirroring the FIXED rename handlers
	// (tools_conversation_lifecycle.go, session_update.go), which set
	// NameExplicit=true and clear NameIsFallback on a non-empty rename.
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "OCINC2573062"
		m.NameExplicit = true
		m.NameIsFallback = false
	}); err != nil {
		t.Fatalf("UpdateMetadata (explicit rename) failed: %v", err)
	}

	// Step 3: an explicitly-renamed conversation must never be reported as
	// still needing a title.
	if SessionNeedsTitle(store, sessionID) {
		t.Fatal("mitto-808: SessionNeedsTitle should return false after an explicit " +
			"rename, but it returned true — auto-title generation stays eligible " +
			"and will clobber the explicit rename (post-turn retryIfNeeded and/or " +
			"the in-flight aux generator).")
	}
}

// TestSessionNeedsTitle_EmptyRenameReenablesAutoTitle verifies the "clear
// name" semantics of the mitto-808 fix: renaming to an empty string clears
// NameExplicit (and NameIsFallback), so SessionNeedsTitle starts reporting
// "needs title" again — auto-title is not permanently disabled once a name
// is cleared.
func TestSessionNeedsTitle_EmptyRenameReenablesAutoTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-empty-rename"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Explicit rename first.
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "Explicit Name"
		m.NameExplicit = true
		m.NameIsFallback = false
	}); err != nil {
		t.Fatalf("UpdateMetadata (explicit rename) failed: %v", err)
	}
	if SessionNeedsTitle(store, sessionID) {
		t.Fatal("SessionNeedsTitle should return false right after an explicit rename")
	}

	// Clear the name, mirroring the fixed handlers' empty-rename branch.
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = ""
		m.NameExplicit = false
		m.NameIsFallback = false
	}); err != nil {
		t.Fatalf("UpdateMetadata (clear rename) failed: %v", err)
	}
	if !SessionNeedsTitle(store, sessionID) {
		t.Fatal("mitto-808: clearing an explicit name should re-enable auto-title, " +
			"but SessionNeedsTitle returned false")
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
