package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/web"
)

var (
	webPort      int
	webHost      string
	webStaticDir string
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
  mitto web --host 0.0.0.0               # Listen on all interfaces
  mitto web --static-dir ./web/static    # Serve from filesystem (for development)`,
	RunE: runWeb,
}

func init() {
	rootCmd.AddCommand(webCmd)

	webCmd.Flags().IntVar(&webPort, "port", 8080, "HTTP server port")
	webCmd.Flags().StringVar(&webHost, "host", "127.0.0.1", "HTTP server host")
	webCmd.Flags().StringVar(&webStaticDir, "static-dir", "", "Serve static files from this directory instead of embedded assets (for development)")
}

func runWeb(cmd *cobra.Command, args []string) error {
	server, err := getSelectedServer()
	if err != nil {
		return err
	}

	// Use config values as defaults if flags weren't explicitly set
	host := webHost
	port := webPort
	staticDir := webStaticDir
	if cfg != nil {
		if !cmd.Flags().Changed("host") && cfg.Web.Host != "" {
			host = cfg.Web.Host
		}
		if !cmd.Flags().Changed("port") && cfg.Web.Port != 0 {
			port = cfg.Web.Port
		}
		if !cmd.Flags().Changed("static-dir") && cfg.Web.StaticDir != "" {
			staticDir = cfg.Web.StaticDir
		}
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	fmt.Printf("🌐 Starting web interface...\n")
	fmt.Printf("   ACP Server: %s\n", server.Name)
	fmt.Printf("   URL: http://%s\n", addr)
	if staticDir != "" {
		fmt.Printf("   Static files: %s (hot-reload enabled)\n", staticDir)
	}
	fmt.Printf("\n   Press Ctrl+C to stop\n\n")

	// Initialize auxiliary session manager for utility tasks (auto-title, etc.)
	var auxLogger *slog.Logger
	if debug {
		auxLogger = slog.Default()
	}
	auxiliary.Initialize(server.Command, auxLogger)
	defer auxiliary.Shutdown()

	// Create web server
	srv, err := web.NewServer(web.Config{
		ACPCommand:  server.Command,
		ACPServer:   server.Name,
		AutoApprove: autoApprove,
		Debug:       debug,
		MittoConfig: cfg,
		StaticDir:   staticDir,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n👋 Shutting down...")
		auxiliary.Shutdown()
		srv.Shutdown()
	}()

	// Start server
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// Serve (blocks until shutdown)
	if err := srv.Serve(listener); err != nil && !srv.IsShutdown() {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
