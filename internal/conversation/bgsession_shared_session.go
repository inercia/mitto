package conversation

// Shared ACP session cluster for BackgroundSession.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/conversion"
	"github.com/inercia/mitto/internal/session"
)

// sessionCreationRPCTimeout is the default timeout for the initial ACP session creation RPC
// (NewSession call). It is intentionally shorter than the HTTP middleware's 30s request
// timeout so that if the RPC times out, the HTTP handler can still return a proper error
// response instead of a generic "Request timeout" from the middleware.
const sessionCreationRPCTimeout = 25 * time.Second

// buildWebClientConfig assembles the WebClientConfig from this session's callbacks and settings.
// Used by both the per-session and shared-process paths to create a WebClient.
func (bs *BackgroundSession) buildWebClientConfig() WebClientConfig {
	cfg := WebClientConfig{
		AutoApprove:          bs.autoApprove,
		SeqProvider:          bs,
		Logger:               bs.logger,
		OnAgentMessage:       bs.onAgentMessage,
		OnAgentThought:       bs.onAgentThought,
		OnToolCall:           bs.onToolCall,
		OnToolUpdate:         bs.onToolUpdate,
		OnPlan:               bs.onPlan,
		OnFileWrite:          bs.onFileWrite,
		OnFileRead:           bs.onFileRead,
		OnPermission:         bs.onPermission,
		OnAvailableCommands:  bs.onAvailableCommands,
		OnCurrentModeChanged: bs.onCurrentModeChanged,
		OnMittoToolCall:      bs.onMittoToolCall,
		OnContextUsageUpdate: bs.onContextUsageUpdate,
		OnActivity:           bs.signalAgentActivity,
	}
	if bs.fileLinksConfig.IsEnabled() {
		cfg.FileLinksConfig = &conversion.FileLinkerConfig{
			WorkingDir:            bs.workingDir,
			WorkspacePath:         bs.workingDir,
			WorkspaceUUID:         bs.workspaceUUID,
			Enabled:               true,
			AllowOutsideWorkspace: bs.fileLinksConfig.IsAllowOutsideWorkspace(),
			APIPrefix:             bs.apiPrefix,
		}
	}
	return cfg
}

// creationRPCCtx returns a context suitable for the initial ACP session creation RPC.
// It uses CreationCtx from the config if it already has a deadline; otherwise it
// applies sessionCreationRPCTimeout.  The returned cancel function must be called.
//
// Design rationale: The 25s default is shorter than the HTTP middleware's 30s request
// timeout so that if the RPC times out, the HTTP handler can still return a proper
// error response (503 with a helpful message) rather than a generic "Request timeout".
func (bs *BackgroundSession) creationRPCCtx() (context.Context, context.CancelFunc) {
	base := bs.creationCtx
	if base == nil {
		base = bs.ctx
	}
	if _, hasDeadline := base.Deadline(); hasDeadline {
		// Caller already set a deadline — honour it, just make it cancellable.
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, sessionCreationRPCTimeout)
}

// prepareSharedACPSession sets up this BackgroundSession to use a session on the
// given shared ACP process WITHOUT issuing the blocking session/new RPC.
// All eager setup (capabilities, MCP server, acpClient, death-channel bridge) is
// done here; the session/new RPC is deferred to the first prompt via
// ensureSharedACPSession so that creating a conversation never blocks on a busy agent.
func (bs *BackgroundSession) prepareSharedACPSession(sharedProcess SharedProcess, workingDir string) error {
	bs.sharedProcess = sharedProcess

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := bs.startSessionMcpServer(bs.store, caps)
	if mcpServers == nil {
		mcpServers = []acp.McpServer{} // Must be empty array, not nil — ACP validates this
	}

	bs.acpClient = NewWebClient(bs.buildWebClientConfig())
	bs.agentSupportsImages = caps.PromptCapabilities.Image

	// Store what ensureSharedACPSession will need for the deferred RPC.
	bs.pendingSharedWorkingDir = workingDir
	bs.pendingSharedMcpServers = mcpServers
	bs.pendingShared = true

	// Release the creation context — it is the HTTP request context and will be
	// cancelled as soon as the create handler returns. The deferred session/new uses
	// bs.ctx instead (see ensureSharedACPSession). resumeSharedACPSession (called on
	// crash restart) also uses creationRPCCtx(), so this nil ensures it falls back to
	// bs.ctx rather than the long-expired HTTP request context.
	bs.creationCtx = nil

	// Bridge the shared process's death channel to bs.acpProcessDone.
	done := make(chan struct{})
	bs.acpProcessDone = done
	bs.acpProcessDoneOnce = sync.Once{}
	sharedDone := sharedProcess.ProcessDone()
	go func() {
		select {
		case <-sharedDone:
			bs.acpProcessDoneOnce.Do(func() { close(done) })
		case <-bs.ctx.Done():
		}
	}()

	if bs.logger != nil {
		bs.logger.Info("Prepared shared ACP session (session/new deferred to first prompt)",
			"session_id", bs.persistedID,
			"supports_images", bs.agentSupportsImages)
	}
	return nil
}

// ensureSharedACPSession performs the deferred session/new RPC for a shared-process
// session. It is idempotent and safe under concurrent callers (guarded by pendingSharedMu).
// Returns nil immediately if the handshake already completed or was handled by a restart.
// On error, the session is left in a retryable state — the caller should surface a clear
// error to the user and allow the next prompt to retry.
func (bs *BackgroundSession) ensureSharedACPSession() error {
	bs.pendingSharedMu.Lock()
	defer bs.pendingSharedMu.Unlock()

	// Return if already done or if a restart path already set bs.acpID.
	if !bs.pendingShared || bs.acpID != "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(bs.ctx, sessionCreationRPCTimeout)
	handle, err := bs.sharedProcess.NewSession(ctx, bs.pendingSharedWorkingDir, bs.pendingSharedMcpServers)
	cancel()
	if err != nil {
		// Leave pendingShared=true so the next prompt can retry.
		return fmt.Errorf("failed to create session on shared process: %w", err)
	}

	bs.sharedProcess.RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       bs.acpClient.SessionUpdate,
		OnReadTextFile:        bs.acpClient.ReadTextFile,
		OnWriteTextFile:       bs.acpClient.WriteTextFile,
		OnRequestPermission:   bs.acpClient.RequestPermission,
		OnCreateTerminal:      bs.acpClient.CreateTerminal,
		OnTerminalOutput:      bs.acpClient.TerminalOutput,
		OnReleaseTerminal:     bs.acpClient.ReleaseTerminal,
		OnWaitForTerminalExit: bs.acpClient.WaitForTerminalExit,
		OnKillTerminal:        bs.acpClient.KillTerminal,
	})

	bs.acpID = handle.SessionID

	// Stash modes and models for applyPendingSharedModes to apply from the prompt
	// goroutine. We must NOT call setSessionModes / setAgentModels here because
	// they trigger store writes (via persistConfigValue / applyConfigConstraints)
	// that may race with concurrent store access from other goroutines (e.g., the
	// test event-injector using a separate Store instance on the same directory).
	bs.pendingSharedModes = handle.Modes
	bs.pendingSharedModels = handle.Models

	bs.pendingShared = false

	if bs.logger != nil {
		bs.logger.Info("Completed deferred session/new on shared process",
			"session_id", bs.persistedID,
			"acp_session_id", bs.acpID)
		bs.logAgentModels(handle.Models)
	}
	return nil
}

// applyPendingSharedModes applies the modes and models that were stashed by
// ensureSharedACPSession. Safe to call only from a single goroutine (the prompt
// goroutine) because setSessionModes and setAgentModels trigger store writes via
// persistConfigValue / applyConfigConstraints.
// Calling this more than once is a no-op once the fields are cleared.
func (bs *BackgroundSession) applyPendingSharedModes() {
	bs.pendingSharedMu.Lock()
	modes := bs.pendingSharedModes
	models := bs.pendingSharedModels
	bs.pendingSharedModes = nil
	bs.pendingSharedModels = nil
	bs.pendingSharedMu.Unlock()

	if modes != nil {
		bs.setSessionModes(modes)
	}
	if models != nil {
		bs.setAgentModels(models)
	}
}

// completeDeferredHandshake performs the deferred session/new RPC for a shared-
// process session, persists the ACP session ID, applies the session's modes and
// models (which populate the config options surfaced to the UI as model/mode
// selectors), and notifies observers that ACP is ready. It serialises these store
// writes via handshakeMu so it is safe to call from either the first-prompt
// goroutine or the background prewarm goroutine (see PrewarmACPSession). It returns
// nil — without notifying — when there is nothing to do (not a deferred shared
// session, or the handshake already completed).
func (bs *BackgroundSession) completeDeferredHandshake() error {
	bs.handshakeMu.Lock()
	defer bs.handshakeMu.Unlock()

	// Nothing to do if this is not a deferred shared session, or the handshake has
	// already completed. pendingShared is flipped to false (under pendingSharedMu)
	// by ensureSharedACPSession once the RPC succeeds.
	bs.pendingSharedMu.Lock()
	pending := bs.pendingShared
	bs.pendingSharedMu.Unlock()
	if bs.sharedProcess == nil || !pending {
		return nil
	}

	if err := bs.ensureSharedACPSession(); err != nil {
		return err
	}

	// Persist the ACP session ID. Done here (not inside ensureSharedACPSession) so
	// that store writes happen from a single serialised goroutine (handshakeMu).
	if bs.store != nil && bs.persistedID != "" && bs.acpID != "" {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to persist ACP session ID after deferred handshake", "error", err)
		}
	}

	bs.applyPendingSharedModes()

	// Notify observers that ACP is now ready and config options (model, mode) are
	// available, so the UI can render the model/mode selectors.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStarted()
	})
	return nil
}

// PrewarmACPSession completes the deferred ACP session/new handshake in the
// background so the model and mode selectors become available before the first
// prompt is sent. It is best-effort and idempotent: a no-op for non-deferred or
// already-started sessions, and on failure it leaves the session retryable so the
// first prompt re-attempts the handshake. Intended to be called from a goroutine.
func (bs *BackgroundSession) PrewarmACPSession() {
	if bs == nil || bs.sharedProcess == nil {
		return
	}
	if err := bs.completeDeferredHandshake(); err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Background ACP prewarm failed (will retry on first prompt)",
				"session_id", bs.persistedID,
				"error", err)
		}
	}
}

// resumeSharedACPSession sets up this BackgroundSession to use a session on the
// given shared ACP process, trying to resume the specified ACP session ID first.
// Falls back to creating a new session if resumption fails.
func (bs *BackgroundSession) resumeSharedACPSession(sharedProcess SharedProcess, workingDir, acpSessionID string) error {
	bs.sharedProcess = sharedProcess

	var caps acp.AgentCapabilities
	if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
		caps = *sharedCaps
	}
	mcpServers := bs.startSessionMcpServer(bs.store, caps)

	bs.acpClient = NewWebClient(bs.buildWebClientConfig())

	var handle *SessionHandle
	var err error

	// Try to resume an existing session if we have an ID.
	// Prefer Resume over Load for speed (no history replay).
	if acpSessionID != "" {
		// Check capabilities
		supportsResume := caps.SessionCapabilities.Resume != nil
		supportsLoad := caps.LoadSession

		// Try Resume first (fast path)
		if supportsResume {
			resumeCtx, resumeCancel := context.WithTimeout(bs.ctx, 10*time.Second)
			handle, err = sharedProcess.ResumeSession(resumeCtx, acpSessionID, workingDir, mcpServers)
			resumeCancel()
			if err != nil {
				logFields := []any{
					"acp_session_id", acpSessionID,
					"error", err,
					"method", "resume",
				}
				if resumeCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if bs.logger != nil {
					bs.logger.Info("Resume failed, will try Load or New",
						logFields...)
				}
				// Fall through to try Load
			} else {
				bs.resumeMethod = "resume"
				if bs.logger != nil {
					bs.logger.Info("Successfully resumed session using UNSTABLE resume API",
						"acp_session_id", acpSessionID,
						"resume_method", "resume")
				}
			}
		}

		// Fallback to Load (slow path with history replay)
		if handle == nil && supportsLoad {
			// Suppress event processing during Load to prevent notification queue overflow.
			// See comment in startACPProcess for details.
			bs.acpClient.SetLoadingSession(true)
			loadCtx, loadCancel := context.WithTimeout(bs.ctx, 30*time.Second)
			handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
			loadCancel()
			bs.acpClient.SetLoadingSession(false)
			if err != nil {
				logFields := []any{
					"acp_session_id", acpSessionID,
					"error", err,
					"method", "load",
				}
				if loadCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
				}
				if bs.logger != nil {
					bs.logger.Info("Load failed, creating new session",
						logFields...)
				}
			} else {
				bs.resumeMethod = "load"
				if bs.logger != nil {
					bs.logger.Info("Successfully loaded session (with history replay)",
						"acp_session_id", acpSessionID,
						"resume_method", "load")
				}
			}
		}
	}

	// Final fallback: create new session
	if handle == nil {
		bs.resumeMethod = "new"
		// Use the creation context so the HTTP handler's timeout can cancel this RPC.
		rpcCtx, rpcCancel := bs.creationRPCCtx()
		handle, err = sharedProcess.NewSession(rpcCtx, workingDir, mcpServers)
		rpcCancel()
		if err != nil {
			bs.stopSessionMcpServer()
			bs.acpClient.Close()
			bs.acpClient = nil
			bs.sharedProcess = nil
			return fmt.Errorf("failed to create session on shared process: %w", err)
		}
	}
	bs.creationCtx = nil // Release reference — only needed for the creation RPCs above.

	sharedProcess.RegisterSession(acp.SessionId(handle.SessionID), &SessionCallbacks{
		OnSessionUpdate:       bs.acpClient.SessionUpdate,
		OnReadTextFile:        bs.acpClient.ReadTextFile,
		OnWriteTextFile:       bs.acpClient.WriteTextFile,
		OnRequestPermission:   bs.acpClient.RequestPermission,
		OnCreateTerminal:      bs.acpClient.CreateTerminal,
		OnTerminalOutput:      bs.acpClient.TerminalOutput,
		OnReleaseTerminal:     bs.acpClient.ReleaseTerminal,
		OnWaitForTerminalExit: bs.acpClient.WaitForTerminalExit,
		OnKillTerminal:        bs.acpClient.KillTerminal,
	})

	bs.acpID = handle.SessionID
	bs.agentSupportsImages = caps.PromptCapabilities.Image
	bs.setSessionModes(handle.Modes)
	bs.setAgentModels(handle.Models)

	// Bridge the shared process's death channel to bs.acpProcessDone.
	done := make(chan struct{})
	bs.acpProcessDone = done
	bs.acpProcessDoneOnce = sync.Once{}
	sharedDone := sharedProcess.ProcessDone()
	go func() {
		select {
		case <-sharedDone:
			bs.acpProcessDoneOnce.Do(func() { close(done) })
		case <-bs.ctx.Done():
		}
	}()

	if bs.logger != nil {
		bs.logger.Info("Resumed ACP session on shared process",
			"session_id", bs.persistedID,
			"acp_session_id", bs.acpID,
			"requested_acp_session_id", acpSessionID,
			"resume_method", bs.resumeMethod,
			"supports_images", bs.agentSupportsImages)
		bs.logAgentModels(handle.Models)
	}

	// Notify observers that ACP is now ready to accept prompts.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStarted()
	})

	return nil
}

// logSessionModes logs the session modes/config options at DEBUG level.
// This helps with debugging which modes are available from the ACP server.
func (bs *BackgroundSession) logSessionModes(modes *acp.SessionModeState) {
	if bs.logger == nil || modes == nil {
		return
	}

	// Log current mode
	bs.logger.Debug("Session mode state",
		"current_mode", modes.CurrentModeId,
		"available_modes_count", len(modes.AvailableModes))

	// Log each available mode
	for _, mode := range modes.AvailableModes {
		desc := ""
		if mode.Description != nil {
			desc = *mode.Description
		}
		bs.logger.Debug("Available session mode",
			"mode_id", mode.Id,
			"mode_name", mode.Name,
			"mode_description", desc)
	}
}

// logAgentInfo logs the agent information and capabilities from the Initialize response at DEBUG level.
// This helps with debugging which agent is being used and what features it supports.
func (bs *BackgroundSession) logAgentInfo(resp acp.InitializeResponse) {
	if bs.logger == nil {
		return
	}

	// Log agent info if available
	if resp.AgentInfo != nil {
		bs.logger.Debug("Agent info",
			"agent_name", resp.AgentInfo.Name,
			"agent_version", resp.AgentInfo.Version)
	}

	// Log protocol version
	bs.logger.Debug("ACP protocol version",
		"protocol_version", resp.ProtocolVersion)

	// Log and store agent capabilities
	caps := resp.AgentCapabilities
	bs.agentSupportsImages = caps.PromptCapabilities.Image
	bs.logger.Debug("Agent capabilities",
		"load_session", caps.LoadSession,
		"mcp_http", caps.McpCapabilities.Http,
		"mcp_sse", caps.McpCapabilities.Sse,
		"prompt_audio", caps.PromptCapabilities.Audio,
		"prompt_embedded_context", caps.PromptCapabilities.EmbeddedContext,
		"prompt_image", caps.PromptCapabilities.Image)

	// Log authentication methods if available
	if len(resp.AuthMethods) > 0 {
		authMethods := make([]string, len(resp.AuthMethods))
		for i, auth := range resp.AuthMethods {
			if auth.Agent != nil {
				authMethods[i] = auth.Agent.Name
			} else if auth.EnvVar != nil {
				authMethods[i] = "env_var"
			} else if auth.Terminal != nil {
				authMethods[i] = "terminal"
			} else {
				authMethods[i] = "unknown"
			}
		}
		bs.logger.Debug("Agent auth methods",
			"count", len(resp.AuthMethods),
			"methods", authMethods)
	}
}
