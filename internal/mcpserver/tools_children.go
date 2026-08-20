// tools_children.go: MCP tool handlers for parent/child task coordination.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Children Tasks Coordination
// =============================================================================

// defaultChildrenTasksTimeout is the default timeout for waiting for children to report.
const defaultChildrenTasksTimeout = 10 * time.Minute

// childResumeRetryDelays bounds eager recovery for persisted children whose
// startup failed transiently. The last failure remains pending in the wait loop,
// which retries at its normal poll cadence until the caller's deadline.
var childResumeRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// Report size limits. These prevent MCP protocol validation failures when the
// parent aggregates multiple children's reports into a single tool result.
// The MCP tool result must fit within the agent's protocol message size limits.
const (
	maxReportSummaryBytes = 8000  // ~8 KB for summary (concise findings)
	maxReportDetailsBytes = 16000 // ~16 KB for details (supporting information)
)

// childrenReportSuffix is appended to the prompt sent to each child,
// instructing them to call mitto_children_tasks_report.
// The %s placeholder is replaced with the task_id instruction (or empty string if no task_id).
const childrenReportSuffix = "\n\n" +
	"IMPORTANT: you must report your results when you are done. " + "\n" +
	"Report what you did and any recommended follow-ups (if any). " + "\n" +
	"Keep your report concise: focus on key conclusions and actions rather than exhaustive details. " + "\n" +
	"Use the `mitto_children_tasks_report` MCP tool with: " + "\n" +
	"1) your `self_id`, " + "\n" +
	"2) a `status` (e.g. \"completed\", \"in_progress\", \"failed\"), " + "\n" +
	"3) a `summary` of your findings/changes/conclusions (max ~8KB), " + "\n" +
	"4) OPTIONALLY some `details` with additional information (max ~16KB). " + "\n" +
	"%s " + "\n" +
	"NOTE: ignore these instructions if you have already sent the report."

// logChildrenWaitTimeout logs the outcome of a children-wait timeout. When there
// are genuinely outstanding (pending) children at the deadline, it logs at WARN
// with the pending list. The timeout is downgraded to DEBUG (meaningless noise)
// when either nothing is still pending (e.g. the parent re-waited after children
// already reported) or every pending child is still actively processing
// (healthy-but-slow): the tool already returns a deterministic "still_processing"
// signal for those, so a WARN would be redundant.
func logChildrenWaitTimeout(logger *slog.Logger, parentSession string, pending, reported, stillProcessing []string, totalRunning int, timeout time.Duration) {
	log := logger.Warn
	if len(pending) == 0 || len(stillProcessing) == len(pending) {
		log = logger.Debug
	}
	log("Timeout waiting for children to report",
		"parent_session", parentSession,
		"pending_children", pending,
		"reported_children", reported,
		"still_processing_children", stillProcessing,
		"total_running", totalRunning,
		"timeout", timeout)
}

// stillProcessingChildren returns the subset of childIDs whose agent is still
// actively responding (IsPrompting). These are healthy-but-slow children that
// have not yet reported; a wait timeout on them is expected rather than an error,
// so it is logged at DEBUG instead of WARN.
func (s *Server) stillProcessingChildren(childIDs []string) []string {
	if s.sessionManager == nil {
		return nil
	}
	var processing []string
	for _, id := range childIDs {
		if bs := s.sessionManager.GetSession(id); bs != nil && bs.IsPrompting() {
			processing = append(processing, id)
		}
	}
	return processing
}

// classifyStoppedChild inspects a stopped child's last recorded event to
// determine why it disappeared from the session manager. reason is
// "processRecycled" when the last event shows the shared ACP process was
// GC-recycled out from under the child (mitto-qy0j: GC Tier 5/6 close every
// session sharing a degraded process — via BackgroundSession.Close, which
// cancels the session's own context — BEFORE stopping the process itself).
// wasPrompting reports whether the child was actively mid-turn when that
// happened: a mid-turn interruption is a genuine loss of in-flight work and
// stays a terminal failure, while a non-prompting interruption (e.g. gated on
// an unresolved startup model constraint that never got to dispatch its
// queued prompt) has nothing to lose and is safe to resume-retry instead of
// being treated as an ordinary completed/stopped child. reason=="" means no
// recognizable interruption signal was found (e.g. a genuine graceful stop),
// preserving the original auto-complete fallback.
func classifyStoppedChild(store *session.Store, childID string) (reason string, wasPrompting bool) {
	events, err := store.ReadEventsLast(childID, 1, 0)
	if err != nil || len(events) == 0 || events[0].Type != session.EventTypeSessionEnd {
		return "", false
	}
	data, err := session.DecodeEventData(events[0])
	if err != nil {
		return "", false
	}
	endData, ok := data.(session.SessionEndData)
	if !ok || endData.Reason != "gc_suspended" {
		return "", false
	}
	return "processRecycled", endData.WasPrompting
}

func (s *Server) isTransientChildResumeError(err error) bool {
	return err != nil && s.sessionManager != nil &&
		(s.sessionManager.IsMCPInitTimeout(err) || errors.Is(err, acperrors.ErrSharedProcessSaturated))
}

// resumeChildWithTransientRetry performs the initial resume plus a short bounded
// backoff sequence. The bool result reports whether the final error is transient,
// allowing the caller to keep the child pending instead of returning not_running.
func (s *Server) resumeChildWithTransientRetry(ctx context.Context, parentID string, child session.Metadata) (BackgroundSession, error, bool) {
	for attempt := 0; ; attempt++ {
		resumed, err := s.sessionManager.ResumeSession(child.SessionID, child.Name, child.WorkingDir)
		if err == nil {
			return resumed, nil, false
		}
		if !s.isTransientChildResumeError(err) {
			return nil, err, false
		}
		if attempt >= len(childResumeRetryDelays) {
			return nil, err, true
		}

		delay := childResumeRetryDelays[attempt]
		s.logger.Info("Child resume hit transient startup failure; retrying",
			"parent_session", parentID,
			"child_session", child.SessionID,
			"attempt", attempt+1,
			"next_delay", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, err, true
		case <-timer.C:
		}
	}
}

func (s *Server) handleChildrenTasksWait(ctx context.Context, req *mcp.CallToolRequest, input ChildrenTasksWaitInput) (*mcp.CallToolResult, ChildrenTasksWaitOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate children_list
	if len(input.ChildrenList) == 0 {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "children_list must contain at least one child conversation ID"}, nil
	}

	// Resolve the self_id to a real session ID (parent)
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Permission check: requires can_send_prompt (we are sending prompts to children)
	if !s.checkSessionFlag(realSessionID, session.FlagCanSendPrompt) {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("tool 'mitto_children_tasks_wait' requires the 'Can Send Prompt' (%s) flag to be enabled in Advanced Settings", session.FlagCanSendPrompt),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "session store not available"}, nil
	}

	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultChildrenTasksTimeout
	}
	waitDeadline := time.Now().Add(timeout)
	resumeCtx, cancelResume := context.WithDeadline(ctx, waitDeadline)
	defer cancelResume()

	// Validate each child exists and is actually a child of this parent.
	// Also check if each child is currently running.
	validChildren := make([]string, 0, len(input.ChildrenList))
	runningChildren := make([]string, 0, len(input.ChildrenList))
	notRunningChildren := make([]string, 0)
	startingChildren := make(map[string]session.Metadata)
	var warnings []string

	for _, childID := range input.ChildrenList {
		childMeta, err := store.GetMetadata(childID)
		if err != nil {
			s.logger.Warn("Child conversation not found, skipping",
				"parent_session", realSessionID,
				"child_session", childID,
				"error", err)
			continue
		}
		if childMeta.ParentSessionID != realSessionID {
			s.logger.Warn("Conversation is not a child of this parent, skipping",
				"parent_session", realSessionID,
				"child_session", childID,
				"actual_parent", childMeta.ParentSessionID)
			continue
		}
		validChildren = append(validChildren, childID)

		// Check if the child is currently running (registered with MCP server).
		// If not running and not archived, try to auto-resume it.
		childReg := s.getSession(childID)
		if childReg == nil && !childMeta.Archived && s.sessionManager != nil {
			// Session is stored or completed (e.g., GC-closed) — try to resume it.
			s.logger.Info("Auto-resuming child session",
				"parent_session", realSessionID,
				"child_session", childID,
				"child_status", string(childMeta.Status))
			resumed, resumeErr, transient := s.resumeChildWithTransientRetry(resumeCtx, realSessionID, childMeta)
			if resumeErr != nil {
				if transient {
					startingChildren[childID] = childMeta
					s.logger.Info("Child remains in transient startup recovery",
						"parent_session", realSessionID,
						"child_session", childID,
						"error", resumeErr)
				} else {
					s.logger.Warn("Failed to auto-resume child session",
						"parent_session", realSessionID,
						"child_session", childID,
						"error", resumeErr)
				}
			} else if resumed != nil {
				// Re-check registration after resume
				childReg = s.getSession(childID)
				go resumed.TryProcessQueuedMessage()
			}
		}
		if childReg == nil {
			if _, starting := startingChildren[childID]; starting {
				runningChildren = append(runningChildren, childID)
				continue
			}
			notRunningChildren = append(notRunningChildren, childID)
			reason := "not running"
			if childMeta.Archived {
				reason = "archived"
			}
			warnings = append(warnings, fmt.Sprintf("child %s is %s and cannot process prompts", childID, reason))
			s.logger.Warn("Child conversation is not running",
				"parent_session", realSessionID,
				"child_session", childID,
				"archived", childMeta.Archived)
		} else {
			runningChildren = append(runningChildren, childID)
		}
	}

	if len(validChildren) == 0 {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   "none of the provided conversation IDs are valid children of this session",
		}, nil
	}

	// Get-or-create the persistent child report collector for this parent.
	collector := s.getOrCreateCollector(realSessionID)

	// Server-side safeguard: auto-report children that have been waited on for too long.
	// This prevents the AI agent from retrying indefinitely when a child is stuck.
	// We inject a synthetic "stuck" report so that startWait sees them as completed.
	stuckChildren := collector.getStuckChildren()
	for _, childID := range stuckChildren {
		s.logger.Warn("Child session considered stuck after prolonged cumulative wait — auto-reporting as stuck",
			"parent_session", realSessionID,
			"child_session", childID,
			"max_wait", maxChildWaitDuration)
		collector.addReport(childID, input.TaskID, json.RawMessage(`{"status":"stuck","summary":"Child session did not report after 30 minutes of cumulative waiting. The child may be unresponsive. Consider archiving this session."}`))
	}

	// If ALL valid children are not running, return immediately with not_running status.
	// We still register them in the collector for record-keeping.
	if len(runningChildren) == 0 {
		reports := make(map[string]ChildReportInfo, len(notRunningChildren))
		for _, childID := range notRunningChildren {
			reason := "session_closed"
			if childMeta, err := store.GetMetadata(childID); err == nil && childMeta.Archived {
				reason = "archived"
			}
			reports[childID] = ChildReportInfo{
				Completed: false,
				Status:    "not_running",
				Reason:    reason,
			}
		}
		return nil, ChildrenTasksWaitOutput{
			Success:  true,
			Reports:  reports,
			Warnings: warnings,
		}, nil
	}

	// Build the prompt to send to pending children.
	// If no prompt is provided, skip sending entirely (wait-only mode).
	// This allows callers to retry waits without re-enqueuing duplicate messages.
	promptText := input.Prompt
	sendPrompt := promptText != ""
	if sendPrompt {
		// A new prompt explicitly retries children whose prior result was only
		// synthetic. Leave genuine reports and terminal failures satisfied.
		collector.resetAutoCompletedForRetry(runningChildren)
	}

	// Set up wait signaling. startWait only clears reports when the task_id
	// changes, preserving reports from the same task across retries.
	waitCh, _ := collector.startWait(input.TaskID, runningChildren)
	defer collector.clearWait()
	childrenToPrompt, _ := collector.getPendingAndReported()

	if sendPrompt {
		taskIDInstruction := ""
		if input.TaskID != "" {
			taskIDInstruction = fmt.Sprintf("5) the `task_id: \"%s\"` is mandatory", input.TaskID)
		}
		promptText += fmt.Sprintf(childrenReportSuffix, taskIDInstruction)
	}

	// Send only to children that still need a report (unless wait-only mode).
	if sendPrompt {
		for _, childID := range childrenToPrompt {
			queue := store.Queue(childID)

			// Dedup: skip if there's already a pending message from this parent in the child's queue.
			// This prevents duplicate report-request messages from accumulating when the parent
			// retries after a timeout and the child hasn't consumed the previous message yet.
			existingMsgs, _ := queue.List()
			alreadyQueued := false
			for _, m := range existingMsgs {
				if m.ClientID == realSessionID {
					alreadyQueued = true
					break
				}
			}
			if alreadyQueued {
				s.logger.Debug("Skipping duplicate prompt — parent already has a pending message in child's queue",
					"parent_session", realSessionID,
					"child_session", childID)
				continue
			}

			msg, err := queue.AddWithOrigin(promptText, nil, nil, realSessionID, nil, 0, nil, "", session.QueueOriginAgent)
			if err != nil {
				s.logger.Warn("Failed to enqueue prompt to child",
					"parent_session", realSessionID,
					"child_session", childID,
					"error", err)
				continue
			}

			s.logger.Info("Progress inquiry sent to child",
				"parent_session", realSessionID,
				"child_session", childID,
				"message_id", msg.ID)

			// Try to process the queued message immediately if agent is idle
			if s.sessionManager != nil {
				if bs := s.sessionManager.GetSession(childID); bs != nil {
					go bs.TryProcessQueuedMessage()
				}
			}
		}
	}

	// The eager recovery above consumes the same caller-provided wait budget.
	timeout = time.Until(waitDeadline)
	if timeout <= 0 {
		timeout = time.Nanosecond
	}

	// Broadcast that this parent is now waiting for children
	if s.sessionManager != nil {
		s.sessionManager.BroadcastWaitingForChildren(realSessionID, true)
		defer func() {
			s.sessionManager.BroadcastWaitingForChildren(realSessionID, false)
		}()
	}

	// Block until all running children report or timeout
	defer s.startProgressHeartbeat(ctx, req)()
	s.logger.Info("Waiting for children to report",
		"parent_session", realSessionID,
		"task_id", input.TaskID,
		"running_children", len(runningChildren),
		"not_running_children", len(notRunningChildren),
		"timeout", timeout)

	// Record how long this call actually blocked, on the parent session, for the
	// "child wait" statistics surfaced in the conversation properties panel.
	waitStart := time.Now()
	defer func() {
		if s.sessionManager != nil {
			if parentBS := s.sessionManager.GetSession(realSessionID); parentBS != nil {
				parentBS.RecordChildWait(time.Since(waitStart))
			}
		}
	}()

	var timedOut bool

	// childIdlePollInterval is how often we check if pending children are still responsive.
	const childIdlePollInterval = 5 * time.Second
	// childIdleGracePeriod is how long a child must be idle (not prompting) before
	// we consider it done without response. This accounts for:
	// - Time for the child to pick up a queued message
	// - Time for the agent to process and call the report tool
	const childIdleGracePeriod = 15 * time.Second

	if s.sessionManager != nil {
		// Polling loop: check child agent status periodically
		pollTicker := time.NewTicker(childIdlePollInterval)
		defer pollTicker.Stop()

		timeoutTimer := time.NewTimer(timeout)
		defer timeoutTimer.Stop()

		// Track when each child was first seen idle (not prompting)
		childIdleSince := make(map[string]time.Time)
		waitStartTime := time.Now()

	waitLoop:
		for {
			select {
			case <-waitCh:
				// All children reported or auto-completed
				break waitLoop
			case <-timeoutTimer.C:
				timedOut = true
				pendingChildren, reportedChildren := collector.getPendingAndReported()
				stillProcessing := s.stillProcessingChildren(pendingChildren)
				logChildrenWaitTimeout(s.logger, realSessionID, pendingChildren, reportedChildren, stillProcessing, len(runningChildren), timeout)
				break waitLoop
			case <-ctx.Done():
				return nil, ChildrenTasksWaitOutput{
					Success: false,
					Error:   "context cancelled while waiting for children to report",
				}, nil
			case <-pollTicker.C:
				// Check status of pending children
				pending, _ := collector.getPendingAndReported()
				for _, childID := range pending {
					bs := s.sessionManager.GetSession(childID)
					if bs == nil {
						if childMeta, starting := startingChildren[childID]; starting {
							resumed, resumeErr := s.sessionManager.ResumeSession(childID, childMeta.Name, childMeta.WorkingDir)
							if resumeErr == nil && resumed != nil {
								delete(startingChildren, childID)
								go resumed.TryProcessQueuedMessage()
								continue
							}
							if s.isTransientChildResumeError(resumeErr) {
								continue
							}
							delete(startingChildren, childID)
							collector.markChildFailed(childID, fmt.Sprintf("startup recovery failed: %v", resumeErr))
							continue
						}
						if reason, wasPrompting := classifyStoppedChild(store, childID); reason != "" {
							if wasPrompting {
								// Mid-turn interruption: real work was lost. Keep the
								// existing behavior of failing immediately.
								s.logger.Info("Child prompt interrupted by session recycle — marking failed",
									"parent_session", realSessionID,
									"child_session", childID,
									"reason", reason)
								collector.markChildFailed(childID, reason)
								delete(childIdleSince, childID)
								continue
							}

							// mitto-qy0j: the shared process was GC-recycled before this
							// child ever dispatched its queued prompt (e.g. still gated
							// on an unresolved startup model constraint). Nothing was
							// lost — retry the same bounded auto-resume path used for
							// children that were already not-running when the wait
							// started, instead of giving up and letting the parent treat
							// this recoverable interruption as a completed/stopped child.
							childMeta, metaErr := store.GetMetadata(childID)
							if metaErr != nil || childMeta.Archived {
								s.logger.Warn("Cannot resume child after GC recycle",
									"parent_session", realSessionID,
									"child_session", childID,
									"error", metaErr,
									"archived", childMeta.Archived)
								collector.markChildFailed(childID, reason)
								delete(childIdleSince, childID)
								continue
							}
							resumed, resumeErr, transient := s.resumeChildWithTransientRetry(resumeCtx, realSessionID, childMeta)
							switch {
							case resumeErr == nil && resumed != nil:
								s.logger.Info("Child resumed after GC recycle interrupted startup model recovery",
									"parent_session", realSessionID,
									"child_session", childID)
								go resumed.TryProcessQueuedMessage()
								delete(childIdleSince, childID)
							case transient:
								startingChildren[childID] = childMeta
								s.logger.Info("Child re-entered transient startup recovery after GC recycle",
									"parent_session", realSessionID,
									"child_session", childID,
									"error", resumeErr)
							default:
								s.logger.Warn("Failed to resume child after GC recycle",
									"parent_session", realSessionID,
									"child_session", childID,
									"error", resumeErr)
								collector.markChildFailed(childID, fmt.Sprintf("%s: resume failed: %v", reason, resumeErr))
								delete(childIdleSince, childID)
							}
							continue
						}
						// Session is no longer running — auto-complete
						s.logger.Info("Child session stopped while waiting — auto-completing",
							"parent_session", realSessionID,
							"child_session", childID)
						collector.markChildAutoCompleted(childID, "session_stopped")
						delete(childIdleSince, childID)
						continue
					}

					if bs.IsPrompting() {
						// Child is actively processing — reset idle timer
						delete(childIdleSince, childID)
						continue
					}

					// mitto-qy0j: a child mid-startup-model-recovery is not "idle" in
					// the sense the grace period below cares about — its queued prompt
					// will dispatch once the required model is applied. Treat this the
					// same as active prompting so the idle-timeout auto-complete never
					// fires while bounded startup recovery is in flight (mirrors
					// WaitForResponseComplete's own StartupRecoveryPending gating).
					if bs.StartupRecoveryPending() {
						delete(childIdleSince, childID)
						continue
					}

					// Signal 2 (Path B): message was popped but is sleeping through the
					// configured delay before dispatch — session appears idle but will
					// become prompting shortly.
					if bs.HasQueuedDeliveryInProgress() {
						delete(childIdleSince, childID)
						continue
					}

					// Re-kick delivery each poll so Path A messages are dispatched once
					// their delay elapses without waiting for the next natural trigger.
					go bs.TryProcessQueuedMessage()

					// Signal 1 (Path A): parent message still in queue (undelivered, not
					// future-scheduled). Prevent false idle while it awaits dispatch.
					parentMsgPending := false
					if store != nil && bs.GetQueueConfig().IsEnabled() {
						childQueue := store.Queue(childID)
						if msgs, _ := childQueue.List(); len(msgs) > 0 {
							now := time.Now()
							for _, m := range msgs {
								if m.ClientID == realSessionID && (m.ScheduledTime == nil || !m.ScheduledTime.After(now)) {
									parentMsgPending = true
									break
								}
							}
						}
					}
					if parentMsgPending {
						delete(childIdleSince, childID)
						continue
					}

					// If a queued-send error occurred after this wait started, surface it
					// as a failure rather than letting the child appear frozen/idle.
					if errMsg, errAt := bs.LastQueuedSendError(); errMsg != "" && errAt.After(waitStartTime) {
						s.logger.Info("Child queued send failed — marking failed",
							"parent_session", realSessionID,
							"child_session", childID,
							"error", errMsg)
						collector.markChildFailed(childID, errMsg)
						delete(childIdleSince, childID)
						continue
					}

					// Child is running but idle (not prompting)
					if idleSince, exists := childIdleSince[childID]; exists {
						if time.Since(idleSince) > childIdleGracePeriod {
							// Been idle too long without reporting — auto-complete
							s.logger.Info("Child agent idle without reporting — auto-completing",
								"parent_session", realSessionID,
								"child_session", childID,
								"idle_duration", time.Since(idleSince).Round(time.Second))
							collector.markChildAutoCompleted(childID, "agent_idle")
							delete(childIdleSince, childID)
						}
					} else {
						// First time seeing this child idle — start tracking
						childIdleSince[childID] = time.Now()
					}
				}
			}
		}
	} else {
		// No session manager available — fall back to simple wait (original behavior)
		select {
		case <-waitCh:
			// All running children reported
		case <-time.After(timeout):
			timedOut = true
			pendingChildren, reportedChildren := collector.getPendingAndReported()
			logChildrenWaitTimeout(s.logger, realSessionID, pendingChildren, reportedChildren, nil, len(runningChildren), timeout)
		case <-ctx.Done():
			return nil, ChildrenTasksWaitOutput{
				Success: false,
				Error:   "context cancelled while waiting for children to report",
			}, nil
		}
	}

	// Build the output with whatever reports we have
	reports := make(map[string]ChildReportInfo, len(validChildren))

	// Add reports from running children (from collector)
	collector.mu.Lock()
	for _, childID := range runningChildren {
		report := collector.reports[childID]
		info := ChildReportInfo{Completed: false, Status: "pending"}
		if report != nil && report.Completed {
			if report.Failed {
				// Queued-send failed before agent could process the message
				info.Completed = false
				info.Status = "failed"
				info.Reason = report.FailMessage
				if !report.Timestamp.IsZero() {
					info.Timestamp = report.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
			} else if report.AutoCompleted {
				// Auto-completed: agent went idle without reporting
				info.Completed = false
				info.Status = "agent_not_responding"
				info.Reason = report.AutoReason
				if !report.Timestamp.IsZero() {
					info.Timestamp = report.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
			} else {
				info.Completed = true
				info.Status = "completed"
				// Unmarshal the raw JSON report into the typed struct for proper schema validation
				if len(report.Report) > 0 {
					var reportData ChildReportData
					if err := json.Unmarshal(report.Report, &reportData); err != nil {
						s.logger.Warn("Failed to unmarshal child report data",
							"child_session", childID,
							"error", err)
					} else {
						info.Report = &reportData
					}
				}
				if !report.Timestamp.IsZero() {
					info.Timestamp = report.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
			}
		} else if timedOut {
			// Add diagnostic reason for timed-out children
			if childReg := s.getSession(childID); childReg == nil {
				info.Reason = "session_unregistered"
			} else if s.sessionManager != nil {
				if bs := s.sessionManager.GetSession(childID); bs != nil && bs.IsPrompting() {
					info.Reason = "still_processing"
				} else {
					info.Reason = "no_report_received"
				}
			} else {
				info.Reason = "no_report_received"
			}
		}
		reports[childID] = info
	}
	collector.mu.Unlock()

	// Add not-running children to reports with diagnostic reason
	for _, childID := range notRunningChildren {
		reason := "session_closed"
		if childMeta, err := store.GetMetadata(childID); err == nil && childMeta.Archived {
			reason = "archived"
		}
		reports[childID] = ChildReportInfo{
			Completed: false,
			Status:    "not_running",
			Reason:    reason,
		}
	}

	return nil, ChildrenTasksWaitOutput{
		Success:  true,
		Reports:  reports,
		TimedOut: timedOut,
		Warnings: warnings,
	}, nil
}

func (s *Server) handleChildrenTasksReport(ctx context.Context, req *mcp.CallToolRequest, input ChildrenTasksReportInput) (*mcp.CallToolResult, ChildrenTasksReportOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate report fields
	if input.Status == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "status is required"}, nil
	}
	if input.Summary == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "summary is required"}, nil
	}

	// Enforce size limits to prevent MCP protocol validation failures when the
	// parent aggregates multiple children's reports into a single tool result.
	if len(input.Summary) > maxReportSummaryBytes {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error: fmt.Sprintf(
				"summary is too long (%d bytes, max %d). Please shorten your summary to the key findings and re-submit. "+
					"Focus on conclusions rather than exhaustive details — you can put extra information in the 'details' field.",
				len(input.Summary), maxReportSummaryBytes),
		}, nil
	}
	if len(input.Details) > maxReportDetailsBytes {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error: fmt.Sprintf(
				"details is too long (%d bytes, max %d). Please condense your details and re-submit. "+
					"Keep only the most important information — the parent can always query you for more context later.",
				len(input.Details), maxReportDetailsBytes),
		}, nil
	}

	// Serialize the report fields into JSON for internal storage
	reportJSON, err := json.Marshal(map[string]string{
		"status":  input.Status,
		"summary": input.Summary,
		"details": input.Details,
	})
	if err != nil {
		return nil, ChildrenTasksReportOutput{Success: false, Error: fmt.Sprintf("failed to serialize report: %v", err)}, nil
	}

	// Resolve the self_id to a real session ID (child)
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "session store not available"}, nil
	}

	// Look up child's metadata to find parent
	childMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to get session metadata: %v", err),
		}, nil
	}

	parentSessionID := childMeta.ParentSessionID
	if parentSessionID == "" {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   "this session has no parent session - only child conversations can report back",
		}, nil
	}

	// Get-or-create the persistent collector for the parent.
	// This ensures reports are stored even if the parent hasn't called _wait yet.
	collector := s.getOrCreateCollector(parentSessionID)

	// Store the report (may also signal a waiting parent). Capture the wait state
	// atomically because signaling can let the parent clear it before we log.
	parentWasWaiting := collector.addReport(realSessionID, input.TaskID, json.RawMessage(reportJSON))

	// Detect orphaned reports: parent unregistered or not actively waiting
	parentReg := s.getSession(parentSessionID)
	if parentReg == nil {
		s.logger.Warn("Child reported to unregistered parent session — report is orphaned",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	} else if !parentWasWaiting {
		s.logger.Info("Child reported to parent (no active wait — report stored for next wait cycle)",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	} else {
		s.logger.Info("Child reported to parent",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	}

	return nil, ChildrenTasksReportOutput{
		Success:         true,
		ParentSessionID: parentSessionID,
	}, nil
}
