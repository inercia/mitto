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
// requests (file/permission) to the optional ClientHooks. Terminal
// operations have no neutral ClientServices analogue yet (contracts.go only
// models ReadFile/WriteFile/RequestPermission) and always fail with
// *agentbackend.UnsupportedError until the neutral contract grows one.
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
		OnCreateTerminal:      unsupportedTerminalHandler[acp.CreateTerminalRequest, acp.CreateTerminalResponse](),
		OnTerminalOutput:      unsupportedTerminalHandler[acp.TerminalOutputRequest, acp.TerminalOutputResponse](),
		OnReleaseTerminal:     unsupportedTerminalHandler[acp.ReleaseTerminalRequest, acp.ReleaseTerminalResponse](),
		OnWaitForTerminalExit: unsupportedTerminalHandler[acp.WaitForTerminalExitRequest, acp.WaitForTerminalExitResponse](),
		OnKillTerminal:        unsupportedTerminalHandler[acp.KillTerminalRequest, acp.KillTerminalResponse](),
	}
}

// unsupportedTerminalHandler builds a SessionCallbacks terminal handler that
// always reports agentbackend.FeatureTerminals as unsupported, since the
// neutral ClientServices contract does not model terminals yet.
func unsupportedTerminalHandler[Req any, Resp any]() func(ctx context.Context, params Req) (Resp, error) {
	return func(ctx context.Context, params Req) (Resp, error) {
		var zero Resp
		return zero, &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
}
