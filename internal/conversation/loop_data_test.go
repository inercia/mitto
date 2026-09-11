package conversation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

func TestBuildLoopUpdatedData_FullConfigAndDeletion(t *testing.T) {
	coalesce := false
	runOnStart := true
	settleWindow := 12
	createdAt := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	lastSentAt := updatedAt.Add(time.Minute)
	nextScheduledAt := lastSentAt.Add(30 * time.Minute)
	firstRunAt := createdAt.Add(15 * time.Minute)
	stoppedAt := updatedAt.Add(2 * time.Hour)
	loop := &session.LoopPrompt{
		Prompt:          "Continue {{.Args.Scope}}",
		PromptName:      "feature-driver",
		Arguments:       map[string]string{"Scope": "frontend"},
		Frequency:       session.Frequency{Value: 2, Unit: session.FrequencyDays, At: "09:30"},
		Enabled:         false,
		FreshContext:    true,
		MaxIterations:   8,
		IterationCount:  3,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		LastSentAt:      &lastSentAt,
		NextScheduledAt: &nextScheduledAt,
		Triggers: []session.LoopTrigger{
			session.TriggerSchedule,
			session.TriggerOnCompletion,
			session.TriggerOnTasks,
			session.TriggerOnChild,
		},
		DelaySeconds:              45,
		MaxDurationSeconds:        7200,
		FirstRunAt:                &firstRunAt,
		StoppedReason:             session.StoppedReasonMaxDuration,
		StoppedAt:                 &stoppedAt,
		AcknowledgedStoppedReason: session.StoppedReasonMaxIterations,
		Condition:                 `issue.status == "open"`,
		ConditionPreset:           "open-issues",
		CooldownSeconds:           60,
		CoalesceDuringBusy:        &coalesce,
		RunOnStart:                &runOnStart,
		SettleWindowSeconds:       &settleWindow,
		ChildEvents: []session.ChildEvent{
			session.ChildEventAnyEndResponse,
			session.ChildEventAnyLoopStopped,
		},
	}
	loop.Normalize()

	data := BuildLoopUpdatedData("sess-full", loop)
	if data["loop_config"] != loop {
		t.Fatal("loop_config does not retain the canonical LoopPrompt")
	}

	wire, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal loop_updated payload: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal loop_updated payload: %v", err)
	}
	config, ok := decoded["loop_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("loop_config = %#v, want object", decoded["loop_config"])
	}
	want := map[string]interface{}{
		"prompt": "Continue {{.Args.Scope}}", "prompt_name": "feature-driver",
		"arguments": map[string]interface{}{"Scope": "frontend"},
		"frequency": map[string]interface{}{"value": float64(2), "unit": "days", "at": "09:30"},
		"enabled":   false, "fresh_context": true, "max_iterations": float64(8),
		"iteration_count": float64(3), "trigger": "schedule",
		"triggers":      []interface{}{"schedule", "onCompletion", "onTasks", "onChild"},
		"delay_seconds": float64(45), "max_duration_seconds": float64(7200),
		"stopped_reason": "maxDuration", "acknowledged_stopped_reason": "maxIterations",
		"condition": `issue.status == "open"`, "condition_preset": "open-issues",
		"cooldown_seconds": float64(60), "coalesce_during_busy": false,
		"run_on_start": true, "settle_window_seconds": float64(12),
		"child_events": []interface{}{"anyEndResponse", "anyLoopStopped"},
	}
	for key, wantValue := range want {
		if !reflect.DeepEqual(config[key], wantValue) {
			t.Errorf("loop_config[%q] = %#v, want %#v", key, config[key], wantValue)
		}
	}
	for _, timestamp := range []string{"created_at", "updated_at", "last_sent_at", "next_scheduled_at", "first_run_at", "stopped_at"} {
		if _, ok := config[timestamp].(string); !ok {
			t.Errorf("loop_config[%q] = %#v, want timestamp string", timestamp, config[timestamp])
		}
	}

	deleted := BuildLoopUpdatedData("sess-full", nil)
	if deleted["loop_config"] != nil || deleted["loop_configured"] != false || deleted["loop_enabled"] != false {
		t.Fatalf("deleted payload = %#v, want explicit nil config and false glance fields", deleted)
	}
}

func TestBuildLoopUpdatedData_PromptFields(t *testing.T) {
	tests := []struct {
		name               string
		loop               *session.LoopPrompt
		wantHasPrompt      bool
		wantPreviewPresent bool
		wantLoopConfigured bool
	}{
		{
			name:               "nil loop yields no prompt fields",
			loop:               nil,
			wantHasPrompt:      false,
			wantPreviewPresent: false,
			wantLoopConfigured: false,
		},
		{
			name: "free-text prompt yields has_prompt=true and non-empty preview",
			loop: &session.LoopPrompt{
				Prompt:    "Run the nightly report\nSecond line",
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyDays},
				Enabled:   true,
			},
			wantHasPrompt:      true,
			wantPreviewPresent: true,
			wantLoopConfigured: true,
		},
		{
			name: "named-prompt-only config yields has_prompt=true but empty preview",
			loop: &session.LoopPrompt{
				PromptName: "my-workspace-prompt",
				Frequency:  session.Frequency{Value: 30, Unit: session.FrequencyMinutes},
				Enabled:    true,
			},
			wantHasPrompt:      true,
			wantPreviewPresent: false,
			wantLoopConfigured: true,
		},
		{
			name: "legacy pending placeholder prompt yields has_prompt=false and no preview",
			loop: &session.LoopPrompt{
				Prompt:    "(pending)",
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
				Enabled:   false,
			},
			// "(pending)" is a legacy draft placeholder: it normalises to an
			// empty body, so the config has nothing deliverable.
			wantHasPrompt:      false,
			wantPreviewPresent: false,
			wantLoopConfigured: true,
		},
		{
			name: "empty draft prompt yields has_prompt=false and no preview",
			loop: &session.LoopPrompt{
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
				Enabled:   false,
			},
			wantHasPrompt:      false,
			wantPreviewPresent: false,
			wantLoopConfigured: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := BuildLoopUpdatedData("sess-123", tt.loop)

			// loop_configured
			configured, _ := data["loop_configured"].(bool)
			if configured != tt.wantLoopConfigured {
				t.Errorf("loop_configured = %v, want %v", configured, tt.wantLoopConfigured)
			}

			// loop_has_prompt
			hasPrompt, hasKey := data["loop_has_prompt"].(bool)
			if !hasKey {
				hasPrompt = false
			}
			if hasPrompt != tt.wantHasPrompt {
				t.Errorf("loop_has_prompt = %v, want %v", hasPrompt, tt.wantHasPrompt)
			}

			// loop_prompt_preview
			preview, previewPresent := data["loop_prompt_preview"].(string)
			if previewPresent && preview == "" {
				previewPresent = false
			}
			if previewPresent != tt.wantPreviewPresent {
				t.Errorf("loop_prompt_preview present = %v (value=%q), want present=%v",
					previewPresent, preview, tt.wantPreviewPresent)
			}
			if tt.wantPreviewPresent && preview == "" {
				t.Errorf("loop_prompt_preview is empty, want non-empty")
			}
		})
	}
}

// TestBuildLoopAutoPauseNotification verifies the proactive operator toast
// (mitto-e4m) is emitted only for the promptUnresolved auto-stop reason, and
// that its message names both the conversation and the unresolved prompt.
func TestBuildLoopAutoPauseNotification(t *testing.T) {
	tests := []struct {
		name        string
		sessionName string
		loop        *session.LoopPrompt
		wantOK      bool
	}{
		{
			name:        "nil loop yields no notification",
			sessionName: "My Loop",
			loop:        nil,
			wantOK:      false,
		},
		{
			name:        "promptUnresolved reason yields a notification",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				PromptName:    "feature-driver",
				StoppedReason: session.StoppedReasonPromptUnresolved,
			},
			wantOK: true,
		},
		{
			name:        "maxIterations reason stays silent",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				StoppedReason: session.StoppedReasonMaxIterations,
			},
			wantOK: false,
		},
		{
			name:        "archived reason stays silent",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				StoppedReason: session.StoppedReasonArchived,
			},
			wantOK: false,
		},
		{
			name:        "pausedByUser reason stays silent",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				StoppedReason: session.StoppedReasonPausedByUser,
			},
			wantOK: false,
		},
		{
			name:        "contextWindowExceeded reason stays silent (future extension)",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				StoppedReason: session.StoppedReasonContextWindowExceeded,
			},
			wantOK: false,
		},
		{
			// mitto-4xf: a persistently-stalling loop that auto-pauses after
			// MaxLoopDeliveryFailures consecutive delivery failures is a
			// genuine "loop gave up / needs manual intervention" signal, not
			// a benign stop — it must also raise a proactive toast so an
			// operator doesn't have to notice a quiet sidebar update.
			name:        "deliveryFailures reason yields a notification (mitto-4xf)",
			sessionName: "My Loop",
			loop: &session.LoopPrompt{
				PromptName:    "feature-driver",
				StoppedReason: session.StoppedReasonDeliveryFailures,
			},
			wantOK: true,
		},
		{
			// mitto-al8: an onSlack watcher's deliveryFailures auto-pause also
			// drops its Slack Socket Mode subscriptions — the message must
			// name that concretely and set the auto-recovery expectation.
			name:        "deliveryFailures reason on an onSlack loop mentions Socket Mode (mitto-al8)",
			sessionName: "Slack Watcher",
			loop: &session.LoopPrompt{
				PromptName:    "slack-watcher",
				StoppedReason: session.StoppedReasonDeliveryFailures,
				Triggers:      []session.LoopTrigger{session.TriggerOnSlack},
			},
			wantOK: true,
		},
		{
			name:        "empty StoppedReason (e.g. onLoopUpdated fired outside auto-stop) stays silent",
			sessionName: "My Loop",
			loop:        &session.LoopPrompt{},
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, ok := BuildLoopAutoPauseNotification(tt.sessionName, tt.loop)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				if req != (UINotifyRequest{}) {
					t.Errorf("req = %#v, want zero value when ok=false", req)
				}
				return
			}
			if req.Title == "" {
				t.Error("Title is empty, want non-empty")
			}
			if req.Style != "warning" {
				t.Errorf("Style = %q, want %q", req.Style, "warning")
			}
			if !req.Native {
				t.Error("Native = false, want true")
			}
			if !strings.Contains(req.Message, tt.sessionName) {
				t.Errorf("Message %q does not mention session name %q", req.Message, tt.sessionName)
			}
			if !strings.Contains(req.Message, tt.loop.PromptName) {
				t.Errorf("Message %q does not mention prompt name %q", req.Message, tt.loop.PromptName)
			}
		})
	}
}

// TestBuildLoopAutoPauseNotification_DeliveryFailures_SlackVsNonSlackWording
// pins the mitto-al8 message-tailoring branch in BuildLoopAutoPauseNotification:
// an onSlack loop's deliveryFailures message must call out the dropped Slack
// Socket Mode subscriptions and the auto-recovery expectation, while a
// non-onSlack loop's message must keep the original "needs manual attention"
// wording and must NOT mention Slack/Socket Mode at all.
func TestBuildLoopAutoPauseNotification_DeliveryFailures_SlackVsNonSlackWording(t *testing.T) {
	onSlack := &session.LoopPrompt{
		PromptName:    "slack-watcher",
		StoppedReason: session.StoppedReasonDeliveryFailures,
		Triggers:      []session.LoopTrigger{session.TriggerOnSlack},
	}
	req, ok := BuildLoopAutoPauseNotification("Slack Watcher", onSlack)
	if !ok {
		t.Fatal("ok = false, want true for onSlack deliveryFailures")
	}
	if !strings.Contains(req.Message, "Socket Mode") {
		t.Errorf("onSlack message %q does not mention Socket Mode", req.Message)
	}
	if !strings.Contains(req.Message, "automatically retry") {
		t.Errorf("onSlack message %q does not set the auto-recovery expectation", req.Message)
	}

	nonSlack := &session.LoopPrompt{
		PromptName:    "generic-loop",
		StoppedReason: session.StoppedReasonDeliveryFailures,
	}
	req2, ok := BuildLoopAutoPauseNotification("Generic Loop", nonSlack)
	if !ok {
		t.Fatal("ok = false, want true for non-onSlack deliveryFailures")
	}
	if strings.Contains(req2.Message, "Socket Mode") || strings.Contains(req2.Message, "Slack") {
		t.Errorf("non-onSlack message %q unexpectedly mentions Slack/Socket Mode", req2.Message)
	}
	if !strings.Contains(req2.Message, "needs manual attention") {
		t.Errorf("non-onSlack message %q lost the original wording", req2.Message)
	}
}

// TestBuildLoopSlackAutoRecoveredNotification verifies the mitto-al8 AC3
// operator-facing toast for a successful onSlack auto-recovery: info style,
// native, and mentions the conversation name, prompt name, and attempt count.
func TestBuildLoopSlackAutoRecoveredNotification(t *testing.T) {
	loop := &session.LoopPrompt{PromptName: "slack-watcher"}
	req := BuildLoopSlackAutoRecoveredNotification("Slack Watcher", loop, 2, 6)

	if req.Style != "info" {
		t.Errorf("Style = %q, want %q", req.Style, "info")
	}
	if !req.Native {
		t.Error("Native = false, want true")
	}
	if !strings.Contains(req.Message, "Slack Watcher") {
		t.Errorf("Message %q does not mention session name", req.Message)
	}
	if !strings.Contains(req.Message, "slack-watcher") {
		t.Errorf("Message %q does not mention prompt name", req.Message)
	}
	if !strings.Contains(req.Message, "2/6") {
		t.Errorf("Message %q does not mention the attempt count", req.Message)
	}
}

// TestBuildLoopSlackAutoRecoveredNotification_NilLoop verifies the builder
// tolerates a nil loop (defensive; the runner always passes a non-nil loop,
// but the builder must not panic) by falling back to an empty prompt name.
func TestBuildLoopSlackAutoRecoveredNotification_NilLoop(t *testing.T) {
	req := BuildLoopSlackAutoRecoveredNotification("", nil, 1, 6)
	if !strings.Contains(req.Message, "(unnamed conversation)") {
		t.Errorf("Message %q does not use the fallback placeholder", req.Message)
	}
}

// TestBuildLoopSlackAutoRecoveryExhaustedNotification verifies the mitto-al8
// AC2/AC3 warning toast fired once auto-recovery attempts are exhausted:
// warning style, native, and mentions the conversation name, prompt name,
// and the max-attempts ceiling.
func TestBuildLoopSlackAutoRecoveryExhaustedNotification(t *testing.T) {
	loop := &session.LoopPrompt{PromptName: "slack-watcher"}
	req := BuildLoopSlackAutoRecoveryExhaustedNotification("Slack Watcher", loop, 6)

	if req.Style != "warning" {
		t.Errorf("Style = %q, want %q", req.Style, "warning")
	}
	if !req.Native {
		t.Error("Native = false, want true")
	}
	if !strings.Contains(req.Message, "Slack Watcher") {
		t.Errorf("Message %q does not mention session name", req.Message)
	}
	if !strings.Contains(req.Message, "slack-watcher") {
		t.Errorf("Message %q does not mention prompt name", req.Message)
	}
	if !strings.Contains(req.Message, "6 attempt") {
		t.Errorf("Message %q does not mention the max-attempts ceiling", req.Message)
	}
	if !strings.Contains(req.Message, "manually re-enable") {
		t.Errorf("Message %q does not tell the operator recovery gave up", req.Message)
	}
}

// TestBuildLoopAutoPauseNotification_EmptySessionName verifies the fallback
// placeholder is used when the session has no display name yet, so the
// notification never renders an empty conversation label.
func TestBuildLoopAutoPauseNotification_EmptySessionName(t *testing.T) {
	loop := &session.LoopPrompt{
		PromptName:    "feature-driver",
		StoppedReason: session.StoppedReasonPromptUnresolved,
	}
	req, ok := BuildLoopAutoPauseNotification("", loop)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if strings.Contains(req.Message, `""`) {
		t.Errorf("Message %q renders an empty conversation name", req.Message)
	}
	if !strings.Contains(req.Message, "(unnamed conversation)") {
		t.Errorf("Message %q does not use the fallback placeholder", req.Message)
	}
}
