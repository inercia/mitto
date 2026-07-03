package conversation

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
)

// SessionCallbacks holds the per-session callback handlers that the SharedProcess
// routes ACP events to. Each BackgroundSession registers its own set of callbacks.
type SessionCallbacks struct {
	// OnSessionUpdate handles streaming updates (agent messages, thoughts, tool calls, etc.)
	OnSessionUpdate func(ctx context.Context, params acp.SessionNotification) error
	// OnReadTextFile handles file read requests from the agent.
	OnReadTextFile func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error)
	// OnWriteTextFile handles file write requests from the agent.
	OnWriteTextFile func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error)
	// OnRequestPermission handles permission requests from the agent.
	OnRequestPermission func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	// OnCreateTerminal handles terminal creation requests.
	OnCreateTerminal func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error)
	// OnTerminalOutput handles terminal output requests.
	OnTerminalOutput func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error)
	// OnReleaseTerminal handles terminal release requests.
	OnReleaseTerminal func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error)
	// OnWaitForTerminalExit handles terminal wait requests.
	OnWaitForTerminalExit func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error)
	// OnKillTerminal handles terminal kill requests.
	OnKillTerminal func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error)
}
