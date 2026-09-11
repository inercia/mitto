package conversation

import (
	"fmt"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// BuildLoopUpdatedData constructs the WebSocket payload map for a loop_updated event.
// loop_configured: true if a loop config exists (controls editor UI mode).
// loop_enabled: true if loop runs are active (controls sidebar category + clock icon).
func BuildLoopUpdatedData(sessionID string, loop *session.LoopPrompt) map[string]interface{} {
	data := map[string]interface{}{
		"session_id": sessionID,
		// loop_config is the authoritative, complete editor state. Keep the
		// top-level fields below as lightweight/backward-compatible glance data.
		// An explicit null tells clients to clear local editor state on deletion.
		"loop_config": nil,
	}

	if loop != nil {
		data["loop_config"] = loop
		// loop_configured: true means the session is in loop mode (shows loop UI)
		data["loop_configured"] = true
		// loop_enabled: true means loop runs are active (locked state)
		data["loop_enabled"] = loop.Enabled
		// fresh_context: true means each scheduled run starts with a clean agent context
		data["fresh_context"] = loop.FreshContext
		data["max_iterations"] = loop.MaxIterations
		data["iteration_count"] = loop.IterationCount
		data["frequency"] = map[string]interface{}{
			"value": loop.Frequency.Value,
			"unit":  loop.Frequency.Unit,
		}
		if loop.Frequency.At != "" {
			data["frequency"].(map[string]interface{})["at"] = loop.Frequency.At
		}
		if loop.NextScheduledAt != nil && !loop.NextScheduledAt.IsZero() {
			data["next_scheduled_at"] = loop.NextScheduledAt.Format(time.RFC3339)
		}
		if loop.StoppedReason != "" {
			data["loop_stopped_reason"] = string(loop.StoppedReason)
		}
		if loop.AcknowledgedStoppedReason != "" {
			data["loop_acknowledged_stopped_reason"] = string(loop.AcknowledgedStoppedReason)
		}
		// Glance fields for conversation header display (trigger resolved via EffectiveTrigger
		// so schedule loops always report "schedule", not the empty-string default).
		// "trigger" stays the primary/first one for back-compat; "triggers" carries
		// the full armed set of a multi-trigger loop (mitto-r6j.2).
		data["trigger"] = string(loop.EffectiveTrigger())
		triggers := loop.EffectiveTriggers()
		triggerNames := make([]string, 0, len(triggers))
		for _, t := range triggers {
			triggerNames = append(triggerNames, string(t))
		}
		data["triggers"] = triggerNames
		effChildEvents := loop.EffectiveChildEvents()
		childEventNames := make([]string, 0, len(effChildEvents))
		for _, e := range effChildEvents {
			childEventNames = append(childEventNames, string(e))
		}
		data["child_events"] = childEventNames
		data["delay_seconds"] = loop.DelaySeconds
		data["max_duration_seconds"] = loop.MaxDurationSeconds
		// Prompt presence flag and free-text preview for the selector UI.
		data["loop_has_prompt"] = loop.HasPrompt()
		if preview := loop.PromptPreview(); preview != "" {
			data["loop_prompt_preview"] = preview
		}
	} else {
		// No loop config - session is not in loop mode
		data["loop_configured"] = false
		data["loop_enabled"] = false
	}

	return data
}

// BuildLoopAutoPauseNotification builds a proactive operator toast for a loop
// that was just auto-stopped, gated to the genuine-failure auto-stop reasons
// StoppedReasonPromptUnresolved (mitto-e4m) and StoppedReasonDeliveryFailures
// (mitto-4xf). Both are "the loop gave up and needs manual attention" signals
// that otherwise only surface as a sidebar websocket update
// (BuildLoopUpdatedData), easy for an operator to miss — StoppedReasonPromptUnresolved
// fires when the loop's configured prompt name can no longer be resolved
// after MaxPromptResolveFailures consecutive attempts; StoppedReasonDeliveryFailures
// fires when a prompt repeatedly fails to deliver (e.g. an unresponsive
// agent tripping the inactivity watchdog) after MaxLoopDeliveryFailures
// consecutive failures.
//
// Other auto-stop reasons (maxIterations, archived, pausedByUser,
// contextWindowExceeded, ...) intentionally do NOT produce a toast here —
// those are benign/expected stops and should stay silent.
//
// Returns ok=false (and a zero-value request) when loop is nil or the reason
// does not match, so callers can skip broadcasting without extra branching.
func BuildLoopAutoPauseNotification(sessionName string, loop *session.LoopPrompt) (UINotifyRequest, bool) {
	if loop == nil {
		return UINotifyRequest{}, false
	}

	name := sessionName
	if name == "" {
		name = "(unnamed conversation)"
	}

	var message string
	switch loop.StoppedReason {
	case session.StoppedReasonPromptUnresolved:
		message = fmt.Sprintf(
			"%q was auto-paused: its scheduled prompt %q could no longer be resolved after repeated attempts.",
			name, loop.PromptName,
		)
	case session.StoppedReasonDeliveryFailures:
		if loop.IsOnSlack() {
			// mitto-al8: an onSlack watcher's Slack Socket Mode subscriptions are
			// dropped the instant this stop lands (the owning loop's Enabled flag
			// gates internal/slackbridge/manager.go's subscription sync), so make
			// that concrete for the operator and set the expectation that
			// auto-recovery will retry with backoff rather than needing a manual
			// re-enable immediately.
			message = fmt.Sprintf(
				"%q was auto-paused: its scheduled prompt %q failed to deliver repeatedly (e.g. an unresponsive agent), and its Slack Socket Mode subscriptions have been dropped. Mitto will automatically retry re-enabling it with backoff; you'll be notified if recovery gives up.",
				name, loop.PromptName,
			)
		} else {
			message = fmt.Sprintf(
				"%q was auto-paused: its scheduled prompt %q failed to deliver repeatedly (e.g. an unresponsive agent) and needs manual attention.",
				name, loop.PromptName,
			)
		}
	default:
		return UINotifyRequest{}, false
	}

	return UINotifyRequest{
		Title:   "Loop conversation auto-paused",
		Message: message,
		Style:   "warning",
		Native:  true,
	}, true
}

// BuildLoopSlackAutoRecoveredNotification builds an informational toast for
// an onSlack watcher loop that was just automatically re-enabled after being
// auto-stopped for a transient/transport reason (mitto-al8, AC3). attempt is
// the 1-indexed recovery cycle that just succeeded; maxAttempts is the
// configured ceiling.
func BuildLoopSlackAutoRecoveredNotification(sessionName string, loop *session.LoopPrompt, attempt, maxAttempts int) UINotifyRequest {
	name := sessionName
	if name == "" {
		name = "(unnamed conversation)"
	}

	promptName := ""
	if loop != nil {
		promptName = loop.PromptName
	}

	return UINotifyRequest{
		Title: "Loop conversation auto-recovered",
		Message: fmt.Sprintf(
			"%q was automatically re-enabled and its Slack Socket Mode subscriptions restored (recovery attempt %d/%d for prompt %q).",
			name, attempt, maxAttempts, promptName,
		),
		Style:  "info",
		Native: true,
	}
}

// BuildLoopSlackAutoRecoveryExhaustedNotification builds a warning toast when
// an onSlack watcher loop's auto-recovery attempts are exhausted — the loop
// is left stopped and needs manual attention (mitto-al8, AC2/AC3).
func BuildLoopSlackAutoRecoveryExhaustedNotification(sessionName string, loop *session.LoopPrompt, maxAttempts int) UINotifyRequest {
	name := sessionName
	if name == "" {
		name = "(unnamed conversation)"
	}

	promptName := ""
	if loop != nil {
		promptName = loop.PromptName
	}

	return UINotifyRequest{
		Title: "Loop auto-recovery exhausted",
		Message: fmt.Sprintf(
			"%q could not be auto-recovered after %d attempt(s): its scheduled prompt %q keeps failing to deliver. Its Slack Socket Mode subscriptions remain dropped until you manually re-enable it.",
			name, maxAttempts, promptName,
		),
		Style:  "warning",
		Native: true,
	}
}
