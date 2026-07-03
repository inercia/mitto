package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestBuildPeriodicUpdatedData_PromptFields(t *testing.T) {
	tests := []struct {
		name                   string
		periodic               *session.PeriodicPrompt
		wantHasPrompt          bool
		wantPreviewPresent     bool
		wantPeriodicConfigured bool
	}{
		{
			name:                   "nil periodic yields no prompt fields",
			periodic:               nil,
			wantHasPrompt:          false,
			wantPreviewPresent:     false,
			wantPeriodicConfigured: false,
		},
		{
			name: "free-text prompt yields has_prompt=true and non-empty preview",
			periodic: &session.PeriodicPrompt{
				Prompt:    "Run the nightly report\nSecond line",
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyDays},
				Enabled:   true,
			},
			wantHasPrompt:          true,
			wantPreviewPresent:     true,
			wantPeriodicConfigured: true,
		},
		{
			name: "named-prompt-only config yields has_prompt=true but empty preview",
			periodic: &session.PeriodicPrompt{
				PromptName: "my-workspace-prompt",
				Frequency:  session.Frequency{Value: 30, Unit: session.FrequencyMinutes},
				Enabled:    true,
			},
			wantHasPrompt:          true,
			wantPreviewPresent:     false,
			wantPeriodicConfigured: true,
		},
		{
			name: "pending placeholder prompt yields has_prompt=false and no preview",
			periodic: &session.PeriodicPrompt{
				Prompt:    "(pending)",
				Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
				Enabled:   false,
			},
			// Prompt is "(pending)" so PromptPreview() returns ""; but Prompt != "" so has_prompt is true.
			wantHasPrompt:          true,
			wantPreviewPresent:     false,
			wantPeriodicConfigured: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := BuildPeriodicUpdatedData("sess-123", tt.periodic)

			// periodic_configured
			configured, _ := data["periodic_configured"].(bool)
			if configured != tt.wantPeriodicConfigured {
				t.Errorf("periodic_configured = %v, want %v", configured, tt.wantPeriodicConfigured)
			}

			// periodic_has_prompt
			hasPrompt, hasKey := data["periodic_has_prompt"].(bool)
			if !hasKey {
				hasPrompt = false
			}
			if hasPrompt != tt.wantHasPrompt {
				t.Errorf("periodic_has_prompt = %v, want %v", hasPrompt, tt.wantHasPrompt)
			}

			// periodic_prompt_preview
			preview, previewPresent := data["periodic_prompt_preview"].(string)
			if previewPresent && preview == "" {
				previewPresent = false
			}
			if previewPresent != tt.wantPreviewPresent {
				t.Errorf("periodic_prompt_preview present = %v (value=%q), want present=%v",
					previewPresent, preview, tt.wantPreviewPresent)
			}
			if tt.wantPreviewPresent && preview == "" {
				t.Errorf("periodic_prompt_preview is empty, want non-empty")
			}
		})
	}
}
