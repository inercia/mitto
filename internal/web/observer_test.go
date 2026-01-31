package web

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
)

// mockObserver implements SessionObserver for testing.
type mockObserver struct {
	agentMessages []string
	agentThoughts []string
	toolCalls     []string
	planCalls     int
	errorMessages []string
	promptsDone   int
	userPrompts   []string
}

func (m *mockObserver) OnAgentMessage(html string) {
	m.agentMessages = append(m.agentMessages, html)
}

func (m *mockObserver) OnAgentThought(text string) {
	m.agentThoughts = append(m.agentThoughts, text)
}

func (m *mockObserver) OnToolCall(id, title, status string) {
	m.toolCalls = append(m.toolCalls, id)
}

func (m *mockObserver) OnToolUpdate(id string, status *string) {
	// no-op for testing
}

func (m *mockObserver) OnPlan() {
	m.planCalls++
}

func (m *mockObserver) OnFileWrite(path string, size int) {
	// no-op for testing
}

func (m *mockObserver) OnFileRead(path string, size int) {
	// no-op for testing
}

func (m *mockObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockObserver) OnPromptComplete(eventCount int) {
	m.promptsDone++
}

func (m *mockObserver) OnUserPrompt(senderID, promptID, message string, imageIDs []string) {
	m.userPrompts = append(m.userPrompts, message)
}

func (m *mockObserver) OnError(message string) {
	m.errorMessages = append(m.errorMessages, message)
}

func TestSessionObserver_Interface(t *testing.T) {
	// Verify mockObserver implements SessionObserver
	var _ SessionObserver = (*mockObserver)(nil)
}

func TestBackgroundSession_AddRemoveObserver(t *testing.T) {
	bs := &BackgroundSession{}

	observer := &mockObserver{}

	// Initially no observers
	if bs.ObserverCount() != 0 {
		t.Errorf("ObserverCount = %d, want 0", bs.ObserverCount())
	}

	// Add observer
	bs.AddObserver(observer)
	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}

	// Add same observer again (should not duplicate)
	bs.AddObserver(observer)
	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1 (no duplicates)", bs.ObserverCount())
	}

	// Remove observer
	bs.RemoveObserver(observer)
	if bs.ObserverCount() != 0 {
		t.Errorf("ObserverCount = %d, want 0", bs.ObserverCount())
	}
}

func TestBackgroundSession_HasObservers(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.HasObservers() {
		t.Error("HasObservers should return false when no observers")
	}

	observer := &mockObserver{}
	bs.AddObserver(observer)

	if !bs.HasObservers() {
		t.Error("HasObservers should return true when observers exist")
	}
}

func TestBackgroundSession_MultipleObservers(t *testing.T) {
	bs := &BackgroundSession{}

	observer1 := &mockObserver{}
	observer2 := &mockObserver{}
	observer3 := &mockObserver{}

	bs.AddObserver(observer1)
	bs.AddObserver(observer2)
	bs.AddObserver(observer3)

	if bs.ObserverCount() != 3 {
		t.Errorf("ObserverCount = %d, want 3", bs.ObserverCount())
	}

	// Remove middle observer
	bs.RemoveObserver(observer2)
	if bs.ObserverCount() != 2 {
		t.Errorf("ObserverCount = %d, want 2", bs.ObserverCount())
	}
}

func TestBackgroundSession_RemoveNonExistentObserver(t *testing.T) {
	bs := &BackgroundSession{}

	observer1 := &mockObserver{}
	observer2 := &mockObserver{}

	bs.AddObserver(observer1)

	// Remove observer that was never added - should not panic
	bs.RemoveObserver(observer2)

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}
}
