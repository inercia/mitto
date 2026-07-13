// tools_conversation_new.go: MCP tool handler for mitto_conversation_new plus
// singleton reuse helper. Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// ConversationStartInput is the input for mitto_conversation_new tool.
type ConversationStartInput struct {
	SelfID             string            `json:"self_id"`                        // YOUR session ID (the caller)
	Title              string            `json:"title,omitempty"`                // Optional title for the new conversation
	InitialPrompt      string            `json:"initial_prompt,omitempty"`       // Optional initial message to queue
	PromptName         string            `json:"prompt_name,omitempty"`          // Optional: name of a predefined prompt to use as the initial prompt (mutually exclusive with initial_prompt)
	InitialPromptDelay string            `json:"initial_prompt_delay,omitempty"` // Optional: delay initial prompt delivery (RFC 3339 timestamp or relative duration like "5m", "1h")
	Arguments          map[string]string `json:"arguments,omitempty"`            // Optional: values for Go-template .Args placeholders in the initial prompt when sent
	ACPServer          string            `json:"acp_server,omitempty"`           // Optional ACP server name (defaults to parent's server)
	BeadsIssue         string            `json:"beads_issue,omitempty"`          // Optional: link the new conversation to a beads issue ID (e.g. "mitto-123")
	Workspace          string            `json:"workspace,omitempty"`            // Optional workspace UUID for cross-workspace operations
	// Loop configuration (optional) - creates the conversation as a loop
	LoopPrompt         string `json:"loop_prompt,omitempty"`          // The prompt to send in the loop
	LoopFrequencyValue int    `json:"loop_frequency_value,omitempty"` // Number of units between sends
	LoopFrequencyUnit  string `json:"loop_frequency_unit,omitempty"`  // Time unit: "minutes", "hours", or "days"
	LoopFrequencyAt    string `json:"loop_frequency_at,omitempty"`    // Time of day HH:MM (UTC), only for "days"
	LoopEnabled        *bool  `json:"loop_enabled,omitempty"`         // Whether loop is active (defaults to true)
	LoopFreshContext   *bool  `json:"loop_fresh_context,omitempty"`   // Start each run with a fresh agent context (default false)
	LoopMaxIterations  *int   `json:"loop_max_iterations,omitempty"`  // Maximum number of scheduled runs (0 = unlimited)
	// On-completion / on-tasks trigger configuration (optional)
	LoopTrigger                string `json:"loop_trigger,omitempty"`                  // "schedule" (default), "onCompletion", or "onTasks"
	LoopCompletionDelaySeconds *int   `json:"loop_completion_delay_seconds,omitempty"` // Wait (s) after agent stops, onCompletion only; clamped to floor
	LoopMaxDurationSeconds     *int   `json:"loop_max_duration_seconds,omitempty"`     // Wall-clock cap (s) since iterating started (0 = unlimited)
	// LoopCondition is a CEL expression gating onTasks firing (only meaningful when
	// loop_trigger is "onTasks"). Empty means fire on ANY beads/task change.
	LoopCondition string `json:"loop_condition,omitempty"`
	// LoopConditionPreset is an optional UI preset id that was compiled into loop_condition.
	LoopConditionPreset string `json:"loop_condition_preset,omitempty"`
}

// ConversationStartOutput is the output for mitto_conversation_new tool.
// Embeds ConversationDetails for the newly created conversation.
type ConversationStartOutput struct {
	ConversationDetails        // Embedded conversation details
	QueuePosition       int    `json:"queue_position,omitempty"`  // Queue position if initial prompt was provided
	LoopConfigured      bool   `json:"loop_configured,omitempty"` // Whether loop was configured
	LoopNextRun         string `json:"loop_next_run,omitempty"`   // Next scheduled run (RFC3339)
	Reused              bool   `json:"reused,omitempty"`          // True when routed to an existing singleton conversation instead of creating a new one
	Error               string `json:"error,omitempty"`
}

func (s *Server) handleConversationStart(ctx context.Context, req *mcp.CallToolRequest, input ConversationStartInput) (*mcp.CallToolResult, ConversationStartOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID)
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("session store not available")
	}

	// Get the source session's metadata
	sourceMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to get source session metadata: %v", err)
	}

	// Permission check: requires can_start_conversation flag
	// This allows users to disable conversation creation via the UI toggle
	if !s.checkSessionFlag(realSessionID, session.FlagCanStartConversation) {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"the '%s' flag is not enabled for this session. Enable it in this session's Advanced Settings (gear icon) to allow creating new conversations",
			session.FlagCanStartConversation)
	}

	// Check if the source session has a parent - if so, it cannot create new sessions
	// This prevents infinite recursion where child sessions keep spawning more children
	if sourceMeta.ParentSessionID != "" {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"this session was created by another session (parent: %s) and cannot create new conversations to prevent infinite recursion",
			sourceMeta.ParentSessionID)
	}

	// Cross-workspace support: if workspace UUID is provided, resolve and potentially confirm
	var targetWorkspace *config.WorkspaceSettings
	if input.Workspace != "" {
		// Cannot specify both workspace and acp_server
		if input.ACPServer != "" {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"cannot specify both 'workspace' and 'acp_server' — workspace already determines the ACP server")
		}

		// Resolve workspace UUID
		if s.sessionManager == nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("session manager not available")
		}
		targetWorkspace = s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWorkspace == nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("workspace not found: %s", input.Workspace)
		}

		// Check if this is a cross-workspace operation (different working directory)
		if targetWorkspace.WorkingDir != sourceMeta.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, ConversationStartOutput{}, fmt.Errorf(
					"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
					session.FlagCanInteractOtherWorkspaces)
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "create a new conversation", targetWorkspace); err != nil {
				return nil, ConversationStartOutput{}, err
			}
		}
	}

	// Resolve the effective initial prompt. A named prompt (prompt_name) is
	// mutually exclusive with an inline initial_prompt: when prompt_name is set,
	// its full text is looked up from the merged prompt list (same resolution as
	// mitto_prompt_get) and used as the initial prompt. Optional 'arguments' are
	// applied to Go-template .Args placeholders when the prompt is sent.
	initialPromptText := input.InitialPrompt
	// originPromptName / promptIsSingleton drive singleton find-or-route below
	// (mitto-4mb.8). They are only set when the conversation originates from a
	// named prompt, matching the web path's OriginPromptName tracking.
	originPromptName := ""
	promptIsSingleton := false
	if input.PromptName != "" {
		if input.InitialPrompt != "" {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"cannot specify both 'prompt_name' and 'initial_prompt' — use one or the other")
		}
		promptWorkingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
		if err != nil {
			return nil, ConversationStartOutput{}, err
		}
		p, found := s.findPromptByName(promptWorkingDir, input.PromptName)
		if !found {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"prompt not found: no prompt named %q is available in this workspace", input.PromptName)
		}
		initialPromptText = p.Prompt
		originPromptName = input.PromptName
		promptIsSingleton = p.Singleton
	}

	// Check max child conversations limit
	// This prevents a single session from spawning too many children and exhausting resources.
	// Auto-children (from workspace config) are excluded from the count.
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	maxChildren := config.DefaultMaxChildConversations
	if cfg != nil && cfg.Conversations != nil {
		maxChildren = cfg.Conversations.GetMaxChildConversations()
	}
	if maxChildren > 0 { // 0 means unlimited
		currentCount, err := store.CountMCPChildSessions(realSessionID)
		if err != nil {
			s.logger.Warn("Failed to count child sessions", "session_id", realSessionID, "error", err)
			// Don't block on count errors - just log and proceed
		} else if currentCount >= maxChildren {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"maximum number of child conversations reached (%d). "+
					"This limit can be changed in Settings → Conversations → Max Child Conversations",
				maxChildren)
		}
	}

	// Check for duplicate title if title is provided
	if input.Title != "" {
		allSessions, err := store.List()
		if err != nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("failed to check for duplicate titles: %v", err)
		}
		for _, existingMeta := range allSessions {
			if existingMeta.Name == input.Title {
				errMsg := fmt.Sprintf(
					"a conversation with the title '%s' already exists (conversation_id: %s)",
					input.Title, existingMeta.SessionID)
				if initialPromptText != "" {
					errMsg += fmt.Sprintf(
						". To send a prompt to it, use 'mitto_conversation_send_prompt' with conversation_id='%s' and prompt='%s'",
						existingMeta.SessionID, truncateForError(initialPromptText, 200))
					if input.InitialPromptDelay != "" {
						errMsg += fmt.Sprintf(" and schedule_time='%s'", input.InitialPromptDelay)
					}
				}
				return nil, ConversationStartOutput{}, fmt.Errorf("%s", errMsg)
			}
		}
	}

	// Determine which ACP server and working directory to use.
	var acpServerName string
	var targetWorkingDir string
	if targetWorkspace != nil {
		// Cross-workspace: use the target workspace's server and directory.
		acpServerName = targetWorkspace.ACPServer
		targetWorkingDir = targetWorkspace.WorkingDir
	} else {
		acpServerName = sourceMeta.ACPServer // Default: inherit from parent
		targetWorkingDir = sourceMeta.WorkingDir
		if input.ACPServer != "" {
			// Validate the requested ACP server exists in config
			s.mu.RLock()
			cfg := s.config
			s.mu.RUnlock()

			if cfg == nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("server configuration not available")
			}
			if _, err := cfg.GetServer(input.ACPServer); err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf(
					"ACP server '%s' not found. Available servers: %v",
					input.ACPServer, cfg.ServerNames())
			}
			acpServerName = input.ACPServer
		}

		// Validate that a workspace exists for the folder + ACP server combination.
		// Conversations can only run in defined workspaces (folder + ACP server pairs).
		if s.sessionManager != nil {
			workspaces := s.sessionManager.GetWorkspacesForFolder(sourceMeta.WorkingDir)
			found := false
			for _, ws := range workspaces {
				if ws.ACPServer == acpServerName {
					found = true
					break
				}
			}
			if !found {
				availableServers := make([]string, 0, len(workspaces))
				for _, ws := range workspaces {
					availableServers = append(availableServers, ws.ACPServer)
				}
				return nil, ConversationStartOutput{}, fmt.Errorf(
					"no workspace configured for folder %q with ACP server %q. "+
						"Available ACP servers for this folder: %v. "+
						"Create a workspace for this folder+server pair in Settings first",
					sourceMeta.WorkingDir, acpServerName, availableServers)
			}
		}
	}

	// Singleton find-or-route (mitto-4mb.8): mirror the web path
	// (internal/web/handlers/session_create.go) — when the originating prompt is
	// declared singleton, route to an existing non-archived conversation in the
	// same working dir instead of creating a duplicate.
	if promptIsSingleton && originPromptName != "" {
		if metas, listErr := store.List(); listErr == nil {
			if existingID, ok := session.FindSingletonCandidate(metas, targetWorkingDir, originPromptName); ok {
				out, rerr := s.reuseSingletonConversation(store, existingID, initialPromptText, realSessionID, input.Arguments)
				if rerr != nil {
					return nil, ConversationStartOutput{}, rerr
				}
				s.logger.Info("Routed mitto_conversation_new to existing singleton conversation",
					"existing_session_id", existingID,
					"origin_prompt_name", originPromptName,
					"working_dir", targetWorkingDir)
				return nil, out, nil
			}
		}
	}

	// Create new session ID using the standard timestamp format
	// This ensures compatibility with IsValidSessionID validation in the web layer
	newSessionID := session.GenerateSessionID()

	// Create the new session metadata
	// NOTE: Recursion is prevented by the ParentSessionID check above — children
	// with a parent cannot create new conversations. When the parent is deleted,
	// the child becomes an orphan (ParentSessionID is cleared) and can then create
	// new conversations since it inherits the parent's flags including can_start_conversation.

	// Inherit parent's advanced settings so orphaned children retain the same flags.
	childSettings := make(map[string]bool)
	for k, v := range sourceMeta.AdvancedSettings {
		childSettings[k] = v
	}

	newMeta := session.Metadata{
		SessionID:        newSessionID,
		Name:             input.Title,
		ACPServer:        acpServerName,
		WorkingDir:       targetWorkingDir,
		ParentSessionID:  realSessionID,          // Mark this session as a child
		ChildOrigin:      session.ChildOriginMCP, // Created via MCP tool
		AdvancedSettings: childSettings,
		BeadsIssue:       input.BeadsIssue,
		OriginPromptName: originPromptName, // Track originating prompt for singleton find-or-route
	}

	// Create the session
	if err := store.Create(newMeta); err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to create session: %v", err)
	}

	s.logger.Info("New conversation created via MCP",
		"new_session_id", newSessionID,
		"parent_session_id", realSessionID,
		"acp_server", acpServerName,
		"working_dir", targetWorkingDir,
		"title", input.Title)

	// Re-fetch metadata to get timestamps set by Create()
	createdMeta, err := store.GetMetadata(newSessionID)
	if err != nil {
		// Use the newMeta we have if re-fetch fails
		createdMeta = newMeta
	}

	// Start the ACP process for the new session.
	// store.Create() only writes metadata to disk - we need to start a BackgroundSession
	// with an actual ACP process so prompts can be executed.
	var bs BackgroundSession
	if s.sessionManager != nil {
		var resumeErr error
		bs, resumeErr = s.sessionManager.ResumeSession(newSessionID, input.Title, targetWorkingDir)
		if resumeErr != nil {
			s.logger.Error("Failed to start ACP for new conversation",
				"session_id", newSessionID,
				"error", resumeErr)
			// Session was created but ACP failed to start - clean up isn't needed
			// since the session can be resumed later, but log the error
		}
	}

	// Broadcast session creation to all global events clients
	// This ensures the sidebar updates immediately when creating via MCP
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionCreated(
			newSessionID,
			input.Title,
			acpServerName,
			targetWorkingDir,
			realSessionID,                  // parent_session_id
			string(session.ChildOriginMCP), // child_origin
		)
	}

	// If loop configuration provided, set it up
	var loopConfigured bool
	var loopNextRun string
	if input.LoopPrompt != "" {
		// Resolve the trigger (default schedule). onCompletion and onTasks are
		// event-driven and do not require a frequency.
		trigger := session.LoopTrigger(input.LoopTrigger)
		switch trigger {
		case "", session.TriggerSchedule, session.TriggerOnCompletion, session.TriggerOnTasks:
			// valid
		default:
			return nil, ConversationStartOutput{}, fmt.Errorf("loop_trigger must be 'schedule', 'onCompletion', or 'onTasks'")
		}
		skipFrequency := trigger == session.TriggerOnCompletion || trigger == session.TriggerOnTasks

		var freq session.Frequency
		if !skipFrequency {
			// Schedule trigger: frequency is required.
			if input.LoopFrequencyValue < 1 {
				return nil, ConversationStartOutput{}, fmt.Errorf("loop_frequency_value must be >= 1 when loop_prompt is provided")
			}

			var freqUnit session.FrequencyUnit
			switch input.LoopFrequencyUnit {
			case "minutes":
				freqUnit = session.FrequencyMinutes
			case "hours":
				freqUnit = session.FrequencyHours
			case "days":
				freqUnit = session.FrequencyDays
			default:
				return nil, ConversationStartOutput{}, fmt.Errorf("loop_frequency_unit must be 'minutes', 'hours', or 'days'")
			}

			freq = session.Frequency{
				Value: input.LoopFrequencyValue,
				Unit:  freqUnit,
				At:    input.LoopFrequencyAt,
			}
			if err := freq.Validate(); err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("invalid loop frequency: %v", err)
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

		loop := &session.LoopPrompt{
			Prompt:             input.LoopPrompt,
			Frequency:          freq,
			Enabled:            enabled,
			FreshContext:       freshContext,
			MaxIterations:      maxIterations,
			Trigger:            trigger,
			DelaySeconds:       delaySeconds,
			MaxDurationSeconds: maxDurationSeconds,
			Condition:          input.LoopCondition,
			ConditionPreset:    input.LoopConditionPreset,
		}
		// Clamp the on-completion delay to the global floor (no-op for schedule).
		loop.ClampDelay(s.loopDelayFloor())

		loopStore := store.Loop(newSessionID)
		if err := loopStore.Set(loop); err != nil {
			s.logger.Error("Failed to set loop on new conversation",
				"session_id", newSessionID,
				"error", err)
			// Don't fail the whole creation - just log the error
		} else {
			loopConfigured = true
			updated, err := loopStore.Get()
			if err == nil && updated.NextScheduledAt != nil {
				loopNextRun = updated.NextScheduledAt.Format("2006-01-02T15:04:05Z07:00")
			}
			s.logger.Info("Loop prompt configured on new conversation",
				"session_id", newSessionID,
				"loop_prompt", input.LoopPrompt,
				"frequency_value", input.LoopFrequencyValue,
				"frequency_unit", input.LoopFrequencyUnit,
				"enabled", enabled)

			// Kick off the very first run for a fresh onCompletion conversation.
			s.mu.RLock()
			runner := s.loopRunner
			s.mu.RUnlock()
			if runner != nil {
				runner.BootstrapOnCompletion(newSessionID)
			}
		}
	}

	// If no explicit title was provided and loop was configured, trigger title
	// generation from the loop prompt text so the conversation has a name right away.
	// ConversationStartInput has no LoopPromptName field, so prompt name is passed as "".
	if input.Title == "" && loopConfigured && bs != nil {
		bs.TriggerTitleGenerationFromLoop(input.LoopPrompt, "")
	}

	// Build unified conversation details
	output := ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(createdMeta, store.SessionDir(newSessionID)),
		LoopConfigured:      loopConfigured,
		LoopNextRun:         loopNextRun,
	}
	// Update runtime status to reflect the running ACP session
	if bs != nil {
		output.IsRunning = true
	}

	// Validate initial_prompt_delay requires an initial prompt (inline or named)
	if input.InitialPromptDelay != "" && initialPromptText == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf("initial_prompt_delay requires initial_prompt or prompt_name to be set")
	}

	// If initial prompt provided, add it to the queue
	if initialPromptText != "" {
		// Reject a free-text initial prompt with broken Go-template syntax up front
		// (mitto-e7u), so it is not enqueued and later silently delivered raw. Named
		// prompts (resolved to text above) were validated at save time.
		if input.PromptName == "" {
			if err := config.ValidatePromptTemplateSyntax("prompt", initialPromptText); err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("invalid initial prompt template: %w", err)
			}
		}

		// Parse optional initial prompt delay
		var scheduledTime *time.Time
		if input.InitialPromptDelay != "" {
			t, err := session.ParseScheduleTime(input.InitialPromptDelay)
			if err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("invalid initial_prompt_delay: %v", err)
			}
			scheduledTime = &t
		}

		queue := store.Queue(newSessionID)
		_, err := queue.AddWithOrigin(initialPromptText, nil, nil, realSessionID, scheduledTime, 0, input.Arguments, "", session.QueueOriginAgent)
		if err != nil {
			s.logger.Warn("Failed to queue initial prompt",
				"session_id", newSessionID,
				"error", err)
		} else {
			queueLen, _ := queue.Len()
			output.QueuePosition = queueLen

			// Try to process the queued message immediately if agent is idle (skip if scheduled for later)
			if bs != nil && scheduledTime == nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}

	return nil, output, nil
}

// reuseSingletonConversation routes an mitto_conversation_new call for a
// singleton prompt to an existing non-archived conversation instead of creating
// a duplicate, mirroring the web reuseSingletonSession behavior. When the
// existing conversation is idle (not prompting and an empty queue) the prompt is
// re-seeded so re-invoking a menu prompt re-runs it; when busy it is left
// untouched (focus-only). The returned output carries reused=true.
func (s *Server) reuseSingletonConversation(store *session.Store, existingID, initialPromptText, clientID string, arguments map[string]string) (ConversationStartOutput, error) {
	meta, err := store.GetMetadata(existingID)
	if err != nil {
		return ConversationStartOutput{}, fmt.Errorf("failed to load existing singleton conversation: %v", err)
	}

	output := ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(meta, store.SessionDir(existingID)),
		Reused:              true,
	}

	if initialPromptText == "" {
		return output, nil
	}

	queue := store.Queue(existingID)
	qlen, _ := queue.Len()
	var bs BackgroundSession
	if s.sessionManager != nil {
		bs = s.sessionManager.GetSession(existingID)
	}
	idle := qlen == 0
	if bs != nil {
		idle = !bs.IsPrompting() && qlen == 0
	}
	if !idle {
		return output, nil
	}

	if _, addErr := queue.AddWithOrigin(initialPromptText, nil, nil, clientID, nil, 0, arguments, "", session.QueueOriginAgent); addErr != nil {
		s.logger.Warn("Failed to re-seed reused singleton conversation",
			"session_id", existingID, "error", addErr)
		return output, nil
	}
	if newLen, lenErr := queue.Len(); lenErr == nil {
		output.QueuePosition = newLen
	}
	if bs != nil {
		go bs.TryProcessQueuedMessage()
	}
	return output, nil
}
