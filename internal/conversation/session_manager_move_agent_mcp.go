// session_manager_move_agent_mcp.go: adapts MoveSessionToAgent (bead
// mitto-f7yo.1, session_manager_move_agent.go) and its broadcast for the
// mitto_conversation_move_agent MCP tool (bead mitto-f7yo.3).
//
// internal/mcpserver cannot import this package directly: this package
// already imports internal/mcpserver (RegisterSession/UnregisterSession for
// global MCP session registration), so the reverse import would create a
// cycle. mcpserver instead declares its own mirror types
// (mcpserver.MoveAgentOptions/MoveAgentResult/MoveAgentSkip and
// ErrMoveAgent* sentinels, see tools_conversation_move_agent.go) satisfied
// by MoveSessionToAgentForMCP below, which translates to/from this
// package's own MoveAgentOptions/MoveAgentResult/ErrMoveAgent*.
package conversation

import (
	"errors"

	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

// MoveSessionToAgentForMCP adapts MoveSessionToAgent to the
// mcpserver.SessionManager interface contract. See the package doc comment
// above for why this translation layer exists instead of mcpserver calling
// MoveSessionToAgent directly.
func (sm *SessionManager) MoveSessionToAgentForMCP(sessionID, targetAgent string, opts mcpserver.MoveAgentOptions) (mcpserver.MoveAgentResult, error) {
	result, err := sm.MoveSessionToAgent(sessionID, targetAgent, MoveAgentOptions{
		IncludeChildren: opts.IncludeChildren,
	})

	out := mcpserver.MoveAgentResult{
		Moved:                 result.Moved,
		PreviousAgent:         result.PreviousAgent,
		PreviousBaselineModel: result.PreviousBaselineModel,
	}
	for _, sk := range result.Skipped {
		out.Skipped = append(out.Skipped, mcpserver.MoveAgentSkip{ID: sk.ID, Reason: sk.Reason})
	}
	if result.ResumeError != nil {
		out.ResumeError = result.ResumeError.Error()
	}

	if err == nil {
		return out, nil
	}

	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		return out, session.ErrSessionNotFound
	case errors.Is(err, ErrMoveAgentArchived):
		return out, mcpserver.ErrMoveAgentArchived
	case errors.Is(err, ErrMoveAgentSameAgent):
		return out, mcpserver.ErrMoveAgentSameAgent
	case errors.Is(err, ErrMoveAgentUnknownTarget):
		return out, mcpserver.ErrMoveAgentUnknownTarget
	case errors.Is(err, ErrMoveAgentNoWorkspace):
		return out, mcpserver.ErrMoveAgentNoWorkspace
	case errors.Is(err, ErrMoveAgentBusy):
		return out, mcpserver.ErrMoveAgentBusy
	default:
		return out, err
	}
}

// BroadcastSessionAgentMoved broadcasts a session_agent_moved event to all
// connected clients. This is called when a conversation's ACP server binding
// changes via SessionManager.MoveSessionToAgent (REST mitto-f7yo.2, MCP
// mitto-f7yo.3), mirroring BroadcastSessionRenamed's pattern.
func (sm *SessionManager) BroadcastSessionAgentMoved(sessionID, newAgent, previousAgent string) {
	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	em.Broadcast(WSMsgTypeSessionAgentMoved, map[string]string{
		"session_id":     sessionID,
		"acp_server":     newAgent,
		"previous_agent": previousAgent,
	})

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session agent moved",
			"session_id", sessionID,
			"acp_server", newAgent,
			"previous_agent", previousAgent,
			"clients", em.ClientCount())
	}
}
