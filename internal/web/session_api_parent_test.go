package web

import (
	"encoding/json"
	"github.com/inercia/mitto/internal/conversation"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestHandleListSessions_ParentSessionID verifies that ParentSessionID is included in the API response.
func TestHandleListSessions_ParentSessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a parent session
	parentMeta := session.Metadata{
		SessionID:  "parent-session-1",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Parent Session",
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create a child session with ParentSessionID set
	childMeta := session.Metadata{
		SessionID:       "child-session-1",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child Session",
		ParentSessionID: "parent-session-1",
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Parse response
	var response []SessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should have 2 sessions
	if len(response) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(response))
	}

	// Find the child session in the response
	var childSession *SessionListResponse
	for i := range response {
		if response[i].SessionID == "child-session-1" {
			childSession = &response[i]
			break
		}
	}

	if childSession == nil {
		t.Fatal("Child session not found in response")
	}

	// Verify ParentSessionID is present and correct
	if childSession.ParentSessionID != "parent-session-1" {
		t.Errorf("ParentSessionID = %q, want %q", childSession.ParentSessionID, "parent-session-1")
	}

	// Verify parent session has no ParentSessionID
	var parentSession *SessionListResponse
	for i := range response {
		if response[i].SessionID == "parent-session-1" {
			parentSession = &response[i]
			break
		}
	}

	if parentSession == nil {
		t.Fatal("Parent session not found in response")
	}

	if parentSession.ParentSessionID != "" {
		t.Errorf("Parent ParentSessionID = %q, want empty string", parentSession.ParentSessionID)
	}
}

// TestHandleListSessions_OriginPromptName verifies that OriginPromptName is
// included in the API response (it flows through automatically because
// SessionListResponse embeds session.Metadata).
func TestHandleListSessions_OriginPromptName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := session.Metadata{
		SessionID:        "session-with-origin-prompt",
		ACPServer:        "test-server",
		WorkingDir:       "/tmp",
		Name:             "Reevaluate Session",
		OriginPromptName: "Reevaluate all issues",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response []SessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(response))
	}

	if response[0].OriginPromptName != "Reevaluate all issues" {
		t.Errorf("OriginPromptName = %q, want %q", response[0].OriginPromptName, "Reevaluate all issues")
	}
}
