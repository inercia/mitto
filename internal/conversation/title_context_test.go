package conversation

import (
	"strconv"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// Tests for BuildTitleContext (mitto-yv2): the transcript excerpt composer
// used by the "Auto-rename" forced-regenerate path.

func TestBuildTitleContext_NilStore(t *testing.T) {
	if got := BuildTitleContext(nil, "some-id"); got != "" {
		t.Errorf("BuildTitleContext(nil, ...) = %q, want empty", got)
	}
}

func TestBuildTitleContext_EmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if got := BuildTitleContext(store, ""); got != "" {
		t.Errorf("BuildTitleContext(store, \"\") = %q, want empty", got)
	}
}

func TestBuildTitleContext_NoEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "no-events-session"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if got := BuildTitleContext(store, sessionID); got != "" {
		t.Errorf("BuildTitleContext with no recorded events = %q, want empty", got)
	}
}

// TestBuildTitleContext_ComposesUserPromptsAndLastAgentMessage verifies the
// basic shape: chronological "User: .../Assistant: ..." transcript excerpt.
func TestBuildTitleContext_ComposesUserPromptsAndLastAgentMessage(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "compose-session"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	events := []session.Event{
		{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: "How do I add auth?"}},
		{Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "Use OAuth2."}},
		{Type: session.EventTypeUserPrompt, Data: session.UserPromptData{Message: "What about sessions?"}},
		{Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "<p>Use <strong>JWT</strong> tokens.</p>"}},
	}
	for _, e := range events {
		if err := store.AppendEvent(sessionID, e); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	got := BuildTitleContext(store, sessionID)

	if !strings.Contains(got, "User: How do I add auth?") {
		t.Errorf("context missing first user prompt: %q", got)
	}
	if !strings.Contains(got, "User: What about sessions?") {
		t.Errorf("context missing second user prompt: %q", got)
	}
	if !strings.Contains(got, "Assistant: Use JWT tokens.") {
		t.Errorf("context missing (HTML-stripped) last agent message: %q", got)
	}
	if strings.Contains(got, "<p>") || strings.Contains(got, "<strong>") {
		t.Errorf("context should have HTML stripped from the agent message: %q", got)
	}
	// Chronological order: the first prompt must appear before the second.
	if strings.Index(got, "How do I add auth?") > strings.Index(got, "What about sessions?") {
		t.Errorf("user prompts should stay in chronological order: %q", got)
	}
}

// TestBuildTitleContext_LimitsToLastNUserPrompts verifies only the most
// recent titleContextMaxUserPrompts user prompts are kept, oldest dropped.
func TestBuildTitleContext_LimitsToLastNUserPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "many-prompts-session"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	const total = titleContextMaxUserPrompts + 2 // 7 prompts, only last 5 expected
	for i := 1; i <= total; i++ {
		if err := store.AppendEvent(sessionID, session.Event{
			Type: session.EventTypeUserPrompt,
			Data: session.UserPromptData{Message: "prompt-" + strconv.Itoa(i)},
		}); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	got := BuildTitleContext(store, sessionID)

	// The two oldest prompts must be dropped.
	if strings.Contains(got, "prompt-1\n") || strings.Contains(got, "prompt-1 ") {
		t.Errorf("oldest prompt should have been dropped: %q", got)
	}
	if strings.Contains(got, "prompt-2\n") {
		t.Errorf("second-oldest prompt should have been dropped: %q", got)
	}
	// The most recent titleContextMaxUserPrompts prompts must be present.
	for i := total - titleContextMaxUserPrompts + 1; i <= total; i++ {
		want := "User: prompt-" + strconv.Itoa(i)
		if !strings.Contains(got, want) {
			t.Errorf("expected retained prompt %q in context: %q", want, got)
		}
	}
}

// TestBuildTitleContext_CapsAtMaxChars verifies the composed excerpt never
// exceeds titleContextMaxChars and stays most-recent-biased (oldest content
// dropped first).
func TestBuildTitleContext_CapsAtMaxChars(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "long-context-session"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	longChunk := strings.Repeat("x", 1500)
	// 5 user prompts of 1500 chars each (7500 total) plus a long agent
	// response comfortably exceed titleContextMaxChars (4000).
	for i := 0; i < titleContextMaxUserPrompts; i++ {
		if err := store.AppendEvent(sessionID, session.Event{
			Type: session.EventTypeUserPrompt,
			Data: session.UserPromptData{Message: "OLDEST-" + strconv.Itoa(i) + "-" + longChunk},
		}); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}
	if err := store.AppendEvent(sessionID, session.Event{
		Type: session.EventTypeAgentMessage,
		Data: session.AgentMessageData{Text: "NEWEST-" + longChunk},
	}); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	got := BuildTitleContext(store, sessionID)

	if len(got) > titleContextMaxChars {
		t.Errorf("BuildTitleContext exceeded titleContextMaxChars: len=%d, max=%d", len(got), titleContextMaxChars)
	}
	if !strings.Contains(got, "NEWEST-") {
		t.Errorf("most-recent agent response should be preserved: %q", got[:min(200, len(got))])
	}
	if strings.Contains(got, "OLDEST-0-") {
		t.Errorf("oldest user prompt should have been dropped to respect the cap")
	}
}
