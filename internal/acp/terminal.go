package acp

import (
	"context"

	"github.com/coder/acp-go-sdk"
)

// CreateTerminal handles terminal creation requests.
func (c *Client) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.print("\n🖥️  CreateTerminal: %v\n", params)
	return acp.CreateTerminalResponse{TerminalId: "term-1"}, nil
}

// TerminalOutput handles requests to get terminal output.
func (c *Client) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	c.print("\n🖥️  TerminalOutput: %v\n", params)
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

// ReleaseTerminal handles terminal release requests.
func (c *Client) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	c.print("\n🖥️  ReleaseTerminal: %v\n", params)
	return acp.ReleaseTerminalResponse{}, nil
}

// WaitForTerminalExit handles requests to wait for terminal exit.
func (c *Client) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	c.print("\n🖥️  WaitForTerminalExit: %v\n", params)
	return acp.WaitForTerminalExitResponse{}, nil
}

// KillTerminalCommand handles requests to kill terminal commands.
func (c *Client) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	c.print("\n🖥️  KillTerminalCommand: %v\n", params)
	return acp.KillTerminalCommandResponse{}, nil
}
