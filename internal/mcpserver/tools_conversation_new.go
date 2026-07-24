// tools_conversation_new.go: MCP tool handler for mitto_conversation_new plus
// singleton reuse helper. Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
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
	// ModelTag, when non-empty, pins the new conversation's active model from the
	// first turn to the first available model whose profile carries this tag (see
	// config.ProfilesByTag + SelectPreferredModel). Applied through the same
	// SetConfigOption path as the user's manual model-dropdown click, so the
	// change persists as the new baseline. Requires the started agent to have
	// advertised a model catalog; if no available model matches, spawn fails
	// loudly so callers can retry or spawn without pinning.
	ModelTag string `json:"model_tag,omitempty"`
	// Loop configuration (optional) - creates the conversation as a loop
	LoopPrompt string `json:"loop_prompt,omitempty"` // The prompt to send in the loop
	// LoopPromptName is the name of a predefined workspace prompt to use as the loop body
	// (mutually exclusive with loop_prompt). Resolved the same way as prompt_name for the
	// initial prompt: case-insensitive lookup against the merged prompt list.
	LoopPromptName string `json:"loop_prompt_name,omitempty"`
	// LoopArguments fills Go-template .Args placeholders in the resolved loop prompt
	// at execution time. Pairs with loop_prompt_name (or with an inline loop_prompt
	// containing placeholders).
	LoopArguments      map[string]string `json:"loop_arguments,omitempty"`
	LoopFrequencyValue int               `json:"loop_frequency_value,omitempty"` // Number of units between sends
	LoopFrequencyUnit  string            `json:"loop_frequency_unit,omitempty"`  // Time unit: "minutes", "hours", or "days"
	LoopFrequencyAt    string            `json:"loop_frequency_at,omitempty"`    // Time of day HH:MM (UTC), only for "days"
	LoopEnabled        *bool             `json:"loop_enabled,omitempty"`         // Whether loop is active (defaults to true)
	LoopFreshContext   *bool             `json:"loop_fresh_context,omitempty"`   // Start each run with a fresh agent context (default false)
	LoopMaxIterations  *int              `json:"loop_max_iterations,omitempty"`  // Maximum number of scheduled runs (0 = unlimited)
	// On-completion / on-tasks trigger configuration (optional)
	LoopTrigger                string `json:"loop_trigger,omitempty"`                  // "schedule" (default), "onCompletion", or "onTasks"
	LoopCompletionDelaySeconds *int   `json:"loop_completion_delay_seconds,omitempty"` // Wait (s) after agent stops, onCompletion only; clamped to floor
	LoopMaxDurationSeconds     *int   `json:"loop_max_duration_seconds,omitempty"`     // Wall-clock cap (s) since iterating started (0 = unlimited)
	// LoopCondition is a CEL expression gating onTasks firing (only meaningful when
	// loop_trigger is "onTasks"). Empty means fire on ANY beads/task change.
	LoopCondition string `json:"loop_condition,omitempty"`
	// LoopConditionPreset is an optional UI preset id that was compiled into loop_condition.
	LoopConditionPreset string `json:"loop_condition_preset,omitempty"`
	// LoopCoalesceDuringBusy controls how the onTasks trigger handles beads changes
	// that arrive while the loop's subtree is busy. Nil or true = silently absorb
	// (default). False = fire once more with the accumulated delta after quiescence.
	LoopCoalesceDuringBusy *bool `json:"loop_coalesce_during_busy,omitempty"`
	// LoopRunOnStart, when *true, causes the loop to fire exactly once shortly
	// after Mitto boots (with an anti-flap window guarding against a recent
	// run). Nil or false = do not fire on start (default).
	LoopRunOnStart *bool `json:"loop_run_on_start,omitempty"`
	// LoopApplyPromptDefaults controls the mitto-r7y auto-apply of a seeded
	// prompt's loop: frontmatter block. When prompt_name resolves to a prompt
	// carrying a loop: block, its fields fill any loop_* fields the caller did
	// not set explicitly, and — if the caller passed no loop_prompt /
	// loop_prompt_name — the seed prompt itself becomes the loop body
	// (self-referential loop). Set to false to skip this merge entirely.
	// Default (nil) is "apply when a loop: block is present".
	LoopApplyPromptDefaults *bool `json:"loop_apply_prompt_defaults,omitempty"`
}

// ConversationStartOutput is the output for mitto_conversation_new tool.
// Embeds ConversationDetails for the newly created conversation.
type ConversationStartOutput struct {
	ConversationDetails        // Embedded conversation details
	QueuePosition       int    `json:"queue_position,omitempty"`  // Queue position if initial prompt was provided
	LoopConfigured      bool   `json:"loop_configured,omitempty"` // Whether loop was configured
	LoopNextRun         string `json:"loop_next_run,omitempty"`   // Next scheduled run (RFC3339)
	Reused              bool   `json:"reused,omitempty"`          // True when routed to an existing singleton conversation instead of creating a new one
	Coalesced           bool   `json:"coalesced,omitempty"`       // True when target.reuseCoalesce suppressed a duplicate in-flight/queued dispatch (mitto-djs1)
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
	// originPromptName / promptIsSingleton / promptReuseIssue drive the
	// find-or-route blocks below (mitto-4mb.8, mitto-bx40). They are only set
	// when the conversation originates from a named prompt, matching the web
	// path's OriginPromptName tracking.
	originPromptName := ""
	promptIsSingleton := false
	promptReuseIssue := false
	promptReuseTitle := false
	promptTargetTitle := ""
	// promptReuseCoalesce: when true, a duplicate (same PromptName + Arguments)
	// dispatch onto an already-in-flight or queued conversation is a no-op
	// (mitto-djs1). Only consulted after a reuse hit resolves to existingID.
	promptReuseCoalesce := false
	if input.PromptName != "" {
		// mitto-kt6: prompt_name wins when both are supplied. Agents forced by
		// strict JSON schemas often fill 'initial_prompt' with a placeholder to
		// satisfy the field even when they only intend a named dispatch;
		// delivering that placeholder would silently override the resolved
		// prompt body.
		if strings.TrimSpace(input.InitialPrompt) != "" {
			s.logger.Info("Both 'initial_prompt' and 'prompt_name' provided; prompt_name wins, ignoring 'initial_prompt'",
				"source_session", realSessionID,
				"prompt_name", input.PromptName)
			input.InitialPrompt = ""
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
		if p.Target != nil {
			promptTargetTitle = p.Target.Title
			if p.Target.Reuse != nil {
				promptReuseIssue = p.Target.Reuse.Issue
				promptReuseTitle = p.Target.Reuse.Title
				if p.Target.Reuse.Coalesce != nil {
					promptReuseCoalesce = *p.Target.Reuse.Coalesce
				}
			}
			// Render target.title as a Go text/template (mitto-5qbo). Fast-path
			// passthrough for literal titles (no "{{"). Fail-closed on render or
			// empty-output error — boundary rejection before the session is
			// created, same shape as the prompt-not-found rejection above.
			if promptTargetTitle != "" {
				ctx := prompts.PromptTargetContext{Args: input.Arguments}
				ctx.Session.BeadsIssue = input.BeadsIssue
				ctx.Workspace.Folder = promptWorkingDir
				rendered, rerr := prompts.RenderPromptTargetTitle(p.Name, promptTargetTitle, ctx)
				if rerr != nil {
					return nil, ConversationStartOutput{}, rerr
				}
				promptTargetTitle = rendered
			}
		}
		// reuseTitle adopts target.title as the conversation's Name so a
		// subsequent scan matches it. When the caller supplied a different
		// Title, log the override (target.title is the canonical lookup key).
		if promptReuseTitle && promptTargetTitle != "" {
			if input.Title != "" && input.Title != promptTargetTitle {
				s.logger.Debug("overriding mitto_conversation_new title with target.title from prompt frontmatter",
					"prompt", input.PromptName, "request_title", input.Title, "target_title", promptTargetTitle)
			}
			input.Title = promptTargetTitle
		} else if !promptReuseTitle && promptTargetTitle != "" && input.Title == "" {
			// Plain target.title (no reuseTitle): adopt as default Title
			// only when the caller did not supply one. Caller override
			// wins; find-or-route is NOT invoked (reuseTitle is opt-in).
			input.Title = promptTargetTitle
		}

		// Auto-apply the seeded prompt's loop: frontmatter block (mitto-r7y):
		// when the resolved prompt carries a loop: block, its fields fill any
		// loop_* fields the caller did not set explicitly, and the seed prompt
		// itself becomes the loop body (self-referential loop) unless the
		// caller passed loop_prompt or loop_prompt_name. Callers can opt out
		// with loop_apply_prompt_defaults=false.
		if p.Loop != nil {
			applyPromptLoopDefaultsToStartInput(&input, p.Loop, p.Name)
		}
	}

	// Resolve the loop body. Like the initial prompt, a named loop prompt
	// (loop_prompt_name) is mutually exclusive with a free-text loop_prompt: when
	// loop_prompt_name is set, its full text is looked up from the merged prompt list
	// and stored as the loop body via LoopPrompt.PromptName so it re-resolves on every
	// run. Optional loop_arguments fills Go-template .Args placeholders at execution time.
	// Errors here (unknown prompt, both set) are boundary rejections raised before the
	// session is created, so no stub conversation is left behind.
	loopPromptText := input.LoopPrompt
	loopPromptName := ""
	if input.LoopPromptName != "" {
		if input.LoopPrompt != "" {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"cannot specify both 'loop_prompt' and 'loop_prompt_name' — use one or the other")
		}
		loopWorkingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
		if err != nil {
			return nil, ConversationStartOutput{}, err
		}
		p, found := s.findPromptByName(loopWorkingDir, input.LoopPromptName)
		if !found {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"loop prompt not found: no prompt named %q is available in this workspace", input.LoopPromptName)
		}
		loopPromptText = p.Prompt
		// Store the canonical name from the merged prompt so downstream consumers see
		// a stable identifier regardless of the caller's case.
		loopPromptName = p.Name
	}

	// Reject a suspiciously short, placeholder-shaped free-text initial_prompt
	// when the conversation is being created as a loop (mitto-dj9). When an
	// orchestrator LLM composes multiple mitto_conversation_new calls in one
	// turn, it can short-circuit the repeated initial_prompt to a self-reference
	// like "[Same driver body]"; without this guard the server enqueues that
	// literal string as the loop child's seed and the loop re-fires an
	// unactionable prompt on every tick. Named prompts are server-expanded and
	// cannot be truncated this way, so the guard applies only to the free-text
	// path (PromptName == "").
	if input.PromptName == "" && (input.LoopPrompt != "" || input.LoopPromptName != "" || input.LoopTrigger != "") {
		if reason, ok := looksLikePlaceholderLoopSeed(initialPromptText); ok {
			s.logger.Warn("Rejecting suspected placeholder loop-driver seed",
				"session_id", realSessionID,
				"initial_prompt", initialPromptText,
				"reason", reason)
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"suspected placeholder loop-driver seed %q: pass the full initial_prompt body or use prompt_name for server-side expansion (%s)",
				initialPromptText, reason)
		}
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

	// Check for duplicate title if title is provided.
	// Skipped when reuseTitle is active: that ladder step below handles
	// reuse (funnel into the existing conversation) instead of rejecting.
	if input.Title != "" && !promptReuseTitle {
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

	// reuseIssue find-or-route (mitto-bx40): mirror the web path
	// (internal/web/handlers/session_create.go). When the call carries
	// beads_issue AND the originating prompt declares target.reuseIssue, the
	// per-issue reuse decision is authoritative: funnel into an existing
	// non-archived conversation with the same beads_issue in the same
	// working_dir instead of creating a duplicate. If it misses, the singleton
	// fallback is SKIPPED for this call — otherwise two distinct beads issues
	// driven by the same singleton prompt would collapse into one conversation.
	// The per-(workingDir, beadsIssue) lock is held until this handler returns
	// so the scan + create/persist is atomic relative to concurrent creates for
	// the same issue.
	reuseIssueEvaluated := false
	if promptReuseIssue && originPromptName != "" && input.BeadsIssue != "" {
		reuseIssueEvaluated = true
		key := targetWorkingDir + "\x00" + input.BeadsIssue
		unlock := s.lockReuseIssue(key)
		defer unlock()

		if metas, listErr := store.List(); listErr == nil {
			if existingID, ok := session.FindConversationByBeadsIssue(metas, targetWorkingDir, input.BeadsIssue); ok {
				if out, coalesced := s.maybeCoalesceMCP(store, existingID, originPromptName, promptReuseCoalesce, input.Arguments); coalesced {
					s.logger.Info("Coalesced mitto_conversation_new duplicate dispatch by beads_issue",
						"existing_session_id", existingID,
						"origin_prompt_name", originPromptName,
						"beads_issue", input.BeadsIssue,
						"working_dir", targetWorkingDir)
					return nil, out, nil
				}
				out, rerr := s.reuseSingletonConversation(store, existingID, initialPromptText, realSessionID, input.Arguments)
				if rerr != nil {
					return nil, ConversationStartOutput{}, rerr
				}
				s.logger.Info("Routed mitto_conversation_new to existing conversation by beads_issue",
					"existing_session_id", existingID,
					"origin_prompt_name", originPromptName,
					"beads_issue", input.BeadsIssue,
					"working_dir", targetWorkingDir)
				return nil, out, nil
			}
		}
		// No candidate — fall through to normal creation. Lock stays held via
		// defer so the BeadsIssue+OriginPromptName persistence below completes
		// before another concurrent waiter's scan runs and misses this new one.
	}

	// reuseTitle find-or-route: mirror the web path
	// (internal/web/handlers/session_create.go). When the originating prompt
	// declares target.reuseTitle (with a non-empty target.title, enforced at
	// load time by ValidatePromptTarget), funnel dispatches into an existing
	// non-archived conversation in the same working_dir whose Name matches
	// the declared title. If no candidate exists, fall through to normal
	// creation; input.Title has already been set to target.title above so
	// the created conversation will match a subsequent scan. Skip singleton
	// fallback on both hit and miss — title reuse is authoritative for
	// this prompt.
	reuseTitleEvaluated := false
	if !reuseIssueEvaluated && promptReuseTitle && promptTargetTitle != "" {
		reuseTitleEvaluated = true
		key := targetWorkingDir + "\x00" + promptTargetTitle
		unlock := s.lockReuseTitle(key)
		defer unlock()

		if metas, listErr := store.List(); listErr == nil {
			if existingID, ok := session.FindConversationByTitle(metas, targetWorkingDir, promptTargetTitle); ok {
				if out, coalesced := s.maybeCoalesceMCP(store, existingID, originPromptName, promptReuseCoalesce, input.Arguments); coalesced {
					s.logger.Info("Coalesced mitto_conversation_new duplicate dispatch by title",
						"existing_session_id", existingID,
						"origin_prompt_name", originPromptName,
						"target_title", promptTargetTitle,
						"working_dir", targetWorkingDir)
					return nil, out, nil
				}
				out, rerr := s.reuseSingletonConversation(store, existingID, initialPromptText, realSessionID, input.Arguments)
				if rerr != nil {
					return nil, ConversationStartOutput{}, rerr
				}
				s.logger.Info("Routed mitto_conversation_new to existing conversation by title",
					"existing_session_id", existingID,
					"origin_prompt_name", originPromptName,
					"target_title", promptTargetTitle,
					"working_dir", targetWorkingDir)
				return nil, out, nil
			}
		}
		// No candidate — fall through to normal creation. Lock stays held via
		// defer so the create/persist below completes before another concurrent
		// waiter's scan runs and misses this new one.
	}

	// Singleton find-or-route (mitto-4mb.8): mirror the web path
	// (internal/web/handlers/session_create.go) — when the originating prompt is
	// declared singleton, route to an existing non-archived conversation in the
	// same working dir instead of creating a duplicate.
	//
	// Skipped when reuseIssue or reuseTitle already evaluated (and missed) for
	// this call: singleton would incorrectly collapse distinct instances into
	// one conversation.
	if !reuseIssueEvaluated && !reuseTitleEvaluated && promptIsSingleton && originPromptName != "" {
		if metas, listErr := store.List(); listErr == nil {
			if existingID, ok := session.FindSingletonCandidate(metas, targetWorkingDir, originPromptName); ok {
				if out, coalesced := s.maybeCoalesceMCP(store, existingID, originPromptName, promptReuseCoalesce, input.Arguments); coalesced {
					s.logger.Info("Coalesced mitto_conversation_new duplicate singleton dispatch",
						"existing_session_id", existingID,
						"origin_prompt_name", originPromptName,
						"working_dir", targetWorkingDir)
					return nil, out, nil
				}
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

	// Note: target.suppressAutoChildren (mitto-nlx) is intentionally NOT
	// consulted on this MCP path. mitto_conversation_new always sets
	// ParentSessionID (see below) and delegates process start to
	// ResumeSession, not CreateSessionWithWorkspace — so the workspace-level
	// auto_children spawn never runs here today. If MCP ever grows a
	// top-level create path, mirror the resolveSuppressAutoChildrenByPromptName
	// wiring used by the REST handler (internal/web/handlers/session_create.go).
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

	// If loop configuration provided, set it up. Either an inline loop_prompt or a
	// resolved loop_prompt_name (loopPromptText populated above) triggers loop setup.
	var loopConfigured bool
	var loopNextRun string
	if loopPromptText != "" {
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
				return nil, ConversationStartOutput{}, fmt.Errorf("loop_frequency_value must be >= 1 when loop_prompt or loop_prompt_name is provided")
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

		// Store the raw name (and free-text body when present) so the runner can
		// re-resolve the workspace prompt on every tick and apply loop_arguments to
		// its .Args placeholders.
		loop := &session.LoopPrompt{
			Prompt:             loopPromptText,
			PromptName:         loopPromptName,
			Arguments:          input.LoopArguments,
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
				"loop_prompt_name", loopPromptName,
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

	// Implementation [tier: Coding]: model_tag pin (mitto-41o1).
	// After loop configuration (so any loop-driven initial-model preference does
	// not race with the explicit spawn-time pin) apply the caller-requested
	// model_tag via the same SetConfigOption path used by the user's manual
	// model-dropdown click. Failures here are loud: the conversation is still
	// created and persisted, but the tool call returns an error so orchestrators
	// see that pinning failed and can decide to retry or spawn without pinning.
	if input.ModelTag != "" && bs != nil {
		resolved, applyErr := bs.ApplyModelTag(ctx, input.ModelTag)
		if applyErr != nil {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"model_tag %q on new session %s: %w",
				input.ModelTag, newSessionID, applyErr)
		}
		s.logger.Info("Model tag applied to new conversation via MCP",
			"session_id", newSessionID,
			"model_tag", input.ModelTag,
			"resolved_model_id", resolved)
	}

	// If no explicit title was provided and loop was configured, trigger title
	// generation from the loop prompt (free-text body and/or workspace prompt name)
	// so the conversation has a name right away.
	if input.Title == "" && loopConfigured && bs != nil {
		bs.TriggerTitleGenerationFromLoop(loopPromptText, loopPromptName)
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
// maybeCoalesceMCP returns a coalesced output when reuseCoalesce is enabled
// and an identical dispatch (same PromptName + Arguments) is already in flight
// or queued on the target. Callers MUST invoke this INSIDE the per-key reuse
// lock (lockReuseIssue / lockReuseTitle) so the check-then-skip is atomic
// against concurrent dupes. Returns (out, false) with a zero-value out when
// no coalesce fires — callers then fall through to reuseSingletonConversation
// as before (mitto-djs1). Mirrors handlers.maybeCoalesce in the REST path.
func (s *Server) maybeCoalesceMCP(store *session.Store, existingID, promptName string, reuseCoalesce bool, arguments map[string]string) (ConversationStartOutput, bool) {
	if !reuseCoalesce || promptName == "" {
		return ConversationStartOutput{}, false
	}
	var bs BackgroundSession
	if s.sessionManager != nil {
		bs = s.sessionManager.GetSession(existingID)
	}
	queue := store.Queue(existingID)
	if !promptMatchesActiveOrQueuedMCP(bs, queue, promptName, arguments) {
		return ConversationStartOutput{}, false
	}
	meta, err := store.GetMetadata(existingID)
	if err != nil {
		// Metadata read failed; abort the coalesce and fall through to the
		// normal reuse path so the caller sees a real error there instead of
		// a silent no-op with an empty ConversationDetails.
		s.logger.Warn("Coalesce metadata read failed; falling through", "session_id", existingID, "error", err)
		return ConversationStartOutput{}, false
	}
	return ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(meta, store.SessionDir(existingID)),
		Reused:              true,
		Coalesced:           true,
	}, true
}

// promptMatchesActiveOrQueuedMCP is the mcpserver-local mirror of
// conversation.PromptMatchesActiveOrQueued. Duplicated to keep mcpserver
// free of any internal/conversation import (see the local BackgroundSession
// interface). Same match semantics: name + Arguments deep-equal, nil map ==
// empty map, free-text (empty promptName) never coalesces.
func promptMatchesActiveOrQueuedMCP(bs BackgroundSession, q *session.Queue, promptName string, arguments map[string]string) bool {
	if promptName == "" {
		return false
	}
	if bs != nil {
		if activeName, activeArgs, ok := bs.ActivePromptDispatch(); ok {
			if activeName == promptName && argsDeepEqualMCP(activeArgs, arguments) {
				return true
			}
		}
	}
	if q != nil {
		msgs, err := q.List()
		if err != nil {
			return false
		}
		for i := range msgs {
			if msgs[i].PromptName == "" {
				continue
			}
			if msgs[i].PromptName == promptName && argsDeepEqualMCP(msgs[i].Arguments, arguments) {
				return true
			}
		}
	}
	return false
}

// argsDeepEqualMCP compares two argument maps for equality treating nil and
// empty maps as equivalent. Kept package-private (mirrors the conversation
// package's argsDeepEqual).
func argsDeepEqualMCP(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || vb != va {
			return false
		}
	}
	return true
}

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

// placeholderLoopSeedPattern matches a bracketed self-reference body like
// "[Same driver body]" or "[see above]" — the shape LLMs collapse repeated
// prompt arguments to when generating multiple tool calls in one turn.
// The trigger words cover the common phrasings observed in mitto-dj9.
var placeholderLoopSeedPattern = regexp.MustCompile(`(?is)^\s*\[[^\]]*(same|see|above|prior|driver|body|prompt)[^\]]*\]\s*$`)

// placeholderLoopSeedMaxLen is the upper bound on total length (bytes, trimmed)
// below which a free-text loop-driver initial_prompt is considered
// suspicious. A real driver body is many KB; anything under this ceiling that
// also matches the bracketed self-reference shape is treated as a shortcut,
// not a real seed.
const placeholderLoopSeedMaxLen = 512

// looksLikePlaceholderLoopSeed reports whether text is a suspiciously short,
// bracketed self-reference string of the form "[Same driver body]" — the
// shape observed in mitto-dj9 when an orchestrator LLM truncated a repeated
// initial_prompt argument. Returns a short human-readable reason for the
// error message when it matches, empty string otherwise. Intended to gate
// mitto_conversation_new calls that create a loop child with a free-text
// initial_prompt.
func looksLikePlaceholderLoopSeed(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	if len(trimmed) >= placeholderLoopSeedMaxLen {
		return "", false
	}
	if !placeholderLoopSeedPattern.MatchString(trimmed) {
		return "", false
	}
	return fmt.Sprintf("length=%d, matches bracketed self-reference pattern", len(trimmed)), true
}
