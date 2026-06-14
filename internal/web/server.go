package web

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	builtinConfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/beads"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/defense"
	"github.com/inercia/mitto/internal/hooks"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
	mittoWeb "github.com/inercia/mitto/web"
)

// Config holds the web server configuration.
type Config struct {
	// Workspaces is the list of configured workspaces (ACP server + directory pairs).
	// If empty, a single workspace is created from ACPCommand/ACPServer/DefaultWorkingDir.
	Workspaces []configPkg.WorkspaceSettings

	// Legacy single-workspace fields (used if Workspaces is empty)
	ACPCommand        string
	ACPServer         string
	DefaultWorkingDir string

	AutoApprove bool
	Debug       bool
	// MittoConfig is the full Mitto configuration (used for /api/config endpoint)
	MittoConfig *configPkg.Config
	// StaticDir is an optional filesystem directory to serve static files from.
	// When set, files are served from this directory instead of the embedded assets.
	// This enables hot-reloading during development (just refresh the browser).
	StaticDir string

	// FromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	FromCLI bool
	// OnWorkspaceSave is called when workspaces are modified (only if FromCLI is false).
	OnWorkspaceSave WorkspaceSaveFunc
	// ConfigReadOnly indicates that configuration was loaded from a custom config file
	// (via --config flag). When true, the Settings dialog is disabled in the UI.
	// Note: RC file config is NOT fully read-only anymore - users can add servers via UI.
	ConfigReadOnly bool
	// RCFilePath is the path to the RC file if config was loaded from one.
	// This is used to show the user which file is being used.
	RCFilePath string
	// HasRCFileServers indicates whether any ACP servers came from the RC file.
	// When true, those servers are marked as read-only in the UI (cannot edit/delete).
	HasRCFileServers bool
	// PromptsCache provides cached access to global prompts from MITTO_DIR/prompts/.
	// If nil, global file prompts are not loaded.
	PromptsCache *configPkg.PromptsCache
	// AccessLog is the configuration for security access logging.
	// If Path is empty, access logging is disabled.
	AccessLog AccessLogConfig

	// DisableAuxiliaryPrewarm disables auxiliary session pre-warming on process creation.
	// Used in tests to avoid interference with mock ACP servers.
	DisableAuxiliaryPrewarm bool

	// Logger overrides the default logger (logging.Web()). When nil, the global
	// web logger is used. Primarily used by tests to capture log output.
	Logger *slog.Logger
}

// GetWorkspaces returns the effective list of workspaces.
// If Workspaces is empty, creates a single workspace from legacy fields.
func (c *Config) GetWorkspaces() []configPkg.WorkspaceSettings {
	if len(c.Workspaces) > 0 {
		return c.Workspaces
	}
	// Create single workspace from legacy fields
	workDir := c.DefaultWorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	// ACPCommandOverride is set from ACPCommand if it's a legacy override value.
	// For the CLI legacy path, ACPCommand on Config is the user-provided command.
	return []configPkg.WorkspaceSettings{{
		ACPServer:          c.ACPServer,
		ACPCommandOverride: c.ACPCommand, // Legacy: CLI-provided command becomes an override
		WorkingDir:         workDir,
	}}
}

// GetDefaultWorkspace returns the first (default) workspace.
func (c *Config) GetDefaultWorkspace() *configPkg.WorkspaceSettings {
	workspaces := c.GetWorkspaces()
	if len(workspaces) == 0 {
		return nil
	}
	return &workspaces[0]
}

// GetWorkspaceByDir returns the workspace for a given directory, or nil if not found.
func (c *Config) GetWorkspaceByDir(dir string) *configPkg.WorkspaceSettings {
	for i := range c.Workspaces {
		if c.Workspaces[i].WorkingDir == dir {
			return &c.Workspaces[i]
		}
	}
	// Check legacy fields
	if len(c.Workspaces) == 0 && c.DefaultWorkingDir == dir {
		return &configPkg.WorkspaceSettings{
			ACPServer:          c.ACPServer,
			ACPCommandOverride: c.ACPCommand, // Legacy: CLI-provided command becomes an override
			WorkingDir:         dir,
		}
	}
	return nil
}

// Server is the web server for Mitto.
type Server struct {
	config     Config
	httpServer *http.Server
	logger     *slog.Logger
	shutdown   atomic.Bool

	// apiPrefix is the URL prefix for all API and WebSocket endpoints.
	// Static assets and root landing page are not prefixed.
	apiPrefix string

	// Global events manager for broadcasting session lifecycle events
	eventsManager *GlobalEventsManager

	// Session manager for background sessions that persist across WebSocket disconnects
	sessionManager *SessionManager

	// Session store for persistence (owned by the server, shared across handlers)
	store *session.Store

	// Auth manager for handling authentication (nil if auth is disabled)
	authManager *AuthManager

	// CSRF manager for protecting state-changing requests
	csrfManager *CSRFManager

	// Security components
	rateLimiter      *GeneralRateLimiter
	wsSecurityConfig WebSocketSecurityConfig
	proxyChecker     *TrustedProxyChecker

	// External access listener management
	externalListener   net.Listener
	externalHTTPServer *http.Server // Separate server for external connections (marks them as external)
	externalMu         sync.Mutex
	externalPort       int // Port for external listener (same as main port by default)

	// Queue title worker for generating titles for queued messages
	queueTitleWorker *QueueTitleWorker

	// Periodic runner for scheduled prompt delivery
	periodicRunner *PeriodicRunner

	// Callback index for mapping callback tokens to session IDs
	callbackIndex       *CallbackIndex
	callbackRateLimiter *CallbackRateLimiter

	// Access logger for security-relevant events (nil if disabled)
	accessLogger *AccessLogger

	// MCP debug server for exposing debugging tools
	mcpServer *mcpserver.Server

	// Scanner defense for blocking malicious IPs at the connection level
	defense *defense.ScannerDefense

	// Prompts watcher for monitoring prompt file changes
	promptsWatcher *configPkg.PromptsWatcher

	// ACP process manager for workspace-scoped shared processes
	acpProcessManager *ACPProcessManager

	// Auxiliary manager for workspace-scoped auxiliary tasks (title generation, etc.)
	auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager

	// Negative session cache for circuit-breaking "Session not found" error storms.
	// Caches session IDs known to not exist, preventing repeated filesystem lookups.
	negativeSessionCache *NegativeSessionCache

	// beads is the injectable Client for bd operations.
	// When nil, beadsClient() falls back to beads.NewClient() (real bd binary).
	beads beads.Client

	// Health monitor for external address reachability checking
	healthMonitor          *hooks.HealthMonitor
	healthMonitorMu        sync.Mutex
	hookPort               int                        // Port used for hook commands
	onHookProcessChanged   func(*hooks.Process)       // Callback to update shutdown manager when hooks restart
	onHealthMonitorChanged func(*hooks.HealthMonitor) // Callback to update shutdown manager when health monitor changes

	// recentStartFails deduplicates BroadcastACPStartFailed calls for the same session.
	// When multiple goroutines coalesce on a single resume failure they all receive the
	// error and each tries to broadcast; only the first broadcast per session per window
	// is emitted, followers are suppressed.
	recentStartFailsMu sync.Mutex
	recentStartFails   map[string]time.Time
}

// APIPrefix returns the URL prefix for all API and WebSocket endpoints.
// This is used by the frontend to construct API URLs.
func (s *Server) APIPrefix() string {
	return s.apiPrefix
}

// NewServer creates a new web server.
func NewServer(config Config) (*Server, error) {
	logger := config.Logger
	if logger == nil {
		logger = logging.Web()
	}

	// Create session store for persistence
	store, err := session.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create session store: %w", err)
	}

	// Run data migrations before any other operations
	migrationCtx := buildMigrationContext(config.MittoConfig)
	if err := store.RunMigrations(migrationCtx); err != nil {
		logger.Warn("Failed to run migrations", "error", err)
		// Continue anyway - migrations are best-effort
	}

	// Cleanup old images on startup
	if removed, err := store.CleanupOldImages(session.ImageCleanupAge, session.ImagePreserveRecent); err != nil {
		logger.Warn("Failed to cleanup old images", "error", err)
	} else if removed > 0 {
		logger.Info("Cleaned up old images on startup", "removed_count", removed)
	}

	// Cleanup old files on startup
	if removed, err := store.CleanupOldFiles(session.FileCleanupAge, session.FilePreserveRecent); err != nil {
		logger.Warn("Failed to cleanup old files", "error", err)
	} else if removed > 0 {
		logger.Info("Cleaned up old files on startup", "removed_count", removed)
	}

	// Cleanup archived sessions on startup based on retention policy
	if config.MittoConfig != nil && config.MittoConfig.Session != nil {
		retentionPeriod := config.MittoConfig.Session.GetArchiveRetentionPeriod()
		if removed, err := store.CleanupArchivedSessions(retentionPeriod); err != nil {
			logger.Warn("Failed to cleanup archived sessions", "error", err)
		} else if removed > 0 {
			logger.Info("Cleaned up archived sessions on startup", "removed_count", removed, "retention_period", retentionPeriod)
		}
	}

	// Crash-recovery: remove git worktrees orphaned by deleted/crashed sessions
	// and clear stale worktree metadata. Best-effort; never blocks startup.
	recoverOrphanedWorktrees(store, logger)

	// Ensure builtin agent definitions are deployed to the agents directory.
	// This is done on every startup so new agents added in updates are deployed automatically.
	if agentsDir, err := appdir.BuiltinAgentsDir(); err == nil {
		if deployed, err := builtinConfig.EnsureBuiltinAgents(agentsDir); err != nil {
			logger.Warn("Failed to deploy builtin agents", "error", err)
		} else if deployed {
			logger.Info("Builtin agents deployed/updated", "dir", agentsDir)
		}
	}

	// Get API prefix early (needed by session manager for HTTP file links)
	apiPrefix := configPkg.DefaultAPIPrefix
	if config.MittoConfig != nil && config.MittoConfig.Web.APIPrefix != "" {
		apiPrefix = config.MittoConfig.Web.APIPrefix
	}

	// ACP command/cwd/env are always resolved from global config at runtime —
	// they are never stored on the workspace struct (those fields were removed).
	workspaces := config.Workspaces // Use direct field, not GetWorkspaces() which creates legacy workspace

	// Create session manager with workspace support
	var sessionMgr *SessionManager
	if len(workspaces) > 0 || !config.FromCLI {
		// Use new options-based constructor for workspace persistence support
		sessionMgr = NewSessionManagerWithOptions(SessionManagerOptions{
			Workspaces:      workspaces,
			AutoApprove:     config.AutoApprove,
			Logger:          logger,
			FromCLI:         config.FromCLI,
			OnWorkspaceSave: config.OnWorkspaceSave,
			APIPrefix:       apiPrefix,
		})
	} else {
		// Legacy single-workspace mode (CLI with no --dir flags and no saved workspaces)
		sessionMgr = NewSessionManager(config.ACPCommand, config.ACPServer, config.AutoApprove, logger)
		sessionMgr.SetAPIPrefix(apiPrefix)
	}
	sessionMgr.SetStore(store)

	// Create shared ACP process manager for workspace-level process sharing.
	// Clean up orphaned ACP processes from any previous Mitto instance that crashed
	// without running its shutdown sequence (not done in tests to avoid killing
	// the developer's live ACP servers when running the test suite).
	acpProcessMgr := NewACPProcessManager(context.Background(), logger)
	if os.Getenv("MITTO_TEST_MODE") == "" {
		acpProcessMgr.CleanupOrphanedProcesses()
	}
	acpProcessMgr.DisableAuxiliary = config.DisableAuxiliaryPrewarm || os.Getenv("MITTO_TEST_MODE") != ""
	// Set workspace config provider so process manager can resolve auxiliary config.
	acpProcessMgr.WorkspaceConfigProvider = func(workspaceUUID string) *configPkg.WorkspaceSettings {
		return sessionMgr.GetWorkspaceByUUID(workspaceUUID)
	}
	sessionMgr.SetACPProcessManager(acpProcessMgr)

	// Start ACP process garbage collector to clean up idle sessions and processes.
	// The GC periodically checks for sessions with no observers, no active prompts,
	// and no pending work, and stops shared ACP processes that have no active sessions.
	if !config.DisableAuxiliaryPrewarm && os.Getenv("MITTO_TEST_MODE") == "" {
		gcConfig := GCConfig{}
		// Apply periodic suspend threshold from settings if configured.
		if config.MittoConfig != nil && config.MittoConfig.Session != nil {
			if d, enabled := config.MittoConfig.Session.ParsePeriodicSuspendTimeout(); enabled {
				gcConfig.PeriodicSuspendThreshold = d
			} else {
				// Explicitly disabled — set to 0 so StartGC doesn't apply default.
				// We need to bypass StartGC's "apply default for <= 0" logic.
				gcConfig.PeriodicSuspendThreshold = -1
			}
		}
		// Apply memory recycle threshold from settings if configured (opt-in).
		// 0/disabled needs no action since the GCConfig default is 0 = disabled.
		if config.MittoConfig != nil && config.MittoConfig.Session != nil {
			if bytes, enabled := config.MittoConfig.Session.ParseMemoryRecycleThreshold(); enabled {
				gcConfig.MemoryRecycleThreshold = bytes
			}
		}
		acpProcessMgr.StartGC(gcConfig, func() map[string][]SessionInfo {
			return sessionMgr.GetSessionInfoByWorkspace()
		}, func(sessionID string) {
			sessionMgr.CloseIdleSession(sessionID)
		})
	}

	// Set global conversations config for message processing
	if config.MittoConfig != nil {
		sessionMgr.SetGlobalConversations(config.MittoConfig.Conversations)
	}

	// Set full MittoConfig for agent-specific lookups
	if config.MittoConfig != nil {
		sessionMgr.SetMittoConfig(config.MittoConfig)
	}

	// Set global restricted runner config for sandboxed execution
	if config.MittoConfig != nil && config.MittoConfig.RestrictedRunners != nil {
		sessionMgr.SetGlobalRestrictedRunners(config.MittoConfig.RestrictedRunners)
		logger.Info("Global restricted runners configured",
			"runner_types", len(config.MittoConfig.RestrictedRunners))
	}

	// Load and set processors from processors directory
	if processorsDir, err := appdir.ProcessorsDir(); err == nil {
		procMgr := processors.NewManager(processorsDir, logger)
		if err := procMgr.Load(); err != nil {
			logger.Warn("Failed to load processors", "error", err)
		} else if len(procMgr.Processors()) > 0 {
			sessionMgr.SetProcessorManager(procMgr)
			logger.Info("Loaded processors", "count", len(procMgr.Processors()))
		}
	}

	// Initialize auth manager if auth is configured
	var authMgr *AuthManager
	if config.MittoConfig != nil && config.MittoConfig.Web.Auth != nil {
		authMgr = NewAuthManager(config.MittoConfig.Web.Auth)
		logger.Info("Authentication enabled", "type", "simple")
	}

	// Initialize security components from config
	var securityCfg *configPkg.WebSecurity
	if config.MittoConfig != nil {
		securityCfg = config.MittoConfig.Web.Security
	}

	// Auto-detect tunnel mode: when hooks.up is configured, a tunnel proxy
	// (e.g., cloudflared, ngrok) connects from localhost. Automatically add
	// 127.0.0.1 as a trusted proxy so the defense middleware can extract the
	// real client IP from forwarded headers. Without this, every tunnel user
	// would need to manually configure trusted_proxies.
	hasTunnelHook := config.MittoConfig != nil && config.MittoConfig.Web.Hooks.Up.Command != ""
	if hasTunnelHook {
		if securityCfg == nil {
			securityCfg = &configPkg.WebSecurity{}
		}
		// Add localhost (both IPv4 and IPv6) as trusted proxy if not already
		// present. cloudflared may connect via either [::1] or 127.0.0.1
		// depending on the OS and how "localhost" resolves.
		hasIPv4 := false
		hasIPv6 := false
		for _, p := range securityCfg.TrustedProxies {
			if p == "127.0.0.1" || p == "127.0.0.0/8" {
				hasIPv4 = true
			}
			if p == "::1" {
				hasIPv6 = true
			}
		}
		if !hasIPv4 {
			securityCfg.TrustedProxies = append(securityCfg.TrustedProxies, "127.0.0.1")
		}
		if !hasIPv6 {
			securityCfg.TrustedProxies = append(securityCfg.TrustedProxies, "::1")
		}
		if !hasIPv4 || !hasIPv6 {
			logger.Info("Tunnel hook detected, auto-added localhost as trusted proxy")
		}
	}

	// Initialize trusted proxy checker
	var proxyChecker *TrustedProxyChecker
	if securityCfg != nil && len(securityCfg.TrustedProxies) > 0 {
		proxyChecker = NewTrustedProxyChecker(securityCfg.TrustedProxies)
		SetDefaultProxyChecker(proxyChecker)
		logger.Info("Trusted proxies configured", "count", len(securityCfg.TrustedProxies))
	}

	// Initialize rate limiter
	rateLimitConfig := DefaultRateLimitConfig()
	if securityCfg != nil {
		if securityCfg.RateLimitRPS > 0 {
			rateLimitConfig.RequestsPerSecond = securityCfg.RateLimitRPS
		}
		if securityCfg.RateLimitBurst > 0 {
			rateLimitConfig.BurstSize = securityCfg.RateLimitBurst
		}
	}
	rateLimiter := NewGeneralRateLimiter(rateLimitConfig)

	// Initialize WebSocket security config
	wsSecurityConfig := DefaultWebSocketSecurityConfig()
	if securityCfg != nil {
		if len(securityCfg.AllowedOrigins) > 0 {
			wsSecurityConfig.AllowedOrigins = securityCfg.AllowedOrigins
		}
		if securityCfg.MaxWSMessageSize > 0 {
			wsSecurityConfig.MaxMessageSize = securityCfg.MaxWSMessageSize
		}
	}

	// Initialize CSRF manager
	csrfMgr := NewCSRFManager()

	// Set API prefix on auth manager for public path matching
	if authMgr != nil {
		authMgr.SetAPIPrefix(apiPrefix)
	}

	// Set API prefix on CSRF manager for exempt path matching
	csrfMgr.SetAPIPrefix(apiPrefix)

	// Initialize access logger (nil if disabled)
	var accessLogger *AccessLogger
	if config.AccessLog.Path != "" {
		accessLogger = NewAccessLogger(config.AccessLog)
		if accessLogger != nil {
			accessLogger.SetAuthManager(authMgr)
			accessLogger.SetAPIPrefix(apiPrefix)
			logger.Info("Access logging enabled", "path", config.AccessLog.Path)
		}
	}

	eventsManager := NewGlobalEventsManager()

	// Initialize auxiliary manager for workspace-scoped auxiliary tasks
	// This provides high-level operations (title generation, follow-up analysis, etc.)
	// Note: reuses acpProcessMgr (the same instance used by sessionMgr) so that
	// auxiliary sessions can find the workspace processes registered by user sessions.
	auxiliaryManager := auxiliary.NewWorkspaceAuxiliaryManager(acpProcessMgr, logger)
	sessionMgr.SetAuxiliaryManager(auxiliaryManager)

	// Initialize scanner defense
	// Enabled by default when external access is configured (ExternalPort >= 0)
	var scannerDefense *defense.ScannerDefense
	var webConfig *configPkg.WebConfig
	if config.MittoConfig != nil {
		webConfig = &config.MittoConfig.Web
	}
	if shouldEnableScannerDefense(webConfig) {
		defenseConfig := configToDefenseConfig(getScannerDefenseConfig(webConfig), true)

		// When a tunnel hook is configured, increase rate limits if the user
		// hasn't explicitly set them. Tunnel proxies (cloudflared, ngrok) forward
		// all browser requests through a single origin, so a page load generating
		// ~30 requests can easily exceed the default 100 req/min limit.
		if hasTunnelHook {
			explicitCfg := getScannerDefenseConfig(webConfig)
			if explicitCfg == nil || explicitCfg.RateLimit == 0 {
				defenseConfig.RateLimit = 500 // 5x default for tunnel traffic
				logger.Info("Tunnel hook detected, increased scanner defense rate limit",
					"rate_limit", defenseConfig.RateLimit)
			}
		}

		var err error
		scannerDefense, err = defense.New(defenseConfig, logger)
		if err != nil {
			logger.Warn("Failed to initialize scanner defense", "error", err)
			// Continue without defense - graceful degradation
		} else {
			logger.Info("Scanner defense enabled",
				"component", "defense",
				"blocked_ips", scannerDefense.BlockedCount(),
				"external_port", config.MittoConfig.Web.ExternalPort,
			)
		}
	}

	s := &Server{
		config:               config,
		logger:               logger,
		apiPrefix:            apiPrefix,
		eventsManager:        eventsManager,
		sessionManager:       sessionMgr,
		store:                store,
		authManager:          authMgr,
		csrfManager:          csrfMgr,
		rateLimiter:          rateLimiter,
		wsSecurityConfig:     wsSecurityConfig,
		proxyChecker:         proxyChecker,
		accessLogger:         accessLogger,
		defense:              scannerDefense,
		acpProcessManager:    acpProcessMgr,
		auxiliaryManager:     auxiliaryManager,
		negativeSessionCache: NewNegativeSessionCache(),
		recentStartFails:     make(map[string]time.Time),
		beads:                beads.NewClient(),
	}

	// Set events manager in session manager for broadcasting
	sessionMgr.SetEventsManager(eventsManager)

	// Surface a toast when the GC's memory-recycle tier (Tier 4) restarts a
	// memory-bloated idle agent process. Resolve a friendly workspace name here
	// (the GC only knows the workspace UUID).
	acpProcessMgr.onMemoryRecycled = func(workspaceUUID string, rssBytes, threshold uint64, sessionCount int) {
		workspaceName := ""
		workingDir := ""
		if ws := sessionMgr.GetWorkspaceByUUID(workspaceUUID); ws != nil {
			workspaceName = ws.Name
			workingDir = ws.WorkingDir
		}
		s.BroadcastMemoryRecycled(workspaceUUID, workspaceName, workingDir, rssBytes, threshold, sessionCount)
	}

	// Initialize MCP server.
	// This serves both global tools and session-scoped tools.
	// Check if MCP server is enabled (default: true)
	// Guard against nil config (can happen in tests)
	mcpEnabled := config.MittoConfig != nil && config.MittoConfig.MCP.IsEnabled()
	if mcpEnabled {
		// Get MCP host and port from config, or use defaults
		mcpHost := config.MittoConfig.MCP.GetHost()
		mcpPort := config.MittoConfig.MCP.GetPort()

		mcpSrv, err := mcpserver.NewServer(
			mcpserver.Config{Host: mcpHost, Port: mcpPort},
			mcpserver.Dependencies{
				Store:          store,
				Config:         config.MittoConfig,
				SessionManager: &sessionManagerAdapter{sm: sessionMgr},
				PromptsCache:   config.PromptsCache,
			},
		)
		if err != nil {
			logger.Warn("Failed to create MCP server", "error", err)
		} else {
			s.mcpServer = mcpSrv
			if err := mcpSrv.Start(context.Background()); err != nil {
				// A common cause is another Mitto instance already listening on the
				// configured MCP port (default 5757). Surface that hypothesis in the
				// log message so users / ops can quickly diagnose port-collision
				// situations.
				msg := "Failed to start MCP server"
				errStr := err.Error()
				if strings.Contains(errStr, "address already in use") || strings.Contains(errStr, "bind:") {
					msg = "Failed to start MCP server — port already in use (another Mitto instance may be running). Mitto will continue without MCP; session-scoped tools, prompts, and stdio proxies will be unavailable until this is resolved."
				}
				logger.Warn(msg, "error", err, "host", mcpHost, "port", mcpPort)
			} else {
				logger.Info("MCP server started", "port", mcpSrv.Port())
				// Set MCP URL on process manager so auxiliary processor sessions
				// can use a stdio proxy to access Mitto tools.
				acpProcessMgr.MCPServerURL = fmt.Sprintf("http://127.0.0.1:%d/mcp", mcpSrv.Port())
			}
			// Pass MCP server to session manager for session registration
			sessionMgr.SetGlobalMCPServer(mcpSrv)
		}
	} else {
		logger.Info("MCP server disabled by configuration")
	}

	// Initialize queue title worker
	s.queueTitleWorker = NewQueueTitleWorker(store, sessionMgr, auxiliaryManager, logger)
	s.queueTitleWorker.OnTitleGenerated = func(sessionID, messageID, title string) {
		// Broadcast title update to all connected clients
		s.eventsManager.Broadcast(WSMsgTypeQueueMessageTitled, map[string]string{
			"session_id": sessionID,
			"message_id": messageID,
			"title":      title,
		})
	}

	// Initialize periodic runner for scheduled prompt delivery and session housekeeping
	s.periodicRunner = NewPeriodicRunner(store, sessionMgr, logger)
	s.periodicRunner.SetOnPeriodicStarted(s.BroadcastPeriodicStarted)
	s.periodicRunner.SetOnAutoArchive(func(sessionID string) {
		s.BroadcastACPStopped(sessionID, "auto_archived")
		s.BroadcastSessionArchived(sessionID, true)
	})

	// Configure startup delay for periodic runner to avoid thundering herd.
	// Interactive sessions resume first via WebSocket; periodic sessions can afford to wait.
	startupPeriodicDelay := configPkg.DefaultStartupPeriodicDelay
	if config.MittoConfig != nil && config.MittoConfig.Session != nil {
		startupPeriodicDelay = config.MittoConfig.Session.GetStartupPeriodicDelay()
	}
	if startupPeriodicDelay > 0 {
		s.periodicRunner.SetStartupDelay(startupPeriodicDelay)
	}

	// Configure stagger delay between consecutive periodic session resumes.
	// Uses the same startup_stagger_ms config as the queue's ProcessPendingQueues.
	staggerMs := configPkg.DefaultStartupStaggerMs
	if config.MittoConfig != nil && config.MittoConfig.Session != nil {
		staggerMs = config.MittoConfig.Session.GetStartupStaggerMs()
	}
	if staggerMs > 0 {
		s.periodicRunner.SetResumeStagger(time.Duration(staggerMs) * time.Millisecond)
	}

	// Initialize callback index and rate limiter
	s.callbackIndex = NewCallbackIndex()
	s.callbackRateLimiter = NewCallbackRateLimiter()

	// Configure auto-archive inactive sessions if enabled
	if config.MittoConfig != nil && config.MittoConfig.Session != nil {
		autoArchivePeriod := config.MittoConfig.Session.GetAutoArchiveInactiveAfter()
		if autoArchiveDuration, err := parseAutoArchivePeriod(autoArchivePeriod); err != nil {
			logger.Warn("Invalid auto-archive period, feature disabled", "period", autoArchivePeriod, "error", err)
		} else if autoArchiveDuration > 0 {
			s.periodicRunner.SetAutoArchiveAfter(autoArchiveDuration)
			logger.Info("Auto-archive inactive sessions enabled", "period", autoArchivePeriod, "duration", autoArchiveDuration)
		}

		// Configure periodic cleanup of archived sessions
		retentionPeriod := config.MittoConfig.Session.GetArchiveRetentionPeriod()
		if retentionPeriod != "" {
			s.periodicRunner.SetArchiveRetentionPeriod(retentionPeriod)
			logger.Info("Periodic archive retention cleanup enabled", "retention_period", retentionPeriod)
		}
	}

	// Set prompt resolver for periodic runner and session manager — resolves prompt names to text at execution time.
	// Both use the same resolver: PeriodicRunner for scheduled prompts, SessionManager for interactive prompt-by-name.
	promptResolverFunc := func(promptName string, workingDir string) (string, error) {
		return s.resolvePromptByName(promptName, workingDir)
	}
	s.periodicRunner.SetPromptResolver(promptResolverFunc)
	if s.sessionManager != nil {
		s.sessionManager.SetPromptResolver(promptResolverFunc)
	}

	s.periodicRunner.Start()

	// Wire up periodic runner to MCP server for the run-now tool.
	// The periodic runner is created after the MCP server, so we use a setter.
	if s.mcpServer != nil {
		s.mcpServer.SetPeriodicRunner(s.periodicRunner)
	}

	// Build callback index from existing sessions
	s.buildCallbackIndex()

	// Initialize prompts watcher for monitoring prompt file changes
	if promptsWatcher, err := configPkg.NewPromptsWatcher(logger); err != nil {
		logger.Warn("Failed to create prompts watcher", "error", err)
	} else {
		s.promptsWatcher = promptsWatcher
		// Subscribe the server to receive prompts change notifications
		// The server will broadcast these to all connected clients
		s.promptsWatcher.Subscribe(s, s.getPromptsWatchDirs())
		s.promptsWatcher.Start()
		logger.Info("Prompts watcher started", "dirs", s.getPromptsWatchDirs())
	}

	// Set up routes
	mux := http.NewServeMux()

	// Auth routes (always register, they handle their own enabled/disabled state)
	// These use the API prefix for security through obscurity
	if authMgr != nil {
		mux.HandleFunc(apiPrefix+"/api/login", authMgr.HandleLogin)
		mux.HandleFunc(apiPrefix+"/api/logout", authMgr.HandleLogout)
	}

	// CSRF token endpoint (always available for getting tokens)
	mux.HandleFunc(apiPrefix+"/api/csrf-token", csrfMgr.HandleCSRFToken)

	// API routes - all use the API prefix for security through obscurity
	mux.HandleFunc(apiPrefix+"/api/sessions", s.handleSessions)
	mux.HandleFunc(apiPrefix+"/api/sessions/running", s.handleRunningSessions)
	mux.HandleFunc(apiPrefix+"/api/sessions/", s.handleSessionDetail)
	mux.HandleFunc(apiPrefix+"/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc(apiPrefix+"/api/workspaces/", s.handleWorkspaceDetail)
	mux.HandleFunc(apiPrefix+"/api/workspace-prompts", s.handleWorkspacePrompts)
	mux.HandleFunc(apiPrefix+"/api/workspace-prompts/toggle-enabled", s.handleWorkspacePromptsToggleEnabled)
	mux.HandleFunc(apiPrefix+"/api/workspace-processors", s.handleWorkspaceProcessors)
	mux.HandleFunc(apiPrefix+"/api/workspace-processors/toggle-enabled", s.handleWorkspaceProcessorsToggleEnabled)
	mux.HandleFunc(apiPrefix+"/api/workspace-mcp-tools", s.handleWorkspaceMCPTools)
	mux.HandleFunc(apiPrefix+"/api/workspace-mcp-install", s.handleWorkspaceMCPInstall)
	mux.HandleFunc(apiPrefix+"/api/workspace-mcp-remove", s.handleWorkspaceMCPRemove)
	mux.HandleFunc(apiPrefix+"/api/workspace-metadata", s.handleWorkspaceMetadata)
	mux.HandleFunc(apiPrefix+"/api/folder-group", s.handleFolderGroup)
	mux.HandleFunc(apiPrefix+"/api/workspace/user-data-schema", s.handleWorkspaceUserDataSchema)
	mux.HandleFunc(apiPrefix+"/api/config", s.handleConfig)
	mux.HandleFunc(apiPrefix+"/api/agent-types", s.handleAgentTypes)
	mux.HandleFunc(apiPrefix+"/api/agents/scan", s.handleScanAgents)
	mux.HandleFunc(apiPrefix+"/api/agents/confirm", s.handleConfirmAgents)
	mux.HandleFunc(apiPrefix+"/api/supported-runners", s.handleSupportedRunners)
	mux.HandleFunc(apiPrefix+"/api/runner-defaults", s.handleRunnerDefaults)
	mux.HandleFunc(apiPrefix+"/api/advanced-flags", s.handleAdvancedFlags)
	mux.HandleFunc(apiPrefix+"/api/external-status", s.handleExternalStatus)
	mux.HandleFunc(apiPrefix+"/api/aux/improve-prompt", s.handleImprovePrompt)
	mux.HandleFunc(apiPrefix+"/api/badge-click", s.handleBadgeClick)
	mux.HandleFunc(apiPrefix+"/api/beads/list", s.handleBeadsList)
	mux.HandleFunc(apiPrefix+"/api/beads/show", s.handleBeadsShow)
	mux.HandleFunc(apiPrefix+"/api/beads/create", s.handleBeadsCreate)
	mux.HandleFunc(apiPrefix+"/api/beads/cleanup", s.handleBeadsCleanup)
	mux.HandleFunc(apiPrefix+"/api/beads/delete", s.handleBeadsDelete)
	mux.HandleFunc(apiPrefix+"/api/beads/status", s.handleBeadsStatus)
	mux.HandleFunc(apiPrefix+"/api/beads/update", s.handleBeadsUpdate)
	mux.HandleFunc(apiPrefix+"/api/beads/comment", s.handleBeadsComment)
	mux.HandleFunc(apiPrefix+"/api/beads/dep", s.handleBeadsDep)
	mux.HandleFunc(apiPrefix+"/api/beads/config", s.handleBeadsConfig)
	mux.HandleFunc(apiPrefix+"/api/beads/upstream", s.handleBeadsUpstream)
	mux.HandleFunc(apiPrefix+"/api/beads/sync", s.handleBeadsSync)
	mux.HandleFunc(apiPrefix+"/api/ui-preferences", s.handleUIPreferences)

	// File save endpoints - restricted to localhost only (used by native macOS app)
	mux.HandleFunc(apiPrefix+"/api/save-file-to-path", s.handleSaveFileToPath)
	mux.HandleFunc(apiPrefix+"/api/check-file-exists", s.handleCheckFileExists)

	// Auth info endpoint (public, used by login page to adapt its UI)
	mux.HandleFunc(apiPrefix+"/api/auth-info", s.HandleAuthInfo)

	// M3: Health check endpoint for load balancer integration and monitoring
	// This endpoint is intentionally NOT behind auth to allow health checks
	mux.HandleFunc(apiPrefix+"/api/health", s.handleHealthCheck)

	// Callback trigger endpoint (public, no auth required)
	mux.HandleFunc(apiPrefix+"/api/callback/", s.handleCallbackTrigger)

	// File server endpoint - serves files from workspace directories (for web browser access)
	fileServer := NewFileServer(sessionMgr, logger)
	mux.Handle(apiPrefix+"/api/files", fileServer)

	// WebSocket endpoints - also use the API prefix
	mux.HandleFunc(apiPrefix+"/api/events", s.handleGlobalEventsWS) // Global events (session lifecycle)

	// Robots.txt: discourage bot crawlers from indexing
	mux.HandleFunc("/robots.txt", handleRobotsTxt)
	if apiPrefix != "" {
		mux.HandleFunc(apiPrefix+"/robots.txt", handleRobotsTxt)
	}

	// Static files: use filesystem directory if specified, otherwise use embedded assets
	var staticFS fs.FS
	if config.StaticDir != "" {
		// Use filesystem directory (enables hot-reloading during development)
		staticFS = os.DirFS(config.StaticDir)
		if logger != nil {
			logger.Info("Serving static files from filesystem", "dir", config.StaticDir)
		}
	} else {
		// Use embedded assets (default, for production)
		var err error
		staticFS, err = fs.Sub(mittoWeb.StaticFS, "static")
		if err != nil {
			return nil, err
		}
	}

	// Serve static files with proper content types and security
	// Mount at both root (/) and API prefix (/mitto/) to support both local and proxied access
	staticHandler := s.staticFileHandler(staticFS)
	mux.Handle("/", staticHandler)
	if apiPrefix != "" {
		// Also serve static files under the API prefix for proxied access
		// e.g., /mitto/viewer.html -> serves viewer.html from static files
		mux.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, staticHandler))
	}

	// Wrap with auth middleware if enabled
	var handler http.Handler = mux
	if authMgr != nil && authMgr.IsEnabled() {
		handler = authMgr.AuthMiddleware(mux)
	}

	// Wrap with CSRF middleware (applies after auth, before request processing)
	// This protects all state-changing API requests (POST, PUT, PATCH, DELETE)
	handler = csrfMgr.CSRFMiddleware(handler)

	// Wrap with security middlewares (applied in reverse order)
	// 1. Request size limit (1MB max for request bodies)
	handler = requestSizeLimitMiddleware(1 * 1024 * 1024)(handler)

	// 2. Rate limiting for API endpoints
	handler = rateLimiter.Middleware(handler)

	// 3. Request timeout (excludes WebSocket connections)
	handler = requestTimeoutMiddleware(DefaultRequestTimeout)(handler)

	// 4. Security headers (non-CSP headers)
	headerSecurityConfig := DefaultSecurityConfig()
	handler = securityHeadersMiddleware(headerSecurityConfig)(handler)

	// 5. CSP nonce injection for HTML responses
	// This must come after security headers but before hide server info
	// so that CSP headers are properly set with nonces for HTML responses.
	// Also injects the API prefix for frontend JavaScript to use.
	allowExternalImages := false
	if config.MittoConfig != nil && config.MittoConfig.Conversations != nil {
		allowExternalImages = config.MittoConfig.Conversations.AreExternalImagesEnabled()
	}
	handler = cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{
		config:              headerSecurityConfig,
		apiPrefix:           apiPrefix,
		allowExternalImages: allowExternalImages,
	})(handler)

	// 6. Gzip compression for external connections only
	// Compresses text/html, text/css, application/javascript, application/json, etc.
	// Skips compression for localhost (no network benefit, just CPU overhead)
	// Skips WebSocket upgrades (they use permessage-deflate compression)
	handler = gzipMiddleware(handler)

	// 7. Hide server info (outermost to catch all responses)
	handler = hideServerInfoMiddleware(handler)

	// Wrap with logging middleware
	handler = s.loggingMiddleware(handler)

	// 8. Access logging for security-relevant events (outermost to capture final status)
	if s.accessLogger != nil {
		handler = s.accessLogger.Middleware(handler)
	}

	// 9. Defense recording middleware for request analysis
	if s.defense != nil {
		handler = s.defenseRecordingMiddleware(handler)
	}

	s.httpServer = &http.Server{Handler: handler}

	logger.Info("Web server initialized", "acp_server", config.ACPServer, "api_prefix", apiPrefix)

	// Process pending queues from previous server run
	// Run in goroutine to not block server startup
	go func() {
		s.sessionManager.ProcessPendingQueues()
	}()

	return s, nil
}

// Serve starts the HTTP server on the given listener.
// Note: Scanner defense is only applied to the external listener, not this local listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.httpServer.Serve(listener)
}

// Handler returns the HTTP handler for the server.
// This is useful for testing with httptest.Server.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	// Use CompareAndSwap as a guard: return early if already shut down.
	// This also sets the flag atomically before we start any blocking work,
	// so IsShutdown() (called by in-flight handlers) does not need s.mu.
	if !s.shutdown.CompareAndSwap(false, true) {
		return nil
	}

	// Stop external listener if running (uses its own externalMu internally)
	s.StopExternalListener()

	// Close all background sessions
	if s.sessionManager != nil {
		s.sessionManager.CloseAll("server_shutdown")
	}

	// Close rate limiter
	if s.rateLimiter != nil {
		s.rateLimiter.Close()
	}

	// Close auth manager (stops rate limiter cleanup)
	if s.authManager != nil {
		s.authManager.Close()
	}

	// Close CSRF manager (stops token cleanup)
	if s.csrfManager != nil {
		s.csrfManager.Close()
	}

	// Close session store
	if s.store != nil {
		s.store.Close()
	}

	// Close queue title worker
	if s.queueTitleWorker != nil {
		s.queueTitleWorker.Close()
	}

	// Stop periodic runner
	if s.periodicRunner != nil {
		s.periodicRunner.Stop()
	}

	// Close access logger
	if s.accessLogger != nil {
		s.accessLogger.Close()
	}

	// Stop health monitor
	s.healthMonitorMu.Lock()
	if s.healthMonitor != nil {
		s.healthMonitor.Stop()
		s.healthMonitor = nil
	}
	s.healthMonitorMu.Unlock()

	// Stop MCP debug server
	if s.mcpServer != nil {
		s.mcpServer.Stop()
	}

	// Close scanner defense (persists blocklist)
	if s.defense != nil {
		s.defense.Close()
	}

	// Close prompts watcher
	if s.promptsWatcher != nil {
		s.promptsWatcher.Close()
	}

	// Shut down the HTTP server with a timeout so we don't hang indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		if s.logger != nil {
			s.logger.Warn("HTTP server shutdown timed out or errored", "error", err)
		}
		return err
	}
	return nil
}

// IsShutdown returns whether the server has been shut down.
func (s *Server) IsShutdown() bool {
	return s.shutdown.Load()
}

// Logger returns the server's logger.
func (s *Server) Logger() *slog.Logger {
	return s.logger
}

// Store returns the server's session store.
// This store is owned by the server and should not be closed by callers.
func (s *Server) Store() *session.Store {
	return s.store
}

// GetSessionManager returns the server's session manager.
// This is primarily used for testing to access session internals.
func (s *Server) GetSessionManager() *SessionManager {
	return s.sessionManager
}

// handleHealthCheck handles the health check endpoint for load balancer integration.
// M3: This endpoint returns server health status and basic metrics.
// It is intentionally NOT behind authentication to allow health checks from load balancers.
func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if server is shutting down
	if s.IsShutdown() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":  "unhealthy",
			"reason":  "server_shutting_down",
			"message": "Server is shutting down",
		})
		return
	}

	// Gather health metrics
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Add session metrics if session manager is available
	if s.sessionManager != nil {
		activeSessions := s.sessionManager.ActiveSessionCount()
		promptingSessions := s.sessionManager.PromptingSessionCount()
		response["sessions"] = map[string]interface{}{
			"active":    activeSessions,
			"prompting": promptingSessions,
		}
	}

	// Add store metrics if available
	if s.store != nil {
		storedCount, err := s.store.CountSessions()
		if err == nil {
			response["stored_sessions"] = storedCount
		}
	}

	writeJSONOK(w, response)
}

// HandleAuthInfo returns information about configured authentication methods.
// This is a public endpoint (no auth required) so the login page can adapt its UI.
func (s *Server) HandleAuthInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	info := map[string]bool{
		"simple":     false,
		"cloudflare": false,
	}

	if s.authManager != nil {
		info["simple"] = s.authManager.HasValidCredentials()
		info["cloudflare"] = s.authManager.HasCloudflareAccess()
	}

	writeJSONOK(w, info)
}

// handleRobotsTxt serves a robots.txt that disallows all crawling.
// This discourages well-behaved bots (e.g., GPTBot, OAI-SearchBot) from probing the server.
func handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
}

// loggingMiddleware logs HTTP requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		clientIP := getClientIPWithProxyCheck(r)

		// Log static assets at debug level, others at info level
		if isStaticAsset(path) {
			s.logger.Debug("HTTP request (static)",
				"method", r.Method,
				"path", path,
				"raw_uri", r.RequestURI,
				"client_ip", clientIP,
			)
		} else {
			s.logger.Debug("HTTP request",
				"method", r.Method,
				"path", path,
				"raw_uri", r.RequestURI,
				"client_ip", clientIP,
				"user_agent", r.UserAgent(),
			)
		}

		next.ServeHTTP(w, r)
	})
}

// BroadcastSessionRenamed notifies all connected clients that a session was renamed.
func (s *Server) BroadcastSessionRenamed(sessionID, newName string) {
	s.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": sessionID,
		"name":       newName,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session renamed", "session_id", sessionID, "name", newName,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionPinned notifies all connected clients that a session's pinned state changed.
func (s *Server) BroadcastSessionPinned(sessionID string, pinned bool) {
	s.eventsManager.Broadcast(WSMsgTypeSessionPinned, map[string]interface{}{
		"session_id": sessionID,
		"pinned":     pinned,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session pinned", "session_id", sessionID, "pinned", pinned,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionArchived notifies all connected clients that a session's archived state changed.
func (s *Server) BroadcastSessionArchived(sessionID string, archived bool, reason ...session.ArchiveReason) {
	data := map[string]interface{}{
		"session_id": sessionID,
		"archived":   archived,
	}
	if len(reason) > 0 && reason[0] != "" {
		data["archive_reason"] = string(reason[0])
	}
	s.eventsManager.Broadcast(WSMsgTypeSessionArchived, data)

	if s.logger != nil {
		s.logger.Debug("Broadcast session archived", "session_id", sessionID, "archived", archived,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionSettingsUpdated notifies all connected clients that a session's advanced settings changed.
func (s *Server) BroadcastSessionSettingsUpdated(sessionID string, settings map[string]bool) {
	// Ensure we send an empty object instead of null for nil maps
	if settings == nil {
		settings = map[string]bool{}
	}
	s.eventsManager.Broadcast(WSMsgTypeSessionSettingsUpdated, map[string]interface{}{
		"session_id": sessionID,
		"settings":   settings,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session settings updated", "session_id", sessionID,
			"settings_count", len(settings), "clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionDeleted notifies all connected clients that a session was deleted.
func (s *Server) BroadcastSessionDeleted(sessionID string) {
	s.eventsManager.Broadcast(WSMsgTypeSessionDeleted, map[string]string{
		"session_id": sessionID,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session deleted", "session_id", sessionID,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastPeriodicUpdated notifies all connected clients that a session's periodic state changed.
// This includes the full periodic config so clients can update their frequency panels.
//
// The broadcast includes:
// - periodic_configured: true if a periodic config exists (controls UI mode)
// - periodic_enabled: true if periodic runs are active (controls lock state)
func (s *Server) BroadcastPeriodicUpdated(sessionID string, periodic *session.PeriodicPrompt) {
	data := map[string]interface{}{
		"session_id": sessionID,
	}

	if periodic != nil {
		// periodic_configured: true means the session is in periodic mode (shows periodic UI)
		data["periodic_configured"] = true
		// periodic_enabled: true means periodic runs are active (locked state)
		data["periodic_enabled"] = periodic.Enabled
		// fresh_context: true means each scheduled run starts with a clean agent context
		data["fresh_context"] = periodic.FreshContext
		data["frequency"] = map[string]interface{}{
			"value": periodic.Frequency.Value,
			"unit":  periodic.Frequency.Unit,
		}
		if periodic.Frequency.At != "" {
			data["frequency"].(map[string]interface{})["at"] = periodic.Frequency.At
		}
		if periodic.NextScheduledAt != nil && !periodic.NextScheduledAt.IsZero() {
			data["next_scheduled_at"] = periodic.NextScheduledAt.Format(time.RFC3339)
		}
	} else {
		// No periodic config - session is not in periodic mode
		data["periodic_configured"] = false
		data["periodic_enabled"] = false
	}

	s.eventsManager.Broadcast(WSMsgTypePeriodicUpdated, data)

	if s.logger != nil {
		configured := periodic != nil
		enabled := periodic != nil && periodic.Enabled
		s.logger.Debug("Broadcast periodic updated", "session_id", sessionID,
			"periodic_configured", configured, "periodic_enabled", enabled,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionStreaming notifies all connected clients that a session's streaming state changed.
// This is called when a session starts (user sends a prompt) or stops streaming (agent completes).
func (s *Server) BroadcastSessionStreaming(sessionID string, isStreaming bool) {
	s.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
		"session_id":   sessionID,
		"is_streaming": isStreaming,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session streaming", "session_id", sessionID, "is_streaming", isStreaming,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastACPStopped notifies all connected clients that an ACP connection was stopped.
func (s *Server) BroadcastACPStopped(sessionID, reason string) {
	s.eventsManager.Broadcast(WSMsgTypeACPStopped, map[string]string{
		"session_id": sessionID,
		"reason":     reason,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast ACP stopped", "session_id", sessionID, "reason", reason,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastACPStarted notifies all connected clients that an ACP connection was started.
func (s *Server) BroadcastACPStarted(sessionID string) {
	s.eventsManager.Broadcast(WSMsgTypeACPStarted, map[string]string{
		"session_id": sessionID,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast ACP started", "session_id", sessionID,
			"clients", s.eventsManager.ClientCount())
	}
}

// acpStartFailWindow is the minimum interval between duplicate BroadcastACPStartFailed
// calls for the same session. Concurrent goroutines that coalesce on the same resume
// failure all receive the shared error and each tries to broadcast; only the first
// broadcast per session within this window is emitted.
const acpStartFailWindow = 5 * time.Second

// BroadcastACPStartFailed notifies all connected clients that an ACP connection failed to start.
// If err is an *ACPClassifiedError with a permanent classification, a more detailed
// "acp_error_permanent" message is broadcast with actionable user guidance.
// Duplicate calls for the same session within acpStartFailWindow are suppressed so that
// coalesced resume waiters do not each emit an error toast.
func (s *Server) BroadcastACPStartFailed(sessionID, sessionName string, err error, command string) {
	if s.eventsManager == nil {
		return
	}

	// Suppress duplicate broadcasts emitted by coalesced follower goroutines.
	now := time.Now()
	s.recentStartFailsMu.Lock()
	if s.recentStartFails == nil {
		s.recentStartFails = make(map[string]time.Time)
	}
	if last, ok := s.recentStartFails[sessionID]; ok && now.Sub(last) < acpStartFailWindow {
		s.recentStartFailsMu.Unlock()
		if s.logger != nil {
			s.logger.Debug("Suppressed duplicate ACP start-failed broadcast", "session_id", sessionID)
		}
		return
	}
	// Evict stale entries to keep the map bounded, then record this broadcast.
	for id, t := range s.recentStartFails {
		if now.Sub(t) >= acpStartFailWindow {
			delete(s.recentStartFails, id)
		}
	}
	s.recentStartFails[sessionID] = now
	s.recentStartFailsMu.Unlock()

	data := map[string]interface{}{
		"session_id":   sessionID,
		"session_name": sessionName,
		"error":        err.Error(),
		"command":      command,
	}

	// Check if this is a classified permanent error — broadcast with extra context.
	if classified, ok := err.(*ACPClassifiedError); ok && !classified.IsRetryable() {
		data["error_class"] = classified.Class.String()
		data["user_message"] = classified.UserMessage
		data["user_guidance"] = classified.UserGuidance

		s.eventsManager.Broadcast(WSMsgTypeACPErrorPermanent, data)

		if s.logger != nil {
			s.logger.Warn("Broadcast ACP permanent error",
				"session_id", sessionID,
				"user_message", classified.UserMessage,
				"command", command,
				"clients", s.eventsManager.ClientCount())
		}
		return
	}

	// Default: broadcast as regular start-failed.
	s.eventsManager.Broadcast(WSMsgTypeACPStartFailed, data)

	if s.logger != nil {
		s.logger.Warn("Broadcast ACP start failed",
			"session_id", sessionID,
			"error", err.Error(),
			"command", command,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastPeriodicStarted notifies all connected clients that a periodic prompt was delivered.
// This allows the frontend to show a toast notification and native OS notification.
func (s *Server) BroadcastPeriodicStarted(sessionID, sessionName string) {
	s.eventsManager.Broadcast(WSMsgTypePeriodicStarted, map[string]string{
		"session_id":   sessionID,
		"session_name": sessionName,
	})

	if s.logger != nil {
		s.logger.Info("Broadcast periodic started", "session_id", sessionID, "session_name", sessionName,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastHookFailed notifies all connected clients that a lifecycle hook failed.
// This allows the frontend to show a toast notification about the hook failure.
func (s *Server) BroadcastHookFailed(name string, exitCode int, errorMsg string, output string) {
	s.eventsManager.Broadcast(WSMsgTypeHookFailed, map[string]interface{}{
		"name":      name,
		"exit_code": exitCode,
		"error":     errorMsg,
		"output":    output,
	})

	if s.logger != nil {
		s.logger.Warn("Broadcast hook failed", "name", name, "exit_code", exitCode, "error", errorMsg,
			"output", output,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastHookRestarted notifies all connected clients that lifecycle hooks were restarted
// due to an external address health check failure.
func (s *Server) BroadcastHookRestarted(attempt int) {
	s.eventsManager.Broadcast("hook_restarted", map[string]interface{}{
		"attempt": attempt,
	})

	if s.logger != nil {
		s.logger.Info("Broadcast hook restarted",
			"attempt", attempt,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastMemoryRecycled notifies all connected clients that the GC's memory-recycle
// tier (Tier 4) stopped a memory-bloated idle agent process to reclaim memory. This
// lets the frontend show a toast. Affected conversations resume transparently on next focus.
func (s *Server) BroadcastMemoryRecycled(workspaceUUID, workspaceName, workingDir string, rssBytes, threshold uint64, sessionCount int) {
	s.eventsManager.Broadcast(WSMsgTypeMemoryRecycled, map[string]interface{}{
		"workspace_uuid":  workspaceUUID,
		"workspace_name":  workspaceName,
		"working_dir":     workingDir,
		"rss_bytes":       rssBytes,
		"threshold_bytes": threshold,
		"session_count":   sessionCount,
	})

	if s.logger != nil {
		s.logger.Info("Broadcast memory recycled",
			"workspace_uuid", workspaceUUID,
			"rss_bytes", rssBytes,
			"threshold_bytes", threshold,
			"session_count", sessionCount,
			"clients", s.eventsManager.ClientCount())
	}
}

// SetHealthMonitorDeps provides dependencies needed for dynamic health monitor management.
// This is called by the startup code to enable the server to manage the health monitor
// lifecycle when configuration changes.
func (s *Server) SetHealthMonitorDeps(hookPort int, onHookProcessChanged func(*hooks.Process), onHealthMonitorChanged func(*hooks.HealthMonitor)) {
	s.healthMonitorMu.Lock()
	defer s.healthMonitorMu.Unlock()
	s.hookPort = hookPort
	s.onHookProcessChanged = onHookProcessChanged
	s.onHealthMonitorChanged = onHealthMonitorChanged
}

// SetHealthMonitor sets the current health monitor reference.
// Called by startup code to register the initially-created health monitor.
func (s *Server) SetHealthMonitor(m *hooks.HealthMonitor) {
	s.healthMonitorMu.Lock()
	defer s.healthMonitorMu.Unlock()
	s.healthMonitor = m
}

// updateHealthMonitor starts, stops, or restarts the health monitor based on the
// current hooks configuration. Called when configuration changes at runtime.
func (s *Server) updateHealthMonitor(hooksConfig configPkg.WebHooks) {
	s.healthMonitorMu.Lock()
	defer s.healthMonitorMu.Unlock()

	// Stop existing monitor if running
	if s.healthMonitor != nil {
		if s.logger != nil {
			s.logger.Info("Stopping existing health monitor")
		}
		s.healthMonitor.Stop()
		s.healthMonitor = nil
	}

	// Start new monitor if external address is configured and up hook exists
	if hooksConfig.ExternalAddress != "" && hooksConfig.Up.Command != "" && s.hookPort > 0 {
		m := hooks.NewHealthMonitor(hooks.HealthMonitorConfig{
			Address:   hooksConfig.ExternalAddress,
			APIPrefix: s.apiPrefix,
			UpHook:    hooksConfig.Up,
			DownHook:  hooksConfig.Down,
			Port:      s.hookPort,
			OnFailure: func(failure hooks.HookFailure) {
				s.BroadcastHookFailed(failure.Name, failure.ExitCode, failure.Error, failure.Output)
			},
			OnRestart: func(attempt int) {
				s.BroadcastHookRestarted(attempt)
			},
			SetUpHook: s.onHookProcessChanged,
		})
		m.Start()
		s.healthMonitor = m

		if s.logger != nil {
			s.logger.Info("Health monitor started for external address",
				"address", hooksConfig.ExternalAddress,
			)
		}
	}

	// Notify shutdown manager about the current monitor state
	if s.onHealthMonitorChanged != nil {
		s.onHealthMonitorChanged(s.healthMonitor)
	}
}

// sessionManagerAdapter adapts SessionManager to mcpserver.SessionManager interface.
type sessionManagerAdapter struct {
	sm *SessionManager
}

// GetSession returns a running session by ID.
func (a *sessionManagerAdapter) GetSession(sessionID string) mcpserver.BackgroundSession {
	bs := a.sm.GetSession(sessionID)
	if bs == nil {
		return nil
	}
	return bs
}

// ListRunningSessions returns the IDs of all running sessions.
func (a *sessionManagerAdapter) ListRunningSessions() []string {
	return a.sm.ListRunningSessions()
}

// CloseSessionGracefully waits for any active response to complete before closing.
func (a *sessionManagerAdapter) CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool {
	return a.sm.CloseSessionGracefully(sessionID, reason, timeout)
}

// CloseSession immediately closes a session.
func (a *sessionManagerAdapter) CloseSession(sessionID, reason string) {
	a.sm.CloseSession(sessionID, reason)
}

// ResumeSession resumes an archived session by starting a new ACP connection.
func (a *sessionManagerAdapter) ResumeSession(sessionID, sessionName, workingDir string) (mcpserver.BackgroundSession, error) {
	bs, err := a.sm.ResumeSession(sessionID, sessionName, workingDir)
	if err != nil {
		return nil, err
	}
	return bs, nil
}

// GetWorkspacesForFolder returns all workspace configurations for the given folder.
func (a *sessionManagerAdapter) GetWorkspacesForFolder(folder string) []configPkg.WorkspaceSettings {
	return a.sm.GetWorkspacesForFolder(folder)
}

// BroadcastSessionCreated broadcasts a session_created event to all connected clients.
func (a *sessionManagerAdapter) BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID, childOrigin string) {
	a.sm.BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID, childOrigin)
}

// BroadcastSessionArchived broadcasts a session_archived event to all connected clients.
func (a *sessionManagerAdapter) BroadcastSessionArchived(sessionID string, archived bool, reason ...session.ArchiveReason) {
	a.sm.BroadcastSessionArchived(sessionID, archived, reason...)
}

// BroadcastSessionDeleted broadcasts a session_deleted event to all connected clients.
func (a *sessionManagerAdapter) BroadcastSessionDeleted(sessionID string) {
	a.sm.BroadcastSessionDeleted(sessionID)
}

// BroadcastWaitingForChildren broadcasts a session_waiting event to all connected clients.
func (a *sessionManagerAdapter) BroadcastWaitingForChildren(sessionID string, isWaiting bool) {
	a.sm.BroadcastWaitingForChildren(sessionID, isWaiting)
}

// DeleteChildSessions permanently deletes all child sessions when a parent is archived.
func (a *sessionManagerAdapter) DeleteChildSessions(parentID string) {
	a.sm.DeleteChildSessions(parentID)
}

// GetWorkspaces returns all configured workspaces.
func (a *sessionManagerAdapter) GetWorkspaces() []configPkg.WorkspaceSettings {
	return a.sm.GetWorkspaces()
}

// GetWorkspaceByUUID returns the workspace with the given UUID.
func (a *sessionManagerAdapter) GetWorkspaceByUUID(uuid string) *configPkg.WorkspaceSettings {
	return a.sm.GetWorkspaceByUUID(uuid)
}

// BroadcastSessionRenamed broadcasts a session_renamed event to all connected clients.
func (a *sessionManagerAdapter) BroadcastSessionRenamed(sessionID string, newName string) {
	a.sm.BroadcastSessionRenamed(sessionID, newName)
}

// GetUserDataSchema returns the user data schema for a workspace.
func (a *sessionManagerAdapter) GetUserDataSchema(workingDir string) *configPkg.UserDataSchema {
	return a.sm.GetUserDataSchema(workingDir)
}

// GetWorkspacePrompts returns prompts defined in the workspace's .mittorc file.
func (a *sessionManagerAdapter) GetWorkspacePrompts(workingDir string) []configPkg.WebPrompt {
	return a.sm.GetWorkspacePrompts(workingDir)
}

// GetWorkspacePromptsDirs returns the prompts_dirs defined in the workspace's .mittorc file.
func (a *sessionManagerAdapter) GetWorkspacePromptsDirs(workingDir string) []string {
	return a.sm.GetWorkspacePromptsDirs(workingDir)
}

// GetWorkspaceRCLastModified returns the last modification time of the workspace's .mittorc file.
func (a *sessionManagerAdapter) GetWorkspaceRCLastModified(workingDir string) time.Time {
	return a.sm.GetWorkspaceRCLastModified(workingDir)
}

// GetWorkspace returns the first workspace matching the working directory.
func (a *sessionManagerAdapter) GetWorkspace(workingDir string) *configPkg.WorkspaceSettings {
	return a.sm.GetWorkspace(workingDir)
}

// InvalidateWorkspaceRC clears the cached .mittorc for a workspace dir.
func (a *sessionManagerAdapter) InvalidateWorkspaceRC(workingDir string) {
	a.sm.InvalidateWorkspaceRC(workingDir)
}

// =============================================================================
// PromptsSubscriber implementation
// =============================================================================

// OnPromptsChanged is called by the PromptsWatcher when prompt files change.
// It broadcasts the change to all connected clients via the global events WebSocket.
func (s *Server) OnPromptsChanged(event configPkg.PromptsChangeEvent) {
	if s.eventsManager == nil {
		return
	}

	// Force reload the prompts cache so next API call gets fresh data
	if s.config.PromptsCache != nil {
		if _, err := s.config.PromptsCache.ForceReload(); err != nil && s.logger != nil {
			s.logger.Warn("Failed to reload prompts cache after file change", "error", err)
		}
	}

	// Broadcast to all connected clients
	s.eventsManager.Broadcast(WSMsgTypePromptsChanged, map[string]interface{}{
		"changed_dirs": event.ChangedDirs,
		"timestamp":    event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	})

	if s.logger != nil {
		s.logger.Debug("Broadcasted prompts_changed event",
			"changed_dirs", event.ChangedDirs,
			"client_count", s.eventsManager.ClientCount())
	}
}

// getPromptsWatchDirs returns the directories to watch for prompt file changes.
// This includes the default MITTO_DIR/prompts/ and any additional configured directories.
func (s *Server) getPromptsWatchDirs() []string {
	var dirs []string

	// Always watch the default prompts directory
	if promptsDir, err := appdir.PromptsDir(); err == nil {
		dirs = append(dirs, promptsDir)
	}

	// Add additional directories from global config
	if s.config.MittoConfig != nil && len(s.config.MittoConfig.PromptsDirs) > 0 {
		dirs = append(dirs, s.config.MittoConfig.PromptsDirs...)
	}

	return dirs
}

// resolvePromptByName resolves a prompt name to its full text for a given working directory.
// Uses the same prompt resolution pipeline as the workspace prompts API endpoint.
func (s *Server) resolvePromptByName(promptName string, workingDir string) (string, error) {
	// 1. Global file prompts
	var globalFilePrompts []configPkg.WebPrompt
	if s.config.PromptsCache != nil {
		gfp, err := s.config.PromptsCache.GetWebPrompts()
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load global file prompts for prompt resolution", "error", err)
		}
		globalFilePrompts = gfp
	}

	// 2. Settings file prompts
	var settingsPrompts []configPkg.WebPrompt
	if s.config.MittoConfig != nil {
		settingsPrompts = s.config.MittoConfig.Prompts
	}

	// 3. ACP server-specific prompts
	var acpServerName, acpServerType string
	if s.sessionManager != nil {
		if ws := s.sessionManager.GetWorkspace(workingDir); ws != nil {
			acpServerName = ws.ACPServer
		}
	}
	if acpServerName != "" && s.config.MittoConfig != nil {
		acpServerType = s.config.MittoConfig.GetServerType(acpServerName)
	}
	if acpServerType == "" {
		acpServerType = acpServerName
	}

	var serverPrompts []configPkg.WebPrompt
	if acpServerType != "" && s.config.PromptsCache != nil {
		sp, err := s.config.PromptsCache.GetWebPromptsSpecificToACP(acpServerType)
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load ACP-specific prompts for resolution", "error", err)
		}
		serverPrompts = sp
	}
	if acpServerName != "" && s.config.MittoConfig != nil {
		for _, srv := range s.config.MittoConfig.ACPServers {
			if srv.Name == acpServerName {
				serverPrompts = append(serverPrompts, srv.Prompts...)
				break
			}
		}
	}

	// 4. Workspace directory prompts
	var workspacePromptsDirs []string
	defaultDir := appdir.WorkspacePromptsDir(workingDir)
	workspacePromptsDirs = append(workspacePromptsDirs, defaultDir)
	if s.sessionManager != nil {
		workspacePromptsDirs = append(workspacePromptsDirs, s.sessionManager.GetWorkspacePromptsDirs(workingDir)...)
	}
	dirPrompts := s.loadPromptsFromDirs(workingDir, workspacePromptsDirs)

	// 5. Workspace inline prompts (.mittorc)
	var inlinePrompts []configPkg.WebPrompt
	if s.sessionManager != nil {
		inlinePrompts = s.sessionManager.GetWorkspacePrompts(workingDir)
	}

	// Merge with proper priority (same as handleWorkspacePromptsGET)
	merged := configPkg.MergePrompts(
		configPkg.MergePrompts(
			configPkg.MergePrompts(globalFilePrompts, settingsPrompts, serverPrompts),
			nil,
			dirPrompts,
		),
		nil,
		inlinePrompts,
	)

	// Find the prompt by name (case-insensitive)
	for _, p := range merged {
		if strings.EqualFold(p.Name, promptName) {
			if p.Prompt == "" {
				return "", fmt.Errorf("prompt %q has no content", promptName)
			}
			return p.Prompt, nil
		}
	}

	return "", fmt.Errorf("prompt %q not found", promptName)
}

// parseAutoArchivePeriod converts an auto-archive period string to a duration.
// Returns 0 for empty string (disabled).
// Supported values: "1d" (1 day), "1w" (1 week), "1m" (1 month), "3m" (3 months).
func parseAutoArchivePeriod(period string) (time.Duration, error) {
	switch period {
	case "":
		return 0, nil
	case "1d":
		return 24 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	case "1m":
		return 30 * 24 * time.Hour, nil
	case "3m":
		return 90 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid auto-archive period: %s", period)
	}
}

// buildMigrationContext creates a MigrationContext from the current configuration.
// This provides information needed by migrations to normalize data.
func buildMigrationContext(cfg *configPkg.Config) *session.MigrationContext {
	if cfg == nil || len(cfg.ACPServers) == 0 {
		return nil
	}

	// Extract server names and use the shared helper
	names := make([]string, len(cfg.ACPServers))
	for i, srv := range cfg.ACPServers {
		names[i] = srv.Name
	}
	return session.NewMigrationContext(names)
}
