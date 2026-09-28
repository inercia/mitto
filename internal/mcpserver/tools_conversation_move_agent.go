// tools_conversation_move_agent.go: MCP tool handler for
// mitto_conversation_move_agent (mitto-f7yo.3). Rebinds an existing
// conversation to a different ACP agent configured for the same folder while
// keeping it active, via SessionManager.MoveSessionToAgent (backend core,
// mitto-f7yo.1). Mirrors the REST entry point
// (internal/web/handlers/session_move_agent.go, mitto-f7yo.2): same error
// mapping, same session_agent_moved broadcast.
package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// Sentinel errors mirroring internal/conversation's ErrMoveAgent* (defined in
// session_manager_move_agent.go, bead mitto-f7yo.1). mcpserver cannot import
// internal/conversation directly: that package already imports
// internal/mcpserver (RegisterSession/UnregisterSession for global MCP
// session registration), so the reverse import would create a cycle.
// *conversation.SessionManager instead implements MoveSessionToAgentForMCP
// (internal/conversation/session_manager_move_agent_mcp.go), which adapts
// its own conversation.ErrMoveAgent* sentinels to these mirrors via
// errors.Is before returning.
var (
	// ErrMoveAgentArchived mirrors conversation.ErrMoveAgentArchived.
	ErrMoveAgentArchived = errors.New("conversation is archived")
	// ErrMoveAgentSameAgent mirrors conversation.ErrMoveAgentSameAgent.
	ErrMoveAgentSameAgent = errors.New("target agent is the same as the current agent")
	// ErrMoveAgentUnknownTarget mirrors conversation.ErrMoveAgentUnknownTarget.
	ErrMoveAgentUnknownTarget = errors.New("target agent is not configured")
	// ErrMoveAgentNoWorkspace mirrors conversation.ErrMoveAgentNoWorkspace.
	ErrMoveAgentNoWorkspace = errors.New("target agent is not configured for this conversation's folder")
	// ErrMoveAgentBusy mirrors conversation.ErrMoveAgentBusy.
	ErrMoveAgentBusy = errors.New("conversation is busy (a turn is streaming or a loop run is in flight)")
)

// MoveAgentOptions mirrors conversation.MoveAgentOptions for the
// SessionManager interface boundary (see the sentinel errors' doc comment
// above for why a mirror, rather than the real type, is needed here).
type MoveAgentOptions struct {
	// IncludeChildren also moves every non-archived, non-busy descendant
	// conversation still bound to the old agent onto the target agent.
	IncludeChildren bool
}

// MoveAgentSkip mirrors conversation.MoveAgentSkip: a descendant conversation
// IncludeChildren declined to move, and why.
type MoveAgentSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// MoveAgentResult mirrors conversation.MoveAgentResult for the SessionManager
// interface boundary. ResumeError is pre-stringified by the adapter (an error
// value doesn't marshal usefully, and can't cross the package boundary as-is
// without importing internal/conversation).
type MoveAgentResult struct {
	// Moved lists every session ID actually rebound to the target agent: the
	// requested conversation first, followed by any moved descendants.
	Moved []string `json:"moved"`
	// Skipped lists descendant conversations IncludeChildren declined to move.
	Skipped []MoveAgentSkip `json:"skipped,omitempty"`
	// PreviousAgent is the ACP server the conversation was bound to before the move.
	PreviousAgent string `json:"previous_agent"`
	// PreviousBaselineModel is the conversation's BaselineModel value before
	// it was cleared by the move (seam for mitto-f7yo.4, not otherwise used here).
	PreviousBaselineModel string `json:"previous_baseline_model,omitempty"`
	// ResumeError is set when the move succeeded but resuming the conversation
	// on the target agent failed; the move still stands (see
	// conversation.MoveSessionToAgent's doc comment for the rationale).
	ResumeError string `json:"-"`
}

// ConversationMoveAgentInput is the input for mitto_conversation_move_agent.
type ConversationMoveAgentInput struct {
	SelfID         string `json:"self_id"`         // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"` // Target conversation to move, or "self"/your own ID to move yourself
	// ACPServer is an optional exact target ACP server name.
	ACPServer string `json:"acp_server,omitempty"`
	// Agent is an optional exact alias for ACPServer (mitto-lrt.13): if only
	// one of acp_server/agent is set, it wins; if both are set, they must
	// resolve to the same configured server or the call errors. Unlike
	// acp_server (exact match only), agent additionally accepts a
	// case-insensitive alias of a configured server name.
	Agent string `json:"agent,omitempty"`
	// IncludeChildren also moves every non-archived, non-busy descendant
	// conversation still bound to the conversation's current agent onto the
	// target agent (recursively). Descendants bound to a different agent,
	// archived, or busy are left untouched and reported in the output's
	// "skipped" list. Defaults to false.
	IncludeChildren bool `json:"include_children,omitempty"`
}

// ConversationMoveAgentOutput is the output for mitto_conversation_move_agent.
type ConversationMoveAgentOutput struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id,omitempty"`
	// Moved lists every session ID actually rebound to the target agent: the
	// requested conversation_id first, followed by any moved descendants
	// (only populated when include_children was set).
	Moved []string `json:"moved,omitempty"`
	// Skipped lists descendant conversations include_children declined to
	// move (busy, archived, or bound to a different agent), with a reason.
	Skipped []MoveAgentSkip `json:"skipped,omitempty"`
	// PreviousAgent is the ACP server conversation_id was bound to before the move.
	PreviousAgent string `json:"previous_agent,omitempty"`
	// NewAgent is the resolved target ACP server the conversation was moved to.
	NewAgent string `json:"new_agent,omitempty"`
	// PreviousBaselineModel is conversation_id's BaselineModel value before
	// it was cleared by the move (seam for mitto-f7yo.4 model mapping).
	PreviousBaselineModel string `json:"previous_baseline_model,omitempty"`
	// ResumeError is set when metadata was rewritten successfully (the move
	// stands) but resuming the conversation on the target agent failed — the
	// conversation will resume normally the next time it is accessed.
	ResumeError string `json:"resume_error,omitempty"`
	Error       string `json:"error,omitempty"`
}

// handleConversationMoveAgent implements mitto_conversation_move_agent. It
// resolves the target agent using the same acp_server/agent alias precedence
// as mitto_conversation_new (mitto-lrt.13), calls
// SessionManager.MoveSessionToAgentForMCP (the mcpserver-facing adapter for
// conversation.SessionManager.MoveSessionToAgent, mitto-f7yo.1), maps
// ErrMoveAgent*/session.ErrSessionNotFound to a clear tool error, and — on
// success — emits the same session_agent_moved global WS broadcast the REST
// handler emits (mitto-f7yo.2) for every moved conversation.
//
// Permission gating deliberately mirrors mitto_conversation_update acting on
// OTHER conversations: no extra flag/cross-workspace check beyond the caller
// being a registered running session and the target conversation existing
// (mitto_conversation_update has no such gate either — see
// handleConversationUpdate). Moving "self" is allowed the same way update
// allows self, but a self-move issued while the caller's own turn is
// streaming will always be rejected as busy: MoveSessionToAgent's busy check
// reads BackgroundSession.IsPrompting(), which is true for the whole
// duration of the tool call handling this very request.
func (s *Server) handleConversationMoveAgent(ctx context.Context, req *mcp.CallToolRequest, input ConversationMoveAgentInput) (*mcp.CallToolResult, ConversationMoveAgentOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationMoveAgentOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ConversationMoveAgentOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Validate target agent
	if input.ACPServer == "" && input.Agent == "" {
		return nil, ConversationMoveAgentOutput{Success: false, Error: "either 'acp_server' or 'agent' is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationMoveAgentOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationMoveAgentOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Self-targeting: agents may pass "self" to move their OWN conversation,
	// same convention as mitto_conversation_update/mitto_conversation_delete.
	if input.ConversationID == "self" {
		input.ConversationID = realSessionID
	}

	s.mu.RLock()
	cfg := s.config
	sm := s.sessionManager
	s.mu.RUnlock()

	if sm == nil {
		return nil, ConversationMoveAgentOutput{Success: false, Error: "session manager not available"}, nil
	}
	if cfg == nil {
		return nil, ConversationMoveAgentOutput{Success: false, Error: "server configuration not available"}, nil
	}

	targetAgent, resolveErr := resolveAgentOrACPServerName(cfg, input.ACPServer, input.Agent)
	if resolveErr != nil {
		return nil, ConversationMoveAgentOutput{Success: false, Error: resolveErr.Error()}, nil
	}

	result, err := sm.MoveSessionToAgentForMCP(input.ConversationID, targetAgent, MoveAgentOptions{
		IncludeChildren: input.IncludeChildren,
	})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			return nil, ConversationMoveAgentOutput{
				Success: false,
				Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
			}, nil
		case errors.Is(err, ErrMoveAgentBusy):
			return nil, ConversationMoveAgentOutput{
				Success: false,
				Error:   err.Error() + "; retry once the conversation is idle",
			}, nil
		case errors.Is(err, ErrMoveAgentArchived),
			errors.Is(err, ErrMoveAgentSameAgent),
			errors.Is(err, ErrMoveAgentUnknownTarget),
			errors.Is(err, ErrMoveAgentNoWorkspace):
			return nil, ConversationMoveAgentOutput{Success: false, Error: err.Error()}, nil
		default:
			return nil, ConversationMoveAgentOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to move conversation to agent: %v", err),
			}, nil
		}
	}

	// Broadcast an agent-moved event for the moved conversation and every
	// moved child so every connected client can refresh acp_server for that
	// row, regardless of whether the move was triggered via REST or MCP.
	for _, movedID := range result.Moved {
		sm.BroadcastSessionAgentMoved(movedID, targetAgent, result.PreviousAgent)
	}

	s.logger.Info("Conversation moved to agent via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"previous_agent", result.PreviousAgent,
		"new_agent", targetAgent,
		"include_children", input.IncludeChildren,
		"moved_count", len(result.Moved),
		"skipped_count", len(result.Skipped))

	return nil, ConversationMoveAgentOutput{
		Success:               true,
		ConversationID:        input.ConversationID,
		Moved:                 result.Moved,
		Skipped:               result.Skipped,
		PreviousAgent:         result.PreviousAgent,
		NewAgent:              targetAgent,
		PreviousBaselineModel: result.PreviousBaselineModel,
		ResumeError:           result.ResumeError,
	}, nil
}
