package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// MoveAgentSkip mirrors conversation.MoveAgentSkip's wire shape: a descendant
// conversation that MoveAgent's include_children declined to move, and why.
type MoveAgentSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// MoveAgentResult mirrors handlers.moveAgentResultJSON's wire shape, the
// response body of POST /api/sessions/{id}/move-agent.
type MoveAgentResult struct {
	// Moved lists every session ID actually rebound to the target agent:
	// the requested session first, followed by any moved descendants (only
	// populated when IncludeChildren was set on the request).
	Moved []string `json:"moved"`
	// Skipped lists descendant conversations IncludeChildren declined to
	// move (busy, archived, or bound to a different agent), with a reason.
	Skipped []MoveAgentSkip `json:"skipped,omitempty"`
	// PreviousAgent is the ACP server the session was bound to before the move.
	PreviousAgent string `json:"previous_agent"`
	// PreviousBaselineModel is the session's BaselineModel value before the
	// move cleared it (mitto-f7yo.4 maps it to a comparable model on the
	// target agent once the new agent's catalog is known).
	PreviousBaselineModel string `json:"previous_baseline_model,omitempty"`
	// ResumeError is set when the metadata rewrite succeeded (the move
	// stands) but resuming the session on the target agent failed.
	ResumeError string `json:"resume_error,omitempty"`
}

// MoveAgentCandidate mirrors conversation.MoveAgentCandidate: one alternative
// agent a conversation could be moved to.
type MoveAgentCandidate struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	Available           bool   `json:"available"`
	LoopPromptAvailable *bool  `json:"loop_prompt_available,omitempty"`
}

// MoveAgentPreflight mirrors conversation.MoveAgentPreflight, the response
// body of GET /api/sessions/{id}/move-agent/preflight.
type MoveAgentPreflight struct {
	CurrentAgent   string               `json:"current_agent"`
	Candidates     []MoveAgentCandidate `json:"candidates"`
	Busy           bool                 `json:"busy"`
	BusyReason     string               `json:"busy_reason,omitempty"`
	Archived       bool                 `json:"archived"`
	IsLoop         bool                 `json:"is_loop"`
	LoopPromptName string               `json:"loop_prompt_name,omitempty"`
	ChildrenCount  int                  `json:"children_count"`
	BaselineModel  string               `json:"baseline_model,omitempty"`
}

// MoveAgentPreflight reports move-agent affordance for sessionID (current
// agent, valid target candidates, busy/archived state, loop info, and
// descendant count) via GET /api/sessions/{id}/move-agent/preflight, without
// performing a move.
func (c *Client) MoveAgentPreflight(sessionID string) (*MoveAgentPreflight, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/move-agent/preflight"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("move agent preflight: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("move agent preflight: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("move agent preflight", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("move agent preflight", resp)
	}

	var preflight MoveAgentPreflight
	if err := json.NewDecoder(resp.Body).Decode(&preflight); err != nil {
		return nil, fmt.Errorf("move agent preflight: decode: %w", err)
	}
	return &preflight, nil
}

// MoveAgent rebinds sessionID (and, when includeChildren is set, its
// eligible descendants) from its current ACP agent to targetAgent, keeping
// it active, via POST /api/sessions/{id}/move-agent. On a non-2xx response
// the returned error is an *APIError (Status carries the HTTP status code,
// e.g. http.StatusConflict for "busy"/"archived", http.StatusBadRequest for
// an invalid/unknown target agent).
func (c *Client) MoveAgent(sessionID, targetAgent string, includeChildren bool) (*MoveAgentResult, error) {
	body, err := json.Marshal(map[string]interface{}{
		"target_agent":     targetAgent,
		"include_children": includeChildren,
	})
	if err != nil {
		return nil, fmt.Errorf("move agent: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/move-agent"), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("move agent: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("move agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sessionNotFoundError("move agent", sessionID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("move agent", resp)
	}

	var result MoveAgentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("move agent: decode: %w", err)
	}
	return &result, nil
}
