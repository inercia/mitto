package web

import (
	"context"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// auxiliaryClient collects agent responses for auxiliary sessions.
// It implements the SessionCallbacks interface for use with MultiplexClient.
type auxiliaryClient struct {
	mu       sync.Mutex
	response strings.Builder
}

// newAuxiliaryClient creates a new auxiliary client.
func newAuxiliaryClient() *auxiliaryClient {
	return &auxiliaryClient{}
}

// reset clears the response buffer for a new request.
func (c *auxiliaryClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.response.Reset()
}

// getResponse returns the collected response text.
func (c *auxiliaryClient) getResponse() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.response.String())
}

// OnSessionUpdate handles streaming updates from the agent.
func (c *auxiliaryClient) OnSessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update

	// Only collect agent message text
	if u.AgentMessageChunk != nil {
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.mu.Lock()
			c.response.WriteString(content.Text.Text)
			c.mu.Unlock()
		}
	}

	// Ignore tool calls, thoughts, plans, etc. for auxiliary tasks
	return nil
}

// OnRequestPermission auto-approves all permission requests.
// The auxiliary session should never block on permissions.
func (c *auxiliaryClient) OnRequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return mittoAcp.AutoApprovePermission(params.Options), nil
}

// auxTerminalStub is the shared stub handler for terminal operations.
// Uses "aux-term-1" to distinguish from main session terminals.
var auxTerminalStub = &mittoAcp.StubTerminalHandler{TerminalID: "aux-term-1"}

// OnReadTextFile handles file read requests - allow for auxiliary session.
func (c *auxiliaryClient) OnReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// OnWriteTextFile handles file write requests - deny for auxiliary session.
// The auxiliary session should not write files.
func (c *auxiliaryClient) OnWriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}
