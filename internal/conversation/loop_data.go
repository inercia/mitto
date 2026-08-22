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
// that was just auto-stopped, gated strictly on StoppedReasonPromptUnresolved
// (mitto-e4m). This is the failure mode where the loop's configured prompt
// name can no longer be resolved after MaxPromptResolveFailures consecutive
// attempts — a silent auto-pause that otherwise only surfaces as a sidebar
// websocket update (BuildLoopUpdatedData), easy for an operator to miss.
//
// Other auto-stop reasons (maxIterations, archived, pausedByUser,
// contextWindowExceeded, deliveryFailures, ...) intentionally do NOT produce
// a toast here — benign stops should stay silent, and the remaining failure
// reasons are left as a natural future extension (out of scope for mitto-e4m).
//
// Returns ok=false (and a zero-value request) when loop is nil or the reason
// does not match, so callers can skip broadcasting without extra branching.
func BuildLoopAutoPauseNotification(sessionName string, loop *session.LoopPrompt) (UINotifyRequest, bool) {
	if loop == nil || loop.StoppedReason != session.StoppedReasonPromptUnresolved {
		return UINotifyRequest{}, false
	}

	name := sessionName
	if name == "" {
		name = "(unnamed conversation)"
	}

	return UINotifyRequest{
		Title: "Loop conversation auto-paused",
		Message: fmt.Sprintf(
			"%q was auto-paused: its scheduled prompt %q could no longer be resolved after repeated attempts.",
			name, loop.PromptName,
		),
		Style:  "warning",
		Native: true,
	}, true
}
