package conversation

// Shared ACP session cluster for BackgroundSession.
// All handshake logic lives in shared_session_handshaker.go (sharedSessionHandshaker collaborator).
// The methods below are thin delegators that pass bs as the handshakeDeps seam.

import (
	"context"
	"log/slog"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/conversion"
	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Thin delegators
// =============================================================================

// buildWebClientConfig assembles the WebClientConfig from this session's callbacks and settings.
// Used by both the per-session path (bgsession_acp_process.go) and shared-process path.
// This is NOT delegated to the collaborator because it accesses ~14 BackgroundSession fields
// directly — exposing each via deps would bloat the interface. Instead it is exposed via
// hsBuildWebClientConfig() so the collaborator can call it through the deps seam.
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

func (bs *BackgroundSession) prepareSharedACPSession(sharedProcess SharedProcess, workingDir string) error {
	return bs.handshaker.prepareSharedACPSession(bs, sharedProcess, workingDir)
}

func (bs *BackgroundSession) completeDeferredHandshake() error {
	return bs.handshaker.completeDeferredHandshake(bs)
}

// PrewarmACPSession completes the deferred ACP session/new handshake in the background
// so model/mode selectors become available before the first prompt. Best-effort + idempotent.
func (bs *BackgroundSession) PrewarmACPSession() {
	if bs == nil || bs.sharedProcess == nil {
		return
	}
	bs.handshaker.prewarmACPSession(bs)
}

func (bs *BackgroundSession) resumeSharedACPSession(sharedProcess SharedProcess, workingDir, acpSessionID string) error {
	return bs.handshaker.resumeSharedACPSession(bs, sharedProcess, workingDir, acpSessionID)
}

// =============================================================================
// handshakeDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) hsSessionID() string            { return bs.persistedID }
func (bs *BackgroundSession) hsLogger() *slog.Logger         { return bs.logger }
func (bs *BackgroundSession) hsSessionCtx() context.Context  { return bs.ctx }
func (bs *BackgroundSession) hsCreationCtx() context.Context { return bs.creationCtx }
func (bs *BackgroundSession) hsNilCreationCtx()              { bs.creationCtx = nil }

func (bs *BackgroundSession) hsBuildWebClientConfig() WebClientConfig {
	return bs.buildWebClientConfig()
}

func (bs *BackgroundSession) hsGetSharedProcess() SharedProcess  { return bs.sharedProcess }
func (bs *BackgroundSession) hsSetSharedProcess(p SharedProcess) { bs.sharedProcess = p }

func (bs *BackgroundSession) hsSetACPClient(c *WebClient) { bs.acpClient = c }
func (bs *BackgroundSession) hsGetACPClient() *WebClient  { return bs.acpClient }

func (bs *BackgroundSession) hsSetAgentSupportsImages(v bool) { bs.agentSupportsImages = v }

func (bs *BackgroundSession) hsGetACPID() string   { return bs.acpID }
func (bs *BackgroundSession) hsSetACPID(id string) { bs.acpID = id }

func (bs *BackgroundSession) hsPendingSharedLock()   { bs.pendingSharedMu.Lock() }
func (bs *BackgroundSession) hsPendingSharedUnlock() { bs.pendingSharedMu.Unlock() }

func (bs *BackgroundSession) hsIsPendingShared() bool              { return bs.pendingShared }
func (bs *BackgroundSession) hsSetPendingShared(v bool)            { bs.pendingShared = v }
func (bs *BackgroundSession) hsGetPendingSharedWorkingDir() string { return bs.pendingSharedWorkingDir }
func (bs *BackgroundSession) hsSetPendingSharedWorkingDir(dir string) {
	bs.pendingSharedWorkingDir = dir
}
func (bs *BackgroundSession) hsGetPendingSharedMcpServers() []acp.McpServer {
	return bs.pendingSharedMcpServers
}
func (bs *BackgroundSession) hsSetPendingSharedMcpServers(servers []acp.McpServer) {
	bs.pendingSharedMcpServers = servers
}
func (bs *BackgroundSession) hsGetPendingSharedModes() *acp.SessionModeState {
	return bs.pendingSharedModes
}
func (bs *BackgroundSession) hsSetPendingSharedModes(m *acp.SessionModeState) {
	bs.pendingSharedModes = m
}
func (bs *BackgroundSession) hsGetPendingSharedModels() *acp.UnstableSessionModelState {
	return bs.pendingSharedModels
}
func (bs *BackgroundSession) hsSetPendingSharedModels(m *acp.UnstableSessionModelState) {
	bs.pendingSharedModels = m
}

func (bs *BackgroundSession) hsHandshakeLock()   { bs.handshakeMu.Lock() }
func (bs *BackgroundSession) hsHandshakeUnlock() { bs.handshakeMu.Unlock() }

func (bs *BackgroundSession) hsInitACPProcessDone(sharedDone <-chan struct{}) {
	done := make(chan struct{})
	bs.acpProcessDone = done
	bs.acpProcessDoneOnce = sync.Once{}
	go func() {
		select {
		case <-sharedDone:
			bs.acpProcessDoneOnce.Do(func() { close(done) })
		case <-bs.ctx.Done():
		}
	}()
}

func (bs *BackgroundSession) hsSetResumeMethod(method string) { bs.resumeMethod = method }
func (bs *BackgroundSession) hsGetResumeMethod() string       { return bs.resumeMethod }

func (bs *BackgroundSession) hsStartMcpServer(caps acp.AgentCapabilities) []acp.McpServer {
	return bs.startSessionMcpServer(bs.store, caps)
}
func (bs *BackgroundSession) hsStopMcpServer() { bs.stopSessionMcpServer() }

func (bs *BackgroundSession) hsApplySessionModes(modes *acp.SessionModeState) {
	bs.setSessionModes(modes)
}
func (bs *BackgroundSession) hsApplyAgentModels(models *acp.UnstableSessionModelState) {
	bs.setAgentModels(models)
}
func (bs *BackgroundSession) hsLogAgentModels(models *acp.UnstableSessionModelState) {
	bs.logAgentModels(models)
}

func (bs *BackgroundSession) hsPersistACPSessionID() {
	if bs.store == nil || bs.persistedID == "" || bs.acpID == "" {
		return
	}
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		m.ACPSessionID = bs.acpID
	}); err != nil && bs.logger != nil {
		bs.logger.Warn("Failed to persist ACP session ID after deferred handshake", "error", err)
	}
}

func (bs *BackgroundSession) hsNotifyObservers(fn func(SessionObserver)) {
	bs.notifyObservers(fn)
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
