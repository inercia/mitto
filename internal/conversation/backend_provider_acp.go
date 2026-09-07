package conversation

// backend_provider_acp.go implements BackendProvider/BackendLease for ACP by
// delegating to the existing ProcessManager/SharedProcess machinery (GC,
// warmup, memory sampling, runner/sandbox, generation-fenced restart all stay
// in internal/acpproc, untouched). This is the default, production backend;
// the fake in backend_provider_fake_test.go proves the same ownership model
// works with no process/PID/runner at all.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// acpBackendProvider adapts a ProcessManager to BackendProvider.
type acpBackendProvider struct {
	pm ProcessManager
}

// NewACPBackendProvider wraps pm as a BackendProvider. pm must not be nil.
func NewACPBackendProvider(pm ProcessManager) BackendProvider {
	return &acpBackendProvider{pm: pm}
}

// AcquireSession gets-or-creates the shared ACP process for req.Workspace,
// then issues exactly the NewSession/LoadSession/ResumeSession RPC that
// today's direct callers issue for the given Intent, returning a lease that
// exposes the resulting SharedProcess+SessionHandle unchanged via the
// ACP-only escape hatches.
func (p *acpBackendProvider) AcquireSession(ctx context.Context, req AcquireRequest) (BackendLease, error) {
	if p == nil || p.pm == nil {
		return nil, agentbackend.ErrNotConnected
	}

	process, err := p.pm.GetOrCreateProcess(req.Workspace, req.ACPCommand, req.ACPCwd, req.ACPEnv, req.Runner, req.Prewarm)
	if err != nil {
		return nil, err
	}
	if process == nil {
		return nil, agentbackend.ErrNotConnected
	}

	providerSessionID := string(req.Session.ProviderSession)
	var handle *SessionHandle
	switch req.Intent {
	case IntentNew:
		handle, err = process.NewSession(ctx, req.CWD, req.MCPServers)
	case IntentLoad:
		handle, err = process.LoadSession(ctx, providerSessionID, req.CWD, req.MCPServers)
	case IntentResume:
		handle, err = process.ResumeSession(ctx, providerSessionID, req.CWD, req.MCPServers)
	default:
		return nil, fmt.Errorf("conversation: unknown acquire intent %d", req.Intent)
	}
	if err != nil {
		return nil, err
	}

	ref := req.Session
	ref.ProviderSession = agentbackend.ProviderSessionID(handle.SessionID)

	return &acpLease{
		process:    process,
		handle:     handle,
		sessionID:  acp.SessionId(handle.SessionID),
		ref:        ref,
		cwd:        req.CWD,
		mcpServers: req.MCPServers,
	}, nil
}

// acpLease implements BackendLease over a SharedProcess + SessionHandle.
type acpLease struct {
	process    SharedProcess
	sessionID  acp.SessionId
	cwd        string
	mcpServers []acp.McpServer

	mu     sync.Mutex
	handle *SessionHandle
	ref    agentbackend.SessionRef

	detached atomic.Bool

	reconnectMu       sync.Mutex
	reconnectInFlight bool
	reconnectDone     chan struct{}
	reconnectErr      error
}

func (l *acpLease) Ref() agentbackend.SessionRef {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ref
}

// State reports Stopped once the underlying OS process has exited,
// Disconnected once this lease has been detached, and Connected otherwise.
func (l *acpLease) State() agentbackend.LifecycleState {
	select {
	case <-l.process.ProcessDone():
		return agentbackend.LifecycleStopped
	default:
	}
	if l.detached.Load() {
		return agentbackend.LifecycleDisconnected
	}
	return agentbackend.LifecycleConnected
}

func (l *acpLease) Capabilities() agentbackend.Capabilities {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &acpCapabilities{handle: l.handle, agentCaps: l.process.Capabilities()}
}

// Detach unregisters this session from the shared process's multiplex layer.
// It never kills the shared OS process, which other sessions may still own.
func (l *acpLease) Detach() {
	l.detached.Store(true)
	l.process.UnregisterSession(l.sessionID)
}

// Terminate restarts the underlying shared OS process (generation-fenced, so
// concurrent Terminate/restart callers observing the same death only cause
// one actual restart). Always supported for ACP.
func (l *acpLease) Terminate(ctx context.Context) error {
	return l.process.Restart(l.process.Generation())
}

// Reconnect re-issues ResumeSession for this lease's session ID. Concurrent
// callers coalesce onto a single in-flight attempt (single-flight), mirroring
// SessionManager's pendingResumes coalescer, so a lost connection is never
// resumed twice nor a possibly-accepted prompt replayed by a duplicate
// attempt.
func (l *acpLease) Reconnect(ctx context.Context) error {
	l.reconnectMu.Lock()
	if l.reconnectInFlight {
		done := l.reconnectDone
		l.reconnectMu.Unlock()
		select {
		case <-done:
			l.reconnectMu.Lock()
			err := l.reconnectErr
			l.reconnectMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.reconnectInFlight = true
	done := make(chan struct{})
	l.reconnectDone = done
	l.reconnectMu.Unlock()

	handle, err := l.process.ResumeSession(ctx, string(l.sessionID), l.cwd, l.mcpServers)

	l.reconnectMu.Lock()
	l.reconnectErr = err
	l.reconnectInFlight = false
	close(done)
	l.reconnectMu.Unlock()

	if err == nil {
		l.mu.Lock()
		l.handle = handle
		l.ref.ProviderSession = agentbackend.ProviderSessionID(handle.SessionID)
		l.mu.Unlock()
		l.detached.Store(false)
	}
	return err
}

// LocalProcess exposes the underlying SharedProcess so the existing ACP
// prompt/streaming pipeline keeps flowing through it unchanged.
func (l *acpLease) LocalProcess() (SharedProcess, bool) {
	return l.process, l.process != nil
}

// SessionHandle exposes the raw handle from NewSession/LoadSession/ResumeSession.
func (l *acpLease) SessionHandle() (*SessionHandle, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handle, l.handle != nil
}

// acpCapabilities adapts *acp.AgentCapabilities + *SessionHandle to
// agentbackend.Capabilities. Only features directly knowable from those two
// sources are answered definitively; everything else reports
// CapabilityUnknown rather than guessing (per agentbackend.Capabilities'
// contract).
type acpCapabilities struct {
	handle    *SessionHandle
	agentCaps *acp.AgentCapabilities
}

func (c *acpCapabilities) Query(feature agentbackend.Feature) agentbackend.CapabilityState {
	switch feature {
	case agentbackend.FeatureImages:
		if c.agentCaps == nil {
			return agentbackend.CapabilityUnknown
		}
		if c.agentCaps.PromptCapabilities.Image {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureModelSelection:
		if c.handle != nil && c.handle.Models != nil {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnknown
	case agentbackend.FeatureModeSelection:
		if c.handle != nil && c.handle.Modes != nil {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnknown
	default:
		return agentbackend.CapabilityUnknown
	}
}

// Compile-time assertions that acpBackendProvider/acpLease/acpCapabilities
// satisfy the neutral seam, mirroring the acpproc `var _ conversation.SharedProcess
// = (*SharedACPProcess)(nil)` convention so interface drift fails the build
// here rather than at call sites.
var (
	_ BackendProvider           = (*acpBackendProvider)(nil)
	_ BackendLease              = (*acpLease)(nil)
	_ agentbackend.Capabilities = (*acpCapabilities)(nil)
)
