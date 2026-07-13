// tools_wait.go: MCP tool handler for mitto_conversation_wait plus the shared
// startProgressHeartbeat helper used by other long-blocking tool handlers.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Parent-Child Task Coordination Handlers
// =============================================================================

// =============================================================================
// Conversation Wait
// =============================================================================

// defaultConversationWaitTimeout is the default timeout for mitto_conversation_wait.
const defaultConversationWaitTimeout = 10 * time.Minute

// mcpHeartbeatInterval is how often a long-blocking tool handler emits a progress
// notification to keep the in-flight request's SSE stream from idling out. Must
// stay comfortably below the transport idle window (tunnel / agent HTTP client).
const mcpHeartbeatInterval = 15 * time.Second

// startProgressHeartbeat emits periodic progress notifications on the in-flight
// request's stream until the returned stop func is called, keeping the SSE
// transport alive during long-blocking waits (mitto-qal.1).
func (s *Server) startProgressHeartbeat(ctx context.Context, req *mcp.CallToolRequest) func() {
	if req == nil || req.Session == nil {
		return func() {}
	}
	hbCtx, cancel := context.WithCancel(ctx)
	token := req.Params.GetProgressToken()
	go func() {
		ticker := time.NewTicker(mcpHeartbeatInterval)
		defer ticker.Stop()
		var n float64
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				n++
				if err := req.Session.NotifyProgress(hbCtx, &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      n,
					Message:       "still working…",
				}); err != nil {
					s.logger.Debug("progress heartbeat failed", "error", err)
					return
				}
			}
		}
	}()
	return cancel
}

// waitConditionAgentResponded is the "what" value for waiting until the agent finishes responding.
const waitConditionAgentResponded = "agent_responded"

func (s *Server) handleConversationWait(ctx context.Context, req *mcp.CallToolRequest, input ConversationWaitInput) (*mcp.CallToolResult, ConversationWaitOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationWaitOutput{Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ConversationWaitOutput{Error: "conversation_id is required"}, nil
	}

	// Validate "what" parameter
	if input.What == "" {
		return nil, ConversationWaitOutput{Error: "what is required"}, nil
	}
	if input.What != waitConditionAgentResponded {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("unsupported wait condition: %q (supported: %q)", input.What, waitConditionAgentResponded),
		}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Get the target session via SessionManager
	if s.sessionManager == nil {
		return nil, ConversationWaitOutput{Error: "session manager not available"}, nil
	}

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, ConversationWaitOutput{
				Error: fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}

		s.mu.RLock()
		store := s.store
		s.mu.RUnlock()

		if store != nil {
			// Validate the target conversation belongs to the workspace
			targetMeta, err := store.GetMetadata(input.ConversationID)
			if err == nil && targetMeta.WorkingDir != targetWS.WorkingDir {
				return nil, ConversationWaitOutput{
					Error: fmt.Sprintf("conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace),
				}, nil
			}

			// Check if cross-workspace (caller's workspace differs from target)
			sourceMeta, err := store.GetMetadata(realSessionID)
			if err == nil && sourceMeta.WorkingDir != targetWS.WorkingDir {
				// Permission check: requires can_interact_other_workspaces flag
				if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
					return nil, ConversationWaitOutput{
						Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
							session.FlagCanInteractOtherWorkspaces),
					}, nil
				}
				if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "wait on a conversation", targetWS); err != nil {
					return nil, ConversationWaitOutput{Error: err.Error()}, nil
				}
			}
		}
	}

	targetBS := s.sessionManager.GetSession(input.ConversationID)
	if targetBS == nil {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("target conversation not running: %s", input.ConversationID),
		}, nil
	}

	// If the agent is not currently responding, return immediately
	if !targetBS.IsPrompting() {
		s.logger.Debug("Conversation wait: agent not prompting, returning immediately",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"what", input.What)
		return nil, ConversationWaitOutput{
			Success: true,
			What:    input.What,
		}, nil
	}

	// Determine timeout
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultConversationWaitTimeout
	}

	s.logger.Info("Waiting for conversation condition",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"what", input.What,
		"timeout", timeout)

	// Broadcast that this session is now waiting (shows hourglass in sidebar)
	if s.sessionManager != nil {
		s.sessionManager.BroadcastWaitingForChildren(realSessionID, true)
		defer func() {
			s.sessionManager.BroadcastWaitingForChildren(realSessionID, false)
		}()
	}

	// Wait for the agent to finish responding, respecting context cancellation.
	// WaitForResponseComplete blocks with its own timeout, but we also need to
	// handle ctx.Done() for MCP-level cancellation.
	defer s.startProgressHeartbeat(ctx, req)()
	done := make(chan bool, 1)
	go func() {
		done <- targetBS.WaitForResponseComplete(timeout)
	}()

	select {
	case completed := <-done:
		if completed {
			s.logger.Info("Conversation wait condition met",
				"source_session", realSessionID,
				"target_conversation", input.ConversationID,
				"what", input.What)
			return nil, ConversationWaitOutput{
				Success: true,
				What:    input.What,
			}, nil
		}
		// Timed out
		stillPrompting := targetBS.IsPrompting()
		var msg string
		if stillPrompting {
			msg = fmt.Sprintf("timed out after %s; the agent is still responding", timeout)
		} else {
			msg = fmt.Sprintf("timed out after %s; the agent has finished responding", timeout)
		}
		s.logger.Warn("Conversation wait timed out",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"what", input.What,
			"timeout", timeout,
			"still_prompting", stillPrompting)
		return nil, ConversationWaitOutput{
			Success:        true,
			What:           input.What,
			TimedOut:       true,
			StillPrompting: stillPrompting,
			Message:        msg,
		}, nil
	case <-ctx.Done():
		return nil, ConversationWaitOutput{
			Error: "context cancelled while waiting",
		}, nil
	}
}
