// tools_wait.go: MCP tool handler for mitto_conversation_wait plus the shared
// startProgressHeartbeat helper used by other long-blocking tool handlers.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	beadswatcher "github.com/inercia/mitto/internal/beads/watcher"
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

// defaultBeadsWaitTimeout is the default timeout for the
// beads_issues_reached_state branch. It mirrors the per-driver maxDuration in
// the L1 loop orchestrator prompt (4h).
const defaultBeadsWaitTimeout = 4 * time.Hour

// beadsWaitPollInterval is the polling cadence used when no BeadsWatcher is
// wired. Long by design — the watcher is the fast path.
const beadsWaitPollInterval = 30 * time.Second

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

// waitConditionBeadsIssuesReachedState is the "what" value for waiting until
// one or more bd issues reach a target status. See beads_issues,
// beads_target_state, and beads_match on ConversationWaitInput.
const waitConditionBeadsIssuesReachedState = "beads_issues_reached_state"

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
	if input.What != waitConditionAgentResponded && input.What != waitConditionBeadsIssuesReachedState {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("unsupported wait condition: %q (supported: %q, %q)",
				input.What, waitConditionAgentResponded, waitConditionBeadsIssuesReachedState),
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

	// Dispatch to the beads-state branch. It has its own workspace/target
	// resolution and does not require the target session to be running,
	// so it runs before the agent_responded branch's target lookup.
	if input.What == waitConditionBeadsIssuesReachedState {
		return s.handleBeadsIssuesReachedState(ctx, req, input, realSessionID)
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

// =============================================================================
// Beads Issues Reached State
// =============================================================================

// beadsMatchAny is the "any" aggregation strategy (default is "all").
const beadsMatchAny = "any"

// beadsWaitSubscriber implements watcher.BeadsSubscriber and forwards each
// debounced change event to a wake channel. The channel is buffered (1) so a
// burst that arrives while the handler is re-evaluating is not lost.
type beadsWaitSubscriber struct {
	wake chan struct{}
}

// OnBeadsChanged wakes the wait loop. Coalesced: if a wake is already
// pending, the extra event is dropped.
func (b *beadsWaitSubscriber) OnBeadsChanged(_ beadswatcher.BeadsChangeEvent) {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// beadsStatusReader is the narrow contract handleBeadsIssuesReachedState needs
// from beads.Client — just the batched Statuses read. Kept as a local
// interface so tests can pass a lightweight stub without implementing the
// full beads.Client.
type beadsStatusReader interface {
	Statuses(ctx context.Context, dir string, ids []string) (map[string]string, error)
}

// evaluateBeadsState fetches current statuses for the given ids and reports
// which ones reached the target state. done reflects the aggregation
// strategy: "all" completes when every id reached the state; "any" completes
// as soon as one has.
func evaluateBeadsState(
	ctx context.Context,
	client beadsStatusReader,
	dir string,
	allIDs []string,
	target string,
	match string,
) (reached []string, pending []string, states map[string]string, done bool, err error) {
	statuses, err := client.Statuses(ctx, dir, allIDs)
	if err != nil {
		return nil, nil, nil, false, err
	}
	states = statuses
	target = strings.ToLower(strings.TrimSpace(target))
	for _, id := range allIDs {
		st, ok := statuses[id]
		if ok && strings.EqualFold(strings.TrimSpace(st), target) {
			reached = append(reached, id)
		} else {
			pending = append(pending, id)
		}
	}
	if match == beadsMatchAny {
		done = len(reached) > 0
	} else {
		done = len(pending) == 0
	}
	return reached, pending, states, done, nil
}

// handleBeadsIssuesReachedState implements the "beads_issues_reached_state"
// branch of mitto_conversation_wait. It validates the new fields, resolves the
// working directory from the target conversation (or the caller when
// conversation_id == "self"), runs a fast-path check, then either subscribes
// to the BeadsWatcher (preferred) or falls back to a periodic poll. The
// caller has already validated self_id / conversation_id / what and resolved
// realSessionID.
func (s *Server) handleBeadsIssuesReachedState(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input ConversationWaitInput,
	realSessionID string,
) (*mcp.CallToolResult, ConversationWaitOutput, error) {
	// Field validation.
	if len(input.BeadsIssues) == 0 {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: "beads_issues is required when what is \"beads_issues_reached_state\"",
		}, nil
	}
	if strings.TrimSpace(input.BeadsTargetState) == "" {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: "beads_target_state is required when what is \"beads_issues_reached_state\"",
		}, nil
	}
	match := strings.ToLower(strings.TrimSpace(input.BeadsMatch))
	if match == "" {
		match = "all"
	}
	if match != "all" && match != beadsMatchAny {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: fmt.Sprintf("invalid beads_match: %q (want \"all\" or \"any\")", input.BeadsMatch),
		}, nil
	}

	// Snapshot the injected dependencies once.
	s.mu.RLock()
	store := s.store
	client := s.beadsClient
	bw := s.beadsWatcher
	s.mu.RUnlock()

	if client == nil {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: "beads client not available: mitto was not started with beads support",
		}, nil
	}
	if store == nil {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: "session store not available",
		}, nil
	}

	// Resolve the working directory: prefer the target conversation's, fall
	// back to the caller's. This mirrors the L1 orchestrator use case where
	// conversation_id == "self".
	var workingDir string
	if input.ConversationID != "self" && input.ConversationID != realSessionID {
		if meta, err := store.GetMetadata(input.ConversationID); err == nil {
			workingDir = meta.WorkingDir
		}
	}
	if workingDir == "" {
		if meta, err := store.GetMetadata(realSessionID); err == nil {
			workingDir = meta.WorkingDir
		}
	}
	if workingDir == "" {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: "could not resolve a working directory for the wait",
		}, nil
	}

	// Cross-workspace gate (mirror agent_responded branch).
	if input.Workspace != "" && s.sessionManager != nil {
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, ConversationWaitOutput{
				What:  input.What,
				Error: fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}
		if sourceMeta, err := store.GetMetadata(realSessionID); err == nil && sourceMeta.WorkingDir != targetWS.WorkingDir {
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, ConversationWaitOutput{
					What: input.What,
					Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces),
				}, nil
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "wait on a beads issue state", targetWS); err != nil {
				return nil, ConversationWaitOutput{What: input.What, Error: err.Error()}, nil
			}
		}
	}

	// Default timeout: 4h (mirrors L1 orchestrator per-driver maxDuration).
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultBeadsWaitTimeout
	}

	// Fast path: evaluate once before subscribing.
	reached, pending, states, done, err := evaluateBeadsState(
		ctx, client, workingDir, input.BeadsIssues, input.BeadsTargetState, match,
	)
	if err != nil {
		return nil, ConversationWaitOutput{
			What:  input.What,
			Error: fmt.Sprintf("failed to read beads statuses: %v", err),
		}, nil
	}
	if done {
		s.logger.Debug("Beads wait: fast-path predicate satisfied",
			"source_session", realSessionID,
			"working_dir", workingDir,
			"target_state", input.BeadsTargetState,
			"match", match,
			"reached", reached)
		return nil, ConversationWaitOutput{
			Success:       true,
			What:          input.What,
			ReachedIssues: reached,
			PendingIssues: pending,
			CurrentStates: states,
		}, nil
	}

	s.logger.Info("Waiting for beads issues to reach state",
		"source_session", realSessionID,
		"working_dir", workingDir,
		"issues", input.BeadsIssues,
		"target_state", input.BeadsTargetState,
		"match", match,
		"timeout", timeout)

	// Broadcast waiting state (sidebar hourglass).
	if s.sessionManager != nil {
		s.sessionManager.BroadcastWaitingForChildren(realSessionID, true)
		defer func() {
			s.sessionManager.BroadcastWaitingForChildren(realSessionID, false)
		}()
	}

	// Progress heartbeat keeps the SSE stream alive during long waits.
	defer s.startProgressHeartbeat(ctx, req)()

	// Subscribe to the watcher when available; fall back to polling.
	sub := &beadsWaitSubscriber{wake: make(chan struct{}, 1)}
	if bw != nil {
		if err := bw.Subscribe(sub, []string{workingDir}); err != nil {
			s.logger.Warn("Beads wait: failed to subscribe to watcher, falling back to poll",
				"error", err, "working_dir", workingDir)
		} else {
			defer bw.Unsubscribe(sub)
		}
	}

	// Slow poll as a safety net for missed events (also the only path when
	// no watcher is wired).
	poll := time.NewTicker(beadsWaitPollInterval)
	defer poll.Stop()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ConversationWaitOutput{
				What:          input.What,
				CurrentStates: states,
				PendingIssues: pending,
				Error:         "context cancelled while waiting",
			}, nil
		case <-deadline.C:
			// Final evaluation on timeout to return the freshest snapshot.
			reached, pending, states, _, err := evaluateBeadsState(
				ctx, client, workingDir, input.BeadsIssues, input.BeadsTargetState, match,
			)
			if err != nil {
				s.logger.Warn("Beads wait: final evaluation after timeout failed", "error", err)
			}
			s.logger.Warn("Beads wait timed out",
				"source_session", realSessionID,
				"working_dir", workingDir,
				"target_state", input.BeadsTargetState,
				"match", match,
				"timeout", timeout,
				"pending", pending)
			return nil, ConversationWaitOutput{
				Success:       true,
				What:          input.What,
				TimedOut:      true,
				Message:       fmt.Sprintf("timed out after %s waiting for beads to reach %q", timeout, input.BeadsTargetState),
				ReachedIssues: reached,
				PendingIssues: pending,
				CurrentStates: states,
			}, nil
		case <-sub.wake:
			// re-evaluate
		case <-poll.C:
			// re-evaluate
		}

		reached, pending, states, done, err = evaluateBeadsState(
			ctx, client, workingDir, input.BeadsIssues, input.BeadsTargetState, match,
		)
		if err != nil {
			// Transient bd failures should not abort a multi-hour wait.
			s.logger.Warn("Beads wait: evaluation failed, retrying",
				"error", err, "working_dir", workingDir)
			continue
		}
		if done {
			s.logger.Info("Beads wait predicate satisfied",
				"source_session", realSessionID,
				"working_dir", workingDir,
				"target_state", input.BeadsTargetState,
				"match", match,
				"reached", reached)
			return nil, ConversationWaitOutput{
				Success:       true,
				What:          input.What,
				ReachedIssues: reached,
				PendingIssues: pending,
				CurrentStates: states,
			}, nil
		}
	}
}
