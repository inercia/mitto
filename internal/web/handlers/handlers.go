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
	"log/slog"

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

	// BootstrapOnCompletion mirrors Server.periodicRunner.BootstrapOnCompletion:
	// kicks off the very first run for a fresh onCompletion conversation. May be
	// nil; callers must nil-guard.
	BootstrapOnCompletion func(sessionID string)
}

// Handlers groups the REST API handler methods extracted from the web server.
type Handlers struct {
	deps Deps
}

// New creates a new Handlers with the given dependencies.
func New(deps Deps) *Handlers {
	return &Handlers{deps: deps}
}
