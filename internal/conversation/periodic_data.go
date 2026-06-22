package conversation

import (
	"time"

	"github.com/inercia/mitto/internal/session"
)

// BuildPeriodicUpdatedData constructs the WebSocket payload map for a periodic_updated event.
// periodic_configured: true if a periodic config exists (controls editor UI mode).
// periodic_enabled: true if periodic runs are active (controls sidebar category + clock icon).
func BuildPeriodicUpdatedData(sessionID string, periodic *session.PeriodicPrompt) map[string]interface{} {
	data := map[string]interface{}{
		"session_id": sessionID,
	}

	if periodic != nil {
		// periodic_configured: true means the session is in periodic mode (shows periodic UI)
		data["periodic_configured"] = true
		// periodic_enabled: true means periodic runs are active (locked state)
		data["periodic_enabled"] = periodic.Enabled
		// fresh_context: true means each scheduled run starts with a clean agent context
		data["fresh_context"] = periodic.FreshContext
		data["max_iterations"] = periodic.MaxIterations
		data["iteration_count"] = periodic.IterationCount
		data["frequency"] = map[string]interface{}{
			"value": periodic.Frequency.Value,
			"unit":  periodic.Frequency.Unit,
		}
		if periodic.Frequency.At != "" {
			data["frequency"].(map[string]interface{})["at"] = periodic.Frequency.At
		}
		if periodic.NextScheduledAt != nil && !periodic.NextScheduledAt.IsZero() {
			data["next_scheduled_at"] = periodic.NextScheduledAt.Format(time.RFC3339)
		}
		if periodic.StoppedReason != "" {
			data["periodic_stopped_reason"] = string(periodic.StoppedReason)
		}
		// Glance fields for conversation header display (trigger resolved via EffectiveTrigger
		// so schedule loops always report "schedule", not the empty-string default).
		data["trigger"] = string(periodic.EffectiveTrigger())
		data["delay_seconds"] = periodic.DelaySeconds
		data["max_duration_seconds"] = periodic.MaxDurationSeconds
		// Prompt presence flag and free-text preview for the selector UI.
		data["periodic_has_prompt"] = periodic.Prompt != "" || periodic.PromptName != ""
		if preview := periodic.PromptPreview(); preview != "" {
			data["periodic_prompt_preview"] = preview
		}
	} else {
		// No periodic config - session is not in periodic mode
		data["periodic_configured"] = false
		data["periodic_enabled"] = false
	}

	return data
}
