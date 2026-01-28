package cmd

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/hooks"
	"github.com/inercia/mitto/internal/web"
)

var (
	webPort         int
	webPortExternal int
	webHost         string
	webStaticDir    string
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
	webCmd.Flags().StringVar(&webHost, "host", "127.0.0.1", "HTTP server host (deprecated: local always binds to 127.0.0.1)")
	webCmd.Flags().StringVar(&webStaticDir, "static-dir", "", "Serve static files from this directory instead of embedded assets (for development)")
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
	if len(webWorkspaces) == 0 {
		fmt.Printf("   No workspaces configured (will prompt in UI)\n")
	} else if len(webWorkspaces) == 1 {
		fmt.Printf("   ACP Server: %s\n", webWorkspaces[0].ACPServer)
		fmt.Printf("   Directory: %s\n", webWorkspaces[0].WorkingDir)
	} else {
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
		onWorkspaceSave = func(workspaces []config.WorkspaceSettings) error {
			return config.SaveWorkspaces(workspaces)
		}
	}

	// Determine if config is read-only and get RC file path if applicable
	configReadOnly := configPath != "" || (configResult != nil && configResult.Source == config.ConfigSourceRCFile)
	var rcFilePath string
	if configResult != nil && configResult.Source == config.ConfigSourceRCFile {
		rcFilePath = configResult.SourcePath
	}

	// Create web server with workspaces
	srv, err := web.NewServer(web.Config{
		Workspaces:      webWorkspaces,
		AutoApprove:     autoApprove,
		Debug:           debug,
		MittoConfig:     cfg,
		StaticDir:       staticDir,
		FromCLI:         fromCLI,
		OnWorkspaceSave: onWorkspaceSave,
		ConfigReadOnly:  configReadOnly,
		RCFilePath:      rcFilePath,
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
	fmt.Printf("   Local URL: http://127.0.0.1:%d\n", actualPort)

	// Start external listener if auth is configured (for external access)
	if cfg != nil && cfg.Web.Auth != nil {
		actualExternalPort, err := srv.StartExternalListener(externalPort)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to start external listener: %v\n", err)
		} else {
			fmt.Printf("   External URL: http://0.0.0.0:%d\n", actualExternalPort)
		}
	} else {
		// Auth not configured, show what port would be used when enabled
		// External port: -1 = disabled, 0 = random, >0 = specific port
		if externalPort < 0 {
			fmt.Printf("   External port (when enabled): disabled\n")
		} else if externalPort == 0 {
			fmt.Printf("   External port (when enabled): random\n")
		} else {
			fmt.Printf("   External port (when enabled): %d\n", externalPort)
		}
	}

	// Run the up hook if configured
	var upHook *hooks.Process
	if cfg != nil {
		upHook = hooks.StartUp(cfg.Web.Hooks.Up, actualPort)
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
