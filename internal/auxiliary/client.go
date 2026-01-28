package auxiliary

import (
	"context"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// auxiliaryClient implements acp.Client for the auxiliary session.
// It collects agent responses into a buffer and auto-approves all permissions.
type auxiliaryClient struct {
	mu       sync.Mutex
	response strings.Builder
}

// Ensure auxiliaryClient implements acp.Client
var _ acp.Client = (*auxiliaryClient)(nil)

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

// SessionUpdate handles streaming updates from the agent.
func (c *auxiliaryClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
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

// RequestPermission auto-approves all permission requests.
// The auxiliary session should never block on permissions.
func (c *auxiliaryClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return mittoAcp.AutoApprovePermission(params.Options), nil
}

// auxTerminalStub is the shared stub handler for terminal operations.
// Uses "aux-term-1" to distinguish from main session terminals.
var auxTerminalStub = &mittoAcp.StubTerminalHandler{TerminalID: "aux-term-1"}

// WriteTextFile handles file write requests - deny for auxiliary session.
// The auxiliary session should not write files.
func (c *auxiliaryClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests - allow for auxiliary session.
func (c *auxiliaryClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// CreateTerminal handles terminal creation requests - stub for auxiliary session.
func (c *auxiliaryClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return auxTerminalStub.CreateTerminal(ctx, params)
}

// TerminalOutput handles requests to get terminal output - stub for auxiliary session.
func (c *auxiliaryClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return auxTerminalStub.TerminalOutput(ctx, params)
}

// ReleaseTerminal handles terminal release requests - stub for auxiliary session.
func (c *auxiliaryClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return auxTerminalStub.ReleaseTerminal(ctx, params)
}

// WaitForTerminalExit handles requests to wait for terminal exit - stub for auxiliary session.
func (c *auxiliaryClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return auxTerminalStub.WaitForTerminalExit(ctx, params)
}

// KillTerminalCommand handles requests to kill terminal commands - stub for auxiliary session.
func (c *auxiliaryClient) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return auxTerminalStub.KillTerminalCommand(ctx, params)
}
