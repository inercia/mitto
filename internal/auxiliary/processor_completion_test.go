package auxiliary

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWorkspaceAuxiliaryManager_PromptProcessorTrackedRequiresMatchingAcknowledgement(t *testing.T) {
	const dispatchID = "dispatch-123"
	provider := &mockProcessProvider{
		promptFunc: func(_ context.Context, workspaceUUID, purpose, message string) (string, error) {
			if workspaceUUID != "ws" || purpose != PurposeProcessorPrefix+"memory" {
				t.Fatalf("PromptAuxiliary destination = %q/%q", workspaceUUID, purpose)
			}
			if !strings.Contains(message, dispatchID) || !strings.Contains(message, "save_count") {
				t.Fatalf("tracked prompt missing acknowledgement instruction: %q", message)
			}
			return "done\n" + processorCompletionMarker + `{"dispatch_id":"dispatch-123","save_count":2}`, nil
		},
	}
	m := NewWorkspaceAuxiliaryManager(provider, nil)

	count, message, err := m.PromptProcessorTracked(context.Background(), "ws", "memory", dispatchID, "persist")
	if err != nil {
		t.Fatalf("PromptProcessorTracked() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("PromptProcessorTracked() save count = %d, want 2", count)
	}
	if message != "done" {
		t.Fatalf("PromptProcessorTracked() message = %q, want %q", message, "done")
	}

	if _, _, err := parseProcessorCompletion(processorCompletionMarker+`{"dispatch_id":"other","save_count":1}`, dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted mismatched dispatch ID")
	}
	if _, _, err := parseProcessorCompletion("silent response", dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted missing acknowledgement")
	}
	if _, _, err := parseProcessorCompletion(processorCompletionMarker+`{"dispatch_id":"dispatch-123"}`, dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted missing save count")
	}
}

func TestParseProcessorCompletion_TolerantFormatting(t *testing.T) {
	const dispatchID = "dispatch-123"
	ackJSON := `{"dispatch_id":"dispatch-123","save_count":2}`

	tests := []struct {
		name         string
		response     string
		wantCount    int
		wantMessage  string
		wantErr      bool
		wantSentinel bool
	}{
		{
			name:        "plain last line",
			response:    "did the work\n" + processorCompletionMarker + ackJSON,
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name:        "wrapped in code fence",
			response:    "did the work\n```\n" + processorCompletionMarker + ackJSON + "\n```",
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name:        "wrapped in single backticks",
			response:    "did the work\n`" + processorCompletionMarker + ackJSON + "`",
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name:        "trailing sentence after JSON",
			response:    "did the work\n" + processorCompletionMarker + ackJSON + " Done.",
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name:        "bulleted marker line",
			response:    "did the work\n- " + processorCompletionMarker + ackJSON,
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name:        "bolded marker line",
			response:    "did the work\n**" + processorCompletionMarker + ackJSON + "**",
			wantCount:   2,
			wantMessage: "did the work",
		},
		{
			name: "multiple markers - last one wins",
			response: "did the work\n" + processorCompletionMarker + `{"dispatch_id":"dispatch-123","save_count":1}` +
				"\nmore work\n" + processorCompletionMarker + ackJSON,
			wantCount:   2,
			wantMessage: "did the work\n" + processorCompletionMarker + `{"dispatch_id":"dispatch-123","save_count":1}` + "\nmore work",
		},
		{
			name:         "genuinely missing acknowledgement",
			response:     "just some prose with no marker at all",
			wantErr:      true,
			wantSentinel: true,
		},
		{
			name:         "mismatched dispatch id still rejected",
			response:     "did the work\n" + processorCompletionMarker + `{"dispatch_id":"other","save_count":2}`,
			wantErr:      true,
			wantSentinel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, message, err := parseProcessorCompletion(tt.response, dispatchID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProcessorCompletion(%q) error = nil, want error", tt.response)
				}
				if tt.wantSentinel && !errors.Is(err, ErrProcessorCompletionAckMissing) {
					t.Fatalf("parseProcessorCompletion(%q) error = %v, want errors.Is(err, ErrProcessorCompletionAckMissing)", tt.response, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProcessorCompletion(%q) error = %v", tt.response, err)
			}
			if count != tt.wantCount {
				t.Fatalf("parseProcessorCompletion(%q) count = %d, want %d", tt.response, count, tt.wantCount)
			}
			if message != tt.wantMessage {
				t.Fatalf("parseProcessorCompletion(%q) message = %q, want %q", tt.response, message, tt.wantMessage)
			}
		})
	}
}
