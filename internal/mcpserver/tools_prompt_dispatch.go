// tools_prompt_dispatch.go: MCP tool handler for mitto_conversation_send_prompt.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// SendPromptToConversationInput is the input for send_prompt_to_conversation tool.
type SendPromptToConversationInput struct {
	SelfID         string            `json:"self_id"`         // YOUR session ID (the caller), not the target
	ConversationID string            `json:"conversation_id"` // Target conversation ID to send prompt to
	Prompt         string            `json:"prompt,omitempty"`
	Workspace      string            `json:"workspace,omitempty"`     // Optional workspace UUID for cross-workspace operations
	ScheduleTime   string            `json:"schedule_time,omitempty"` // Optional: RFC 3339 timestamp or relative duration (e.g., "5m", "1h")
	Arguments      map[string]string `json:"arguments,omitempty"`     // Optional: values for Go-template .Args placeholders in the prompt text when sent
	PromptName     string            `json:"prompt_name,omitempty"`   // Optional: name of a workspace prompt to send by name (resolved at dispatch in the target conversation's context)
}

func (s *Server) handleSendPromptToConversation(ctx context.Context, req *mcp.CallToolRequest, input SendPromptToConversationInput) (*mcp.CallToolResult, SendPromptOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, SendPromptOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, SendPromptOutput{
			Success: false,
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved",
				input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, SendPromptOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	// Permission check: requires can_send_prompt on the SOURCE session
	if !s.checkSessionFlag(realSessionID, session.FlagCanSendPrompt) {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("tool 'mitto_send_prompt_to_conversation' requires the 'Can Send Prompt' (%s) flag to be enabled in Advanced Settings", session.FlagCanSendPrompt),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, SendPromptOutput{Success: false, Error: "session store not available"}, nil
	}

	// Validate input
	if input.ConversationID == "" {
		return nil, SendPromptOutput{Success: false, Error: "conversation_id is required"}, nil
	}
	if strings.TrimSpace(input.Prompt) == "" && strings.TrimSpace(input.PromptName) == "" {
		return nil, SendPromptOutput{Success: false, Error: "either 'prompt' or 'prompt_name' is required"}, nil
	}

	// mitto-kt6: prompt_name wins when both are supplied. Agents forced by strict
	// JSON schemas often fill 'prompt' with a placeholder to satisfy the field
	// even when they only intend a named dispatch; delivering that placeholder
	// would silently override the resolved prompt body.
	if strings.TrimSpace(input.PromptName) != "" && strings.TrimSpace(input.Prompt) != "" {
		s.logger.Info("Both 'prompt' and 'prompt_name' provided; prompt_name wins, ignoring 'prompt'",
			"source_session", realSessionID,
			"target_session", input.ConversationID,
			"prompt_name", input.PromptName)
		input.Prompt = ""
	}

	// Check if target conversation exists
	targetMeta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		if s.sessionManager == nil {
			return nil, SendPromptOutput{Success: false, Error: "session manager not available"}, nil
		}
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}

		// Validate conversation belongs to the specified workspace
		if targetMeta.WorkingDir != targetWS.WorkingDir {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace),
			}, nil
		}

		// Check if cross-workspace (caller's workspace differs from target)
		sourceMeta, err := store.GetMetadata(realSessionID)
		if err != nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to get source session metadata: %v", err),
			}, nil
		}
		if sourceMeta.WorkingDir != targetWS.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, SendPromptOutput{
					Success: false,
					Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces),
				}, nil
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "send a prompt to a conversation", targetWS); err != nil {
				return nil, SendPromptOutput{Success: false, Error: err.Error()}, nil
			}
		}
	}

	// Parse optional scheduled time (supports RFC 3339 or relative duration like "5m", "1h")
	var scheduledTime *time.Time
	if input.ScheduleTime != "" {
		t, err := session.ParseScheduleTime(input.ScheduleTime)
		if err != nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
		scheduledTime = &t
	}

	// Reject a free-text body with broken Go-template syntax up front (mitto-e7u),
	// so the orchestrator gets a clear, synchronous error instead of the body being
	// enqueued and later silently delivered raw to a child that cannot act on it.
	// Named prompts (prompt_name) are validated when their body is resolved at
	// dispatch in the target's context.
	if strings.TrimSpace(input.PromptName) == "" {
		if err := config.ValidatePromptTemplateSyntax("prompt", input.Prompt); err != nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   "invalid prompt template: " + err.Error(),
			}, nil
		}
	}

	// Get the queue for the target conversation
	queue := store.Queue(input.ConversationID)

	// Add the prompt to the queue (agent origin: cross-session MCP dispatch, fail-closed on broken templates)
	msg, err := queue.AddWithOrigin(input.Prompt, nil, nil, realSessionID, scheduledTime, 0, input.Arguments, input.PromptName, session.QueueOriginAgent)
	if err != nil {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to add prompt to queue: %v", err),
		}, nil
	}

	// Get queue length for position info
	queueLen, _ := queue.Len()

	s.logger.Info("Prompt sent to conversation queue",
		"source_session", realSessionID,
		"target_session", input.ConversationID,
		"message_id", msg.ID,
		"queue_position", queueLen,
		"scheduled", scheduledTime != nil)

	// Try to process the queued message immediately if agent is idle.
	// Skip for scheduled messages — the loop runner will deliver them when due.
	if scheduledTime == nil {
		if s.sessionManager != nil {
			bs := s.sessionManager.GetSession(input.ConversationID)
			if bs == nil && !targetMeta.Archived {
				// Session is stored or completed (e.g., GC-closed) — try to resume it so the queue gets processed.
				s.logger.Info("Auto-resuming session to process queued prompt",
					"target_session", input.ConversationID,
					"source_session", realSessionID,
					"target_status", string(targetMeta.Status))
				resumed, resumeErr := s.sessionManager.ResumeSession(input.ConversationID, targetMeta.Name, targetMeta.WorkingDir)
				if resumeErr != nil {
					// mitto-54k.6: a cold-start MCP-init timeout is transient on a
					// shared ACP process (warm-once barrier mitto-54k.3 will let a
					// later resume succeed). Do not log the hard "Failed to auto-
					// resume stored session" warning; schedule a bounded background
					// retry so the queued prompt is delivered once the process warms.
					if s.sessionManager.IsMCPInitTimeout(resumeErr) {
						s.logger.Info("Auto-resume deferred: transient cold-start MCP-init timeout; scheduling bounded retry",
							"target_session", input.ConversationID,
							"error", resumeErr)
						sm := s.sessionManager
						convID := input.ConversationID
						convName := targetMeta.Name
						convWD := targetMeta.WorkingDir
						go func() {
							backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
							for attempt, delay := range backoffs {
								time.Sleep(delay)
								if sm == nil {
									return
								}
								// Session may already be running (a foreground resume or a
								// prior retry attached first); if so, kick the queue and stop.
								if existing := sm.GetSession(convID); existing != nil {
									go existing.TryProcessQueuedMessage()
									return
								}
								retriedBS, retryErr := sm.ResumeSession(convID, convName, convWD)
								if retryErr == nil {
									s.logger.Info("Auto-resume retry succeeded",
										"target_session", convID,
										"attempt", attempt+1)
									if retriedBS != nil {
										go retriedBS.TryProcessQueuedMessage()
									}
									return
								}
								if !sm.IsMCPInitTimeout(retryErr) {
									s.logger.Warn("Auto-resume retry aborted: non-transient error",
										"target_session", convID,
										"attempt", attempt+1,
										"error", retryErr)
									return
								}
								s.logger.Debug("Auto-resume retry still hitting MCP-init timeout",
									"target_session", convID,
									"attempt", attempt+1)
							}
							s.logger.Warn("Auto-resume gave up after bounded retries; queued prompt will be delivered on next ensure_resumed/reconnect",
								"target_session", convID,
								"attempts", len(backoffs))
						}()
					} else {
						s.logger.Warn("Failed to auto-resume stored session",
							"target_session", input.ConversationID,
							"error", resumeErr)
					}
				} else {
					bs = resumed
				}
			}
			if bs != nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}

	return nil, SendPromptOutput{
		Success:       true,
		MessageID:     msg.ID,
		QueuePosition: queueLen,
	}, nil
}
