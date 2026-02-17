package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/hooks"
	"github.com/inercia/mitto/internal/web"
)

var (
	webPort         int
	webPortExternal int
	webStaticDir    string
	webAccessLog    string
)

// webCmd represents the web command
var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start a web-based interface for ACP communication",
	Long: `Start a web server that provides a browser-based UI for
interacting with ACP servers.

The interface provides a chat-like experience similar to messaging apps,
with support for viewing past sessions, real-time streaming, and
mobile-friendly responsive design.

Example:
  mitto web                              # Start on default port 8080
  mitto web --port 3000                  # Start on custom port
  mitto web --port 0                     # Use random port (auto-selected)
  mitto web --port-external 8443         # Set external access port
  mitto web --static-dir ./web/static    # Serve from filesystem (for development)`,
	RunE: runWeb,
}

func init() {
	rootCmd.AddCommand(webCmd)

	webCmd.Flags().IntVar(&webPort, "port", 8080, "HTTP server port for local access (127.0.0.1). Use 0 for random port")
	webCmd.Flags().IntVar(&webPortExternal, "port-external", 0, "HTTP server port for external access when enabled (0.0.0.0). Use 0 for random port")
	webCmd.Flags().StringVar(&webStaticDir, "static-dir", "", "Serve static files from this directory instead of embedded assets (for development)")
	webCmd.Flags().StringVar(&webAccessLog, "access-log", "", "Path to security access log file (logs auth events, unauthorized access, etc.)")
}

func runWeb(cmd *cobra.Command, args []string) error {
	// Parse workspaces from --dir flags
	cliWorkspaces, err := parseWorkspaces()
	if err != nil {
		return err
	}

	// Determine if workspaces came from CLI flags
	fromCLI := len(dirFlags) > 0

	// If no CLI workspaces, try to load from workspaces.json
	var webWorkspaces []config.WorkspaceSettings
	if fromCLI {
		// Convert CLI workspaces to config.WorkspaceSettings
		webWorkspaces = make([]config.WorkspaceSettings, len(cliWorkspaces))
		for i, ws := range cliWorkspaces {
			webWorkspaces[i] = config.WorkspaceSettings{
				ACPServer:  ws.ServerName,
				ACPCommand: ws.Server.Command,
				WorkingDir: ws.Dir,
			}
		}
	} else {
		// Try to load from workspaces.json
		savedWorkspaces, err := config.LoadWorkspaces()
		if err != nil {
			return fmt.Errorf("failed to load workspaces: %w", err)
		}
		if savedWorkspaces != nil {
			// Use saved workspaces directly (same type)
			webWorkspaces = savedWorkspaces
		}
		// If no saved workspaces, webWorkspaces will be empty
		// The frontend will show the Settings dialog
	}

	// Determine local port: CLI flag > config > default (8080)
	// Port 0 means random port (let OS choose)
	localPort := webPort
	staticDir := webStaticDir
	externalPort := webPortExternal
	if cfg != nil {
		if !cmd.Flags().Changed("port") && cfg.Web.Port != 0 {
			localPort = cfg.Web.Port
		}
		// For external port: -1 = disabled, 0 = random, >0 = specific port
		// Always use config value if CLI flag wasn't explicitly set
		if !cmd.Flags().Changed("port-external") {
			externalPort = cfg.Web.ExternalPort
		}
		if !cmd.Flags().Changed("static-dir") && cfg.Web.StaticDir != "" {
			staticDir = cfg.Web.StaticDir
		}
	}

	// Local listener always binds to 127.0.0.1 for security
	// External access uses a separate listener on 0.0.0.0 when enabled
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)

	fmt.Printf("🌐 Starting web interface...\n")
	switch len(webWorkspaces) {
	case 0:
		fmt.Printf("   No workspaces configured (will prompt in UI)\n")
	case 1:
		fmt.Printf("   ACP Server: %s\n", webWorkspaces[0].ACPServer)
		fmt.Printf("   Directory: %s\n", webWorkspaces[0].WorkingDir)
	default:
		fmt.Printf("   Workspaces:\n")
		for _, ws := range webWorkspaces {
			fmt.Printf("     - %s: %s\n", ws.ACPServer, ws.WorkingDir)
		}
	}
	if fromCLI {
		fmt.Printf("   Source: CLI flags (changes not persisted)\n")
	} else {
		fmt.Printf("   Source: workspaces.json (changes will be saved)\n")
	}
	if staticDir != "" {
		fmt.Printf("   Static files: %s (hot-reload enabled)\n", staticDir)
	}

	// Initialize auxiliary session manager for utility tasks (auto-title, etc.)
	// Use the first workspace's command for auxiliary sessions, or first ACP server from config
	var auxLogger *slog.Logger
	if debug {
		auxLogger = slog.Default()
	}
	var auxCommand string
	if len(webWorkspaces) > 0 {
		auxCommand = webWorkspaces[0].ACPCommand
	} else if cfg != nil && len(cfg.ACPServers) > 0 {
		auxCommand = cfg.ACPServers[0].Command
	}
	if auxCommand != "" {
		auxiliary.Initialize(auxCommand, auxLogger)
		defer auxiliary.Shutdown()
	}

	// Create workspace save callback (only used when not from CLI)
	var onWorkspaceSave web.WorkspaceSaveFunc
	if !fromCLI {
		onWorkspaceSave = config.SaveWorkspaces
	}

	// Determine if config is fully read-only (only when using --config flag)
	// Note: RC file config is NOT fully read-only anymore with config layering.
	// Users can add new servers via UI (saved to settings.json), but RC file servers are read-only.
	configReadOnly := configPath != ""

	// Get RC file path and whether any servers came from it
	var rcFilePath string
	var hasRCFileServers bool
	if configResult != nil {
		rcFilePath = configResult.RCFilePath
		hasRCFileServers = configResult.HasRCFileServers
	}

	// Initialize prompts cache for global prompts from MITTO_DIR/prompts/
	// and any additional directories from config
	promptsCache := config.NewPromptsCache()
	if len(cfg.PromptsDirs) > 0 {
		promptsCache.SetAdditionalDirs(cfg.PromptsDirs)
	}

	// Configure access log
	// Priority: --access-log flag > settings.json > disabled by default for CLI
	accessLogConfig := resolveAccessLogConfig(cfg, webAccessLog)

	// Create web server with workspaces
	srv, err := web.NewServer(web.Config{
		Workspaces:       webWorkspaces,
		AutoApprove:      autoApprove,
		Debug:            debug,
		MittoConfig:      cfg,
		StaticDir:        staticDir,
		FromCLI:          fromCLI,
		OnWorkspaceSave:  onWorkspaceSave,
		ConfigReadOnly:   configReadOnly,
		RCFilePath:       rcFilePath,
		HasRCFileServers: hasRCFileServers,
		PromptsCache:     promptsCache,
		AccessLog:        accessLogConfig,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Set external port configuration (used when external access is enabled)
	srv.SetExternalPort(externalPort)

	// Start local listener (always on 127.0.0.1 for security)
	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", localAddr, err)
	}

	// Get actual port (may differ if we requested port 0 for random)
	actualPort := listener.Addr().(*net.TCPAddr).Port
	slog.Info("Local listener started", "address", fmt.Sprintf("127.0.0.1:%d", actualPort), "port", actualPort)
	fmt.Printf("   Local URL: http://127.0.0.1:%d\n", actualPort)

	// Start external listener if auth is configured (for external access)
	// Track the actual external port for the up hook
	var actualExternalPort int
	if cfg != nil && cfg.Web.Auth != nil {
		var err error
		actualExternalPort, err = srv.StartExternalListener(externalPort)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to start external listener: %v\n", err)
		} else {
			// Note: StartExternalListener already logs success
			fmt.Printf("   External URL: http://0.0.0.0:%d\n", actualExternalPort)
		}
	} else {
		// Auth not configured, show what port would be used when enabled
		// External port: -1 = disabled, 0 = random, >0 = specific port
		switch {
		case externalPort < 0:
			fmt.Printf("   External port (when enabled): disabled\n")
		case externalPort == 0:
			fmt.Printf("   External port (when enabled): random\n")
		default:
			fmt.Printf("   External port (when enabled): %d\n", externalPort)
		}
	}

	// Run the up hook if configured
	// Use external port if available (for tunneling services like Tailscale/ngrok),
	// otherwise fall back to local port
	var upHook *hooks.Process
	if cfg != nil {
		hookPort := actualPort
		if actualExternalPort > 0 {
			hookPort = actualExternalPort
		}
		upHook = hooks.StartUp(cfg.Web.Hooks.Up, hookPort)
	}
	if upHook == nil && debug {
		// Log why hook wasn't started
		if cfg == nil {
			fmt.Printf("   [debug] No up hook: config is nil\n")
		} else if cfg.Web.Hooks.Up.Command == "" {
			fmt.Printf("   [debug] No up hook: command is empty (configure web.hooks.up.command)\n")
		}
	}

	// Set up shutdown manager for graceful shutdown
	shutdown := hooks.NewShutdownManager()

	// Configure hooks
	var downHook config.WebHook
	if cfg != nil {
		downHook = cfg.Web.Hooks.Down
	}
	shutdown.SetHooks(upHook, downHook, actualPort)

	// Add cleanup functions
	shutdown.AddCleanup(func(reason string) {
		fmt.Println("\n👋 Shutting down...")
	})
	shutdown.AddCleanup(func(reason string) {
		auxiliary.Shutdown()
	})
	shutdown.AddCleanup(func(reason string) {
		srv.Shutdown()
	})

	// Start listening for signals
	shutdown.Start()

	fmt.Printf("\n   Press Ctrl+C to stop\n\n")

	// Serve (blocks until shutdown)
	if err := srv.Serve(listener); err != nil && !srv.IsShutdown() {
		return fmt.Errorf("server error: %w", err)
	}

	// Ensure cleanup runs on normal exit as well
	shutdown.Shutdown("server_stopped")

	return nil
}

// resolveAccessLogConfig determines the access log configuration based on:
// 1. CLI flag (--access-log) - highest priority
// 2. Settings from config file
// 3. Disabled by default for CLI usage
func resolveAccessLogConfig(cfg *config.Config, cliPath string) web.AccessLogConfig {
	accessLogConfig := web.DefaultAccessLogConfig()

	// CLI flag has highest priority
	if cliPath != "" {
		accessLogConfig.Path = cliPath
		fmt.Printf("   Access log: %s\n", cliPath)
		return accessLogConfig
	}

	// Check settings from config file
	if cfg != nil && cfg.Web.AccessLog != nil {
		alCfg := cfg.Web.AccessLog

		// Check if explicitly disabled
		if alCfg.Enabled != nil && !*alCfg.Enabled {
			return web.AccessLogConfig{} // Disabled
		}

		// Use custom path if specified
		if alCfg.Path != "" {
			accessLogConfig.Path = alCfg.Path
		} else if alCfg.Enabled != nil && *alCfg.Enabled {
			// Enabled but no custom path - use default logs directory
			logsDir, err := appdir.LogsDir()
			if err != nil {
				slog.Warn("Failed to get logs directory for access log", "error", err)
			} else {
				if err := appdir.EnsureLogsDir(); err != nil {
					slog.Warn("Failed to create logs directory", "error", err)
				} else {
					accessLogConfig.Path = filepath.Join(logsDir, "access.log")
				}
			}
		}

		// Apply custom settings
		if alCfg.MaxSizeMB > 0 {
			accessLogConfig.MaxSizeMB = alCfg.MaxSizeMB
		}
		if alCfg.MaxBackups > 0 {
			accessLogConfig.MaxBackups = alCfg.MaxBackups
		}

		if accessLogConfig.Path != "" {
			fmt.Printf("   Access log: %s\n", accessLogConfig.Path)
		}
	}

	// Default: disabled for CLI usage (empty path)
	return accessLogConfig
}
