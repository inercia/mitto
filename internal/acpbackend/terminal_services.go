package acpbackend

import (
	"context"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// TerminalHooks lets a caller supply real terminal-execution behavior for
// the neutral agentbackend.TerminalServices contract, mirroring ClientHooks.
// A nil *TerminalHooks (the default) means every terminal request from the
// agent is answered with *agentbackend.UnsupportedError rather than
// silently executing commands with no oversight — callers that want real
// terminal support must wire it in explicitly via Connection.SetTerminalHooks.
// Real handlers (backed by the existing WebClient/internal/acp terminal
// path) are plugged in by later BackgroundSession wiring work, matching how
// ClientHooks itself is deferred to mitto-lrt.7.
type TerminalHooks struct {
	CreateTerminal      func(ctx context.Context, ref agentbackend.SessionRef, command string, args []string, cwd string, env map[string]string) (agentbackend.TerminalHandle, error)
	TerminalOutput      func(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (output string, truncated bool, exit *agentbackend.TerminalExitStatus, err error)
	WaitForTerminalExit func(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (agentbackend.TerminalExitStatus, error)
	KillTerminal        func(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error
	ReleaseTerminal     func(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error
}

// SetTerminalHooks installs the optional terminal-execution delegate used to
// answer ACP terminal/* requests via the neutral TerminalServices contract.
// Must be called before NewSession/LoadSession/ResumeSession register a
// session's callbacks for it to take effect for that session.
func (c *Connection) SetTerminalHooks(hooks *TerminalHooks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.terminalHooks = hooks
}

// CreateTerminal implements agentbackend.TerminalServices.
func (c *Connection) CreateTerminal(ctx context.Context, ref agentbackend.SessionRef, command string, args []string, cwd string, env map[string]string) (agentbackend.TerminalHandle, error) {
	c.mu.RLock()
	hooks := c.terminalHooks
	c.mu.RUnlock()
	if hooks == nil || hooks.CreateTerminal == nil {
		return "", &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
	return hooks.CreateTerminal(ctx, ref, command, args, cwd, env)
}

// TerminalOutput implements agentbackend.TerminalServices.
func (c *Connection) TerminalOutput(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (string, bool, *agentbackend.TerminalExitStatus, error) {
	c.mu.RLock()
	hooks := c.terminalHooks
	c.mu.RUnlock()
	if hooks == nil || hooks.TerminalOutput == nil {
		return "", false, nil, &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
	return hooks.TerminalOutput(ctx, ref, handle)
}

// WaitForTerminalExit implements agentbackend.TerminalServices.
func (c *Connection) WaitForTerminalExit(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) (agentbackend.TerminalExitStatus, error) {
	c.mu.RLock()
	hooks := c.terminalHooks
	c.mu.RUnlock()
	if hooks == nil || hooks.WaitForTerminalExit == nil {
		return agentbackend.TerminalExitStatus{}, &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
	return hooks.WaitForTerminalExit(ctx, ref, handle)
}

// KillTerminal implements agentbackend.TerminalServices.
func (c *Connection) KillTerminal(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
	c.mu.RLock()
	hooks := c.terminalHooks
	c.mu.RUnlock()
	if hooks == nil || hooks.KillTerminal == nil {
		return &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
	return hooks.KillTerminal(ctx, ref, handle)
}

// ReleaseTerminal implements agentbackend.TerminalServices.
func (c *Connection) ReleaseTerminal(ctx context.Context, ref agentbackend.SessionRef, handle agentbackend.TerminalHandle) error {
	c.mu.RLock()
	hooks := c.terminalHooks
	c.mu.RUnlock()
	if hooks == nil || hooks.ReleaseTerminal == nil {
		return &agentbackend.UnsupportedError{Feature: agentbackend.FeatureTerminals}
	}
	return hooks.ReleaseTerminal(ctx, ref, handle)
}

// onCreateTerminal builds the conversation.SessionCallbacks.OnCreateTerminal
// handler for ref, translating the ACP request into a
// TerminalServices.CreateTerminal call.
func (c *Connection) onCreateTerminal(ref agentbackend.SessionRef) func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
		cwd := ""
		if params.Cwd != nil {
			cwd = *params.Cwd
		}
		env := make(map[string]string, len(params.Env))
		for _, e := range params.Env {
			env[e.Name] = e.Value
		}
		handle, err := c.CreateTerminal(ctx, ref, params.Command, params.Args, cwd, env)
		if err != nil {
			return acp.CreateTerminalResponse{}, err
		}
		return acp.CreateTerminalResponse{TerminalId: string(handle)}, nil
	}
}

// onTerminalOutput builds the OnTerminalOutput handler for ref.
func (c *Connection) onTerminalOutput(ref agentbackend.SessionRef) func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
		output, truncated, exit, err := c.TerminalOutput(ctx, ref, agentbackend.TerminalHandle(params.TerminalId))
		if err != nil {
			return acp.TerminalOutputResponse{}, err
		}
		return acp.TerminalOutputResponse{Output: output, Truncated: truncated, ExitStatus: toACPExitStatus(exit)}, nil
	}
}

// onWaitForTerminalExit builds the OnWaitForTerminalExit handler for ref.
func (c *Connection) onWaitForTerminalExit(ref agentbackend.SessionRef) func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
		exit, err := c.WaitForTerminalExit(ctx, ref, agentbackend.TerminalHandle(params.TerminalId))
		if err != nil {
			return acp.WaitForTerminalExitResponse{}, err
		}
		return acp.WaitForTerminalExitResponse{ExitCode: exit.ExitCode, Signal: exit.Signal}, nil
	}
}

// onKillTerminal builds the OnKillTerminal handler for ref.
func (c *Connection) onKillTerminal(ref agentbackend.SessionRef) func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return func(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
		if err := c.KillTerminal(ctx, ref, agentbackend.TerminalHandle(params.TerminalId)); err != nil {
			return acp.KillTerminalResponse{}, err
		}
		return acp.KillTerminalResponse{}, nil
	}
}

// onReleaseTerminal builds the OnReleaseTerminal handler for ref.
func (c *Connection) onReleaseTerminal(ref agentbackend.SessionRef) func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
		if err := c.ReleaseTerminal(ctx, ref, agentbackend.TerminalHandle(params.TerminalId)); err != nil {
			return acp.ReleaseTerminalResponse{}, err
		}
		return acp.ReleaseTerminalResponse{}, nil
	}
}

// toACPExitStatus translates a neutral *agentbackend.TerminalExitStatus into
// the ACP wire shape, preserving a nil (command still running) as nil.
func toACPExitStatus(exit *agentbackend.TerminalExitStatus) *acp.TerminalExitStatus {
	if exit == nil {
		return nil
	}
	return &acp.TerminalExitStatus{ExitCode: exit.ExitCode, Signal: exit.Signal}
}

var _ agentbackend.TerminalServices = (*Connection)(nil)
