package conversation

// backend_provider_acp.go implements BackendProvider/BackendLease for ACP by
// delegating to the existing ProcessManager/SharedProcess machinery (GC,
// warmup, memory sampling, runner/sandbox, generation-fenced restart all stay
// in internal/acpproc, untouched). This is the default, production backend;
// the fake in backend_provider_fake_test.go proves the same ownership model
// works with no process/PID/runner at all.

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	// DeferSession: return a process-only lease, skipping the session RPC
	// entirely (see AcquireRequest.DeferSession doc). The lease's
	// SessionHandle is nil until the caller performs its own deferred
	// handshake via LocalProcess() and, once that completes, callers still
	// route teardown through the SharedProcess directly today — lease-based
	// Detach/Bind for the deferred path is follow-up work.
	if req.DeferSession {
		return &acpLease{
			process:    process,
			ref:        req.Session,
			cwd:        req.CWD,
			mcpServers: req.MCPServers,
		}, nil
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
// A lease acquired with AcquireRequest.DeferSession and never bound to a real
// ACP session ID (sessionID still "") has nothing registered to unregister —
// detaching it is a safe no-op rather than unregistering a bogus empty ID.
func (l *acpLease) Detach() {
	l.detached.Store(true)
	if l.sessionID == "" {
		return
	}
	l.process.UnregisterSession(l.sessionID)
}

// Bind attaches this lease to the real ACP session ID established by a
// deferred handshake (AcquireRequest.DeferSession): AcquireSession returns
// such a lease with sessionID empty, so Detach/Reconnect have nothing to
// target until the caller's own session/new|load|resume RPC completes and
// calls Bind with the resulting identity.
func (l *acpLease) Bind(ref agentbackend.SessionRef) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ref = ref
	l.sessionID = acp.SessionId(ref.ProviderSession)
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

// SessionOps returns the neutral Prompt/Cancel/SetModel/SetMode seam for
// this lease's session, once bound. A DeferSession lease starts with
// sessionID empty (see AcquireRequest.DeferSession) until the caller's own
// deferred handshake completes and calls Bind — before that, ok is false and
// callers must keep using their existing LocalProcess()-based path.
func (l *acpLease) SessionOps() (SessionPromptOps, agentbackend.SessionRef, bool) {
	l.mu.Lock()
	ref := l.ref
	sessionID := l.sessionID
	l.mu.Unlock()
	if sessionID == "" {
		return nil, agentbackend.SessionRef{}, false
	}
	return &acpSessionPromptOps{process: l.process}, ref, true
}

// acpSessionPromptOps adapts an already-established SharedProcess onto
// SessionPromptOps's Prompt/Cancel/SetModel/SetMode surface for one lease's
// session, identified per-call by the SessionRef.ProviderSession the caller
// passes in (mirroring agentbackend.SessionOps's own contract). It
// deliberately does NOT implement session creation or registration — those
// stay owned by acpBackendProvider.AcquireSession / acpLease.Bind, per
// SessionPromptOps's own doc.
//
// The small ACP<->neutral translations below (content blocks, stop reason,
// method-not-found -> UnsupportedError) duplicate a handful of lines already
// factored out into internal/acpbackend's dedicated translators
// (content.go, outcome.go, errors.go) rather than importing that package:
// internal/acpbackend already imports internal/conversation (for
// conversation.SharedProcess), so importing it back here would cycle. This
// mirrors the existing acpCapabilities translator in this same file, which
// makes the same tradeoff for the same reason.
//
// Note: Prompt's returned PromptOutcome carries only StopReason/Content —
// agentbackend.PromptOutcome has no field for ACP's per-turn Usage yet, so
// this seam is only wired into call sites that don't need it (Cancel/
// SetModel/SetMode, and the response-discarding context-flush Prompt in
// flushContextInPlace). The main prompt-loop Prompt call keeps using
// SharedProcess directly until PromptOutcome grows a Usage field, to avoid a
// token-usage-accounting regression (tracked as mx9.1.2 follow-up).
type acpSessionPromptOps struct {
	process SharedProcess
}

// acpLeaseJSONRPCMethodNotFound is the standard JSON-RPC "Method not found"
// error code, mirroring internal/acpbackend's jsonRPCMethodNotFound (see the
// package-cycle note on acpSessionPromptOps above for why it's duplicated
// here instead of imported).
const acpLeaseJSONRPCMethodNotFound = -32601

// translateACPLeaseError maps a raw ACP/acpproc error into the agentbackend
// sentinel errors, mirroring internal/acpbackend's translateError.
func translateACPLeaseError(err error, feature agentbackend.Feature) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return agentbackend.ErrCancelled
	}
	var reqErr *acp.RequestError
	if errors.As(err, &reqErr) && reqErr.Code == acpLeaseJSONRPCMethodNotFound {
		return &agentbackend.UnsupportedError{Feature: feature}
	}
	return err
}

// acpLeaseStopReasonToNeutral translates an ACP stop reason into its neutral
// counterpart, mirroring internal/acpbackend's ToNeutralStopReason.
func acpLeaseStopReasonToNeutral(r acp.StopReason) agentbackend.StopReason {
	switch r {
	case acp.StopReasonEndTurn:
		return agentbackend.StopReasonEndTurn
	case acp.StopReasonCancelled:
		return agentbackend.StopReasonCancelled
	case acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests:
		return agentbackend.StopReasonMaxTokens
	case acp.StopReasonRefusal:
		return agentbackend.StopReasonRefusal
	default:
		return agentbackend.StopReasonError
	}
}

// acpLeaseContentBlocksToACP translates neutral content blocks into their
// ACP counterparts, mirroring internal/acpbackend's FromNeutralContentBlock.
func acpLeaseContentBlocksToACP(blocks []agentbackend.ContentBlock) []acp.ContentBlock {
	out := make([]acp.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, acp.TextBlock(b.Text.Text))
		case b.Image != nil:
			out = append(out, acp.ImageBlock(b.Image.Data, b.Image.MimeType))
		case b.File != nil:
			out = append(out, acp.ResourceLinkBlock(b.File.Path, acpLeaseFileURI(b.File.Path)))
		}
	}
	return out
}

// acpLeaseFileURI mirrors internal/acpbackend's fileURI.
func acpLeaseFileURI(path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	return "file://" + path
}

// acpLeaseContentBlocksToNeutral translates ACP content blocks into their
// neutral counterparts, mirroring internal/acpbackend's
// ToNeutralContentBlocks. Used by hot-path callers (e.g.
// BackgroundSession.flushContextInPlace) that already built ACP-shaped
// blocks and need to route them through SessionPromptOps.Prompt. Content
// kinds without a neutral analogue (Audio, embedded Resource) are silently
// skipped, exactly like the acpbackend original.
func acpLeaseContentBlocksToNeutral(blocks []acp.ContentBlock) []agentbackend.ContentBlock {
	out := make([]agentbackend.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, agentbackend.ContentBlock{Text: &agentbackend.TextBlock{Text: b.Text.Text}})
		case b.Image != nil:
			out = append(out, agentbackend.ContentBlock{Image: &agentbackend.ImageBlock{Data: b.Image.Data, MimeType: b.Image.MimeType}})
		case b.ResourceLink != nil:
			mime := ""
			if b.ResourceLink.MimeType != nil {
				mime = *b.ResourceLink.MimeType
			}
			out = append(out, agentbackend.ContentBlock{File: &agentbackend.FileBlock{Path: b.ResourceLink.Uri, MimeType: mime}})
		}
	}
	return out
}

func (o *acpSessionPromptOps) Prompt(ctx context.Context, ref agentbackend.SessionRef, content []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error) {
	resp, err := o.process.Prompt(ctx, acp.SessionId(ref.ProviderSession), acpLeaseContentBlocksToACP(content))
	if err != nil {
		return agentbackend.PromptOutcome{}, translateACPLeaseError(err, "")
	}
	return agentbackend.PromptOutcome{StopReason: acpLeaseStopReasonToNeutral(resp.StopReason)}, nil
}

func (o *acpSessionPromptOps) Cancel(ctx context.Context, ref agentbackend.SessionRef) error {
	return translateACPLeaseError(o.process.Cancel(ctx, acp.SessionId(ref.ProviderSession)), "")
}

func (o *acpSessionPromptOps) SetModel(ctx context.Context, ref agentbackend.SessionRef, modelID string) error {
	err := o.process.SetSessionModel(ctx, acp.SessionId(ref.ProviderSession), modelID)
	return translateACPLeaseError(err, agentbackend.FeatureModelSelection)
}

func (o *acpSessionPromptOps) SetMode(ctx context.Context, ref agentbackend.SessionRef, modeID string) error {
	err := o.process.SetSessionMode(ctx, acp.SessionId(ref.ProviderSession), modeID)
	return translateACPLeaseError(err, agentbackend.FeatureModeSelection)
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
	_ SessionPromptOps          = (*acpSessionPromptOps)(nil)
)
