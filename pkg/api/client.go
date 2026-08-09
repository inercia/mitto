// Package client provides a Go client for connecting to the Mitto backend.
//
// Authentication is optional: by default the client is unauthenticated
// (zero-config), matching every existing integration-test consumer. See
// WithBearerToken, WithTokenSupplier and (*Client).Login for the shared-token
// and interactive cookie-login modes; both are documented in doc.go.
package client

import (
	"bytes"
	"context"
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

	// auth decorates outgoing REST requests and WebSocket handshakes with
	// credentials. Defaults to noAuth{} (no-op), so New(baseURL) keeps the
	// historical zero-config, unauthenticated behaviour.
	auth authProvider

	// csrfToken holds the CSRF token obtained during Login, for cookieAuth
	// to attach to subsequent state-changing requests. Empty outside of
	// cookie-login mode.
	csrfToken string
}

// Option configures the client.
type Option func(*Client)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(client *Client) {
		client.httpClient.Timeout = d
	}
}

// WithBearerToken configures the client to authenticate every request with
// a fixed "Authorization: Bearer <token>" header, matching the backend's
// shared-token authentication (mitto-7gta.26). For a token that can change
// over the client's lifetime (env var, keychain, config reload), use
// WithTokenSupplier instead. The token is never logged and never placed in
// a URL or query string.
func WithBearerToken(token string) Option {
	return WithTokenSupplier(func() (string, error) { return token, nil })
}

// WithTokenSupplier configures the client to authenticate every request with
// an "Authorization: Bearer <token>" header, where the token is resolved by
// calling supplier immediately before each request. This allows callers to
// source the token lazily (environment variable, keychain, config file) and
// to rotate it without reconstructing the Client. A supplier error fails the
// request with a wrapped error; the token itself is never included in that
// error, logged, or placed in a URL or query string.
func WithTokenSupplier(supplier func() (string, error)) Option {
	return func(client *Client) {
		client.auth = bearerAuth{supplier: supplier}
	}
}

// New creates a new Mitto client.
// baseURL should be the Mitto server address (e.g., "http://localhost:8080").
// By default the client is unauthenticated; see WithBearerToken,
// WithTokenSupplier and Login for the available authentication modes.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:   baseURL,
		apiPrefix: "/mitto", // Default API prefix
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		auth: noAuth{},
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

// Login performs the interactive password login flow, for parity with the
// browser: it fetches a CSRF token, then posts credentials to obtain a
// mitto_session cookie. On success, subsequent requests (REST and
// WebSocket) made through this Client are authenticated via that cookie
// plus the CSRF token, until Logout is called or the session expires.
//
// Login lazily installs a cookie jar on the Client's http.Client if one is
// not already present, so a Client created with New(baseURL) and never
// logged in keeps its zero-config, jar-less default.
func (c *Client) Login(ctx context.Context, username, password string) error {
	if err := c.ensureJar(); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	csrfReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("/api/csrf-token"), nil)
	if err != nil {
		return fmt.Errorf("login: build csrf request: %w", err)
	}
	csrfResp, err := c.httpClient.Do(csrfReq)
	if err != nil {
		return fmt.Errorf("login: fetch csrf token: %w", err)
	}
	defer csrfResp.Body.Close()
	if csrfResp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: %w", c.apiError("fetch csrf token", csrfResp))
	}
	var csrfBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(csrfResp.Body).Decode(&csrfBody); err != nil {
		return fmt.Errorf("login: decode csrf token: %w", err)
	}

	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return fmt.Errorf("login: marshal: %w", err)
	}
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("/api/login"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("login: build request: %w", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set(csrfTokenHeaderClient, csrfBody.Token)

	loginResp, err := c.httpClient.Do(loginReq)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: %w", c.apiError("login", loginResp))
	}

	// Cookies (mitto_session, mitto_csrf) are now in the jar. Keep the CSRF
	// token for cookieAuth to attach to subsequent state-changing requests.
	c.csrfToken = csrfBody.Token
	c.auth = cookieAuth{client: c}
	return nil
}

// Logout invalidates the current cookie-login session, if any, by posting
// to /api/logout. It is a no-op (returns nil) if the client is not
// currently in cookie-login mode. After Logout, the client reverts to
// whatever auth mode was configured before Login (or noAuth if none).
func (c *Client) Logout(ctx context.Context) error {
	if _, ok := c.auth.(cookieAuth); !ok {
		return nil
	}
	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/logout"), "", nil)
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	req = req.WithContext(ctx)
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	defer resp.Body.Close()

	c.auth = noAuth{}
	c.csrfToken = ""

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logout: %w", c.apiError("logout", resp))
	}
	return nil
}

// BaseURL returns the base URL of the client.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// newRequest builds an HTTP request against the Mitto API and decorates it
// with the client's configured authentication (see authProvider). All REST
// call sites in this package must go through newRequest+do rather than
// c.httpClient directly, so authentication is applied consistently.
// contentType may be empty (e.g. for GET/DELETE); when non-empty it is set
// as the Content-Type header.
func (c *Client) newRequest(method, fullURL, contentType string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if err := c.auth.applyREST(req); err != nil {
		return nil, err
	}
	return req, nil
}

// do executes a request built by newRequest.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
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
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("list sessions", resp)
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

	httpReq, err := c.newRequest(http.MethodPost, c.apiURL("/api/sessions"), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("create session", resp)
	}

	var session SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("create session: decode: %w", err)
	}
	return &session, nil
}

// GetSession returns information about a specific session.
func (c *Client) GetSession(sessionID string) (*SessionInfo, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("get session", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get session", resp)
	}

	var session SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("get session: decode: %w", err)
	}
	return &session, nil
}

// DeleteSession deletes a session.
func (c *Client) DeleteSession(sessionID string) error {
	req, err := c.newRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)), "", nil)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("delete session", resp)
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

	req, err := c.newRequest(http.MethodPatch, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.apiError("archive session", resp)
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

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/images"),
		writer.FormDataContentType(),
		&buf,
	)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.apiError("upload image", resp)
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

// --- Loop API ---

// LoopFrequency represents a loop schedule frequency.
type LoopFrequency struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
	At    string `json:"at,omitempty"` // HH:MM in UTC, only for unit=days
}

// SetLoopRequest is the request body for PUT /api/sessions/{id}/loop.
type SetLoopRequest struct {
	PromptName    string        `json:"prompt_name,omitempty"`
	Prompt        string        `json:"prompt,omitempty"`
	Frequency     LoopFrequency `json:"frequency"`
	Enabled       bool          `json:"enabled"`
	MaxIterations int           `json:"max_iterations,omitempty"`
	// On-completion trigger fields (mitto-icf).
	Trigger            string `json:"trigger,omitempty"`              // "schedule" | "onCompletion" | "onTasks"
	DelaySeconds       int    `json:"delay_seconds,omitempty"`        // clamped to server floor
	MaxDurationSeconds int    `json:"max_duration_seconds,omitempty"` // 0 = unlimited
	// onTasks trigger fields (mitto-oja).
	Condition       string `json:"condition,omitempty"`        // CEL expression; empty = fire on ANY beads change
	ConditionPreset string `json:"condition_preset,omitempty"` // optional UI preset id that compiled to Condition
	CooldownSeconds int    `json:"cooldown_seconds,omitempty"` // per-conversation cooldown floor; 0 = use global floor
	// RunOnStart, when *true, causes the loop to fire exactly once shortly after
	// Mitto boots (mitto-ystk). Nil or false = do not fire on start (default).
	RunOnStart *bool `json:"run_on_start,omitempty"`
}

// LoopConfig represents the loop configuration for a session.
type LoopConfig struct {
	Prompt          string        `json:"prompt,omitempty"`
	PromptName      string        `json:"prompt_name,omitempty"`
	Frequency       LoopFrequency `json:"frequency"`
	Enabled         bool          `json:"enabled"`
	MaxIterations   int           `json:"max_iterations,omitempty"`
	NextScheduledAt string        `json:"next_scheduled_at,omitempty"`
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
	// RunOnStart mirrors the schema field (mitto-ystk). Nil = unset/default.
	RunOnStart *bool `json:"run_on_start,omitempty"`
}

// SetLoop configures a loop schedule on a session via PUT.
func (c *Client) SetLoop(sessionID string, req SetLoopRequest) (*LoopConfig, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("set loop: marshal: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPut,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("set loop: build request: %w", err)
	}

	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("set loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("set loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("set loop: decode: %w", err)
	}
	return &config, nil
}

// GetLoop returns the loop configuration for a session.
func (c *Client) GetLoop(sessionID string) (*LoopConfig, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get loop: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "get loop", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("loop not configured for session: %s", sessionID),
			Details: map[string]any{"session_id": sessionID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("get loop: decode: %w", err)
	}
	return &config, nil
}

// RunLoopNow triggers an immediate run of the loop prompt.
// resetTimer controls whether the next scheduled run timer is reset.
func (c *Client) RunLoopNow(sessionID string, resetTimer bool) error {
	reqBody := struct {
		ResetTimer bool `json:"reset_timer"`
	}{ResetTimer: resetTimer}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("run loop now: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop/run-now"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("run loop now: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("run loop now: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("run loop now", resp)
	}
	return nil
}
