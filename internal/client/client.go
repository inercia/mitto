// Package client provides a Go client for connecting to the Mitto backend.
// This client is designed for internal use (no authentication) and is useful
// for integration testing and CLI tools.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"time"
)

// Client provides HTTP methods for the Mitto REST API.
// It is safe for concurrent use.
type Client struct {
	baseURL    string
	apiPrefix  string // API prefix (e.g., "/mitto")
	httpClient *http.Client
}

// Option configures the client.
type Option func(*Client)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(client *Client) {
		client.httpClient.Timeout = d
	}
}

// New creates a new Mitto client.
// baseURL should be the Mitto server address (e.g., "http://localhost:8080").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:   baseURL,
		apiPrefix: "/mitto", // Default API prefix
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// apiURL builds a full API URL with the prefix.
func (c *Client) apiURL(path string) string {
	return c.baseURL + c.apiPrefix + path
}

// BaseURL returns the base URL of the client.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// SessionInfo represents information about a session.
type SessionInfo struct {
	SessionID    string `json:"session_id"`
	ACPSessionID string `json:"acp_session_id,omitempty"`
	Name         string `json:"name,omitempty"`
	WorkingDir   string `json:"working_dir,omitempty"`
	ACPServer    string `json:"acp_server,omitempty"`
	Status       string `json:"status,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	// Reused is true when CreateSession was routed to an existing singleton-prompt
	// conversation instead of creating a new one (see find-or-route, mitto-4mb.3).
	Reused bool `json:"reused,omitempty"`
}

// CreateSessionRequest represents a request to create a new session.
type CreateSessionRequest struct {
	Name              string            `json:"name,omitempty"`
	WorkingDir        string            `json:"working_dir,omitempty"`
	ACPServer         string            `json:"acp_server,omitempty"`
	OriginPromptName  string            `json:"origin_prompt_name,omitempty"`  // Optional: name of the prompt that originated this conversation
	InitialPromptName string            `json:"initial_prompt_name,omitempty"` // Optional: seed the queue with a named prompt atomically on creation
	Arguments         map[string]string `json:"arguments,omitempty"`           // Optional: Go-template .Args values for the initial prompt
}

// ListSessions returns all sessions.
func (c *Client) ListSessions() ([]SessionInfo, error) {
	resp, err := c.httpClient.Get(c.apiURL("/api/sessions"))
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list sessions: status %d: %s", resp.StatusCode, string(body))
	}

	var sessions []SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("list sessions: decode: %w", err)
	}
	return sessions, nil
}

// CreateSession creates a new session.
func (c *Client) CreateSession(req CreateSessionRequest) (*SessionInfo, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("create session: marshal: %w", err)
	}

	resp, err := c.httpClient.Post(c.apiURL("/api/sessions"), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create session: status %d: %s", resp.StatusCode, string(respBody))
	}

	var session SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("create session: decode: %w", err)
	}
	return &session, nil
}

// GetSession returns information about a specific session.
func (c *Client) GetSession(sessionID string) (*SessionInfo, error) {
	resp, err := c.httpClient.Get(c.apiURL("/api/sessions/" + url.PathEscape(sessionID)))
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get session: status %d: %s", resp.StatusCode, string(body))
	}

	var session SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("get session: decode: %w", err)
	}
	return &session, nil
}

// DeleteSession deletes a session.
func (c *Client) DeleteSession(sessionID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)), nil)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ArchiveSession archives or unarchives a session.
func (c *Client) ArchiveSession(sessionID string, archive bool) error {
	body, err := json.Marshal(map[string]interface{}{
		"archived": archive,
	})
	if err != nil {
		return fmt.Errorf("archive session: marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPatch, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("archive session: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// --- Image API ---

// ImageInfo represents an uploaded image.
type ImageInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
}

// UploadImage uploads an image to a session via multipart form.
func (c *Client) UploadImage(sessionID string, filename string, mimeType string, data []byte) (*ImageInfo, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "image",
		"filename": filename,
	}))
	h.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("upload image: create form: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("upload image: write data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("upload image: close writer: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images"),
		writer.FormDataContentType(),
		&buf,
	)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload image: status %d: %s", resp.StatusCode, string(body))
	}

	var info ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("upload image: decode: %w", err)
	}
	return &info, nil
}

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

// ListQueue returns all queued messages for a session.
func (c *Client) ListQueue(sessionID string) (*QueueListResponse, error) {
	resp, err := c.httpClient.Get(c.apiURL("/api/sessions/" + url.PathEscape(sessionID) + "/queue"))
	if err != nil {
		return nil, fmt.Errorf("list queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list queue: status %d: %s", resp.StatusCode, string(body))
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

	resp, err := c.httpClient.Post(
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode == http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue full: %s", string(respBody))
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("add to queue: status %d: %s", resp.StatusCode, string(respBody))
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("add to queue: decode: %w", err)
	}
	return &msg, nil
}

// GetQueueMessage returns a specific queued message.
func (c *Client) GetQueueMessage(sessionID, messageID string) (*QueuedMessage, error) {
	resp, err := c.httpClient.Get(c.apiURL("/api/sessions/" + url.PathEscape(sessionID) + "/queue/" + url.PathEscape(messageID)))
	if err != nil {
		return nil, fmt.Errorf("get queue message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get queue message: status %d: %s", resp.StatusCode, string(body))
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("get queue message: decode: %w", err)
	}
	return &msg, nil
}

// RemoveFromQueue removes a message from the session's queue.
func (c *Client) RemoveFromQueue(sessionID, messageID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue/"+url.PathEscape(messageID)), nil)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("message not found: %s", messageID)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove from queue: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ClearQueue removes all messages from the session's queue.
func (c *Client) ClearQueue(sessionID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"), nil)
	if err != nil {
		return fmt.Errorf("clear queue: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("clear queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clear queue: status %d: %s", resp.StatusCode, string(body))
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

	resp, err := c.httpClient.Post(
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add named to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("add named to queue: status %d: %s", resp.StatusCode, string(respBody))
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

	resp, err := c.httpClient.Post(
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/queue"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("add named+args to queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("add named+args to queue: status %d: %s", resp.StatusCode, string(respBody))
	}

	var msg QueuedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("add named+args to queue: decode: %w", err)
	}
	return &msg, nil
}

// GetPromptArgCache returns the names of parameters currently cached (fresh) for a
// named prompt in a conversation. On a 404 the session is unknown; on any other
// non-2xx an error with the status and body is returned.
func (c *Client) GetPromptArgCache(sessionID, promptName string) ([]string, error) {
	qp := url.Values{"prompt": {promptName}}
	reqURL := c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/prompt-arg-cache") + "?" + qp.Encode()
	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("get prompt-arg-cache: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get prompt-arg-cache: status %d: %s", resp.StatusCode, string(respBody))
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

// --- Periodic API ---

// PeriodicFrequency represents a periodic schedule frequency.
type PeriodicFrequency struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
	At    string `json:"at,omitempty"` // HH:MM in UTC, only for unit=days
}

// SetPeriodicRequest is the request body for PUT /api/sessions/{id}/periodic.
type SetPeriodicRequest struct {
	PromptName    string            `json:"prompt_name,omitempty"`
	Prompt        string            `json:"prompt,omitempty"`
	Frequency     PeriodicFrequency `json:"frequency"`
	Enabled       bool              `json:"enabled"`
	MaxIterations int               `json:"max_iterations,omitempty"`
	// On-completion trigger fields (mitto-icf).
	Trigger            string `json:"trigger,omitempty"`              // "schedule" | "onCompletion" | "onTasks"
	DelaySeconds       int    `json:"delay_seconds,omitempty"`        // clamped to server floor
	MaxDurationSeconds int    `json:"max_duration_seconds,omitempty"` // 0 = unlimited
	// onTasks trigger fields (mitto-oja).
	Condition       string `json:"condition,omitempty"`        // CEL expression; empty = fire on ANY beads change
	ConditionPreset string `json:"condition_preset,omitempty"` // optional UI preset id that compiled to Condition
	CooldownSeconds int    `json:"cooldown_seconds,omitempty"` // per-conversation cooldown floor; 0 = use global floor
}

// PeriodicConfig represents the periodic configuration for a session.
type PeriodicConfig struct {
	Prompt          string            `json:"prompt,omitempty"`
	PromptName      string            `json:"prompt_name,omitempty"`
	Frequency       PeriodicFrequency `json:"frequency"`
	Enabled         bool              `json:"enabled"`
	MaxIterations   int               `json:"max_iterations,omitempty"`
	NextScheduledAt string            `json:"next_scheduled_at,omitempty"`
	// On-completion trigger fields (mitto-icf).
	Trigger            string `json:"trigger,omitempty"`
	DelaySeconds       int    `json:"delay_seconds,omitempty"`
	MaxDurationSeconds int    `json:"max_duration_seconds,omitempty"`
	IterationCount     int    `json:"iteration_count,omitempty"`
	FreshContext       bool   `json:"fresh_context,omitempty"`
	// onTasks trigger fields (mitto-oja).
	Condition       string `json:"condition,omitempty"`
	ConditionPreset string `json:"condition_preset,omitempty"`
	CooldownSeconds int    `json:"cooldown_seconds,omitempty"`
	StoppedReason   string `json:"stopped_reason,omitempty"`
}

// SetPeriodic configures a periodic schedule on a session via PUT.
func (c *Client) SetPeriodic(sessionID string, req SetPeriodicRequest) (*PeriodicConfig, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("set periodic: marshal: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPut,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/periodic"),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("set periodic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("set periodic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("set periodic: status %d: %s", resp.StatusCode, string(respBody))
	}

	var config PeriodicConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("set periodic: decode: %w", err)
	}
	return &config, nil
}

// GetPeriodic returns the periodic configuration for a session.
func (c *Client) GetPeriodic(sessionID string) (*PeriodicConfig, error) {
	resp, err := c.httpClient.Get(c.apiURL("/api/sessions/" + url.PathEscape(sessionID) + "/periodic"))
	if err != nil {
		return nil, fmt.Errorf("get periodic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("periodic not configured for session: %s", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get periodic: status %d: %s", resp.StatusCode, string(respBody))
	}

	var config PeriodicConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("get periodic: decode: %w", err)
	}
	return &config, nil
}

// RunPeriodicNow triggers an immediate run of the periodic prompt.
// resetTimer controls whether the next scheduled run timer is reset.
func (c *Client) RunPeriodicNow(sessionID string, resetTimer bool) error {
	reqBody := struct {
		ResetTimer bool `json:"reset_timer"`
	}{ResetTimer: resetTimer}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("run periodic now: marshal: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/periodic/run-now"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("run periodic now: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("run periodic now: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
