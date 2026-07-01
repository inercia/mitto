package conversation

// Shared-process session handshake collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// handshakeDeps is the minimal interface sharedSessionHandshaker needs from BackgroundSession.
// All methods are prefixed with "hs" to avoid clashes with BackgroundSession's public API.
type handshakeDeps interface {
	// Identity / lifecycle
	hsSessionID() string
	hsLogger() *slog.Logger
	hsSessionCtx() context.Context  // bs.ctx — session lifetime context
	hsCreationCtx() context.Context // bs.creationCtx (may be nil)
	hsNilCreationCtx()              // bs.creationCtx = nil (releases HTTP request context)

	// WebClient config — built from BackgroundSession fields directly; exposed via seam
	// rather than duplicating all ~14 individual field accessors in the interface.
	hsBuildWebClientConfig() WebClientConfig

	// Shared process
	hsGetSharedProcess() SharedProcess
	hsSetSharedProcess(p SharedProcess)

	// ACP client
	hsSetACPClient(c *WebClient)
	hsGetACPClient() *WebClient

	// Agent capabilities
	hsSetAgentSupportsImages(v bool)

	// ACP session ID
	hsGetACPID() string
	hsSetACPID(id string)

	// Pending shared handshake state.
	// Lock ordering: pendingSharedMu may be nested under handshakeMu (never reverse).
	hsPendingSharedLock()
	hsPendingSharedUnlock()
	hsIsPendingShared() bool   // caller manages pendingSharedMu
	hsSetPendingShared(v bool) // caller manages pendingSharedMu
	hsGetPendingSharedWorkingDir() string
	hsSetPendingSharedWorkingDir(dir string)
	hsGetPendingSharedMcpServers() []acp.McpServer
	hsSetPendingSharedMcpServers(servers []acp.McpServer)
	hsGetPendingSharedModes() *acp.SessionModeState            // caller manages pendingSharedMu
	hsSetPendingSharedModes(m *acp.SessionModeState)           // caller manages pendingSharedMu
	hsGetPendingSharedModels() *acp.UnstableSessionModelState  // caller manages pendingSharedMu
	hsSetPendingSharedModels(m *acp.UnstableSessionModelState) // caller manages pendingSharedMu

	// Handshake serialization mutex
	hsHandshakeLock()
	hsHandshakeUnlock()

	// ACP process-done channel bridge (creates done chan + goroutine; captures bs.ctx)
	hsInitACPProcessDone(sharedDone <-chan struct{})

	// Resume method tracking
	hsSetResumeMethod(method string)
	hsGetResumeMethod() string

	// MCP server lifecycle
	hsStartMcpServer(caps acp.AgentCapabilities) []acp.McpServer
	hsStopMcpServer()

	// Session-level ACP state applied after session is established
	hsApplySessionModes(modes *acp.SessionModeState)
	hsApplyAgentModels(models *acp.UnstableSessionModelState)
	hsLogAgentModels(models *acp.UnstableSessionModelState)

	// Store persistence (no-op when no store)
	hsPersistACPSessionID()

	// Observer fan-out
	hsNotifyObservers(fn func(SessionObserver))
}

// sharedSessionHandshaker is a stateless collaborator owning the lazy/deferred shared-
// process session handshake logic previously in bgsession_shared_session.go.
type sharedSessionHandshaker struct{}

// creationRPCCtx returns a cancellable context for the session/new RPC. The per-attempt
// deadline and bounded retry-with-jitter now live in SharedACPProcess.NewSession
// (mitto-4no7), so this no longer imposes its own create timeout — it only forwards the
// base context (an HTTP creation deadline, if any, still applies).
func (c sharedSessionHandshaker) creationRPCCtx(d handshakeDeps) (context.Context, context.CancelFunc) {
	base := d.hsCreationCtx()
	if base == nil {
		base = d.hsSessionCtx()
	}
	return context.WithCancel(base)
}

// buildWebClientConfig delegates to the deps seam (builds from BackgroundSession fields).
func (c sharedSessionHandshaker) buildWebClientConfig(d handshakeDeps) WebClientConfig {
	return d.hsBuildWebClientConfig()
}

// prepareSharedACPSession sets up the session to use a shared ACP process WITHOUT
// issuing the blocking session/new RPC (deferred to the first prompt).
func (c sharedSessionHandshaker) prepareSharedACPSession(d handshakeDeps, sharedProcess SharedProcess, workingDir string) error {
	d.hsSetSharedProcess(sharedProcess)

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := d.hsStartMcpServer(caps)
	if mcpServers == nil {
		mcpServers = []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	}

	d.hsSetACPClient(NewWebClient(c.buildWebClientConfig(d)))
	d.hsSetAgentSupportsImages(caps.PromptCapabilities.Image)
	d.hsSetPendingSharedWorkingDir(workingDir)
	d.hsSetPendingSharedMcpServers(mcpServers)
	d.hsSetPendingShared(true)
	d.hsNilCreationCtx()
	d.hsInitACPProcessDone(sharedProcess.ProcessDone())

	if l := d.hsLogger(); l != nil {
		l.Info("Prepared shared ACP session (session/new deferred to first prompt)",
			"session_id", d.hsSessionID(),
			"supports_images", caps.PromptCapabilities.Image)
	}
	return nil
}

// ensureSharedACPSession performs the deferred session/new RPC for a shared-process session.
// Idempotent and safe under concurrent callers (guarded by pendingSharedMu).
func (c sharedSessionHandshaker) ensureSharedACPSession(d handshakeDeps) error {
	d.hsPendingSharedLock()
	defer d.hsPendingSharedUnlock()

	if !d.hsIsPendingShared() || d.hsGetACPID() != "" {
		return nil
	}

	handle, err := d.hsGetSharedProcess().NewSession(d.hsSessionCtx(), d.hsGetPendingSharedWorkingDir(), d.hsGetPendingSharedMcpServers())
	if err != nil {
		return fmt.Errorf("failed to create session on shared process: %w", err)
	}

	client := d.hsGetACPClient()
	d.hsGetSharedProcess().RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       client.SessionUpdate,
		OnReadTextFile:        client.ReadTextFile,
		OnWriteTextFile:       client.WriteTextFile,
		OnRequestPermission:   client.RequestPermission,
		OnCreateTerminal:      client.CreateTerminal,
		OnTerminalOutput:      client.TerminalOutput,
		OnReleaseTerminal:     client.ReleaseTerminal,
		OnWaitForTerminalExit: client.WaitForTerminalExit,
		OnKillTerminal:        client.KillTerminal,
	})

	d.hsSetACPID(handle.SessionID)
	d.hsSetPendingSharedModes(handle.Modes)
	d.hsSetPendingSharedModels(handle.Models)
	d.hsSetPendingShared(false)

	if l := d.hsLogger(); l != nil {
		l.Info("Completed deferred session/new on shared process",
			"session_id", d.hsSessionID(),
			"acp_session_id", handle.SessionID)
		d.hsLogAgentModels(handle.Models)
	}
	return nil
}

// applyPendingSharedModes applies modes and models stashed by ensureSharedACPSession.
// Must be called from a single goroutine (prompt goroutine) — setSessionModes/setAgentModels
// trigger store writes that are not safe to call concurrently.
func (c sharedSessionHandshaker) applyPendingSharedModes(d handshakeDeps) {
	d.hsPendingSharedLock()
	modes := d.hsGetPendingSharedModes()
	models := d.hsGetPendingSharedModels()
	d.hsSetPendingSharedModes(nil)
	d.hsSetPendingSharedModels(nil)
	d.hsPendingSharedUnlock()

	if modes != nil {
		d.hsApplySessionModes(modes)
	}
	if models != nil {
		d.hsApplyAgentModels(models)
	}
}

// completeDeferredHandshake performs the deferred handshake, persists the ACP session ID,
// applies modes/models, and notifies observers. Serialised via handshakeMu.
func (c sharedSessionHandshaker) completeDeferredHandshake(d handshakeDeps) error {
	d.hsHandshakeLock()
	defer d.hsHandshakeUnlock()

	d.hsPendingSharedLock()
	pending := d.hsIsPendingShared()
	d.hsPendingSharedUnlock()
	if d.hsGetSharedProcess() == nil || !pending {
		return nil
	}

	if err := c.ensureSharedACPSession(d); err != nil {
		return err
	}

	d.hsPersistACPSessionID()
	c.applyPendingSharedModes(d)
	d.hsNotifyObservers(func(o SessionObserver) { o.OnACPStarted() })
	return nil
}

// prewarmACPSession completes the deferred handshake in the background (best-effort).
func (c sharedSessionHandshaker) prewarmACPSession(d handshakeDeps) {
	if d.hsGetSharedProcess() == nil {
		return
	}
	if err := c.completeDeferredHandshake(d); err != nil {
		if l := d.hsLogger(); l != nil {
			l.Warn("Background ACP prewarm failed (will retry on first prompt)",
				"session_id", d.hsSessionID(), "error", err)
		}
	}
}

// resumeSharedACPSession sets up the session on a shared process, trying to resume/load
// the specified ACP session ID first, falling back to creating a new session.
func (c sharedSessionHandshaker) resumeSharedACPSession(d handshakeDeps, sharedProcess SharedProcess, workingDir, acpSessionID string) error {
	d.hsSetSharedProcess(sharedProcess)

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := d.hsStartMcpServer(caps)
	d.hsSetACPClient(NewWebClient(c.buildWebClientConfig(d)))

	var handle *SessionHandle
	var err error

	if acpSessionID != "" {
		supportsResume := caps.SessionCapabilities.Resume != nil
		supportsLoad := caps.LoadSession

		if supportsResume {
			resumeCtx, resumeCancel := context.WithTimeout(d.hsSessionCtx(), 10*time.Second)
			handle, err = sharedProcess.ResumeSession(resumeCtx, acpSessionID, workingDir, mcpServers)
			resumeCancel()
			if err != nil {
				logFields := []any{"acp_session_id", acpSessionID, "error", err, "method", "resume"}
				if resumeCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if l := d.hsLogger(); l != nil {
					l.Info("Resume failed, will try Load or New", logFields...)
				}
			} else {
				d.hsSetResumeMethod("resume")
				if l := d.hsLogger(); l != nil {
					l.Info("Successfully resumed session using UNSTABLE resume API",
						"acp_session_id", acpSessionID, "resume_method", "resume")
				}
			}
		}

		if handle == nil && supportsLoad {
			client := d.hsGetACPClient()
			client.SetLoadingSession(true)
			loadCtx, loadCancel := context.WithTimeout(d.hsSessionCtx(), 30*time.Second)
			handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
			loadCancel()
			client.SetLoadingSession(false)
			if err != nil {
				logFields := []any{"acp_session_id", acpSessionID, "error", err, "method", "load"}
				if loadCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if l := d.hsLogger(); l != nil {
					l.Info("Load failed, creating new session", logFields...)
				}
			} else {
				d.hsSetResumeMethod("load")
				if l := d.hsLogger(); l != nil {
					l.Info("Successfully loaded session (with history replay)",
						"acp_session_id", acpSessionID, "resume_method", "load")
				}
			}
		}
	}

	if handle == nil {
		d.hsSetResumeMethod("new")
		rpcCtx, rpcCancel := c.creationRPCCtx(d)
		handle, err = sharedProcess.NewSession(rpcCtx, workingDir, mcpServers)
		rpcCancel()
		if err != nil {
			d.hsStopMcpServer()
			d.hsGetACPClient().Close()
			d.hsSetACPClient(nil)
			d.hsSetSharedProcess(nil)
			return fmt.Errorf("failed to create session on shared process: %w", err)
		}
	}
	d.hsNilCreationCtx()

	client := d.hsGetACPClient()
	sharedProcess.RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       client.SessionUpdate,
		OnReadTextFile:        client.ReadTextFile,
		OnWriteTextFile:       client.WriteTextFile,
		OnRequestPermission:   client.RequestPermission,
		OnCreateTerminal:      client.CreateTerminal,
		OnTerminalOutput:      client.TerminalOutput,
		OnReleaseTerminal:     client.ReleaseTerminal,
		OnWaitForTerminalExit: client.WaitForTerminalExit,
		OnKillTerminal:        client.KillTerminal,
	})

	d.hsSetACPID(handle.SessionID)
	d.hsSetAgentSupportsImages(caps.PromptCapabilities.Image)
	d.hsApplySessionModes(handle.Modes)
	d.hsApplyAgentModels(handle.Models)
	d.hsInitACPProcessDone(sharedProcess.ProcessDone())

	if l := d.hsLogger(); l != nil {
		l.Info("Resumed ACP session on shared process",
			"session_id", d.hsSessionID(),
			"acp_session_id", handle.SessionID,
			"requested_acp_session_id", acpSessionID,
			"resume_method", d.hsGetResumeMethod(),
			"supports_images", caps.PromptCapabilities.Image)
		d.hsLogAgentModels(handle.Models)
	}

	d.hsNotifyObservers(func(o SessionObserver) { o.OnACPStarted() })
	return nil
}
