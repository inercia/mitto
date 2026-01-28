package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/web"
)

var (
	webPort         int
	webPortExternal int
	webHost         string
	webStaticDir    string
)

// hookProcess manages a running hook command and its lifecycle.
type hookProcess struct {
	name string
	cmd  *exec.Cmd
	mu   sync.Mutex
	done bool
}

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
	var webWorkspaces []web.WorkspaceConfig
	if fromCLI {
		// Convert CLI workspaces to web.WorkspaceConfig
		webWorkspaces = make([]web.WorkspaceConfig, len(cliWorkspaces))
		for i, ws := range cliWorkspaces {
			webWorkspaces[i] = web.WorkspaceConfig{
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
			webWorkspaces = make([]web.WorkspaceConfig, len(savedWorkspaces))
			for i, ws := range savedWorkspaces {
				webWorkspaces[i] = web.WorkspaceConfig{
					ACPServer:  ws.ACPServer,
					ACPCommand: ws.ACPCommand,
					WorkingDir: ws.WorkingDir,
				}
			}
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
		if !cmd.Flags().Changed("port-external") && cfg.Web.ExternalPort != 0 {
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
		onWorkspaceSave = func(workspaces []web.WorkspaceConfig) error {
			settings := make([]config.WorkspaceSettings, len(workspaces))
			for i, ws := range workspaces {
				settings[i] = config.WorkspaceSettings{
					ACPServer:  ws.ACPServer,
					ACPCommand: ws.ACPCommand,
					WorkingDir: ws.WorkingDir,
					Color:      ws.Color,
				}
			}
			return config.SaveWorkspaces(settings)
		}
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
		ConfigReadOnly:  configPath != "", // Read-only when using custom config file
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
	if externalPort != 0 {
		fmt.Printf("   External port (when enabled): %d\n", externalPort)
	} else {
		fmt.Printf("   External port (when enabled): random\n")
	}

	// Run the up hook if configured
	var hook *hookProcess
	if cfg != nil && cfg.Web.Hooks.Up.Command != "" {
		hook = startUpHook(cfg.Web.Hooks.Up.Command, cfg.Web.Hooks.Up.Name, actualPort)
	}

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n👋 Shutting down...")
		// Stop the up hook process first (if running)
		if hook != nil {
			hook.Stop()
		}
		// Run the down hook synchronously before shutdown
		if cfg != nil && cfg.Web.Hooks.Down.Command != "" {
			runDownHook(cfg.Web.Hooks.Down.Command, cfg.Web.Hooks.Down.Name, actualPort)
		}
		auxiliary.Shutdown()
		srv.Shutdown()
	}()

	fmt.Printf("\n   Press Ctrl+C to stop\n\n")

	// Serve (blocks until shutdown)
	if err := srv.Serve(listener); err != nil && !srv.IsShutdown() {
		return fmt.Errorf("server error: %w", err)
	}

	// Ensure hook is stopped on normal exit as well
	if hook != nil {
		hook.Stop()
	}

	// Run down hook on normal exit as well (if not already run via signal)
	// Note: This won't run if we exited via signal since srv.Shutdown() was already called
	// The signal handler already runs the down hook, so this is for other exit paths

	return nil
}

// startUpHook starts the web.hooks.up command asynchronously and returns
// a hookProcess that can be used to stop it during shutdown.
// It replaces ${PORT} in the command with the actual port number.
func startUpHook(command, name string, port int) *hookProcess {
	logger := logging.Hook()

	// Replace ${PORT} placeholder with actual port
	command = strings.ReplaceAll(command, "${PORT}", strconv.Itoa(port))

	// Log the hook execution
	hookName := name
	if hookName == "" {
		hookName = "up"
	}
	fmt.Printf("🔗 Running hook: %s\n", hookName)
	logger.Info("Starting up hook",
		"name", hookName,
		"command", command,
		"port", port,
	)

	// Create the command with a new process group so we can kill all children
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Set process group ID so we can kill the entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	hp := &hookProcess{
		name: name,
		cmd:  cmd,
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		fmt.Printf("⚠️  Hook start error: %v\n", err)
		logger.Error("Failed to start up hook",
			"name", hookName,
			"error", err,
		)
		return nil
	}

	logger.Debug("Up hook started",
		"name", hookName,
		"pid", cmd.Process.Pid,
	)

	// Wait for the command in a goroutine to handle short-lived commands
	go func() {
		err := cmd.Wait()
		hp.mu.Lock()
		hp.done = true
		hp.mu.Unlock()

		if err != nil {
			// Only log if it wasn't killed by us (exit status 0 or signal)
			if exitErr, ok := err.(*exec.ExitError); ok {
				// Check if it was killed by a signal (normal during shutdown)
				if exitErr.ExitCode() == -1 {
					// Killed by signal, don't log as error
					return
				}
			}
			fmt.Printf("⚠️  Hook error: %v\n", err)
			logger.Error("Up hook exited with error",
				"name", hookName,
				"error", err,
			)
		} else {
			logger.Debug("Up hook completed",
				"name", hookName,
			)
		}
	}()

	return hp
}

// Stop terminates the hook process if it's still running.
// It sends SIGTERM to the process group to ensure all child processes are also terminated.
func (hp *hookProcess) Stop() {
	if hp == nil {
		return
	}

	logger := logging.Hook()

	hp.mu.Lock()
	defer hp.mu.Unlock()

	if hp.done {
		return
	}

	if hp.cmd.Process == nil {
		return
	}

	hookName := hp.name
	if hookName == "" {
		hookName = "up"
	}

	// Kill the entire process group (negative PID)
	pgid, err := syscall.Getpgid(hp.cmd.Process.Pid)
	if err == nil {
		// Send SIGTERM to the process group
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			// If SIGTERM fails, try SIGKILL
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	} else {
		// Fallback: kill just the process
		_ = hp.cmd.Process.Kill()
	}

	fmt.Printf("🔗 Stopped hook: %s\n", hookName)
	logger.Info("Stopped up hook",
		"name", hookName,
	)

	hp.done = true
}

// runDownHook runs the web.hooks.down command synchronously.
// It waits for the command to complete before returning.
// It replaces ${PORT} in the command with the actual port number.
func runDownHook(command, name string, port int) {
	logger := logging.Hook()

	// Replace ${PORT} placeholder with actual port
	command = strings.ReplaceAll(command, "${PORT}", strconv.Itoa(port))

	// Log the hook execution
	hookName := name
	if hookName == "" {
		hookName = "down"
	}
	fmt.Printf("🔗 Running down hook: %s\n", hookName)
	logger.Info("Starting down hook",
		"name", hookName,
		"command", command,
		"port", port,
	)

	// Create and run the command synchronously
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("⚠️  Down hook error: %v\n", err)
		logger.Error("Down hook failed",
			"name", hookName,
			"error", err,
		)
	} else {
		fmt.Printf("🔗 Down hook completed: %s\n", hookName)
		logger.Info("Down hook completed",
			"name", hookName,
		)
	}
}
