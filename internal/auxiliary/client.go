package auxiliary

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
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
	// Find the first "allow" option, or use the first option
	for _, opt := range params.Options {
		if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
				},
			}, nil
		}
	}

	// Fallback to first option
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
			},
		}, nil
	}

	// No options available, cancel
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// WriteTextFile handles file write requests - deny for auxiliary session.
func (c *auxiliaryClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	// Auxiliary session should not write files
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests - allow for auxiliary session.
func (c *auxiliaryClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: string(b)}, nil
}

// CreateTerminal handles terminal creation requests - stub for auxiliary session.
func (c *auxiliaryClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{TerminalId: "aux-term-1"}, nil
}

// TerminalOutput handles requests to get terminal output - stub for auxiliary session.
func (c *auxiliaryClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

// ReleaseTerminal handles terminal release requests - stub for auxiliary session.
func (c *auxiliaryClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

// WaitForTerminalExit handles requests to wait for terminal exit - stub for auxiliary session.
func (c *auxiliaryClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

// KillTerminalCommand handles requests to kill terminal commands - stub for auxiliary session.
func (c *auxiliaryClient) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, nil
}
