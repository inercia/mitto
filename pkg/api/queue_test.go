package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClient_MoveQueueMessage_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/queue/msg-1/move").
		RespondJSON(http.StatusOK, `{"messages":[{"id":"msg-2","message":"a","queued_at":"2026-01-01T00:00:00Z"},{"id":"msg-1","message":"b","queued_at":"2026-01-01T00:00:01Z"}],"count":2}`)

	c := f.Client()
	result, err := c.MoveQueueMessage("sess-1", "msg-1", "up")
	if err != nil {
		t.Fatalf("MoveQueueMessage: %v", err)
	}
	req := f.LastRequest()
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.Path != "/mitto/api/sessions/sess-1/queue/msg-1/move" {
		t.Errorf("path = %q, unexpected", req.Path)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(req.Body, &gotBody)
	if gotBody["direction"] != "up" {
		t.Errorf("request body direction = %v, want up", gotBody["direction"])
	}
	if result.Count != 2 || len(result.Messages) != 2 || result.Messages[0].ID != "msg-2" {
		t.Errorf("QueueListResponse = %+v, unexpected", result)
	}
}

func TestClient_MoveQueueMessage_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/queue/missing/move").RespondRaw(http.StatusNotFound, "", nil)

	c := f.Client()
	_, err := c.MoveQueueMessage("sess-1", "missing", "down")
	apiErr := assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
	if apiErr.Details["message_id"] != "missing" {
		t.Errorf("Details[message_id] = %v, want %q", apiErr.Details["message_id"], "missing")
	}
}

// --- Coverage for previously-0%-tested queue methods (mitto-rwxq.9) ---

func TestClient_ListQueue_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/queue").
		RespondJSON(http.StatusOK, `{"messages":[{"id":"msg-1","message":"a","queued_at":"2026-01-01T00:00:00Z"}],"count":1}`)

	result, err := f.Client().ListQueue("sess-1")
	if err != nil {
		t.Fatalf("ListQueue: %v", err)
	}
	if result.Count != 1 || len(result.Messages) != 1 {
		t.Errorf("QueueListResponse = %+v, unexpected", result)
	}
}

func TestClient_ListQueue_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/missing/queue").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().ListQueue("missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_GetQueueMessage_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/queue/msg-1").
		RespondJSON(http.StatusOK, `{"id":"msg-1","message":"hi","queued_at":"2026-01-01T00:00:00Z"}`)

	msg, err := f.Client().GetQueueMessage("sess-1", "msg-1")
	if err != nil {
		t.Fatalf("GetQueueMessage: %v", err)
	}
	if msg.ID != "msg-1" || msg.Message != "hi" {
		t.Errorf("QueuedMessage = %+v, unexpected", msg)
	}
}

func TestClient_GetQueueMessage_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/queue/missing").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().GetQueueMessage("sess-1", "missing")
	apiErr := assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
	if apiErr.Details["message_id"] != "missing" {
		t.Errorf("Details[message_id] = %v, want %q", apiErr.Details["message_id"], "missing")
	}
}

func TestClient_RemoveFromQueue_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/queue/msg-1").RespondRaw(http.StatusNoContent, "", nil)

	if err := f.Client().RemoveFromQueue("sess-1", "msg-1"); err != nil {
		t.Fatalf("RemoveFromQueue: %v", err)
	}
}

func TestClient_RemoveFromQueue_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/queue/missing").RespondRaw(http.StatusNotFound, "", nil)

	err := f.Client().RemoveFromQueue("sess-1", "missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_ClearQueue_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/queue").RespondRaw(http.StatusOK, "", nil)

	if err := f.Client().ClearQueue("sess-1"); err != nil {
		t.Fatalf("ClearQueue: %v", err)
	}
}

func TestClient_ClearQueue_500_ReturnsGenericAPIError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/queue").Fail(http.StatusInternalServerError, CodeServerError, "boom", nil)

	err := f.Client().ClearQueue("sess-1")
	assertAPIError(t, err, ErrServerError, http.StatusInternalServerError, CodeServerError)
}

func TestClient_AddToQueueNamed_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/queue").
		RespondRaw(http.StatusCreated, "application/json", []byte(`{"id":"msg-1","message":"","queued_at":"2026-01-01T00:00:00Z"}`))

	msg, err := f.Client().AddToQueueNamed("sess-1", "my-prompt")
	if err != nil {
		t.Fatalf("AddToQueueNamed: %v", err)
	}
	if msg.ID != "msg-1" {
		t.Errorf("QueuedMessage = %+v, unexpected", msg)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	if gotBody["prompt_name"] != "my-prompt" {
		t.Errorf("request body prompt_name = %v, want my-prompt", gotBody["prompt_name"])
	}
	if _, hasArgs := gotBody["arguments"]; hasArgs {
		t.Error("request body must omit arguments when none are given")
	}
}

func TestClient_AddToQueueNamedWithArgs_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/queue").
		RespondRaw(http.StatusCreated, "application/json", []byte(`{"id":"msg-1","message":"","queued_at":"2026-01-01T00:00:00Z"}`))

	_, err := f.Client().AddToQueueNamedWithArgs("sess-1", "my-prompt", map[string]string{"Commit": "true"})
	if err != nil {
		t.Fatalf("AddToQueueNamedWithArgs: %v", err)
	}
	var gotBody map[string]any
	_ = json.Unmarshal(f.LastRequest().Body, &gotBody)
	args, _ := gotBody["arguments"].(map[string]any)
	if args["Commit"] != "true" {
		t.Errorf("request body arguments = %v, want Commit=true", gotBody["arguments"])
	}
}

func TestClient_GetPromptArgCache_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/prompt-arg-cache").
		RespondJSON(http.StatusOK, `{"prompt":"p","cached":["Commit","Foo"]}`)

	cached, err := f.Client().GetPromptArgCache("sess-1", "p")
	if err != nil {
		t.Fatalf("GetPromptArgCache: %v", err)
	}
	if len(cached) != 2 || cached[0] != "Commit" {
		t.Errorf("cached = %v, unexpected", cached)
	}
	if got := f.LastRequest().RawQuery; got != "prompt=p" {
		t.Errorf("query = %q, want prompt=p", got)
	}
}
