package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// --- Queue API ---

// QueuedMessage represents a message waiting to be sent to the agent.
type QueuedMessage struct {
	ID            string   `json:"id"`
	Message       string   `json:"message"`
	ImageIDs      []string `json:"image_ids,omitempty"`
	QueuedAt      string   `json:"queued_at"`
	ClientID      string   `json:"client_id,omitempty"`
	Title         string   `json:"title,omitempty"`
	ScheduledTime *string  `json:"scheduled_time,omitempty"`
}

// QueueListResponse represents the response for listing queued messages.
type QueueListResponse struct {
	Messages []QueuedMessage `json:"messages"`
	Count    int             `json:"count"`
}

// QueueAddRequest represents a request to add a message to the queue.
type QueueAddRequest struct {
	Message       string   `json:"message"`
	ImageIDs      []string `json:"image_ids,omitempty"`
	ScheduledTime *string  `json:"scheduled_time,omitempty"`
}

// QueueMoveRequest is the request body for POST /api/sessions/{id}/queue/{msgId}/move.
type QueueMoveRequest struct {
	Direction string `json:"direction"` // "up" or "down"
}

// ListQueue returns all queued messages for a session.
func (c *Client) ListQueue(sessionID string) (*QueueListResponse, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("list queue: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("list queue", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("list queue", resp)
	}

	var result QueueListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("list queue: decode: %w", err)
	}
	return &result, nil
}

// AddToQueue adds a message to the session's queue.
func (c *Client) AddToQueue(sessionID, message string) (*QueuedMessage, error) {
	return c.AddToQueueWithImages(sessionID, message, nil)
}

// AddToQueueWithImages adds a message with images to the session's queue.
func (c *Client) AddToQueueWithImages(sessionID, message string, imageIDs []string) (*QueuedMessage, error) {
	reqBody := QueueAddRequest{
		Message:  message,
		ImageIDs: imageIDs,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("add to queue: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add to queue: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("add to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("add to queue", sessionID)
	}
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return nil, errorFromResponse("add to queue", resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("add to queue", resp)
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("add to queue: decode: %w", err)
	}
	return &msg, nil
}

// GetQueueMessage returns a specific queued message.
func (c *Client) GetQueueMessage(sessionID, messageID string) (*QueuedMessage, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue/"+url.PathEscape(messageID)), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get queue message: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get queue message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "get queue message", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("message not found: %s", messageID),
			Details: map[string]any{"message_id": messageID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get queue message", resp)
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("get queue message: decode: %w", err)
	}
	return &msg, nil
}

// RemoveFromQueue removes a message from the session's queue.
func (c *Client) RemoveFromQueue(sessionID, messageID string) error {
	req, err := c.newRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue/"+url.PathEscape(messageID)), "", nil)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &APIError{Op: "remove from queue", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("message not found: %s", messageID),
			Details: map[string]any{"message_id": messageID}}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("remove from queue", resp)
	}
	return nil
}

// ClearQueue removes all messages from the session's queue.
func (c *Client) ClearQueue(sessionID string) error {
	req, err := c.newRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"), "", nil)
	if err != nil {
		return fmt.Errorf("clear queue: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("clear queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("clear queue", resp)
	}
	return nil
}

// AddToQueueNamed adds a named prompt to the session's queue (resolved by name at dispatch).
// The message body contains only prompt_name; the full prompt is resolved server-side.
func (c *Client) AddToQueueNamed(sessionID, promptName string) (*QueuedMessage, error) {
	reqBody := struct {
		PromptName string `json:"prompt_name"`
	}{PromptName: promptName}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("add named to queue: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add named to queue: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("add named to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("add named to queue", sessionID)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("add named to queue", resp)
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("add named to queue: decode: %w", err)
	}
	return &msg, nil
}

// AddToQueueNamedWithArgs adds a named prompt with optional arguments to the session's queue.
// When args is nil or empty, the request omits the arguments field (identical to AddToQueueNamed).
func (c *Client) AddToQueueNamedWithArgs(sessionID, promptName string, args map[string]string) (*QueuedMessage, error) {
	reqBody := struct {
		PromptName string            `json:"prompt_name"`
		Arguments  map[string]string `json:"arguments,omitempty"`
	}{PromptName: promptName, Arguments: args}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("add named+args to queue: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add named+args to queue: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("add named+args to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("add named+args to queue", sessionID)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("add named+args to queue", resp)
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("add named+args to queue: decode: %w", err)
	}
	return &msg, nil
}

// MoveQueueMessage reorders a queued message one position "up" or "down"
// (direction must be exactly one of those two strings) and returns the
// reordered queue.
func (c *Client) MoveQueueMessage(sessionID, messageID, direction string) (*QueueListResponse, error) {
	reqBody := QueueMoveRequest{Direction: direction}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("move queue message: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue/"+url.PathEscape(messageID)+"/move"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("move queue message: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("move queue message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "move queue message", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("message not found: %s", messageID),
			Details: map[string]any{"message_id": messageID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("move queue message", resp)
	}

	var result QueueListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("move queue message: decode: %w", err)
	}
	return &result, nil
}

// GetPromptArgCache returns the names of parameters currently cached (fresh) for a
// named prompt in a conversation. On a 404 the session is unknown; on any other
// non-2xx an error with the status and body is returned.
func (c *Client) GetPromptArgCache(sessionID, promptName string) ([]string, error) {
	qp := url.Values{"prompt": {promptName}}
	reqURL := c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/prompt-arg-cache") + "?" + qp.Encode()
	req, err := c.newRequest(http.MethodGet, reqURL, "", nil)
	if err != nil {
		return nil, fmt.Errorf("get prompt-arg-cache: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get prompt-arg-cache: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("get prompt-arg-cache", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get prompt-arg-cache", resp)
	}

	var result struct {
		Prompt string   `json:"prompt"`
		Cached []string `json:"cached"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("get prompt-arg-cache: decode: %w", err)
	}
	return result.Cached, nil
}
