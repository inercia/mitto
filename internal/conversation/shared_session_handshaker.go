package conversation

// Shared-process session handshake collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// staleLoadProbeTimeout caps the wall-clock time the resume path spends on a
// single session/load probe before giving up and falling back to session/new
// (mitto-1ut). A persisted acp_session_id that the agent no longer recognises
// otherwise makes LoadSession dead-wait the agent's full MCP-init budget
// (~240s cold), and the subsequent session/new fallback then derives ANOTHER
// full cold budget — stacking to ~480s (an ~8min wedge). Capping the probe lets
// a doomed load fail fast so the shared handshake budget (see resumeSharedACPSession)
// is spent overwhelmingly on the session/new that actually establishes the session.
//
// mitto-54k.9: tightened from 45s → 25s. The mitto-1ut starvation exception
// releases the shared cap when the probe times out (probeTimedOut=true), which
// gives the session/new fallback a fresh MCPInitTimeout budget (up to ~240s).
// Field logs (2026-07-14) showed the probe cap hitting at 45001ms stacking with
// a 240001ms session/new for a ~285s worst-case wedge on cold/contended processes
// where the agent's stderr MCP-init abort signal never fired. The signal, when it
// does fire, empirically arrives at 11-25s, so 25s aligns with observed abort
// behaviour and shaves ~20s off worst case (~285s → ~265s) without regressing
// warm loads (which resolve well under 25s).
const staleLoadProbeTimeout = 25 * time.Second

// minColdFallbackAttemptFraction is the smallest fraction of a fresh cold
// budget (RecommendedLoadTimeout) that the session/new fallback is guaranteed
// after a timed-out load probe (mitto-s1rt.1). See the probeTimedOut branch in
// resumeSharedACPSession: rather than granting a FULL fresh MCPInitTimeout on
// top of whatever the probe already burned (the mitto-l9as ~265s field wedge,
// staleLoadProbeTimeout 25s + MCPInitTimeout 240s), the fallback gets the
// LARGER of (a) whatever remains of the single combined resume budget, or (b)
// this fraction of one fresh cold budget — a floor that only matters when the
// probe consumed virtually the entire original budget itself (e.g. a very
// small RecommendedLoadTimeout, where staleLoadProbeTimeout no longer bounds
// the probe below it). On realistic production values (staleLoadProbeTimeout
// 25s vs MCPInitTimeout 240s) the probe burns a small slice of the budget, so
// branch (a) dominates and the combined wall-clock stays within ONE cold
// budget instead of stacking two.
const minColdFallbackAttemptFraction = 0.4

// isSessionNotFoundErr reports whether err from LoadSession/ResumeSession
// indicates the requested acp_session_id is no longer known to the agent
// (mitto-z70). Agents return JSON-RPC -32602 "Invalid params" for a stale
// session id; falling back to session/new is the correct recovery — retrying
// the same load would just fail again identically.
func isSessionNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var re *acp.RequestError
	if errors.As(err, &re) && re != nil && re.Code == -32602 {
		return true
	}
	return false
}

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

	// ACP context virginity tracking (mitto-s9g2). hsMarkContextFresh is called
	// only when the ACP session was created fresh (session/new) in this process;
	// hsMarkContextUnknown is called on resume/load, where agent-side history may
	// already exist and cannot be observed from Go.
	hsMarkContextFresh()
	hsMarkContextUnknown()

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
	hsGetPendingSharedModes() *acp.SessionModeState         // caller manages pendingSharedMu
	hsSetPendingSharedModes(m *acp.SessionModeState)        // caller manages pendingSharedMu
	hsGetPendingSharedModels() *SessionModelState           // caller manages pendingSharedMu
	hsSetPendingSharedModels(m *SessionModelState)          // caller manages pendingSharedMu
	hsGetPendingSharedModelConfigId() acp.SessionConfigId   // caller manages pendingSharedMu
	hsSetPendingSharedModelConfigId(id acp.SessionConfigId) // caller manages pendingSharedMu

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
	hsApplyAgentModels(models *SessionModelState)
	hsApplyAgentModelConfigId(id acp.SessionConfigId)
	hsLogAgentModels(models *SessionModelState)
	// hsApplySynthesizedModelsIfEmpty is the shared-process branch of the
	// mitto-886 local-profile fallback. Invoked once immediately after
	// hsApplyAgentModels: if the agent supplied no model catalog (or the SDK
	// dropped it) AND local config has model profiles, this synthesizes one
	// from EffectiveModelProfiles() so bs.ConfigOptions() surfaces a
	// Category=model entry in the WebSocket `connected` message. No-op when
	// the agent already advertised a real catalog.
	hsApplySynthesizedModelsIfEmpty()

	// Store persistence (no-op when no store)
	hsPersistACPSessionID()
	// hsClearPersistedACPSessionID clears the persisted acp_session_id (no-op when
	// no store). Called when session/load fails so a stale/unknown id is not
	// re-probed on the next cold start (the doomed probe otherwise burns up to
	// staleLoadProbeTimeout every attempt).
	hsClearPersistedACPSessionID()

	// Observer fan-out
	hsNotifyObservers(fn func(SessionObserver))

	// Cold-start diagnostics (mitto-3mv WI-2) — nil-safe by construction.
	// hsColdPhase records a phase on the session's cold-start Trace (no-op when
	// the trace is not active). hsFinishColdTrace finalizes the trace one-shot.
	// hsMarkMcpInitStart records the boundary of an MCP-init episode; the paired
	// hsMarkMcpInitEnd emits an "mcp_init" phase with the elapsed duration.
	// hsColdTraceCtx wraps base with the session's active cold-start Trace so the
	// acpproc RPC layer can correlate its NewSession/LoadSession/ResumeSession logs
	// via cold_start_id (WI-3). Returns base unchanged when no trace is active.
	hsColdPhase(name string, kv ...any)
	hsMarkMcpInitStart()
	hsMarkMcpInitEnd()
	hsFinishColdTrace(outcome string, kv ...any)
	hsColdTraceCtx(base context.Context) context.Context
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
	// Cold-start diagnostics (mitto-3mv): carry the active trace so the acpproc
	// RPC layer can correlate its logs via cold_start_id (WI-3).
	return context.WithCancel(d.hsColdTraceCtx(base))
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

	// Cold-start diagnostics (mitto-3mv): if the shared process's MCP-init
	// window is still open, mark the boundary so the closing MCP-init phase
	// (emitted from completeDeferredHandshake) has a duration to report.
	if sp := d.hsGetSharedProcess(); sp != nil && !sp.MCPInitDone() {
		d.hsColdPhase("mcp_init_wait_begin",
			"has_mcp_servers", len(d.hsGetPendingSharedMcpServers()) > 0,
			"deferred", true)
		d.hsMarkMcpInitStart()
	}

	newStart := time.Now()
	handle, err := d.hsGetSharedProcess().NewSession(d.hsColdTraceCtx(d.hsSessionCtx()), d.hsGetPendingSharedWorkingDir(), d.hsGetPendingSharedMcpServers())
	if err != nil {
		d.hsColdPhase("session_new_failed",
			"rpc_ms", time.Since(newStart).Milliseconds(),
			"deferred", true,
			"error", err.Error())
		return fmt.Errorf("failed to create session on shared process: %w", err)
	}
	d.hsColdPhase("session_new",
		"rpc_ms", time.Since(newStart).Milliseconds(),
		"deferred", true,
		"acp_session_id", handle.SessionID)

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
	// Deferred session/new: this session was created fresh in this process,
	// so it is provably empty (mitto-s9g2).
	d.hsMarkContextFresh()
	d.hsSetPendingSharedModes(handle.Modes)
	d.hsSetPendingSharedModels(handle.Models)
	d.hsSetPendingSharedModelConfigId(handle.ModelConfigId)
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
	modelCfgId := d.hsGetPendingSharedModelConfigId()
	d.hsSetPendingSharedModes(nil)
	d.hsSetPendingSharedModels(nil)
	d.hsSetPendingSharedModelConfigId("")
	d.hsPendingSharedUnlock()

	if modes != nil {
		d.hsApplySessionModes(modes)
	}
	if models != nil {
		d.hsApplyAgentModels(models)
	}
	// mitto-886: local-profile fallback. No-op when the agent already
	// advertised a real catalog (bs.agentModels non-nil) or when local config
	// has no model profiles.
	d.hsApplySynthesizedModelsIfEmpty()
	if modelCfgId != "" {
		d.hsApplyAgentModelConfigId(modelCfgId)
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
		d.hsFinishColdTrace("deferred_handshake_failed", "error", err.Error())
		return err
	}

	d.hsPersistACPSessionID()
	c.applyPendingSharedModes(d)
	d.hsNotifyObservers(func(o SessionObserver) { o.OnACPStarted() })

	// Cold-start diagnostics (mitto-3mv): the deferred handshake path leaves
	// the trace open across NewBackgroundSession's return; close the MCP-init
	// episode (if any) and finalize the trace here.
	d.hsMarkMcpInitEnd()
	d.hsColdPhase("ready", "acp_id", d.hsGetACPID(), "deferred", true)
	d.hsFinishColdTrace("ready", "acp_id", d.hsGetACPID(), "deferred", true)
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

	// Cold-start diagnostics (mitto-3mv): if the shared process's MCP-init
	// window is still open, this handshake will be gated on it. Mark the
	// boundary now so the closing d.hsMarkMcpInitEnd() at the end of this
	// function can emit an "mcp_init" phase with the elapsed duration.
	if !sharedProcess.MCPInitDone() {
		d.hsColdPhase("mcp_init_wait_begin", "has_mcp_servers", len(mcpServers) > 0)
		d.hsMarkMcpInitStart()
	}

	var handle *SessionHandle
	var err error

	// Combined resume budget (mitto-1ut): the resume path may issue a
	// session/load PROBE followed by a session/new FALLBACK. Both are gated on
	// the shared process's cold MCP-init window, so without a shared ceiling each
	// derives its own full ~240s budget and they STACK to ~480s (an ~8min wedge)
	// when the persisted acp_session_id is stale. Derive ONE wall-clock deadline
	// for the whole resume operation from RecommendedLoadTimeout (the cold MCP-init
	// budget; 0 when the process is already warm) and cap BOTH the load probe and
	// the new-session fallback by it. Net worst case ≈ one budget, not two.
	var handshakeDeadline time.Time
	if acpSessionID != "" {
		if rec := sharedProcess.RecommendedLoadTimeout(len(mcpServers) > 0); rec > 0 {
			handshakeDeadline = time.Now().Add(rec)
		}
	}
	// probeTimedOut records whether the load PROBE below failed by DEADLINE (the
	// process is genuinely cold / mid-MCP-init) rather than by a fast JSON-RPC
	// -32602 "session not found" (a stale id). It gates whether the shared
	// handshake deadline is applied to the session/new FALLBACK — see the fallback
	// block for the rationale (mitto-1ut starvation fix).
	var probeTimedOut bool

	if acpSessionID != "" {
		supportsResume := caps.SessionCapabilities.Resume != nil
		supportsLoad := caps.LoadSession

		if supportsResume {
			resumeCtx, resumeCancel := context.WithTimeout(d.hsColdTraceCtx(d.hsSessionCtx()), 10*time.Second)
			resumeStart := time.Now()
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
				d.hsColdPhase("session_resume_failed",
					"rpc_ms", time.Since(resumeStart).Milliseconds(),
					"error", err.Error())
			} else {
				d.hsSetResumeMethod("resume")
				// Resumed sessions may already hold agent-side history we cannot
				// see from Go; virginity is not authoritative (mitto-s9g2).
				d.hsMarkContextUnknown()
				if l := d.hsLogger(); l != nil {
					l.Info("Successfully resumed session using UNSTABLE resume API",
						"acp_session_id", acpSessionID, "resume_method", "resume")
				}
				d.hsColdPhase("session_resume",
					"rpc_ms", time.Since(resumeStart).Milliseconds(),
					"acp_session_id", acpSessionID)
			}
		}

		if handle == nil && supportsLoad {
			client := d.hsGetACPClient()
			client.SetLoadingSession(true)
			// Cap the load PROBE (mitto-1ut). A stale/unknown acp_session_id makes
			// LoadSession dead-wait the agent's full cold MCP-init budget, so probe
			// with a short timeout (staleLoadProbeTimeout) and fail fast to the
			// session/new fallback. Never exceed the shared handshake deadline:
			// use whichever is sooner so the probe + fallback stay within one budget.
			loadTimeout := staleLoadProbeTimeout
			if !handshakeDeadline.IsZero() {
				if remaining := time.Until(handshakeDeadline); remaining < loadTimeout {
					loadTimeout = remaining
				}
			}
			if loadTimeout <= 0 {
				loadTimeout = time.Millisecond // degenerate: budget already spent
			}
			loadCtx, loadCancel := context.WithTimeout(d.hsColdTraceCtx(d.hsSessionCtx()), loadTimeout)
			loadStart := time.Now()
			handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
			loadCancel()
			client.SetLoadingSession(false)
			if err != nil {
				// Classify the failure so the fallback path emits an accurate cold
				// phase and log: JSON-RPC -32602 from the agent means the persisted
				// acp_session_id is stale/unknown, so exactly one session/new is the
				// correct recovery (mitto-z70). Any other error (timeout, transport,
				// internal) is still handled by the same single-shot fallback below,
				// but tagged as a generic load failure.
				staleSession := isSessionNotFoundErr(err)
				logFields := []any{"acp_session_id", acpSessionID, "error", err, "method", "load"}
				if loadCtx.Err() == context.DeadlineExceeded {
					logFields = append(logFields, "timeout", true)
					// The probe hit its cap rather than returning -32602: the process
					// is genuinely cold / mid-MCP-init, not the id stale. Remember this
					// so the session/new fallback below gets a REAL budget instead of the
					// truncated remainder of the shared handshake deadline (mitto-1ut
					// starvation fix). A stale (-32602) probe fails in ~ms and leaves
					// probeTimedOut false, so the shared cap still prevents stacking there.
					probeTimedOut = true
				}
				if staleSession {
					logFields = append(logFields, "stale_session", true)
				}
				if l := d.hsLogger(); l != nil {
					l.Info("Load failed, creating new session", logFields...)
				}
				coldPhase := "session_load_failed"
				if staleSession {
					coldPhase = "session_load_stale"
				}
				d.hsColdPhase(coldPhase,
					"rpc_ms", time.Since(loadStart).Milliseconds(),
					"error", err.Error(),
					"stale_session", staleSession)
				// Clear the persisted acp_session_id now that this probe has proven
				// it can't be loaded. The fallback session/new below persists a
				// fresh, loadable id on success; but if the whole handshake fails,
				// clearing here ensures the next cold start skips the doomed load
				// probe (which otherwise burns up to staleLoadProbeTimeout every
				// attempt) and goes straight to session/new.
				d.hsClearPersistedACPSessionID()
			} else {
				d.hsSetResumeMethod("load")
				// Session/load replays history agent-side; virginity is not
				// authoritative (mitto-s9g2).
				d.hsMarkContextUnknown()
				if l := d.hsLogger(); l != nil {
					l.Info("Successfully loaded session (with history replay)",
						"acp_session_id", acpSessionID, "resume_method", "load")
				}
				d.hsColdPhase("session_load",
					"rpc_ms", time.Since(loadStart).Milliseconds(),
					"acp_session_id", acpSessionID)
			}
		}
	}

	if handle == nil {
		d.hsSetResumeMethod("new")
		rpcCtx, rpcCancel := c.creationRPCCtx(d)
		// Cap the session/new FALLBACK by the shared resume deadline (mitto-1ut) so
		// the load probe + this fallback stay within ONE cold budget instead of
		// stacking two. handshakeDeadline is zero on the warm path and on the
		// non-resume (acpSessionID=="") path, where NewSession derives its own
		// budget as before.
		//
		// EXCEPTION (mitto-1ut starvation fix): when the load probe FAILED BY DEADLINE
		// (probeTimedOut) rather than a fast -32602, the process is proven genuinely
		// cold. Applying the ORIGINAL shared cap would hand NewSession only the remainder
		// (handshakeDeadline - ~probe), which can be too small for a legitimate
		// session/new attempt on a cold/contended process.
		//
		// mitto-l9as attempted to fix this by granting a FRESH full cold budget from
		// the current instant — but that STACKS on top of whatever the probe already
		// burned, e.g. probe(25s) + freshNewSessionBudget(240s) ≈ 265s, the exact
		// field wedge documented at 2026-07-23T08:55:24 (total_ms=199032/265009).
		//
		// mitto-s1rt.1 fix: give the fallback the LARGER of (a) whatever remains of
		// the ORIGINAL single combined budget (handshakeDeadline), or (b) a minimum
		// floor of minColdFallbackAttemptFraction of one fresh cold budget — a floor
		// that only matters when the probe consumed virtually the whole original
		// budget itself. On realistic production values (staleLoadProbeTimeout 25s
		// vs MCPInitTimeout 240s) branch (a) dominates (215s remains, well above the
		// 96s floor), so the combined wall-clock stays within ONE cold budget (~240s)
		// instead of stacking two (~265s). Stale-id path (probeTimedOut=false) is
		// unchanged: a -32602 probe returns in ~ms and keeps the ORIGINAL shared cap.
		if !handshakeDeadline.IsZero() {
			fallbackDeadline := handshakeDeadline
			if probeTimedOut {
				if rec := sharedProcess.RecommendedLoadTimeout(len(mcpServers) > 0); rec > 0 {
					remaining := time.Until(handshakeDeadline)
					minAttempt := time.Duration(float64(rec) * minColdFallbackAttemptFraction)
					fallbackDeadline = time.Now().Add(max(remaining, minAttempt))
				}
			}
			var deadlineCancel context.CancelFunc
			rpcCtx, deadlineCancel = context.WithDeadline(rpcCtx, fallbackDeadline)
			defer deadlineCancel()
		}
		newStart := time.Now()
		handle, err = sharedProcess.NewSession(rpcCtx, workingDir, mcpServers)
		rpcCancel()
		if err != nil {
			d.hsStopMcpServer()
			d.hsGetACPClient().Close()
			d.hsSetACPClient(nil)
			d.hsSetSharedProcess(nil)
			d.hsColdPhase("session_new_failed",
				"rpc_ms", time.Since(newStart).Milliseconds(),
				"error", err.Error())
			return fmt.Errorf("failed to create session on shared process: %w", err)
		}
		d.hsColdPhase("session_new",
			"rpc_ms", time.Since(newStart).Milliseconds(),
			"acp_session_id", handle.SessionID)
		// This session was created fresh in this process, so it is provably
		// empty (mitto-s9g2).
		d.hsMarkContextFresh()
	}
	// Cold-start diagnostics (mitto-3mv): if the agent reported "waiting for MCP
	// server..." during this handshake episode, close the episode now — session
	// establishment is done. Nil-safe when no episode was recorded.
	d.hsMarkMcpInitEnd()
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
	// mitto-886: local-profile fallback for the resume path when the agent
	// omits a catalog entirely. No-op when handle.Models was non-nil.
	d.hsApplySynthesizedModelsIfEmpty()
	if handle.ModelConfigId != "" {
		d.hsApplyAgentModelConfigId(handle.ModelConfigId)
	}
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
