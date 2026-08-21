package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// setupQueueTestHandlers creates a test Handlers backed by a session store with a
// single test session, for exercising the queue REST handlers.
func setupQueueTestHandlers(t *testing.T) (*session.Store, *Handlers, string) {
	t.Helper()

	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-120000-test1234"
	if err := store.Create(session.Metadata{SessionID: sessionID, Status: "active"}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store, APIPrefix: "/mitto"})
	return store, h, sessionID
}

// TestReuseSingletonSession_NotLoadedIdle_EnqueuesWithoutDispatch verifies that
// reusing a singleton session that is not currently loaded in memory (no live
// BackgroundSession) and has an empty queue enqueues the prompt directly
// (no SessionManager → no dispatch attempted) and responds with reused:true.
func TestReuseSingletonSession_NotLoadedIdle_EnqueuesWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-130000-singleton1"
	if err := store.Create(session.Metadata{SessionID: sessionID, Status: "active"}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store})

	w := httptest.NewRecorder()
	h.reuseSingletonSession(w, sessionID, "my-prompt", map[string]string{"X": "y"})

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %q", resp["session_id"], sessionID)
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}

	queue := store.Queue(sessionID)
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Queue.List failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].PromptName != "my-prompt" {
		t.Errorf("PromptName = %q, want %q", messages[0].PromptName, "my-prompt")
	}
}

func TestHandleSessionQueue_List_Empty(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp QueueListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("Count = %d, want 0", resp.Count)
	}
	if len(resp.Messages) != 0 {
		t.Errorf("Messages = %d, want 0", len(resp.Messages))
	}

	queue.Delete()
}

func TestHandleSessionQueue_Add(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	body := `{"message": "Test message", "image_ids": ["img1", "img2"]}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusCreated)
	}

	var msg session.QueuedMessage
	if err := json.NewDecoder(w.Body).Decode(&msg); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if msg.ID == "" {
		t.Error("Message ID should not be empty")
	}
	if msg.Message != "Test message" {
		t.Errorf("Message = %q, want %q", msg.Message, "Test message")
	}
	if len(msg.ImageIDs) != 2 {
		t.Errorf("ImageIDs = %v, want 2 items", msg.ImageIDs)
	}

	queue.Delete()
}

func TestHandleSessionQueue_Add_EmptyMessage(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	body := `{"message": ""}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	queue.Delete()
}

// TestHandleSessionQueue_Add_TriggersConversationTitle verifies that POST
// /queue triggers auto-title generation when the session has no title yet,
// mirroring the WebSocket prompt path (mitto-58b). Without this, sessions
// whose first user prompt arrives via the queue REST path stay titled
// "Conversation" until the ACP turn eventually completes.
func TestHandleSessionQueue_Add_TriggersConversationTitle(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	tmpDir := t.TempDir()
	const sid = "20260201-140000-queue-title"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:  sid,
		WorkingDir: tmpDir,
		Store:      store,
		// IsPrompting: true makes HandleSessionQueue_Add's fire-and-forget
		// `go bs.TryProcessQueuedMessage()` bail out at the queueIsPrompting
		// check in tryProcess before it ever reaches Queue.Pop's disk write,
		// so the goroutine can't race this test's t.TempDir() cleanup
		// (mitto-vnq). This test only asserts the synchronous quick-title
		// fallback, so skipping the queued-dispatch side effect doesn't
		// weaken the mitto-58b assertion.
		IsPrompting: true,
	})
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.AddSessionForTest(bs)

	h := New(Deps{Store: store, SessionManager: sm})

	body := `{"message": "Investigate the cold-start MCP wedge on cgw-managed-tools"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.Name == "" {
		t.Errorf("metadata.Name is empty after POST /queue; expected a synchronous quick fallback title to be set (mitto-58b regression)")
	}
}

// TestHandleSessionQueue_Add_NamedPromptTriggersConversationTitle verifies that
// POST /queue with a bare prompt_name (no inline message) still triggers
// auto-title generation, using the prompt name (or its resolved body) as the
// title source text. Guards the named-prompt fallback of mitto-58b.
func TestHandleSessionQueue_Add_NamedPromptTriggersConversationTitle(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	tmpDir := t.TempDir()
	const sid = "20260201-140000-queue-named"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:  sid,
		WorkingDir: tmpDir,
		Store:      store,
		PromptResolver: func(name, dir string) (string, error) {
			return "The actual resolved body for " + name, nil
		},
		// IsPrompting: true — see comment in
		// TestHandleSessionQueue_Add_TriggersConversationTitle (mitto-vnq).
		IsPrompting: true,
	})
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.AddSessionForTest(bs)

	h := New(Deps{Store: store, SessionManager: sm})

	body := `{"prompt_name": "Run scripted test"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sid, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.Name == "" {
		t.Errorf("metadata.Name is empty after POST /queue with prompt_name; expected a synchronous quick fallback title derived from the resolved prompt (mitto-58b regression)")
	}
	if strings.Contains(strings.ToLower(meta.Name), "pending") {
		t.Errorf("metadata.Name = %q should not contain 'pending'", meta.Name)
	}
}

// TestHandleSessionQueue_Add_QueuedGoroutineRacesTempDirCleanup is the
// mitto-vnq regression guard. HandleSessionQueue_Add spawns
// `go bs.TryProcessQueuedMessage()` (queue.go) fire-and-forget when the
// agent is idle. Without IsPrompting: true on the test's BackgroundSession,
// that goroutine reaches Queue.Pop, which persists the queue file via an
// atomic writeQueue (internal/session/queue.go) into the session directory
// under the test's t.TempDir(). That write races the test's deferred
// t.TempDir() RemoveAll: when the goroutine's write lands mid-cleanup,
// RemoveAll's unlinkat fails with "directory not empty" (confirmed root
// cause; NOT async title generation, which the bead's original description
// assumed — the quick fallback title is set synchronously and the title
// goroutine is a no-op in these tests since WorkspaceUUID/AuxiliaryManager
// are unset).
//
// The fix sets IsPrompting: true on the test BackgroundSession, which makes
// tryProcess's queueIsPrompting guard bail out before it ever reaches
// Queue.Pop, so the goroutine never touches disk. This test repeats the
// pattern across many independent subtests (each with its own t.TempDir()
// lifecycle, mirroring TestHandleSessionQueue_Add_TriggersConversationTitle /
// TestHandleSessionQueue_Add_NamedPromptTriggersConversationTitle) so a
// future regression that drops the guard reliably resurfaces the cleanup
// failure instead of only intermittently flaking.
func TestHandleSessionQueue_Add_QueuedGoroutineRacesTempDirCleanup(t *testing.T) {
	const iterations = 40
	for i := 0; i < iterations; i++ {
		i := i
		t.Run(fmt.Sprintf("iter%d", i), func(t *testing.T) {
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			t.Cleanup(func() { store.Close() })

			tmpDir := t.TempDir()
			sid := fmt.Sprintf("20260201-140000-race-%d", i)
			if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
				t.Fatalf("Create: %v", err)
			}

			bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
				SessionID:  sid,
				WorkingDir: tmpDir,
				Store:      store,
				// IsPrompting: true — see comment in
				// TestHandleSessionQueue_Add_TriggersConversationTitle (mitto-vnq).
				IsPrompting: true,
			})
			sm := conversation.NewSessionManager("", "", false, nil)
			sm.AddSessionForTest(bs)

			h := New(Deps{Store: store, SessionManager: sm})

			body := `{"message": "trigger the queued-message dispatch goroutine"}`
			req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sid+"/queue", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.HandleSessionQueue(w, req, sid, "")

			if w.Code != http.StatusCreated {
				t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
			}
			// No further assertions: with the fix, the handler's
			// `go bs.TryProcessQueuedMessage()` goroutine bails out at the
			// queueIsPrompting guard and never calls Queue.Pop -> writeQueue
			// on the session directory, so t.TempDir()'s cleanup no longer
			// races that write (mitto-vnq).
		})
	}
}

func TestHandleSessionQueue_Delete_Message(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	msg, _ := queue.Add("Test", nil, nil, "", nil, 0, nil, "")

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID, nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/"+msg.ID)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if _, err := queue.Get(msg.ID); err != session.ErrMessageNotFound {
		t.Error("Message should have been deleted")
	}

	queue.Delete()
}

func TestHandleSessionQueue_Delete_NotFound(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	queue.Delete()
}
