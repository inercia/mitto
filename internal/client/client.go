// Package client provides a Go client for connecting to the Mitto backend.
// This client is designed for internal use (no authentication) and is useful
// for integration testing and CLI tools.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(client *Client) {
		client.httpClient = c
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(client *Client) {
		client.httpClient.Timeout = d
	}
}

// WithAPIPrefix sets the API prefix (e.g., "/mitto").
// Default is "/mitto".
func WithAPIPrefix(prefix string) Option {
	return func(client *Client) {
		client.apiPrefix = prefix
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
}

// CreateSessionRequest represents a request to create a new session.
type CreateSessionRequest struct {
	Name       string `json:"name,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	ACPServer  string `json:"acp_server,omitempty"`
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

// --- Queue API ---

// QueuedMessage represents a message waiting to be sent to the agent.
type QueuedMessage struct {
	ID       string   `json:"id"`
	Message  string   `json:"message"`
	ImageIDs []string `json:"image_ids,omitempty"`
	QueuedAt string   `json:"queued_at"`
	ClientID string   `json:"client_id,omitempty"`
	Title    string   `json:"title,omitempty"`
}

// QueueListResponse represents the response for listing queued messages.
type QueueListResponse struct {
	Messages []QueuedMessage `json:"messages"`
	Count    int             `json:"count"`
}

// QueueAddRequest represents a request to add a message to the queue.
type QueueAddRequest struct {
	Message  string   `json:"message"`
	ImageIDs []string `json:"image_ids,omitempty"`
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
