package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// --- Session update / metadata ---

// SessionUpdateRequest is the request body for PATCH /api/sessions/{id}.
// All fields are optional pointers; only non-nil fields are applied
// server-side. An empty string clears the corresponding field where noted.
type SessionUpdateRequest struct {
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	Pinned          *bool   `json:"pinned,omitempty"`           // Deprecated: use Archived instead
	Archived        *bool   `json:"archived,omitempty"`         // If true, session is archived
	BeadsIssue      *string `json:"beads_issue,omitempty"`      // Linked beads issue ID (empty string clears it)
	BackgroundColor *string `json:"background_color,omitempty"` // Conversation accent color, hex (empty string clears it)
}

// SessionMetadata mirrors the server's session.Metadata (field-for-field, same
// json tags), returned by UpdateSession and available via GetSession's raw
// decode. pkg/api does not import internal/session, so this is a local copy
// of the wire shape rather than a type alias.
type SessionMetadata struct {
	SessionID               string          `json:"session_id"`
	Name                    string          `json:"name,omitempty"`
	NameIsFallback          bool            `json:"name_is_fallback,omitempty"`
	ACPServer               string          `json:"acp_server"`
	ACPSessionID            string          `json:"acp_session_id,omitempty"`
	WorkingDir              string          `json:"working_dir"`
	CreatedAt               time.Time       `json:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
	LastUserMessageAt       time.Time       `json:"last_user_message_at,omitempty"`
	EventCount              int             `json:"event_count"`
	MaxSeq                  int64           `json:"max_seq,omitempty"`
	Status                  string          `json:"status"`
	Description             string          `json:"description,omitempty"`
	Pinned                  bool            `json:"pinned,omitempty"`
	Archived                bool            `json:"archived,omitempty"`
	ArchivedAt              time.Time       `json:"archived_at,omitempty"`
	ArchiveReason           string          `json:"archived_reason,omitempty"`
	NoArchive               bool            `json:"no_archive,omitempty"`
	RunnerType              string          `json:"runner_type,omitempty"`
	RunnerRestricted        bool            `json:"runner_restricted,omitempty"`
	CurrentModeID           string          `json:"current_mode_id,omitempty"`
	BaselineModel           string          `json:"baseline_model,omitempty"`
	BeadsIssue              string          `json:"beads_issue,omitempty"`
	OriginPromptName        string          `json:"origin_prompt_name,omitempty"`
	BackgroundColor         string          `json:"background_color,omitempty"`
	AdvancedSettings        map[string]bool `json:"advanced_settings,omitempty"`
	ProcessorActivations    int             `json:"processor_activations,omitempty"`
	ProcessorLastActivation time.Time       `json:"processor_last_activation,omitempty"`
	ParentSessionID         string          `json:"parent_session_id,omitempty"`
	ChildOrigin             string          `json:"child_origin,omitempty"`
	ACPStartFailureCount    int             `json:"acp_start_failure_count,omitempty"`
}

// UpdateSession applies a partial update to a session's metadata via PATCH,
// returning the full updated metadata. For the archive-only convenience form,
// see ArchiveSession.
func (c *Client) UpdateSession(sessionID string, req SessionUpdateRequest) (*SessionMetadata, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("update session: marshal: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPatch,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("update session: %w", err)
	}
	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("update session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("update session", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("update session", resp)
	}

	var meta SessionMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("update session: decode: %w", err)
	}
	return &meta, nil
}

// --- Events ---

// GetSessionEventsOptions configures GetSessionEvents' pagination.
type GetSessionEventsOptions struct {
	// Limit caps the number of events returned; 0 = all events (no pagination).
	Limit int
	// BeforeSeq, when > 0, restricts results to events before this sequence number.
	BeforeSeq int64
	// Reverse, when true, returns the newest events first.
	Reverse bool
}

// GetSessionEvents returns a session's recorded events (the raw event log),
// each decoded as a generic Event with Data left as json.RawMessage for the
// caller to type per Event.Type.
func (c *Client) GetSessionEvents(sessionID string, opts GetSessionEventsOptions) ([]SessionEvent, error) {
	qp := url.Values{}
	if opts.Limit > 0 {
		qp.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.BeforeSeq > 0 {
		qp.Set("before", strconv.FormatInt(opts.BeforeSeq, 10))
	}
	if opts.Reverse {
		qp.Set("order", "desc")
	}
	reqURL := c.apiURL("/api/sessions/" + url.PathEscape(sessionID) + "/events")
	if encoded := qp.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	req, err := c.newRequest(http.MethodGet, reqURL, "", nil)
	if err != nil {
		return nil, fmt.Errorf("get session events: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get session events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("get session events", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get session events", resp)
	}

	var events []SessionEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("get session events: decode: %w", err)
	}
	return events, nil
}

// SessionEvent is a single entry in a session's event log, as returned by
// GetSessionEvents. Data is left as raw JSON since its shape depends on Type;
// see docs/devel/session-management.md for the per-type payloads.
type SessionEvent struct {
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
	Meta      map[string]any  `json:"meta,omitempty"`
}

// --- Changes (git status) ---

// ChangedFile represents a file changed in a session's workspace.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // "A", "M", "D", "R", "?"
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	OldPath   string `json:"old_path,omitempty"`
}

// ChangesResponse is the response for GetSessionChanges.
type ChangesResponse struct {
	Files     []ChangedFile `json:"files"`
	IsGitRepo bool          `json:"is_git_repo"`
	Branch    string        `json:"branch"`
	Error     string        `json:"error,omitempty"`
}

// GetSessionChanges returns the git status (changed files) for a session's workspace.
func (c *Client) GetSessionChanges(sessionID string) (*ChangesResponse, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/changes"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get session changes: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get session changes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get session changes", resp)
	}

	var result ChangesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("get session changes: decode: %w", err)
	}
	return &result, nil
}

// --- Advanced settings ---

// SettingsResponse is the response for GetSessionSettings/UpdateSessionSettings.
type SettingsResponse struct {
	Settings map[string]bool `json:"settings"`
}

// GetSessionSettings returns a session's advanced settings (feature flags).
func (c *Client) GetSessionSettings(sessionID string) (*SettingsResponse, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/settings"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get session settings: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get session settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("get session settings", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get session settings", resp)
	}

	var result SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("get session settings: decode: %w", err)
	}
	return &result, nil
}

// UpdateSessionSettings performs a partial update (merge) of a session's
// advanced settings and returns the full settings map after the update.
func (c *Client) UpdateSessionSettings(sessionID string, settings map[string]bool) (*SettingsResponse, error) {
	body, err := json.Marshal(SettingsResponse{Settings: settings})
	if err != nil {
		return nil, fmt.Errorf("update session settings: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPatch,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/settings"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("update session settings: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("update session settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("update session settings", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("update session settings", resp)
	}

	var result SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("update session settings: decode: %w", err)
	}
	return &result, nil
}

// --- Flush (context reset) ---

// FlushResponse is the response for FlushSession.
type FlushResponse struct {
	Status  string `json:"status"`
	Command string `json:"command"`
}

// FlushSession clears the agent's conversation context by sending the
// configured agent-native context-flush command (e.g. "/clear"). Returns a
// 409 *APIError (ErrConflict) if the session is currently processing a
// prompt, and a 400 *APIError (ErrBadRequest) if context flush is not
// configured for the session's ACP server.
func (c *Client) FlushSession(sessionID string) (*FlushResponse, error) {
	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/flush"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("flush session: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("flush session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("flush session", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("flush session", resp)
	}

	var result FlushResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("flush session: decode: %w", err)
	}
	return &result, nil
}

// --- User data ---

// UserDataAttribute is a single name/value pair of conversation user data.
type UserDataAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// UserData is the collection of user data attributes for a conversation.
type UserData struct {
	Attributes []UserDataAttribute `json:"attributes"`
}

// GetSessionUserData returns a session's user data attributes.
func (c *Client) GetSessionUserData(sessionID string) (*UserData, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/user-data"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get session user data: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get session user data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("get session user data", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get session user data", resp)
	}

	var result UserData
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("get session user data: decode: %w", err)
	}
	return &result, nil
}

// SetSessionUserData replaces a session's user data attributes wholesale.
// Returns a 400 *APIError (ErrBadRequest, code "validation_error") if the
// attributes fail the workspace's user-data schema validation.
func (c *Client) SetSessionUserData(sessionID string, attributes []UserDataAttribute) (*UserData, error) {
	body, err := json.Marshal(UserData{Attributes: attributes})
	if err != nil {
		return nil, fmt.Errorf("set session user data: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPut,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/user-data"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("set session user data: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("set session user data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("set session user data", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("set session user data", resp)
	}

	var result UserData
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("set session user data: decode: %w", err)
	}
	return &result, nil
}

// --- Prune ---

// PruneResponse is the response for PruneSession.
type PruneResponse struct {
	PrunedCount    int   `json:"pruned_count"`
	RemainingCount int   `json:"remaining_count"`
	NewMaxSeq      int64 `json:"new_max_seq"`
}

// PruneSession removes old events from a session, keeping the last keepLast
// events (0 uses the server default of 500; the server enforces a minimum of
// 50). Returns a 409 *APIError (ErrConflict) if the session is currently
// processing a prompt.
func (c *Client) PruneSession(sessionID string, keepLast int) (*PruneResponse, error) {
	body, err := json.Marshal(struct {
		KeepLast int `json:"keep_last"`
	}{KeepLast: keepLast})
	if err != nil {
		return nil, fmt.Errorf("prune session: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/prune"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("prune session: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("prune session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("prune session", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("prune session", resp)
	}

	var result PruneResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("prune session: decode: %w", err)
	}
	return &result, nil
}

// --- Running sessions ---

// RunningSessionInfo describes one currently-running session.
type RunningSessionInfo struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	WorkingDir    string `json:"working_dir"`
	IsPrompting   bool   `json:"is_prompting"`
	PromptCount   int    `json:"prompt_count"`
	WorkspaceUUID string `json:"workspace_uuid"`
	ACPServer     string `json:"acp_server"`
}

// RunningSessionsResponse is the response for ListRunningSessions.
type RunningSessionsResponse struct {
	TotalRunning int                  `json:"total_running"`
	Prompting    int                  `json:"prompting"`
	Sessions     []RunningSessionInfo `json:"sessions"`
}

// ListRunningSessions returns information about all running sessions,
// including which ones are actively prompting.
func (c *Client) ListRunningSessions() (*RunningSessionsResponse, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/running"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("list running sessions: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list running sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("list running sessions", resp)
	}

	var result RunningSessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("list running sessions: decode: %w", err)
	}
	return &result, nil
}
