// Package handlers contains the REST API request handlers for the Mitto web
// server, extracted from the flat internal/web package into a dedicated
// sub-package.
//
// Handlers are methods on the Handlers struct rather than on *web.Server.
// They receive their dependencies through the Deps facade, which exposes only
// the subset of server state a handler needs. This decouples the handlers from
// the concrete *web.Server type and prevents an import cycle (web imports
// handlers, never the other way around).
//
// Routing remains in the web package's server.go: the server constructs a
// *Handlers and registers its HandleXxx methods on the mux.
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/inercia/mitto/internal/beads"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// Deps holds the dependencies that REST handlers need from the web server.
// It is a facade that decouples handlers from the concrete *web.Server type.
//
// The struct grows as more handlers are migrated; each field documents which
// server-owned dependency it mirrors.
type Deps struct {
	// Logger is the structured logger. May be nil; all uses are nil-guarded.
	Logger *slog.Logger

	// ConfigReadOnly mirrors Server.config.ConfigReadOnly: when true, the
	// configuration was loaded from a custom --config file and must not be
	// modified by write endpoints.
	ConfigReadOnly bool

	// MittoConfig mirrors Server.config.MittoConfig: the full in-memory Mitto
	// configuration. It is a pointer, so handlers that mutate it (e.g. appending
	// ACP servers) update the running server's view. May be nil.
	MittoConfig *configPkg.Config

	// RCFilePath mirrors Server.config.RCFilePath: the path to the RC file when
	// the config was loaded from one (empty otherwise). Surfaced by the config
	// GET endpoint so the frontend can show the active RC file.
	RCFilePath string

	// HasRCFileServers mirrors Server.config.HasRCFileServers: whether any ACP
	// servers came from the RC file. Surfaced by the config GET endpoint.
	HasRCFileServers bool

	// PromptsCache mirrors Server.config.PromptsCache: cached access to global
	// prompts from MITTO_DIR/prompts/. May be nil; callers must nil-guard.
	PromptsCache *configPkg.PromptsCache

	// HasExistingSimpleAuth mirrors Server.hasExistingSimpleAuth: reports whether
	// a simple-auth password already exists (in keychain or settings) without
	// exposing it. May be nil; the config GET handler then omits the flag.
	HasExistingSimpleAuth func() bool

	// ValidateAndPrepareConfig runs the web package's pre-save pipeline for a
	// config save request: structural validation, workspace-removal conflict
	// checks, default-workspace normalization, and restricted-runner validation.
	// It writes the error response itself and returns false when the request must
	// be rejected; otherwise it returns true with req normalized in place. It is a
	// closure so the handler need not import the web package's private
	// validation-error type. Required by HandleSaveConfig; nil means "reject".
	ValidateAndPrepareConfig func(w http.ResponseWriter, req *ConfigSaveRequest) bool

	// BuildNewSettings mirrors Server.buildNewSettings: it builds the persisted
	// settings from a save request (storing the external-access password in the
	// keychain on supported platforms). Required by HandleSaveConfig.
	BuildNewSettings func(req *ConfigSaveRequest) (*configPkg.Settings, error)

	// ApplyConfigChanges mirrors Server.applyConfigChanges: it applies the new
	// configuration to the running server (ACP servers, workspaces, web/auth
	// config, external listener). Required by HandleSaveConfig. Returns a non-nil
	// *ExternalAccessWarning when the save results in the external listener not
	// running even though external access was intended to be on (e.g. incomplete
	// credentials tore down the listener, or StartExternalListener failed). Nil
	// means everything is fine.
	ApplyConfigChanges func(req *ConfigSaveRequest, settings *configPkg.Settings) *ExternalAccessWarning

	// AuthEnabled reports whether the auth manager is currently enabled, surfaced
	// in the save-config response's "applied" block. May be nil; the handler then
	// reports auth_enabled=false.
	AuthEnabled func() bool

	// FilterPromptsForSession mirrors the buildPromptEnabledContext +
	// filterPromptsByEnabled pipeline in the web package: it filters the given
	// prompts using the enabledWhen CEL context of the named session, returning
	// the prompts unchanged when no context can be built. It is a closure so
	// handlers need not import the CEL context type from the web package. May be
	// nil; callers must nil-guard.
	FilterPromptsForSession func(prompts []configPkg.WebPrompt, sessionID string) []configPkg.WebPrompt

	// MigrateWorkspacePrompts mirrors Server.migrateWorkspacePrompts: it migrates
	// any legacy .md prompt files in a workspace to the .prompt.yaml format and
	// returns the files migrated this call. Idempotent. May be nil; callers must
	// nil-guard.
	MigrateWorkspacePrompts func(workingDir string) []configPkg.MigratedPrompt

	// LoadPromptsFromDirs mirrors Server.loadPromptsFromDirs: it loads and merges
	// prompts from a list of directories (relative paths resolved against
	// workspaceRoot, non-existent dirs ignored). May be nil; callers must
	// nil-guard.
	LoadPromptsFromDirs func(workspaceRoot string, dirs []string) []configPkg.WebPrompt

	// BuildPromptEnabledContext mirrors Server.buildPromptEnabledContext: it builds
	// the enabledWhen CEL evaluation context for a session, or nil when no context
	// can be built. May be nil; callers must nil-guard.
	BuildPromptEnabledContext func(sessionID string) *configPkg.PromptEnabledContext

	// ApplyWorkspaceNamespace mirrors Server.applyWorkspaceNamespace: it populates
	// the workspace/ACP/tools namespaces of ctx from workingDir, making the
	// requested dir authoritative for dir-based gates. May be nil; callers must
	// nil-guard.
	ApplyWorkspaceNamespace func(ctx *configPkg.PromptEnabledContext, workingDir string)

	// BuildWorkspacePromptEnabledContext mirrors
	// Server.buildWorkspacePromptEnabledContext: it builds a session-less CEL
	// context from the workspace/ACP/tools namespaces and default permission flags.
	// May be nil; callers must nil-guard.
	BuildWorkspacePromptEnabledContext func(workingDir string) *configPkg.PromptEnabledContext

	// FilterPromptsByEnabled mirrors Server.filterPromptsByEnabled: it filters
	// prompts using a prebuilt enabledWhen CEL context, returning all prompts when
	// ctx is nil or no evaluator is available. May be nil; callers must nil-guard.
	FilterPromptsByEnabled func(prompts []configPkg.WebPrompt, ctx *configPkg.PromptEnabledContext) []configPkg.WebPrompt

	// Store mirrors the value returned by Server.Store(): the session store used
	// for reading/writing session metadata and events. May be nil.
	Store *session.Store

	// SessionManager mirrors Server.sessionManager: the runtime conversation
	// manager used to look up live BackgroundSessions. May be nil.
	SessionManager *conversation.SessionManager

	// BroadcastSettingsUpdated mirrors Server.BroadcastSessionSettingsUpdated:
	// it broadcasts an advanced-settings change to all connected clients for the
	// given session. May be nil; callers must nil-guard.
	BroadcastSettingsUpdated func(sessionID string, settings map[string]bool)

	// BroadcastSessionDeleted mirrors Server.BroadcastSessionDeleted: it notifies
	// all connected clients that a session was deleted. May be nil; callers must
	// nil-guard.
	BroadcastSessionDeleted func(sessionID string)

	// BroadcastACPStartFailed mirrors Server.BroadcastACPStartFailed: it notifies
	// all connected clients that an ACP process failed to start for a session
	// creation attempt. May be nil; callers must nil-guard.
	BroadcastACPStartFailed func(sessionID, sessionName string, err error, command string)

	// BroadcastACPStopped mirrors Server.BroadcastACPStopped: it notifies all
	// connected clients that a session's ACP process was stopped (e.g. on
	// archive). May be nil; callers must nil-guard.
	BroadcastACPStopped func(sessionID, reason string)

	// BroadcastACPStarted mirrors Server.BroadcastACPStarted: it notifies all
	// connected clients that a session's ACP process was started (e.g. on
	// unarchive resume). May be nil; callers must nil-guard.
	BroadcastACPStarted func(sessionID string)

	// BroadcastSessionRenamed mirrors Server.BroadcastSessionRenamed: it notifies
	// all connected clients that a session was renamed. May be nil; callers must
	// nil-guard.
	BroadcastSessionRenamed func(sessionID, newName string)

	// BroadcastSessionPinned mirrors Server.BroadcastSessionPinned: it notifies
	// all connected clients that a session's pinned state changed. May be nil;
	// callers must nil-guard.
	BroadcastSessionPinned func(sessionID string, pinned bool)

	// BroadcastSessionArchived mirrors Server.BroadcastSessionArchived: it
	// notifies all connected clients that a session's archived state changed. The
	// optional reason is supplied when archiving. May be nil; callers must
	// nil-guard.
	BroadcastSessionArchived func(sessionID string, archived bool, reason ...session.ArchiveReason)

	// BroadcastSessionCreated mirrors Server.eventsManager.Broadcast for the
	// WSMsgTypeSessionCreated message: it notifies all global events clients that
	// a new session was created. May be nil; callers must nil-guard.
	BroadcastSessionCreated func(data map[string]interface{})

	// RemoveNegativeCache mirrors Server.negativeSessionCache.Remove: it evicts a
	// session ID from the negative (not-found) cache after the session is created.
	// May be nil; callers must nil-guard.
	RemoveNegativeCache func(sessionID string)

	// DefaultACPServer mirrors Server.config.ACPServer: the default ACP server
	// name used in the create-session response when the resolved workspace does
	// not specify one.
	DefaultACPServer string

	// APIPrefix mirrors Server.apiPrefix: the URL prefix for all API endpoints
	// (e.g. "" or "/mitto"). Used to parse path tokens and build callback URLs.
	APIPrefix string

	// CallbackIndex mirrors Server.callbackIndex: the in-memory token→session
	// index for periodic callback triggers. May be nil; callers must nil-guard.
	CallbackIndex *conversation.CallbackIndex

	// CallbackRateLimiter mirrors Server.callbackRateLimiter: the per-token rate
	// limiter for callback triggers. May be nil; callers must nil-guard.
	CallbackRateLimiter *conversation.CallbackRateLimiter

	// GetExternalPort mirrors Server.GetExternalPort: returns the configured
	// external port (0 if none). May be nil; callers must nil-guard.
	GetExternalPort func() int

	// IsExternalListenerRunning mirrors Server.IsExternalListenerRunning:
	// reports whether the external (0.0.0.0) listener is currently running.
	// May be nil; callers must nil-guard.
	IsExternalListenerRunning func() bool

	// TriggerPeriodicNow mirrors Server.periodicRunner.TriggerNow: triggers an
	// immediate periodic run for a session. May be nil; callers must nil-guard.
	TriggerPeriodicNow func(sessionID string, resetTimer bool) error

	// StopPeriodicForArchive mirrors Server.periodicRunner.StopPeriodicForArchive bound
	// to the "archived" stopped reason: it authoritatively stops a conversation's
	// periodic loop when the conversation is archived. May be nil; callers must nil-guard.
	StopPeriodicForArchive func(sessionID string)

	// ErrSessionBusy and ErrPeriodicNotEnabled mirror the web package's
	// periodic-runner sentinel errors. They are exposed here so callback handlers
	// can map TriggerPeriodicNow failures to HTTP status codes without importing
	// the web package (which would create an import cycle). May be nil.
	ErrSessionBusy        error
	ErrPeriodicNotEnabled error

	// PeriodicDelayFloor mirrors Server.periodicDelayFloor: the configured global
	// floor (in seconds) for the on-completion delay. When nil, handlers fall back
	// to the package default.
	PeriodicDelayFloor func() int

	// BroadcastPeriodicUpdated mirrors Server.BroadcastPeriodicUpdated: broadcasts
	// a periodic-config change to all connected clients for the given session
	// (nil periodic means deleted/disabled). May be nil; callers must nil-guard.
	BroadcastPeriodicUpdated func(sessionID string, periodic *session.PeriodicPrompt)

	// BroadcastBeadsCleanupProgress mirrors Server.BroadcastBeadsCleanupProgress:
	// it broadcasts a global-events message reporting bulk closed-issue cleanup
	// progress to all connected clients. May be nil.
	BroadcastBeadsCleanupProgress func(workingDir string, deleted, total int, done bool, errMsg string)

	// BootstrapOnCompletion mirrors Server.periodicRunner.BootstrapOnCompletion:
	// kicks off the very first run for a fresh onCompletion conversation. May be
	// nil; callers must nil-guard.
	BootstrapOnCompletion func(sessionID string)

	// QueueTitleWorker mirrors Server.queueTitleWorker: the background worker that
	// generates titles for queued messages. May be nil; callers must nil-guard.
	QueueTitleWorker *conversation.QueueTitleWorker

	// NotifyQueueUpdate mirrors Server.notifyQueueUpdate: broadcasts a queue
	// update (added/removed/cleared) to all connected clients for the given
	// session. May be nil; callers must nil-guard.
	NotifyQueueUpdate func(sessionID, action, messageID string)

	// NotifyQueueReorder mirrors Server.notifyQueueReorder: broadcasts a queue
	// reorder to all connected clients for the given session. May be nil; callers
	// must nil-guard.
	NotifyQueueReorder func(sessionID string, messages []session.QueuedMessage)

	// BeadsClient is the injectable bd client used by the beads handlers. When
	// nil, beadsClient() falls back to beads.NewClient() (the real bd binary).
	BeadsClient beads.Client

	// GenerateAuxTitle mirrors Server.auxiliaryManager.GenerateTitle: generates a
	// short title from a description via the workspace auxiliary session. May be
	// nil (no auxiliary manager wired); callers must nil-guard and fall back.
	GenerateAuxTitle func(ctx context.Context, workspaceUUID, description string) (string, error)

	// GetWorkspacePromptsAll mirrors Server.getWorkspacePromptsAll: returns the
	// full merged prompt list for a working directory, used to validate prompt
	// names for the beads "prompts" upstream. May be nil; callers must nil-guard.
	GetWorkspacePromptsAll func(workingDir string) []configPkg.WebPrompt

	// MCPServerURL returns the live Mitto MCP server URL (http://127.0.0.1:PORT/mcp),
	// using the actual runtime port when the embedded MCP server is running and the
	// well-known default port otherwise. May be nil; callers must nil-guard and fall
	// back to the default-port URL.
	MCPServerURL func() string

	// SyncConfigWorkspaces mirrors the server's write-back
	// (Server.config.Workspaces = SessionManager.GetWorkspaces()) performed after
	// adding or removing a workspace, keeping the server's Config view in sync with
	// the SessionManager. May be nil; callers must nil-guard.
	SyncConfigWorkspaces func()

	// RestartWorkspaceACP mirrors Server.acpProcessManager.RestartProcess: restarts
	// the shared ACP process for a workspace so MCP changes take effect. It is nil
	// when the server has no ACP process manager; the restart handler treats a nil
	// value as "ACP process manager not available".
	RestartWorkspaceACP func(workspaceUUID string) error

	// HasLiveWorkspaceACP mirrors Server.acpProcessManager.HasLiveProcess: reports
	// whether a live shared ACP process exists for a workspace UUID. It is nil when
	// the server has no ACP process manager; callers must nil-guard (treat nil as
	// "unknown" → false).
	HasLiveWorkspaceACP func(workspaceUUID string) bool

	// IsShutdown mirrors Server.IsShutdown: reports whether the server is shutting
	// down. Used by the health check to return 503 while draining. May be nil; the
	// health handler treats a nil value as "not shutting down".
	IsShutdown func() bool

	// AuthInfo mirrors the auth-manager state read by HandleAuthInfo: it returns
	// whether simple credential auth and Cloudflare Access are configured. It is a
	// closure (rather than exposing *middleware.AuthManager) so handlers need not
	// import the web/middleware package and to capture the late-initialized manager.
	// May be nil; the auth-info handler then reports both as false.
	AuthInfo func() (simple bool, cloudflare bool)

	// ImprovePrompt mirrors Server.auxiliaryManager.ImprovePrompt: it rewrites a
	// user prompt via the workspace-scoped auxiliary session. It is nil when the
	// server has no auxiliary manager; the improve-prompt handler treats a nil
	// value as "service unavailable" (503), matching the original behavior.
	ImprovePrompt func(ctx context.Context, workspaceUUID, prompt string) (string, error)
}

// Handlers groups the REST API handler methods extracted from the web server.
type Handlers struct {
	deps Deps

	beadsCleanupMu     sync.Mutex
	beadsCleanupActive map[string]bool
}

// New creates a new Handlers with the given dependencies.
func New(deps Deps) *Handlers {
	return &Handlers{deps: deps, beadsCleanupActive: make(map[string]bool)}
}
