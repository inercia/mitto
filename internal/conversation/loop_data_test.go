package conversation

import (
	"encoding/json"
	"reflect"
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
