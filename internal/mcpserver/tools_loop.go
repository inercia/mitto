// tools_loop.go: MCP tool handler for mitto_conversation_run_loop_now.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunLoopNowInput is the input for mitto_conversation_run_loop_now tool.
type RunLoopNowInput struct {
	SelfID         string `json:"self_id"`               // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`       // Target conversation to trigger
	ResetTimer     *bool  `json:"reset_timer,omitempty"` // Whether to reset the countdown timer (default: true)
}

// RunLoopNowOutput is the output for mitto_conversation_run_loop_now tool.
type RunLoopNowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleRunLoopNow(ctx context.Context, req *mcp.CallToolRequest, input RunLoopNowInput) (*mcp.CallToolResult, RunLoopNowOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, RunLoopNowOutput{Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, RunLoopNowOutput{Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, RunLoopNowOutput{
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, RunLoopNowOutput{Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	// Check if loop runner is available
	s.mu.RLock()
	runner := s.loopRunner
	s.mu.RUnlock()

	if runner == nil {
		return nil, RunLoopNowOutput{Error: "loop runner not available"}, nil
	}

	// Determine reset_timer (default: true — same as normal scheduled runs)
	resetTimer := true
	if input.ResetTimer != nil {
		resetTimer = *input.ResetTimer
	}

	// Trigger immediate delivery
	if err := runner.TriggerNow(input.ConversationID, resetTimer); err != nil {
		return nil, RunLoopNowOutput{Error: fmt.Sprintf("failed to trigger loop run: %v", err)}, nil
	}

	msg := "Loop prompt triggered successfully"
	if !resetTimer {
		msg += " (countdown timer preserved)"
	} else {
		msg += " (countdown timer reset)"
	}

	s.logger.Info("Loop prompt triggered via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"reset_timer", resetTimer)

	return nil, RunLoopNowOutput{Success: true, Message: msg}, nil
}
