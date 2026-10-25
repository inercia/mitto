package mcpserver

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// ModelTagCallerTestHarness exposes the existing MCP caller fixture to external
// tests. It exists only to let those tests use a real conversation.BackgroundSession
// without creating the conversation↔mcpserver import cycle in this package's tests.
type ModelTagCallerTestHarness struct {
	store    *session.Store
	server   *Server
	manager  *mockSessionManagerForModelTag
	callerID string
}

// NewModelTagCallerTestHarness creates the same registered MCP caller fixture
// used by the model_tag handler tests.
func NewModelTagCallerTestHarness(t *testing.T) *ModelTagCallerTestHarness {
	t.Helper()
	store, server, manager, callerID := setupModelTagServer(t)
	return &ModelTagCallerTestHarness{server: server, manager: manager, callerID: callerID, store: store}
}

// AddRunningTarget registers target in the MCP fixture and installs session as
// its live BackgroundSession.
func (h *ModelTagCallerTestHarness) AddRunningTarget(t *testing.T, session BackgroundSession) string {
	t.Helper()
	id, _ := registerRunningTarget(t, h.store, h.server, h.manager)
	h.manager.sessions[id] = session
	return id
}

// UpdateModelTag calls the real mitto_conversation_update model_tag handler.
func (h *ModelTagCallerTestHarness) UpdateModelTag(ctx context.Context, targetID, tag string) (ConversationUpdateOutput, error) {
	_, out, err := h.server.handleConversationUpdate(ctx, nil, ConversationUpdateInput{
		SelfID: h.callerID, ConversationID: targetID, ModelTag: &tag,
	})
	return out, err
}
