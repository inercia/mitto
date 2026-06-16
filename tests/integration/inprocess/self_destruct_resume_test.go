//go:build integration

package inprocess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/session"
)

// TestSelfDestructAfterResume is a regression test for the bug where a session
// that had been closed and re-created via ResumeBackgroundSession (e.g. after GC
// idle-close, or an archive→unarchive cycle) failed to self-destruct at the end
// of a turn.
//
// Root cause: ResumeBackgroundSession's BackgroundSessionConfig struct literal
// omitted the OnSelfDestruct callback that NewBackgroundSession wired up. The
// deferred self-destruct check at the end of PromptWithMeta requires BOTH the
// in-memory flag AND a non-nil bs.onSelfDestruct, so resumed sessions silently
// skipped deletion even though RequestSelfDestruct had set the flag.
//
// This test forces the resume path through archive→unarchive (the same code path
// exercised by TestResumeModelConstraint for the sibling MittoConfig bug), marks
// the resumed session for self-destruct, runs one turn, and asserts the session
// is actually removed from both the SessionManager and the persistent store.
func TestSelfDestructAfterResume(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name: "Self Destruct After Resume",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	var (
		mu             sync.Mutex
		promptComplete bool
	)
	callbacks := client.SessionCallbacks{
		OnPromptComplete: func(int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
		},
	}

	// 1. Initial prompt so the deferred session/new handshake completes and the
	//    session is fully initialized before we archive it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}
	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "initial prompt to complete")
	ws.Close()

	// 2. Archive → unarchive forces ResumeBackgroundSession (the buggy path).
	if err := ts.Client.ArchiveSession(sess.SessionID, true); err != nil {
		t.Fatalf("ArchiveSession failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := ts.Client.ArchiveSession(sess.SessionID, false); err != nil {
		t.Fatalf("Unarchive failed: %v", err)
	}

	sm := ts.Server.GetSessionManager()
	waitFor(t, 10*time.Second, func() bool {
		return sm.GetSession(sess.SessionID) != nil
	}, "resumed BackgroundSession to be registered")

	// 3. Reconnect and mark the RESUMED session for self-destruct. Fetching the
	//    instance after the WS connect avoids any chance of acting on a stale
	//    pre-resume instance.
	mu.Lock()
	promptComplete = false
	mu.Unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	ws2, err := ts.Client.Connect(ctx2, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()
	if err := ws2.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Second LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	resumedBS := sm.GetSession(sess.SessionID)
	if resumedBS == nil {
		t.Fatalf("resumed session not found after reconnect")
	}
	resumedBS.RequestSelfDestruct()
	if !resumedBS.IsSelfDestructRequested() {
		t.Fatalf("RequestSelfDestruct did not set the flag on the resumed session")
	}

	// 4. Run one turn. The deferred self-destruct check at the end of
	//    PromptWithMeta must fire bs.onSelfDestruct — which is exactly what the
	//    fix restores for resumed sessions.
	if err := ws2.SendPrompt("trigger self destruct"); err != nil {
		t.Fatalf("SendPrompt (trigger) failed: %v", err)
	}
	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "self-destruct trigger prompt to complete")

	// 5. The session must now be deleted from both the manager and the store.
	//    Before the fix, onSelfDestruct was nil on the resumed session, so the
	//    deletion never ran and these assertions would fail.
	waitFor(t, 15*time.Second, func() bool {
		if sm.GetSession(sess.SessionID) != nil {
			return false
		}
		_, err := ts.Store.GetMetadata(sess.SessionID)
		return errors.Is(err, session.ErrSessionNotFound)
	}, "resumed session to be self-destructed (removed from manager and store)")
}
