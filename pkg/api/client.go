// Package client provides a Go client for connecting to the Mitto backend.
//
// Authentication is optional: by default the client is unauthenticated
// (zero-config), matching every existing integration-test consumer. See
// WithBearerToken, WithTokenSupplier and (*Client).Login for the shared-token
// and interactive cookie-login modes; both are documented in doc.go.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Client provides HTTP methods for the Mitto REST API.
// It is safe for concurrent use.
type Client struct {
	baseURL    string
	apiPrefix  string // API prefix (e.g., "/mitto")
	httpClient *http.Client

	// mu guards the authentication state below, which Login and Logout
	// mutate while other goroutines may be issuing requests.
	mu sync.RWMutex

	// auth decorates outgoing REST requests and WebSocket handshakes with
	// credentials. Defaults to noAuth{} (no-op), so New(baseURL) keeps the
	// historical zero-config, unauthenticated behaviour.
	auth authProvider

	// baseAuth is the auth mode configured via options, restored by Logout.
	baseAuth authProvider

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
			Jar:     newJar(),
		},
		auth: noAuth{},
	}
	for _, opt := range opts {
		opt(c)
	}
	c.baseAuth = c.auth
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
// The session cookie is held in the Client's cookie jar, which is empty —
// and therefore sends nothing — for a Client that never logs in.
func (c *Client) Login(ctx context.Context, username, password string) error {
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
	c.mu.Lock()
	c.csrfToken = csrfBody.Token
	c.auth = cookieAuth{client: c}
	c.mu.Unlock()
	return nil
}

// Logout invalidates the current cookie-login session, if any, by posting
// to /api/logout. It is a no-op (returns nil) if the client is not
// currently in cookie-login mode. After Logout, the client reverts to
// whatever auth mode was configured before Login (or noAuth if none).
func (c *Client) Logout(ctx context.Context) error {
	if _, ok := c.currentAuth().(cookieAuth); !ok {
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

	c.mu.Lock()
	c.auth = c.baseAuth
	c.csrfToken = ""
	c.mu.Unlock()

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
	if err := c.currentAuth().applyREST(req); err != nil {
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
	// Archived mirrors session.Metadata.Archived. GET /api/sessions already
	// serializes it (SessionListResponse embeds session.Metadata), but this
	// decode-side struct previously dropped it, so `conversation list
	// --archived` had no field to filter on (mitto-pscc.5 Plan comment, Key
	// Decision 1).
	Archived bool `json:"archived,omitempty"`
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

// Image, queue, and loop APIs have moved to media.go, queue.go, and loop.go
// respectively as part of the pkg/api resource-file split (mitto-rwxq.7).
