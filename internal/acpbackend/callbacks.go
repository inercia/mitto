package acpbackend

import (
	"context"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// buildCallbacks constructs the conversation.SessionCallbacks the adapter
// registers with the underlying SharedProcess for a session, translating
// streamed updates into neutral Events and delegating client-service
// requests (file/permission/terminal) to the optional ClientHooks/
// TerminalHooks. With no hooks installed (the default), every request is
// answered with *agentbackend.UnsupportedError — installing hooks is
// required to opt into real behavior (mitto-lrt.11).
func (c *Connection) buildCallbacks(ref agentbackend.SessionRef) *conversation.SessionCallbacks {
	return &conversation.SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			if ev, ok := translateSessionUpdate(ref, params.Update); ok {
				c.publish(ev)
			}
			return nil
		},
		OnReadTextFile:        c.onReadTextFile(ref),
		OnWriteTextFile:       c.onWriteTextFile(ref),
		OnRequestPermission:   c.onRequestPermission(ref),
		OnCreateTerminal:      c.onCreateTerminal(ref),
		OnTerminalOutput:      c.onTerminalOutput(ref),
		OnReleaseTerminal:     c.onReleaseTerminal(ref),
		OnWaitForTerminalExit: c.onWaitForTerminalExit(ref),
		OnKillTerminal:        c.onKillTerminal(ref),
	}
}
