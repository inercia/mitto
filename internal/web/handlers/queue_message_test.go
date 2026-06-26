package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestHandleSessionQueue_Clear(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	queue.Add("First", nil, nil, "", nil, 0, nil, "")
	queue.Add("Second", nil, nil, "", nil, 0, nil, "")
	queue.Add("Third", nil, nil, "", nil, 0, nil, "")

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	length, _ := queue.Len()
	if length != 0 {
		t.Errorf("Queue length = %d, want 0", length)
	}

	queue.Delete()
}

func TestHandleSessionQueue_Get_Message(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	msg, _ := queue.Add("Test message", []string{"img1"}, nil, "client1", nil, 0, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID, nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/"+msg.ID)

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

	queue.Delete()
}

func TestHandleSessionQueue_Get_NotFound(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	queue.Delete()
}

func TestHandleSessionQueue_SessionNotFound(t *testing.T) {
	_, h, _ := setupQueueTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/nonexistent/queue", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, "nonexistent", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionQueue_MethodNotAllowed(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodPut, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	queue.Delete()
}

func TestHandleMoveQueueMessage(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)
	defer queue.Delete()

	msg1, _ := queue.Add("First message", nil, nil, "", nil, 0, nil, "")
	msg2, _ := queue.Add("Second message", nil, nil, "", nil, 0, nil, "")

	body := `{"direction": "up"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/"+msg2.ID+"/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMoveQueueMessage(w, req, queue, sessionID, msg2.ID)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp QueueListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].ID != msg2.ID {
		t.Errorf("First message ID = %s, want %s", resp.Messages[0].ID, msg2.ID)
	}
	if resp.Messages[1].ID != msg1.ID {
		t.Errorf("Second message ID = %s, want %s", resp.Messages[1].ID, msg1.ID)
	}
}

func TestHandleMoveQueueMessage_InvalidDirection(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)
	defer queue.Delete()

	msg, _ := queue.Add("Test message", nil, nil, "", nil, 0, nil, "")

	body := `{"direction": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID+"/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMoveQueueMessage(w, req, queue, sessionID, msg.ID)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleMoveQueueMessage_MessageNotFound(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)
	defer queue.Delete()

	body := `{"direction": "up"}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMoveQueueMessage(w, req, queue, sessionID, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionQueue_AddByPromptName(t *testing.T) {
	t.Run("named prompt queued and stored", func(t *testing.T) {
		store, h, sessionID := setupQueueTestHandlers(t)
		queue := store.Queue(sessionID)
		defer queue.Delete()

		body := `{"prompt_name": "some-name"}`
		req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.HandleSessionQueue(w, req, sessionID, "")

		if w.Code != http.StatusCreated {
			t.Errorf("Status = %d, want %d (body: %s)", w.Code, http.StatusCreated, w.Body.String())
		}

		var created session.QueuedMessage
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		if created.PromptName != "some-name" {
			t.Errorf("PromptName = %q, want %q", created.PromptName, "some-name")
		}
		if created.Message != "" {
			t.Errorf("Message = %q, want empty", created.Message)
		}

		req2 := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue", nil)
		w2 := httptest.NewRecorder()
		h.HandleSessionQueue(w2, req2, sessionID, "")

		var resp QueueListResponse
		if err := json.NewDecoder(w2.Body).Decode(&resp); err != nil {
			t.Fatalf("Failed to decode list response: %v", err)
		}
		if resp.Count != 1 {
			t.Fatalf("Count = %d, want 1", resp.Count)
		}
		if resp.Messages[0].PromptName != "some-name" {
			t.Errorf("stored PromptName = %q, want %q", resp.Messages[0].PromptName, "some-name")
		}
		if resp.Messages[0].Message != "" {
			t.Errorf("stored Message = %q, want empty", resp.Messages[0].Message)
		}
	})

	t.Run("both message and prompt_name empty returns 400", func(t *testing.T) {
		store, h, sessionID := setupQueueTestHandlers(t)
		queue := store.Queue(sessionID)
		defer queue.Delete()

		body := `{}`
		req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		h.HandleSessionQueue(w, req, sessionID, "")

		if w.Code != http.StatusBadRequest {
			t.Errorf("Status = %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
		}

		var errResp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
			t.Fatalf("Failed to decode error response: %v", err)
		}
		if errResp.Error.Code != "empty_message" {
			t.Errorf("error code = %q, want %q", errResp.Error.Code, "empty_message")
		}
	})
}
