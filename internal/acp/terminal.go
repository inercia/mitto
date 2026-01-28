package acp

import (
	"context"

	"github.com/coder/acp-go-sdk"
)

// terminalStub is the shared stub handler for terminal operations.
var terminalStub = &StubTerminalHandler{}

// CreateTerminal handles terminal creation requests.
// The CLI client logs terminal operations for debugging purposes.
func (c *Client) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.print("\n🖥️  CreateTerminal: %v\n", params)
	return terminalStub.CreateTerminal(ctx, params)
}

// TerminalOutput handles requests to get terminal output.
func (c *Client) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	c.print("\n🖥️  TerminalOutput: %v\n", params)
	return terminalStub.TerminalOutput(ctx, params)
}

// ReleaseTerminal handles terminal release requests.
func (c *Client) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	c.print("\n🖥️  ReleaseTerminal: %v\n", params)
	return terminalStub.ReleaseTerminal(ctx, params)
}

// WaitForTerminalExit handles requests to wait for terminal exit.
func (c *Client) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	c.print("\n🖥️  WaitForTerminalExit: %v\n", params)
	return terminalStub.WaitForTerminalExit(ctx, params)
}

// KillTerminalCommand handles requests to kill terminal commands.
func (c *Client) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	c.print("\n🖥️  KillTerminalCommand: %v\n", params)
	return terminalStub.KillTerminalCommand(ctx, params)
}
