// Package acp provides ACP (Agent Communication Protocol) client implementation.
package acp

import (
	"context"

	"github.com/coder/acp-go-sdk"
)

// TerminalHandler defines the interface for terminal operations in ACP.
// This interface matches the terminal-related methods required by acp.Client.
type TerminalHandler interface {
	CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error)
	TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error)
	ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error)
	WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error)
	KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error)
}

// StubTerminalHandler provides stub implementations for terminal operations.
// It can be embedded in ACP client implementations that don't need real terminal support.
// The TerminalID field can be customized; it defaults to "term-1".
type StubTerminalHandler struct {
	// TerminalID is the ID returned when creating a terminal.
	// Defaults to "term-1" if empty.
	TerminalID string
}

// Ensure StubTerminalHandler implements TerminalHandler at compile time.
var _ TerminalHandler = (*StubTerminalHandler)(nil)

// getTerminalID returns the terminal ID, using a default if not set.
func (s *StubTerminalHandler) getTerminalID() string {
	if s.TerminalID != "" {
		return s.TerminalID
	}
	return "term-1"
}

// CreateTerminal returns a stub terminal response.
func (s *StubTerminalHandler) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{TerminalId: s.getTerminalID()}, nil
}

// TerminalOutput returns an empty output response.
func (s *StubTerminalHandler) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

// ReleaseTerminal returns an empty response.
func (s *StubTerminalHandler) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

// WaitForTerminalExit returns an empty response.
func (s *StubTerminalHandler) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

// KillTerminalCommand returns an empty response.
func (s *StubTerminalHandler) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, nil
}
