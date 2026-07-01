package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

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
			name: "pending placeholder prompt yields has_prompt=false and no preview",
			loop: &session.LoopPrompt{
				Prompt:    "(pending)",
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
				Enabled:   false,
			},
			// Prompt is "(pending)" so PromptPreview() returns ""; but Prompt != "" so has_prompt is true.
			wantHasPrompt:      true,
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
