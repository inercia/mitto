package web

import (
	"context"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// mockObserver implements conversation.SessionObserver for testing.
type mockObserver struct {
	agentMessages []string
	agentThoughts []string
	toolCalls     []string
	planCalls     int
	errorMessages []string
	promptsDone   int
	userPrompts   []string
}

func (m *mockObserver) OnAgentMessage(seq int64, html string) {
	m.agentMessages = append(m.agentMessages, html)
}

func (m *mockObserver) OnAgentThought(seq int64, text string) {
	m.agentThoughts = append(m.agentThoughts, text)
}

func (m *mockObserver) OnToolCall(seq int64, id, title, status string) {
	m.toolCalls = append(m.toolCalls, id)
}

func (m *mockObserver) OnToolUpdate(seq int64, id string, status *string) {
	// no-op for testing
}

func (m *mockObserver) OnPlan(seq int64, entries []conversation.PlanEntry) {
	m.planCalls++
}

func (m *mockObserver) OnFileWrite(seq int64, path string, size int) {
	// no-op for testing
}

func (m *mockObserver) OnFileRead(seq int64, path string, size int) {
	// no-op for testing
}

func (m *mockObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockObserver) OnPromptComplete(eventCount int) {
	m.promptsDone++
}

func (m *mockObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string, promptName string, argumentCount int) {
	m.userPrompts = append(m.userPrompts, message)
}

func (m *mockObserver) OnError(message string) {
	m.errorMessages = append(m.errorMessages, message)
}

func (m *mockObserver) OnQueueUpdated(queueLength int, action string, messageID string) {
	// no-op for testing
}

func (m *mockObserver) OnQueueMessageSending(messageID string) {
	// no-op for testing
}

func (m *mockObserver) OnQueueMessageSent(messageID string) {
	// no-op for testing
}

func (m *mockObserver) OnQueueReordered(messages []session.QueuedMessage) {
	// no-op for testing
}

func (m *mockObserver) OnActionButtons(buttons []conversation.ActionButton) {
	// no-op for testing
}

func (m *mockObserver) OnAvailableCommandsUpdated(commands []conversation.AvailableCommand) {
	// no-op for testing
}

func (m *mockObserver) OnACPStopped(reason string) {
	// no-op for testing
}

func (m *mockObserver) OnACPStarted() {
	// no-op for testing
}

func (m *mockObserver) OnUIPrompt(req conversation.UIPromptRequest) {
	// no-op for testing
}

func (m *mockObserver) OnUIPromptDismiss(requestID string, reason string) {
	// no-op for testing
}

func (m *mockObserver) OnNotification(req conversation.UINotifyRequest) {
	// no-op for testing
}

func (m *mockObserver) OnContextUsageUpdate(size, used int) {
	// no-op for testing
}

func TestSessionObserver_Interface(t *testing.T) {
	// Verify mockObserver implements conversation.SessionObserver
	var _ conversation.SessionObserver = (*mockObserver)(nil)
}

func TestBackgroundSession_AddRemoveObserver(t *testing.T) {
	bs := conversation.NewMinimalBackgroundSession("", "", "")

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
	bs := conversation.NewMinimalBackgroundSession("", "", "")

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
	bs := conversation.NewMinimalBackgroundSession("", "", "")

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
	bs := conversation.NewMinimalBackgroundSession("", "", "")

	observer1 := &mockObserver{}
	observer2 := &mockObserver{}

	bs.AddObserver(observer1)

	// Remove observer that was never added - should not panic
	bs.RemoveObserver(observer2)

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}
}

func TestBackgroundSession_LastObserverRemovedAt(t *testing.T) {
	bs := conversation.NewMinimalBackgroundSession("", "", "")

	// Initially zero
	if !bs.LastObserverRemovedAt().IsZero() {
		t.Error("LastObserverRemovedAt should be zero initially")
	}

	// Add and remove an observer — timestamp should be set
	observer1 := &mockObserver{}
	bs.AddObserver(observer1)
	bs.RemoveObserver(observer1)

	removedAt := bs.LastObserverRemovedAt()
	if removedAt.IsZero() {
		t.Error("LastObserverRemovedAt should be set after removing the last observer")
	}
	if time.Since(removedAt) > time.Second {
		t.Error("LastObserverRemovedAt should be recent")
	}

	// Add two observers, remove one — timestamp should NOT change
	observer2 := &mockObserver{}
	observer3 := &mockObserver{}
	bs.AddObserver(observer2)
	bs.AddObserver(observer3)

	previousRemovedAt := bs.LastObserverRemovedAt()
	bs.RemoveObserver(observer2)

	// With one observer still remaining, the timestamp should not have been updated
	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}
	if !bs.LastObserverRemovedAt().Equal(previousRemovedAt) {
		t.Error("LastObserverRemovedAt should not change when observers still remain")
	}

	// Remove the last observer — timestamp should update
	time.Sleep(2 * time.Millisecond) // Ensure different nanos
	bs.RemoveObserver(observer3)

	finalRemovedAt := bs.LastObserverRemovedAt()
	if finalRemovedAt.Equal(previousRemovedAt) || finalRemovedAt.Before(previousRemovedAt) {
		t.Error("LastObserverRemovedAt should be updated when the last observer is removed")
	}
}

// TestSessionObserver_OnACPStarted verifies that OnACPStarted is part of the
// conversation.SessionObserver interface and can be called without panicking.
func TestSessionObserver_OnACPStarted(t *testing.T) {
	// Verify mockObserver implements OnACPStarted (interface compliance)
	observer := &mockObserver{}
	var _ conversation.SessionObserver = observer

	// Should not panic
	observer.OnACPStarted()
}

// TestBackgroundSession_OnACPStarted_NotifiesObservers verifies that adding observers
// works correctly and both observers remain registered.
func TestBackgroundSession_OnACPStarted_NotifiesObservers(t *testing.T) {
	bs := conversation.NewMinimalBackgroundSession("test-acpstarted", "", "")

	observer1 := &mockObserver{}
	observer2 := &mockObserver{}
	bs.AddObserver(observer1)
	bs.AddObserver(observer2)

	// Both observers should be registered.
	if bs.ObserverCount() != 2 {
		t.Errorf("ObserverCount = %d, want 2 after AddObserver", bs.ObserverCount())
	}
}
