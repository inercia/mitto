//go:build integration

package inprocess

import (
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestSessionLifecycle tests the complete session lifecycle:
// Create -> List -> Get -> Delete
func TestSessionLifecycle(t *testing.T) {
	ts := SetupTestServer(t)

	// 1. List sessions (should be empty initially)
	sessions, err := ts.Client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	initialCount := len(sessions)
	t.Logf("Initial session count: %d", initialCount)

	// 2. Create a new session
	createReq := client.CreateSessionRequest{
		Name: "Test Session",
	}
	session, err := ts.Client.CreateSession(createReq)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if session.SessionID == "" {
		t.Fatal("CreateSession returned empty session ID")
	}
	t.Logf("Created session: %s", session.SessionID)

	// 3. List sessions (should have one more)
	sessions, err = ts.Client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after create failed: %v", err)
	}
	if len(sessions) != initialCount+1 {
		t.Errorf("Expected %d sessions, got %d", initialCount+1, len(sessions))
	}

	// 4. Get the specific session
	got, err := ts.Client.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.SessionID != session.SessionID {
		t.Errorf("GetSession returned wrong session: got %s, want %s", got.SessionID, session.SessionID)
	}

	// 5. Delete the session
	err = ts.Client.DeleteSession(session.SessionID)
	if err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	t.Logf("Deleted session: %s", session.SessionID)

	// 6. Verify session is deleted
	_, err = ts.Client.GetSession(session.SessionID)
	if err == nil {
		t.Error("GetSession should fail for deleted session")
	}

	// 7. List sessions (should be back to initial count)
	sessions, err = ts.Client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after delete failed: %v", err)
	}
	if len(sessions) != initialCount {
		t.Errorf("Expected %d sessions after delete, got %d", initialCount, len(sessions))
	}
}

// TestCreateMultipleSessions tests creating multiple sessions.
func TestCreateMultipleSessions(t *testing.T) {
	ts := SetupTestServer(t)

	// Create 3 sessions
	var sessionIDs []string
	for i := 0; i < 3; i++ {
		session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
		if err != nil {
			t.Fatalf("CreateSession %d failed: %v", i, err)
		}
		sessionIDs = append(sessionIDs, session.SessionID)
	}

	// Verify all sessions exist
	sessions, err := ts.Client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	// Check that all created sessions are in the list
	sessionMap := make(map[string]bool)
	for _, s := range sessions {
		sessionMap[s.SessionID] = true
	}

	for _, id := range sessionIDs {
		if !sessionMap[id] {
			t.Errorf("Session %s not found in list", id)
		}
	}

	// Cleanup: delete all created sessions
	for _, id := range sessionIDs {
		if err := ts.Client.DeleteSession(id); err != nil {
			t.Errorf("Failed to delete session %s: %v", id, err)
		}
	}
}

// TestGetNonExistentSession tests getting a session that doesn't exist.
func TestGetNonExistentSession(t *testing.T) {
	ts := SetupTestServer(t)

	_, err := ts.Client.GetSession("nonexistent-session-id")
	if err == nil {
		t.Error("GetSession should fail for non-existent session")
	}
}

// TestDeleteNonExistentSession tests deleting a session that doesn't exist.
func TestDeleteNonExistentSession(t *testing.T) {
	ts := SetupTestServer(t)

	err := ts.Client.DeleteSession("nonexistent-session-id")
	if err == nil {
		t.Error("DeleteSession should fail for non-existent session")
	}
}
