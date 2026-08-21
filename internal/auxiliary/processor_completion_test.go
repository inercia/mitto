package auxiliary

import (
	"context"
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

	count, err := m.PromptProcessorTracked(context.Background(), "ws", "memory", dispatchID, "persist")
	if err != nil {
		t.Fatalf("PromptProcessorTracked() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("PromptProcessorTracked() save count = %d, want 2", count)
	}

	if _, err := parseProcessorCompletion(processorCompletionMarker+`{"dispatch_id":"other","save_count":1}`, dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted mismatched dispatch ID")
	}
	if _, err := parseProcessorCompletion("silent response", dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted missing acknowledgement")
	}
	if _, err := parseProcessorCompletion(processorCompletionMarker+`{"dispatch_id":"dispatch-123"}`, dispatchID); err == nil {
		t.Fatal("parseProcessorCompletion() accepted missing save count")
	}
}
