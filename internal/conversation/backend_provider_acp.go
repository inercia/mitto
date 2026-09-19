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
			provider:   req.Agent.Provider,
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
		provider:   req.Agent.Provider,
		ref:        ref,
		cwd:        req.CWD,
		mcpServers: req.MCPServers,
	}, nil
}

// acpLease implements BackendLease over a SharedProcess + SessionHandle.
type acpLease struct {
	process    SharedProcess
	sessionID  acp.SessionId
	provider   agentbackend.ProviderID // set once at construction from AcquireRequest.Agent.Provider; never mutated afterwards
	cwd        string
	mcpServers []agentbackend.MCPServerDescriptor

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
	return &acpCapabilities{handle: l.handle, processCaps: l.process.Capabilities()}
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
	l.process.UnregisterSession(string(l.sessionID))
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

// Providers implements ProviderDiscoverer by reporting this lease's single
// configured provider. It mirrors acpbackend.Connection.Providers (the ACP
// protocol runs exactly one agent per process) but cannot delegate to it
// directly: internal/acpbackend already imports internal/conversation (for
// conversation.SharedProcess), so importing it back here would cycle — see
// the acpSessionPromptOps package-cycle note above for the same tradeoff.
// l.provider is set once at construction and never mutated, so this is a
// pure, lock-free read of already-established connection state; no local
// install/status/MCP script is invoked.
func (l *acpLease) Providers(ctx context.Context) ([]agentbackend.ProviderID, error) {
	return []agentbackend.ProviderID{l.provider}, nil
}

// ProviderDiscoverer implements BackendLease.ProviderDiscoverer. A lease
// with no provider identity (should not occur for a lease actually returned
// by AcquireSession, which always sets it from AcquireRequest.Agent.Provider)
// reports (nil, false) rather than a discoverer that would report an
// invalid empty ProviderID.
func (l *acpLease) ProviderDiscoverer() (ProviderDiscoverer, bool) {
	if l.provider == "" {
		return nil, false
	}
	return l, true
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
// mitto-mx9.1.1: SharedProcess.Prompt/Cancel/SetSessionMode/SetSessionModel
// now take/return agentbackend types directly (no acp.* type named), so this
// adapter is a thin passthrough + error translation — the neutral<->ACP
// translation itself now lives at the SharedProcess implementation's own
// boundary (internal/acpproc.SharedACPProcess). agentbackend.PromptOutcome
// also now carries per-turn token usage (PromptUsage), routed straight
// through from the implementation.
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
//
// mitto-mx9.3: this is an intentional duplicate, not dead drift-risk code.
// internal/acpbackend already imports internal/conversation (capabilities.go
// uses conversation.SessionModelState), so internal/conversation cannot
// import internal/acpbackend without creating a cycle — this package cannot
// call acpbackend.ToNeutralStopReason directly. The two mappings are pinned
// against each other by a parity test (added in the Test phase) rather than
// consolidated; breaking the cycle (e.g. moving SessionModelState to a leaf
// package) is tracked as a separate, deliberately out-of-scope follow-up.
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
	outcome, err := o.process.Prompt(ctx, string(ref.ProviderSession), content)
	if err != nil {
		return agentbackend.PromptOutcome{}, translateACPLeaseError(err, "")
	}
	return outcome, nil
}

func (o *acpSessionPromptOps) Cancel(ctx context.Context, ref agentbackend.SessionRef) error {
	return translateACPLeaseError(o.process.Cancel(ctx, string(ref.ProviderSession)), "")
}

func (o *acpSessionPromptOps) SetModel(ctx context.Context, ref agentbackend.SessionRef, modelID string) error {
	err := o.process.SetSessionModel(ctx, string(ref.ProviderSession), modelID)
	return translateACPLeaseError(err, agentbackend.FeatureModelSelection)
}

func (o *acpSessionPromptOps) SetMode(ctx context.Context, ref agentbackend.SessionRef, modeID string) error {
	err := o.process.SetSessionMode(ctx, string(ref.ProviderSession), modeID)
	return translateACPLeaseError(err, agentbackend.FeatureModeSelection)
}

// acpLeaseStopReasonFromNeutral is the reverse of acpLeaseStopReasonToNeutral,
// used by promptOutcomeToACPResponse to reconstruct an acp.PromptResponse-
// shaped value from a neutral PromptOutcome for the main prompt-loop's
// existing ACP-typed downstream bookkeeping (token accounting, follow-up
// analysis — bgsession_prompt.go/prompt_dispatcher.go/follow_up_coordinator.go,
// out of scope for mitto-mx9.1.1). The mapping is lossy in this direction only
// for agentbackend.StopReasonMaxTokens, which collapses both acp.
// StopReasonMaxTokens and acp.StopReasonMaxTurnRequests on the way in (see
// acpLeaseStopReasonToNeutral) and so always reconstructs as acp.
// StopReasonMaxTokens specifically; no consumer of the reconstructed value
// distinguishes the two ACP variants (only StopReasonEndTurn is checked).
func acpLeaseStopReasonFromNeutral(r agentbackend.StopReason) acp.StopReason {
	switch r {
	case agentbackend.StopReasonEndTurn:
		return acp.StopReasonEndTurn
	case agentbackend.StopReasonCancelled:
		return acp.StopReasonCancelled
	case agentbackend.StopReasonMaxTokens:
		return acp.StopReasonMaxTokens
	case agentbackend.StopReasonRefusal:
		return acp.StopReasonRefusal
	default:
		// No ACP stop reason represents a generic "error" terminal state;
		// leaving this empty is a safe default since the only production
		// comparison is against acp.StopReasonEndTurn.
		return acp.StopReason("")
	}
}

// acpLeaseUsageFromNeutral reconstructs an *acp.Usage from the neutral
// PromptUsage for the same reconstruction purpose as
// acpLeaseStopReasonFromNeutral. Returns nil when u is nil.
func acpLeaseUsageFromNeutral(u *agentbackend.PromptUsage) *acp.Usage {
	if u == nil {
		return nil
	}
	return &acp.Usage{
		InputTokens:  int(u.InputTokens),
		OutputTokens: int(u.OutputTokens),
		TotalTokens:  int(u.TotalTokens),
	}
}

// promptOutcomeToACPResponse reconstructs an acp.PromptResponse-shaped value
// from a neutral PromptOutcome. Used only by the main prompt-loop
// (bgsession_prompt.go) when it routes its Prompt RPC through the neutral
// SharedProcess.Prompt seam (mitto-mx9.1.1 acceptance: "all 4 planned
// hot-path sites route through the neutral seam") but must keep feeding its
// existing ACP-typed downstream pipeline (accumulateTokenUsage,
// handlePromptSuccess, the follow-up/after-processors pipeline) unchanged —
// neutralizing that pipeline's own types is out of scope for this bead.
// Token-accounting parity is exact for the 3 fields that pipeline reads
// (Input/Output/TotalTokens); see acpLeaseStopReasonFromNeutral for the one
// accepted lossy mapping.
func promptOutcomeToACPResponse(o agentbackend.PromptOutcome) acp.PromptResponse {
	return acp.PromptResponse{
		StopReason: acpLeaseStopReasonFromNeutral(o.StopReason),
		Usage:      acpLeaseUsageFromNeutral(o.Usage),
	}
}

// acpCapabilities adapts a process-level agentbackend.Capabilities (as
// returned by SharedProcess.Capabilities()) + *SessionHandle to a full,
// session-aware agentbackend.Capabilities. Model/mode selection is a
// per-session fact only the handle knows about; everything else (Images,
// MCP-HTTP, ...) is a process-level fact delegated to processCaps.
//
// mitto-mx9.3: mirrors internal/acpbackend's sessionCapabilities (same
// cycle constraint as acpLeaseStopReasonToNeutral above — acpbackend already
// imports this package, so this package cannot import acpbackend back). The
// two Query() implementations are pinned against each other by a parity
// test (added in the Test phase) for the shared feature set.
type acpCapabilities struct {
	handle      *SessionHandle
	processCaps agentbackend.Capabilities
}

func (c *acpCapabilities) Query(feature agentbackend.Feature) agentbackend.CapabilityState {
	switch feature {
	case agentbackend.FeatureFiles:
		// Mitto's ACP Initialize handshake always advertises
		// ClientCapabilities.Fs{ReadTextFile,WriteTextFile}=true (see
		// internal/acpproc.SharedACPProcess's Initialize call) independent of
		// which agent is connected, so file support is a constant fact about
		// this host, not something to read off the agent's capabilities.
		// Parity with internal/acpbackend's sessionCapabilities.Query
		// (mitto-mx9.8).
		return agentbackend.CapabilitySupported
	case agentbackend.FeaturePermissions:
		// Mitto always wires a permission handler
		// (SessionCallbacks.OnRequestPermission), auto-approving when no
		// interactive client is present — likewise a constant fact about this
		// host rather than an agent-advertised capability. Parity with
		// internal/acpbackend's sessionCapabilities.Query (mitto-mx9.8).
		return agentbackend.CapabilitySupported
	case agentbackend.FeatureModelSelection:
		if c.handle != nil && c.handle.Models != nil && len(c.handle.Models.AvailableModels) > 0 {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureModeSelection:
		if c.handle != nil && c.handle.Modes != nil && len(c.handle.Modes.Available) > 0 {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	default:
		if c.processCaps == nil {
			return agentbackend.CapabilityUnknown
		}
		return c.processCaps.Query(feature)
	}
}

// acpProcessCapabilities adapts a raw *acp.AgentCapabilities snapshot (as
// held by a SharedProcess implementation — production: internal/acpproc;
// tests: fakes across this package and internal/acpbackend) to
// agentbackend.Capabilities. Only process-level facts directly knowable from
// AgentCapabilities are answered definitively; everything else reports
// CapabilityUnknown rather than guessing (per agentbackend.Capabilities'
// contract).
type acpProcessCapabilities struct {
	caps *acp.AgentCapabilities
}

// NewProcessCapabilities wraps a raw ACP AgentCapabilities snapshot (possibly
// nil, e.g. before Initialize completes) as a protocol-neutral
// agentbackend.Capabilities value. Exported for SharedProcess implementations
// outside this package (internal/acpproc) and their test doubles.
func NewProcessCapabilities(caps *acp.AgentCapabilities) agentbackend.Capabilities {
	return &acpProcessCapabilities{caps: caps}
}

func (c *acpProcessCapabilities) Query(feature agentbackend.Feature) agentbackend.CapabilityState {
	if c.caps == nil {
		return agentbackend.CapabilityUnknown
	}
	switch feature {
	case agentbackend.FeatureImages:
		if c.caps.PromptCapabilities.Image {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureMCPHttp:
		if c.caps.McpCapabilities.Http {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureSessionResume:
		if c.caps.SessionCapabilities.Resume != nil {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureSessionLoad:
		if c.caps.LoadSession {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	default:
		return agentbackend.CapabilityUnknown
	}
}

// MCPServersFromACP translates a slice of acp.McpServer entries into their
// protocol-neutral agentbackend.MCPServerDescriptor equivalents (mitto-mx9.1.2).
// Used by SharedProcess implementations' shared-process handshake callers
// (bgsession_shared_session.go's hsStartMcpServer) that build the entry list
// via the still-ACP-typed startSessionMcpServer helper. Sse/Acp entries (not
// constructed anywhere in this codebase) are silently skipped.
func MCPServersFromACP(servers []acp.McpServer) []agentbackend.MCPServerDescriptor {
	out := make([]agentbackend.MCPServerDescriptor, 0, len(servers))
	for _, s := range servers {
		switch {
		case s.Http != nil:
			headers := make([]agentbackend.HTTPHeader, 0, len(s.Http.Headers))
			for _, h := range s.Http.Headers {
				headers = append(headers, agentbackend.HTTPHeader{Name: h.Name, Value: h.Value})
			}
			out = append(out, agentbackend.MCPServerDescriptor{HTTP: &agentbackend.MCPServerHTTP{
				Name:    s.Http.Name,
				URL:     s.Http.Url,
				Headers: headers,
			}})
		case s.Stdio != nil:
			env := make([]agentbackend.EnvVar, 0, len(s.Stdio.Env))
			for _, e := range s.Stdio.Env {
				env = append(env, agentbackend.EnvVar{Name: e.Name, Value: e.Value})
			}
			out = append(out, agentbackend.MCPServerDescriptor{Stdio: &agentbackend.MCPServerStdio{
				Name:    s.Stdio.Name,
				Command: s.Stdio.Command,
				Args:    append([]string(nil), s.Stdio.Args...),
				Env:     env,
			}})
		}
	}
	return out
}

// MCPServersToACP is the reverse of MCPServersFromACP: it rebuilds the
// ACP-shaped []acp.McpServer request payload from the protocol-neutral
// descriptors that now flow through the SharedProcess.NewSession/LoadSession/
// ResumeSession interface (mitto-mx9.1.2). Used by SharedProcess
// implementations (internal/acpproc) at the point they actually issue the
// ACP RPC.
func MCPServersToACP(servers []agentbackend.MCPServerDescriptor) []acp.McpServer {
	out := make([]acp.McpServer, 0, len(servers))
	for _, s := range servers {
		switch {
		case s.HTTP != nil:
			headers := make([]acp.HttpHeader, 0, len(s.HTTP.Headers))
			for _, h := range s.HTTP.Headers {
				headers = append(headers, acp.HttpHeader{Name: h.Name, Value: h.Value})
			}
			out = append(out, acp.McpServer{Http: &acp.McpServerHttpInline{
				Type:    "http",
				Name:    s.HTTP.Name,
				Url:     s.HTTP.URL,
				Headers: headers,
			}})
		case s.Stdio != nil:
			env := make([]acp.EnvVariable, 0, len(s.Stdio.Env))
			for _, e := range s.Stdio.Env {
				env = append(env, acp.EnvVariable{Name: e.Name, Value: e.Value})
			}
			out = append(out, acp.McpServer{Stdio: &acp.McpServerStdio{
				Name:    s.Stdio.Name,
				Command: s.Stdio.Command,
				Args:    append([]string(nil), s.Stdio.Args...),
				Env:     env,
			}})
		}
	}
	return out
}

// ModeStateFromACP translates a raw ACP SessionModeState into the neutral
// agentbackend.ModeState (mitto-mx9.1.2), for populating SessionHandle.Modes.
// Mirrors internal/acpbackend's ToNeutralModeState (duplicated here rather
// than imported: internal/acpbackend already imports internal/conversation,
// so importing it back would cycle — see the acpCapabilities translator note
// above for the same tradeoff). Returns nil for a nil input.
func ModeStateFromACP(s *acp.SessionModeState) *agentbackend.ModeState {
	if s == nil {
		return nil
	}
	out := &agentbackend.ModeState{
		CurrentModeID: string(s.CurrentModeId),
		Available:     make([]agentbackend.ModeDescriptor, 0, len(s.AvailableModes)),
	}
	for _, m := range s.AvailableModes {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		out.Available = append(out.Available, agentbackend.ModeDescriptor{
			ID:          string(m.Id),
			Name:        m.Name,
			Description: desc,
		})
	}
	return out
}

// Compile-time assertions that acpBackendProvider/acpLease/acpCapabilities
// satisfy the neutral seam, mirroring the acpproc `var _ conversation.SharedProcess
// = (*SharedACPProcess)(nil)` convention so interface drift fails the build
// here rather than at call sites.
var (
	_ BackendProvider                = (*acpBackendProvider)(nil)
	_ BackendLease                   = (*acpLease)(nil)
	_ ProviderDiscoverer             = (*acpLease)(nil)
	_ agentbackend.ProviderDiscovery = (*acpLease)(nil) // structurally identical to ProviderDiscoverer; pins the two contracts together
	_ agentbackend.Capabilities      = (*acpCapabilities)(nil)
	_ agentbackend.Capabilities      = (*acpProcessCapabilities)(nil)
	_ SessionPromptOps               = (*acpSessionPromptOps)(nil)
)
