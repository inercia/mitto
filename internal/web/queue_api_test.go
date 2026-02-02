package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// setupQueueTestServer creates a test server with a session store for queue testing.
func setupQueueTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a test session
	sessionID := "20260201-120000-test1234"
	meta := session.Metadata{
		SessionID: sessionID,
		Status:    "active",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	server := &Server{
		store:     store,
		apiPrefix: "/mitto",
	}

	return server, sessionID
}

func TestHandleSessionQueue_List_Empty(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "")

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

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Add(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	body := `{"message": "Test message", "image_ids": ["img1", "img2"]}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "")

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

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Add_EmptyMessage(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	body := `{"message": ""}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Delete_Message(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	// Add a message first (0 = no limit)
	msg, _ := queue.Add("Test", nil, "", 0)

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID, nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "/"+msg.ID)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify message is gone
	_, err := queue.Get(msg.ID)
	if err != session.ErrMessageNotFound {
		t.Error("Message should have been deleted")
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Delete_NotFound(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "/nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Clear(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	// Add some messages (0 = no limit)
	queue.Add("First", nil, "", 0)
	queue.Add("Second", nil, "", 0)
	queue.Add("Third", nil, "", 0)

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify queue is empty
	length, _ := queue.Len()
	if length != 0 {
		t.Errorf("Queue length = %d, want 0", length)
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Get_Message(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	// Add a message (0 = no limit)
	msg, _ := queue.Add("Test message", []string{"img1"}, "client1", 0)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID, nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "/"+msg.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var got session.QueuedMessage
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if got.ID != msg.ID {
		t.Errorf("ID = %q, want %q", got.ID, msg.ID)
	}
	if got.Message != "Test message" {
		t.Errorf("Message = %q, want %q", got.Message, "Test message")
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_Get_NotFound(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "/nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Clean up
	queue.Delete()
}

func TestHandleSessionQueue_SessionNotFound(t *testing.T) {
	server, _ := setupQueueTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/nonexistent/queue", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, "nonexistent", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionQueue_MethodNotAllowed(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodPut, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	server.handleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	// Clean up
	queue.Delete()
}

func TestNewQueueConfigResponse(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.QueueConfig
		expected QueueConfigResponse
	}{
		{
			name:   "nil config uses defaults",
			config: nil,
			expected: QueueConfigResponse{
				Enabled:      true,                       // default
				MaxSize:      config.DefaultQueueMaxSize, // 10
				DelaySeconds: 0,                          // default
			},
		},
		{
			name: "custom config",
			config: &config.QueueConfig{
				Enabled:      boolPtr(false),
				MaxSize:      intPtr(50),
				DelaySeconds: 5,
			},
			expected: QueueConfigResponse{
				Enabled:      false,
				MaxSize:      50,
				DelaySeconds: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewQueueConfigResponse(tt.config)
			if got.Enabled != tt.expected.Enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.expected.Enabled)
			}
			if got.MaxSize != tt.expected.MaxSize {
				t.Errorf("MaxSize = %v, want %v", got.MaxSize, tt.expected.MaxSize)
			}
			if got.DelaySeconds != tt.expected.DelaySeconds {
				t.Errorf("DelaySeconds = %v, want %v", got.DelaySeconds, tt.expected.DelaySeconds)
			}
		})
	}
}

func TestHandleMoveQueueMessage(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)
	defer queue.Delete()

	// Add two messages to the queue (message, imageIDs, clientID, maxSize)
	msg1, _ := queue.Add("First message", nil, "", 0)
	msg2, _ := queue.Add("Second message", nil, "", 0)

	// Move second message up
	body := `{"direction": "up"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/"+msg2.ID+"/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleMoveQueueMessage(w, req, queue, sessionID, msg2.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the order changed
	var resp QueueListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(resp.Messages))
	}

	// After moving msg2 up, it should be first
	if resp.Messages[0].ID != msg2.ID {
		t.Errorf("First message ID = %s, want %s", resp.Messages[0].ID, msg2.ID)
	}
	if resp.Messages[1].ID != msg1.ID {
		t.Errorf("Second message ID = %s, want %s", resp.Messages[1].ID, msg1.ID)
	}
}

func TestHandleMoveQueueMessage_InvalidDirection(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)
	defer queue.Delete()

	msg, _ := queue.Add("Test message", nil, "", 0)

	body := `{"direction": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID+"/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleMoveQueueMessage(w, req, queue, sessionID, msg.ID)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleMoveQueueMessage_MessageNotFound(t *testing.T) {
	server, sessionID := setupQueueTestServer(t)
	queue := server.store.Queue(sessionID)
	defer queue.Delete()

	body := `{"direction": "up"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleMoveQueueMessage(w, req, queue, sessionID, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestNotifyQueueUpdate_NilSessionManager(t *testing.T) {
	server := &Server{
		sessionManager: nil,
	}

	// Should not panic
	server.notifyQueueUpdate("session-id", "add", "msg-id")
}

func TestNotifyQueueReorder_NilSessionManager(t *testing.T) {
	server := &Server{
		sessionManager: nil,
	}

	// Should not panic
	server.notifyQueueReorder("session-id", nil)
}

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}
