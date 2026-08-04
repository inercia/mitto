// tools_conversation_lifecycle.go: MCP tool handlers for conversation CRUD operations
// (mitto_get_conversation, mitto_conversation_archive, mitto_conversation_delete,
// mitto_conversation_update). Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// GetConversationInput is the input for mitto_get_conversation tool.
type GetConversationInput struct {
	SelfID         string `json:"self_id"`             // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`     // Target conversation ID to get properties for
	Workspace      string `json:"workspace,omitempty"` // Optional workspace UUID for cross-workspace operations
}

// GetConversationOutput is the output for mitto_get_conversation tool.
// It returns the same ConversationDetails as other conversation tools.
type GetConversationOutput = ConversationDetails

func (s *Server) handleGetConversation(ctx context.Context, req *mcp.CallToolRequest, input GetConversationInput) (*mcp.CallToolResult, GetConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf("self_id is required")
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf("conversation_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID)
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, GetConversationOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, GetConversationOutput{}, fmt.Errorf("session store not available")
	}

	// Get metadata for the target conversation
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, GetConversationOutput{}, fmt.Errorf("conversation not found: %s", input.ConversationID)
	}

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		if s.sessionManager == nil {
			return nil, GetConversationOutput{}, fmt.Errorf("session manager not available")
		}
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, GetConversationOutput{}, fmt.Errorf("workspace not found: %s", input.Workspace)
		}

		// Validate conversation belongs to the specified workspace
		if meta.WorkingDir != targetWS.WorkingDir {
			return nil, GetConversationOutput{}, fmt.Errorf(
				"conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace)
		}

		// Check if cross-workspace (caller's workspace differs from target)
		sourceMeta, err := store.GetMetadata(realSessionID)
		if err != nil {
			return nil, GetConversationOutput{}, fmt.Errorf("failed to get source session metadata: %v", err)
		}
		if sourceMeta.WorkingDir != targetWS.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, GetConversationOutput{}, fmt.Errorf(
					"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
					session.FlagCanInteractOtherWorkspaces)
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "view a conversation", targetWS); err != nil {
				return nil, GetConversationOutput{}, err
			}
		}
	}

	// Build unified conversation details
	output := s.buildConversationDetails(meta, store.SessionDir(meta.SessionID))

	s.logger.Debug("Get conversation properties",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"is_running", output.IsRunning,
		"is_prompting", output.IsPrompting)

	return nil, output, nil
}

// ArchiveConversationInput is the input for mitto_conversation_archive tool.
type ArchiveConversationInput struct {
	SelfID         string `json:"self_id"`            // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`    // Target conversation to archive/unarchive
	Archived       *bool  `json:"archived,omitempty"` // true to archive, false to unarchive (defaults to true)
}

// ArchiveConversationOutput is the output for mitto_conversation_archive tool.
type ArchiveConversationOutput struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id,omitempty"`
	Archived       bool   `json:"archived,omitempty"`
	ArchivedAt     string `json:"archived_at,omitempty"` // RFC3339 format, only when archiving
	Error          string `json:"error,omitempty"`
}

// archiveWaitTimeout is the maximum time to wait for a response to complete when archiving.
const archiveWaitTimeout = 5 * time.Minute

func (s *Server) handleArchiveConversation(ctx context.Context, req *mcp.CallToolRequest, input ArchiveConversationInput) (*mcp.CallToolResult, ArchiveConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ArchiveConversationOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ArchiveConversationOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Default to archiving if not specified
	archived := true
	if input.Archived != nil {
		archived = *input.Archived
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ArchiveConversationOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	s.mu.RLock()
	store := s.store
	sessionManager := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, ArchiveConversationOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// When archiving a child session, delegate to handleDeleteConversation (which enforces parent-only permission)
	if archived && meta.ParentSessionID != "" {
		if meta.ParentSessionID != realSessionID {
			return nil, ArchiveConversationOutput{
				Success: false,
				Error:   "permission denied: only the parent can archive/delete a child conversation",
			}, nil
		}
		_, deleteOut, err := s.handleDeleteConversation(ctx, req, DeleteConversationInput{
			SelfID:         input.SelfID,
			ConversationID: input.ConversationID,
		})
		if err != nil {
			return nil, ArchiveConversationOutput{Success: false, Error: err.Error()}, nil
		}
		return nil, ArchiveConversationOutput{
			Success:        deleteOut.Success,
			ConversationID: deleteOut.ConversationID,
			Archived:       deleteOut.Success,
			Error:          deleteOut.Error,
		}, nil
	}

	// Check if already in the desired state
	if meta.Archived == archived {
		state := "archived"
		if !archived {
			state = "unarchived"
		}
		return nil, ArchiveConversationOutput{
			Success:        true,
			ConversationID: input.ConversationID,
			Archived:       archived,
			Error:          fmt.Sprintf("conversation is already %s", state),
		}, nil
	}

	// Handle archive lifecycle
	if archived {
		if sessionManager != nil {
			// Wait for any active response to complete before archiving
			reason := "archived_via_mcp"
			if !sessionManager.CloseSessionGracefully(input.ConversationID, reason, archiveWaitTimeout) {
				// Timeout waiting for response - still proceed with archive but log warning
				s.logger.Warn("Timeout waiting for response before archiving via MCP, proceeding anyway",
					"session_id", input.ConversationID)
				// Force close the session
				reason = "archived_timeout_via_mcp"
				sessionManager.CloseSession(input.ConversationID, reason)
			}
		}
	}

	// Update metadata
	var archivedAt time.Time
	err = store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
		m.Archived = archived
		if archived {
			archivedAt = time.Now()
			m.ArchivedAt = archivedAt
			m.ArchiveReason = session.ArchiveReasonManual
		} else {
			m.ArchivedAt = time.Time{}
			m.ArchiveReason = ""
			m.AutoUnarchiveLastAttemptAt = time.Time{}
		}
	})
	if err != nil {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to update metadata: %v", err),
		}, nil
	}

	// Broadcast the archived state change to all connected WebSocket clients.
	// For archive: broadcast immediately so clients know to disconnect.
	// For unarchive: broadcast AFTER ResumeSession so the session is already in
	// sm.sessions when clients reconnect (prevents pendingResumes race).
	if archived && s.sessionManager != nil {
		s.sessionManager.BroadcastSessionArchived(input.ConversationID, true, session.ArchiveReasonManual)
	}

	// Delete all child sessions when parent is archived
	if archived && s.sessionManager != nil {
		go s.sessionManager.DeleteChildSessions(input.ConversationID)
	}

	// Handle unarchive lifecycle: restart ACP session FIRST, then broadcast
	if !archived && sessionManager != nil {
		_, err := sessionManager.ResumeSession(input.ConversationID, meta.Name, meta.WorkingDir)
		if err != nil {
			s.logger.Warn("Failed to resume ACP session after unarchive via MCP",
				"session_id", input.ConversationID,
				"error", err)
			// Don't fail the request - the session is unarchived, ACP will start when user sends a message
		} else {
			s.logger.Info("Resumed ACP session after unarchive via MCP",
				"session_id", input.ConversationID)
		}
		// Broadcast AFTER resume — session is now in sm.sessions
		if s.sessionManager != nil {
			s.sessionManager.BroadcastSessionArchived(input.ConversationID, false)
		}
	}

	action := "archived"
	if !archived {
		action = "unarchived"
	}
	s.logger.Info("Conversation "+action+" via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"archived", archived)

	output := ArchiveConversationOutput{
		Success:        true,
		ConversationID: input.ConversationID,
		Archived:       archived,
	}

	if archived && !archivedAt.IsZero() {
		output.ArchivedAt = archivedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return nil, output, nil
}

func (s *Server) handleDeleteConversation(ctx context.Context, req *mcp.CallToolRequest, input DeleteConversationInput) (*mcp.CallToolResult, DeleteConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, DeleteConversationOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, DeleteConversationOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	sessionManager := s.sessionManager
	s.mu.RUnlock()

	// Self-deletion: the agent requests deletion of its OWN conversation by passing
	// "self" or its actual conversation ID. We cannot delete synchronously here —
	// the agent is mid-turn and the ACP connection is in use — and the parent-only
	// security check below would also reject it. Instead, set an in-memory flag on
	// the calling session; the backend deletes the conversation once the turn
	// completes (see BackgroundSession.PromptWithMeta).
	if input.ConversationID == "self" || input.ConversationID == realSessionID {
		if sessionManager == nil {
			return nil, DeleteConversationOutput{Success: false, Error: "session manager not available"}, nil
		}
		bs := sessionManager.GetSession(realSessionID)
		if bs == nil {
			return nil, DeleteConversationOutput{
				Success: false,
				Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
			}, nil
		}
		bs.RequestSelfDestruct()
		s.logger.Info("Conversation marked for self-destruction via MCP",
			"session_id", realSessionID)
		return nil, DeleteConversationOutput{
			Success:        true,
			ConversationID: realSessionID,
		}, nil
	}

	if store == nil {
		return nil, DeleteConversationOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Security check: caller must be the parent of the target conversation
	if meta.ParentSessionID != realSessionID {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   "permission denied: can only delete your own child conversations",
		}, nil
	}

	// Find ALL descendants recursively BEFORE deletion so we can close their ACP processes
	allDescendantIDs, findErr := store.FindAllChildrenRecursive(input.ConversationID)
	if findErr != nil {
		s.logger.Warn("Failed to find descendants for cascade deletion via MCP",
			"child_session", input.ConversationID,
			"error", findErr)
	}

	// Gracefully stop the child and all its descendants
	if sessionManager != nil {
		reason := "deleted_by_parent_via_mcp"
		if !sessionManager.CloseSessionGracefully(input.ConversationID, reason, archiveWaitTimeout) {
			s.logger.Warn("Timeout waiting for response before deleting child via MCP, proceeding anyway",
				"parent_session", realSessionID,
				"child_session", input.ConversationID)
			sessionManager.CloseSession(input.ConversationID, "deleted_by_parent_timeout_via_mcp")
		}
		// Close ACP for all descendants
		for _, descendantID := range allDescendantIDs {
			sessionManager.CloseSession(descendantID, "ancestor_deleted_via_mcp")
		}
	}

	// Permanently delete the child conversation from disk
	// store.Delete() will cascade-delete all descendants via handleChildSessionsOnParentDelete
	err = store.Delete(input.ConversationID)
	if err != nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to delete conversation: %v", err),
		}, nil
	}

	// Broadcast deletion for the child and all descendants
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionDeleted(input.ConversationID)
		for _, descendantID := range allDescendantIDs {
			s.sessionManager.BroadcastSessionDeleted(descendantID)
		}
	}

	s.logger.Info("Child conversation permanently deleted by parent via MCP",
		"parent_session", realSessionID,
		"child_session", input.ConversationID,
		"descendants_deleted", len(allDescendantIDs))

	return nil, DeleteConversationOutput{
		Success:        true,
		ConversationID: input.ConversationID,
	}, nil
}

func (s *Server) handleConversationUpdate(ctx context.Context, req *mcp.CallToolRequest, input ConversationUpdateInput) (*mcp.CallToolResult, ConversationUpdateOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationUpdateOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ConversationUpdateOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Self-targeting: agents may pass "self" to update their OWN conversation
	// (e.g. a loop conversation disabling its own loop). Unlike delete,
	// an update only touches metadata/loop config and is safe to perform
	// synchronously, so we simply resolve the alias to the caller's real ID. This
	// keeps the tool consistent with mitto_conversation_delete, which also accepts "self".
	if input.ConversationID == "self" {
		input.ConversationID = realSessionID
	}

	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, ConversationUpdateOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	var updated []string

	// Update name if provided
	if input.Name != nil {
		if err := store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
			m.Name = *input.Name
		}); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to update name: %v", err),
			}, nil
		}
		updated = append(updated, "name")

		// Broadcast rename to all connected WebSocket clients
		if sm != nil {
			sm.BroadcastSessionRenamed(input.ConversationID, *input.Name)
		}

		s.logger.Info("Conversation renamed via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"new_name", *input.Name)
	}

	// Update beads_issue if provided
	if input.BeadsIssue != nil {
		if err := store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
			m.BeadsIssue = *input.BeadsIssue
		}); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to update beads_issue: %v", err),
			}, nil
		}
		updated = append(updated, "beads_issue")

		// Broadcast the link change so the target conversation's header
		// linked-issue button/badge updates immediately, without waiting for
		// a full session-list refresh.
		if sm != nil {
			sm.BroadcastSessionBeadsIssueUpdated(input.ConversationID, *input.BeadsIssue)
		}
	}

	// Implementation [tier: Coding]: model_tag support (mitto-41o1).
	// Switches the target conversation's active model tier via the same
	// SetConfigOption path used by the user's manual model-dropdown click, so
	// the change persists as the new baseline, records a `session_change`
	// timeline event, and broadcasts `configChanged` to observers. An empty
	// string clears any transient prompt-level model override.
	if input.ModelTag != nil {
		if sm == nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   "session manager not available for model_tag",
			}, nil
		}
		targetBS := sm.GetSession(input.ConversationID)
		if targetBS == nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("model_tag requires a running target conversation: %s", input.ConversationID),
			}, nil
		}
		resolved, applyErr := targetBS.ApplyModelTag(ctx, *input.ModelTag)
		if applyErr != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("model_tag %q on conversation %s: %v", *input.ModelTag, input.ConversationID, applyErr),
			}, nil
		}
		updated = append(updated, "model_tag")
		s.logger.Info("Model tag applied via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"model_tag", *input.ModelTag,
			"resolved_model_id", resolved)
	}

	// Update user data if provided
	if len(input.UserData) > 0 {
		// Determine merge mode (default: true)
		merge := input.UserDataMerge == nil || *input.UserDataMerge

		var finalAttrs []session.UserDataAttribute
		if merge {
			// Load existing user data and merge
			existing, err := store.GetUserData(input.ConversationID)
			if err == nil && existing != nil {
				attrMap := make(map[string]string)
				var orderedNames []string
				seen := make(map[string]bool)
				for _, a := range existing.Attributes {
					attrMap[a.Name] = a.Value
					if !seen[a.Name] {
						orderedNames = append(orderedNames, a.Name)
						seen[a.Name] = true
					}
				}
				for _, a := range input.UserData {
					attrMap[a.Name] = a.Value
					if !seen[a.Name] {
						orderedNames = append(orderedNames, a.Name)
						seen[a.Name] = true
					}
				}
				for _, name := range orderedNames {
					finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: name, Value: attrMap[name]})
				}
			} else {
				for _, a := range input.UserData {
					finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: a.Name, Value: a.Value})
				}
			}
		} else {
			// Replace mode
			for _, a := range input.UserData {
				finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: a.Name, Value: a.Value})
			}
		}

		userData := &session.UserData{Attributes: finalAttrs}

		// Validate against workspace schema. Relative filename paths are resolved
		// against the conversation's working directory.
		if sm != nil {
			schema := sm.GetUserDataSchema(meta.WorkingDir)
			if err := userData.Validate(schema, meta.WorkingDir); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("user_data validation error: %v", err),
				}, nil
			}
		}

		// Save user data
		if err := store.SetUserData(input.ConversationID, userData); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to save user data: %v", err),
			}, nil
		}
		updated = append(updated, "user_data")

		s.logger.Info("User data updated via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"attributes_count", len(finalAttrs),
			"merge", merge)
	}

	// Update loop configuration if any loop fields provided
	if input.LoopPrompt != nil || input.LoopPromptName != nil || input.LoopArguments != nil ||
		input.LoopFrequencyValue != nil || input.LoopFrequencyUnit != nil || input.LoopEnabled != nil || input.LoopFreshContext != nil || input.LoopMaxIterations != nil ||
		input.LoopTrigger != nil || input.LoopCompletionDelaySeconds != nil || input.LoopMaxDurationSeconds != nil ||
		input.LoopCondition != nil || input.LoopConditionPreset != nil || input.LoopCoalesceDuringBusy != nil ||
		input.LoopRunOnStart != nil {
		loopStore := store.Loop(input.ConversationID)

		// Mutual exclusion + name resolution for a named loop prompt. Callers may set
		// either loop_prompt (free-text) or loop_prompt_name (workspace-prompt lookup),
		// but not both non-empty in the same request. When loop_prompt_name is set to a
		// non-empty value, resolve it now so isNew can require a non-empty body and the
		// partial-update path can persist both the name and the resolved text.
		if input.LoopPrompt != nil && input.LoopPromptName != nil &&
			*input.LoopPrompt != "" && *input.LoopPromptName != "" {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   "cannot specify both 'loop_prompt' and 'loop_prompt_name' — use one or the other",
			}, nil
		}
		var resolvedLoopText, resolvedLoopName string
		// Captured BEFORE applyPromptLoopDefaultsToUpdateInput may fill LoopEnabled
		// from the resolved prompt's loop: frontmatter (mitto-ydj), so the
		// StoppedReasonDisabledByAgent bookkeeping below reflects only an explicit
		// caller-supplied loop_enabled:false, not a frontmatter-derived one.
		callerSetLoopEnabled := input.LoopEnabled != nil
		if input.LoopPromptName != nil && *input.LoopPromptName != "" {
			loopWorkingDir, err := s.resolvePromptWorkingDir(realSessionID, "")
			if err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   err.Error(),
				}, nil
			}
			p, found := s.findPromptByName(loopWorkingDir, *input.LoopPromptName)
			if !found {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("loop prompt not found: no prompt named %q is available in this workspace", *input.LoopPromptName),
				}, nil
			}
			resolvedLoopText = p.Prompt
			// Canonical name from the merged prompt list; ignores caller casing.
			resolvedLoopName = p.Name

			// Auto-apply the resolved loop prompt's loop: frontmatter block
			// (mitto-r7y): its fields fill any loop_* fields the caller did
			// not set explicitly. Callers can opt out with
			// loop_apply_prompt_defaults=false.
			if p.Loop != nil {
				applyPromptLoopDefaultsToUpdateInput(&input, p.Loop)
			}
		}

		// Check if this is an update to existing loop config or a new setup
		existing, existErr := loopStore.Get()
		isNew := existErr != nil || existing == nil

		if isNew {
			// Resolve the trigger (default schedule). onCompletion and onTasks are
			// event-driven and do not require a frequency.
			trigger := session.TriggerSchedule
			if input.LoopTrigger != nil {
				trigger = session.LoopTrigger(*input.LoopTrigger)
			}
			switch trigger {
			case "", session.TriggerSchedule, session.TriggerOnCompletion, session.TriggerOnTasks:
				// valid
			default:
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   "loop_trigger must be 'schedule', 'onCompletion', or 'onTasks'",
				}, nil
			}
			skipFrequency := trigger == session.TriggerOnCompletion || trigger == session.TriggerOnTasks

			// Creating new loop config — require a body via either a non-empty
			// loop_prompt or a resolved loop_prompt_name.
			hasFreeText := input.LoopPrompt != nil && *input.LoopPrompt != ""
			hasNamed := input.LoopPromptName != nil && *input.LoopPromptName != ""
			if !hasFreeText && !hasNamed {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   "loop_prompt or loop_prompt_name is required when creating new loop configuration",
				}, nil
			}

			var freq session.Frequency
			if !skipFrequency {
				// Schedule trigger: frequency is mandatory.
				if input.LoopFrequencyValue == nil || *input.LoopFrequencyValue < 1 {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "loop_frequency_value (>= 1) is required when creating new loop configuration",
					}, nil
				}
				if input.LoopFrequencyUnit == nil || *input.LoopFrequencyUnit == "" {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "loop_frequency_unit is required when creating new loop configuration",
					}, nil
				}

				var freqUnit session.FrequencyUnit
				switch *input.LoopFrequencyUnit {
				case "minutes":
					freqUnit = session.FrequencyMinutes
				case "hours":
					freqUnit = session.FrequencyHours
				case "days":
					freqUnit = session.FrequencyDays
				default:
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "loop_frequency_unit must be 'minutes', 'hours', or 'days'",
					}, nil
				}

				freq = session.Frequency{
					Value: *input.LoopFrequencyValue,
					Unit:  freqUnit,
				}
				if input.LoopFrequencyAt != nil {
					freq.At = *input.LoopFrequencyAt
				}
				if err := freq.Validate(); err != nil {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   fmt.Sprintf("invalid loop frequency: %v", err),
					}, nil
				}
			}

			enabled := true
			if input.LoopEnabled != nil {
				enabled = *input.LoopEnabled
			}

			freshContext := false
			if input.LoopFreshContext != nil {
				freshContext = *input.LoopFreshContext
			}

			maxIterations := 0
			if input.LoopMaxIterations != nil {
				maxIterations = *input.LoopMaxIterations
			}

			delaySeconds := 0
			if input.LoopCompletionDelaySeconds != nil {
				delaySeconds = *input.LoopCompletionDelaySeconds
			}

			maxDurationSeconds := 0
			if input.LoopMaxDurationSeconds != nil {
				maxDurationSeconds = *input.LoopMaxDurationSeconds
			}

			// Resolve the effective body: prefer the resolved named-prompt text when
			// loop_prompt_name was provided; otherwise fall back to the inline free text.
			effectiveBody := ""
			if hasNamed {
				effectiveBody = resolvedLoopText
			} else if hasFreeText {
				effectiveBody = *input.LoopPrompt
			}
			effectiveName := ""
			if hasNamed {
				effectiveName = resolvedLoopName
			}
			loop := &session.LoopPrompt{
				Prompt:             effectiveBody,
				PromptName:         effectiveName,
				Arguments:          input.LoopArguments,
				Frequency:          freq,
				Enabled:            enabled,
				FreshContext:       freshContext,
				MaxIterations:      maxIterations,
				Trigger:            trigger,
				DelaySeconds:       delaySeconds,
				MaxDurationSeconds: maxDurationSeconds,
			}
			if input.LoopCondition != nil {
				loop.Condition = *input.LoopCondition
			}
			if input.LoopConditionPreset != nil {
				loop.ConditionPreset = *input.LoopConditionPreset
			}
			if input.LoopCoalesceDuringBusy != nil {
				v := *input.LoopCoalesceDuringBusy
				loop.CoalesceDuringBusy = &v
			}
			if input.LoopRunOnStart != nil {
				v := *input.LoopRunOnStart
				loop.RunOnStart = &v
			}
			// Clamp the on-completion delay to the global floor (no-op for schedule).
			loop.ClampDelay(s.loopDelayFloor())

			if err := loopStore.Set(loop); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("failed to set loop: %v", err),
				}, nil
			}
			// A freshly-defined loop supersedes any previously-detached settings, so
			// drop the saved slot — parity with the REST make-loop path so the
			// un-loop⇄re-loop toggle stays symmetric regardless of which interface
			// (re)created the loop.
			if err := loopStore.ClearSaved(); err != nil {
				s.logger.Warn("Failed to clear stale saved loop settings on MCP set", "error", err)
			}
		} else {
			// Updating existing loop config — use partial update
			var prompt *string
			var promptName *string
			var freq *session.Frequency
			var enabled *bool

			// Prompt/name switching in a partial update:
			//   - loop_prompt_name non-empty → store resolved text as Prompt and set PromptName.
			//   - loop_prompt_name empty ("") → explicit clear of the stored name (caller likely
			//     switching to free-text); Prompt takes whatever loop_prompt provides.
			//   - loop_prompt only → free-text override, leave PromptName untouched.
			if input.LoopPromptName != nil && *input.LoopPromptName != "" {
				text := resolvedLoopText
				prompt = &text
				name := resolvedLoopName
				promptName = &name
			} else if input.LoopPromptName != nil {
				empty := ""
				promptName = &empty
				if input.LoopPrompt != nil {
					prompt = input.LoopPrompt
				}
			} else if input.LoopPrompt != nil {
				prompt = input.LoopPrompt
			}

			if input.LoopFrequencyValue != nil || input.LoopFrequencyUnit != nil || input.LoopFrequencyAt != nil {
				// Build frequency from existing + overrides
				f := existing.Frequency
				if input.LoopFrequencyValue != nil {
					f.Value = *input.LoopFrequencyValue
				}
				if input.LoopFrequencyUnit != nil {
					switch *input.LoopFrequencyUnit {
					case "minutes":
						f.Unit = session.FrequencyMinutes
					case "hours":
						f.Unit = session.FrequencyHours
					case "days":
						f.Unit = session.FrequencyDays
					default:
						return nil, ConversationUpdateOutput{
							Success: false,
							Error:   "loop_frequency_unit must be 'minutes', 'hours', or 'days'",
						}, nil
					}
				}
				if input.LoopFrequencyAt != nil {
					f.At = *input.LoopFrequencyAt
				}
				freq = &f
			}

			if input.LoopEnabled != nil {
				enabled = input.LoopEnabled
			}

			// On-completion fields (partial). Convert the trigger string to the typed pointer.
			var trigger *session.LoopTrigger
			if input.LoopTrigger != nil {
				t := session.LoopTrigger(*input.LoopTrigger)
				trigger = &t
			}
			delaySeconds := input.LoopCompletionDelaySeconds

			// Clamp the on-completion delay to the global floor on write. The effective
			// trigger is the patched value when provided, otherwise the stored one.
			if delaySeconds != nil {
				floor := s.loopDelayFloor()
				if *delaySeconds < floor {
					effTrigger := existing.Trigger
					if trigger != nil {
						effTrigger = *trigger
					}
					if effTrigger == session.TriggerOnCompletion {
						clamped := floor
						delaySeconds = &clamped
					}
				}
			}

			// LoopArguments in the input is non-nil (map) when the caller explicitly
			// sends loop_arguments; forward it as a *map[string]string so LoopStore.Update
			// distinguishes "no change" (nil) from "replace with this map" (non-nil).
			var argsPtr *map[string]string
			if input.LoopArguments != nil {
				a := input.LoopArguments
				argsPtr = &a
			}
			if err := loopStore.Update(prompt, promptName, freq, enabled, input.LoopFreshContext, input.LoopMaxIterations, trigger, delaySeconds, input.LoopMaxDurationSeconds, argsPtr, input.LoopCondition, input.LoopConditionPreset, nil, input.LoopCoalesceDuringBusy, input.LoopRunOnStart); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("failed to update loop: %v", err),
				}, nil
			}

			// Agent self-disabled loop — record it as a resumable "Paused by the agent"
			// (amber) reason so the header pill is unambiguous. Re-enabling clears it.
			// Gated on callerSetLoopEnabled (captured before the frontmatter merge
			// above) so a prompt's mode:optional/default:false does not get
			// mislabelled as an agent-initiated pause (mitto-ydj).
			if callerSetLoopEnabled && input.LoopEnabled != nil && !*input.LoopEnabled {
				if err := loopStore.MarkStopped(session.StoppedReasonDisabledByAgent); err != nil {
					s.logger.Warn("Failed to record disabledByAgent reason", "error", err)
				}
			}
		}

		updated = append(updated, "loop")

		// Broadcast the loop state change so all clients refresh live (parity with REST paths).
		if sm != nil {
			if p, getErr := loopStore.Get(); getErr == nil {
				sm.BroadcastLoopUpdated(input.ConversationID, p)
			}
		}

		// Kick off the very first run for a fresh onCompletion conversation.
		s.mu.RLock()
		runner := s.loopRunner
		s.mu.RUnlock()
		if runner != nil {
			runner.BootstrapOnCompletion(input.ConversationID)
		}

		// If the session has no title and a loop prompt was set, trigger title generation.
		// Prefer the caller-supplied loop_prompt_name (empty string means clear); otherwise
		// fall back to the stored value so the resolver can still name a loop that only has a
		// PromptName in storage.
		if input.Name == nil && meta.Name == "" && sm != nil {
			if bs := sm.GetSession(input.ConversationID); bs != nil {
				var pPrompt, pName string
				if input.LoopPrompt != nil {
					pPrompt = *input.LoopPrompt
				}
				if input.LoopPromptName != nil {
					// Use canonical name when resolution succeeded; otherwise the raw
					// input value (which is "" for an explicit clear).
					if resolvedLoopName != "" {
						pName = resolvedLoopName
					} else {
						pName = *input.LoopPromptName
					}
				}
				if p, getErr := loopStore.Get(); getErr == nil && p != nil {
					if pPrompt == "" {
						pPrompt = p.Prompt
					}
					if input.LoopPromptName == nil {
						pName = p.PromptName
					}
				}
				bs.TriggerTitleGenerationFromLoop(pPrompt, pName)
			}
		}

		s.logger.Info("Loop configuration updated via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"is_new", isNew,
			"loop_prompt_name", func() string {
				if input.LoopPromptName != nil {
					return *input.LoopPromptName
				}
				return ""
			}())
	}

	// Check if anything was actually updated
	if len(updated) == 0 {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   "no properties to update: specify at least one of 'name', 'beads_issue', 'model_tag', 'user_data', or loop fields",
		}, nil
	}

	// Build output with current state
	output := ConversationUpdateOutput{
		Success:        true,
		ConversationID: input.ConversationID,
		Updated:        updated,
	}

	// Read back current name and beads_issue
	if currentMeta, err := store.GetMetadata(input.ConversationID); err == nil {
		output.Name = currentMeta.Name
		output.BeadsIssue = currentMeta.BeadsIssue
	}

	// Read back current user data
	if currentData, err := store.GetUserData(input.ConversationID); err == nil && currentData != nil {
		for _, a := range currentData.Attributes {
			output.UserData = append(output.UserData, UserDataAttributeUpdate{Name: a.Name, Value: a.Value})
		}
	}

	// Read back current loop config
	if p, err := store.Loop(input.ConversationID).Get(); err == nil && p != nil {
		output.LoopPrompt = p.Prompt
		output.LoopPromptName = p.PromptName
		output.LoopArguments = p.Arguments
		output.LoopFrequencyValue = p.Frequency.Value
		output.LoopFrequencyUnit = string(p.Frequency.Unit)
		output.LoopFrequencyAt = p.Frequency.At
		output.LoopEnabled = p.Enabled
		output.LoopFreshContext = p.FreshContext
		output.LoopMaxIterations = p.MaxIterations
		output.LoopIterationCount = p.IterationCount
		output.LoopTrigger = string(p.EffectiveTrigger())
		output.LoopCompletionDelaySeconds = p.DelaySeconds
		output.LoopMaxDurationSeconds = p.MaxDurationSeconds
		output.LoopCondition = p.Condition
		output.LoopConditionPreset = p.ConditionPreset
		if p.CoalesceDuringBusy != nil {
			v := *p.CoalesceDuringBusy
			output.LoopCoalesceDuringBusy = &v
		}
		if p.RunOnStart != nil {
			v := *p.RunOnStart
			output.LoopRunOnStart = &v
		}
		if p.NextScheduledAt != nil {
			output.LoopNextRun = p.NextScheduledAt.Format("2006-01-02T15:04:05Z07:00")
		}
	}

	return nil, output, nil
}
