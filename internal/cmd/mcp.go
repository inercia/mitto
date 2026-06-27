// Package cmd provides the CLI commands for Mitto.
package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

var (
	mcpProxyTo string
)

// mcpCmd represents the mcp command for running MCP servers
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run an MCP server or proxy for Mitto tools",
	Long: `Run an MCP server that exposes Mitto tools to AI agents.

When used with --proxy-to, acts as a STDIO-to-HTTP proxy for agents that
don't support HTTP MCP transport directly. This allows the agent to
communicate via STDIO while the actual MCP server runs over HTTP.

Examples:
  # Run global MCP server in STDIO mode (for debugging tools)
  mitto mcp

  # Run as STDIO-to-HTTP proxy (used by agents that don't support HTTP)
  mitto mcp --proxy-to http://127.0.0.1:12345/mcp`,
	RunE: runMCPServer,
}

func init() {
	rootCmd.AddCommand(mcpCmd)

	mcpCmd.Flags().StringVar(&mcpProxyTo, "proxy-to", "", "URL to proxy MCP requests to (STDIO-to-HTTP proxy mode)")
}

func runMCPServer(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// If --proxy-to is specified, run as STDIO-to-HTTP proxy
	if mcpProxyTo != "" {
		return runMCPProxy(ctx, mcpProxyTo)
	}

	// Otherwise, run as a standalone STDIO MCP server
	return runStandaloneMCPServer(ctx)
}

// runStandaloneMCPServer runs the global MCP server in STDIO mode.
func runStandaloneMCPServer(ctx context.Context) error {
	// Get sessions directory
	sessionsDir, err := appdir.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions directory: %w", err)
	}

	// Create session store
	store, err := session.NewStore(sessionsDir)
	if err != nil {
		return fmt.Errorf("failed to create session store: %w", err)
	}
	defer store.Close()

	// Run data migrations
	migrationCtx := buildMigrationContextFromConfig(cfg)
	if err := store.RunMigrations(migrationCtx); err != nil {
		// Log warning but continue - migrations are best-effort
		slog.Warn("Failed to run migrations", "error", err)
	}

	// Initialize prompts cache for global prompts
	promptsCache := config.NewPromptsCache()
	if len(cfg.PromptsDirs) > 0 {
		promptsCache.SetAdditionalDirs(cfg.PromptsDirs)
	}

	srv, err := mcpserver.NewServer(
		mcpserver.Config{Mode: mcpserver.TransportModeSTDIO},
		mcpserver.Dependencies{Store: store, Config: cfg, PromptsCache: promptsCache},
	)
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	// Wait for the server to finish (blocks until context cancelled or stdin closes)
	return srv.Wait()
}

// runMCPProxy runs as a STDIO-to-HTTP proxy using os.Stdin and os.Stdout.
// It is a thin wrapper around runMCPProxyIO for production use.
func runMCPProxy(ctx context.Context, targetURL string) error {
	return runMCPProxyIO(ctx, targetURL, os.Stdin, os.Stdout)
}

// runMCPProxyIO runs as a STDIO-to-HTTP proxy with explicit IO streams.
// It reads JSON-RPC messages from in, forwards them to the HTTP MCP server,
// and writes responses to out.
//
// Concurrency model:
//   - stdin is read SERIALLY (single reader goroutine — the main loop).
//   - Each JSON-RPC request is dispatched to its own goroutine so long-running
//     tool calls do not starve keepalive pings or other requests.
//   - All writes to out are serialized via outMu.
//   - mcpSessionID is guarded by sessionMu (read before dispatch, write after response).
//   - A WaitGroup ensures all in-flight goroutines finish before the function returns,
//     so no responses are lost on EOF or context cancellation.
func runMCPProxyIO(ctx context.Context, targetURL string, in io.Reader, out io.Writer) error {
	client := &http.Client{}
	reader := bufio.NewReader(in)

	// outMu serializes all writes to out.
	var outMu sync.Mutex

	// sessionMu guards mcpSessionID (read + write from multiple goroutines).
	var sessionMu sync.Mutex
	var mcpSessionID string

	// wg tracks in-flight request goroutines.
	var wg sync.WaitGroup

	// writeLine writes data to out under the mutex, appending a newline if needed.
	writeLine := func(data []byte) {
		outMu.Lock()
		defer outMu.Unlock()
		out.Write(data)
		if data[len(data)-1] != '\n' {
			out.Write([]byte("\n"))
		}
	}

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		default:
		}

		// Read a line (JSON-RPC message) — MCP uses newline-delimited JSON.
		// This is the only goroutine that reads; no mutex needed here.
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			wg.Wait()
			return nil
		}
		if err != nil {
			wg.Wait()
			return fmt.Errorf("read error: %w", err)
		}

		// Skip empty lines.
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}

		// Extract request ID for error responses (notifications have no id).
		var reqID interface{}
		var reqMsg struct {
			ID interface{} `json:"id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &reqMsg); err == nil {
			reqID = reqMsg.ID
		}

		// Snapshot session ID before launching the goroutine.
		sessionMu.Lock()
		currentSessionID := mcpSessionID
		sessionMu.Unlock()

		// Dispatch this request concurrently so the read loop is never blocked.
		wg.Add(1)
		go func(body string, id interface{}, sessID string) {
			defer wg.Done()

			resp, newSessionID, err := forwardToHTTP(ctx, client, targetURL, body, sessID)
			if err != nil {
				outMu.Lock()
				writeJSONRPCError(out, id, -32603, fmt.Sprintf("proxy error: %v", err))
				outMu.Unlock()
				return
			}

			// Update shared session ID if the response carried a new one.
			if newSessionID != "" {
				sessionMu.Lock()
				mcpSessionID = newSessionID
				sessionMu.Unlock()
			}

			// Write response (notifications produce an empty resp — write nothing).
			if len(resp) > 0 {
				writeLine(resp)
			}
		}(trimmed, reqID, currentSessionID)
	}
}

// forwardToHTTP forwards a JSON-RPC request to the HTTP MCP server.
// The Streamable HTTP transport returns responses in SSE format, so we need
// to parse the SSE events and extract the JSON-RPC messages.
//
// The sessionID parameter is the MCP session ID to include in the request header.
// Returns the response body, any new session ID from the response, and an error.
func forwardToHTTP(ctx context.Context, client *http.Client, targetURL, jsonBody, sessionID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBufferString(jsonBody))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	// Include session ID if we have one (required for subsequent requests)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Extract session ID from response (returned on initialize)
	newSessionID := resp.Header.Get("Mcp-Session-Id")

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, newSessionID, fmt.Errorf("http error %d: %s", resp.StatusCode, string(body))
	}

	// HTTP 202 Accepted means the notification was received but there's no response
	// (used for notifications like notifications/initialized)
	if resp.StatusCode == http.StatusAccepted {
		return nil, newSessionID, nil
	}

	// Check if response is SSE format (Streamable HTTP transport)
	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		body, err := parseSSEResponse(resp.Body)
		return body, newSessionID, err
	}

	// Plain JSON response
	body, err := io.ReadAll(resp.Body)
	return body, newSessionID, err
}

// parseSSEResponse extracts JSON-RPC messages from an SSE stream.
// The Streamable HTTP transport sends responses as SSE events with "message" type.
func parseSSEResponse(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	// Set a larger buffer to handle large MCP tool responses (e.g., mitto_get_config)
	// Default is 64KB which may be too small for some responses
	const maxScannerBuffer = 1024 * 1024 // 1MB
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)

	var result bytes.Buffer
	var dataBuffer bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			// Append data to buffer (SSE data can span multiple lines)
			if dataBuffer.Len() > 0 {
				dataBuffer.WriteString("\n")
			}
			dataBuffer.WriteString(line[6:]) // Skip "data: " prefix
		} else if line == "" && dataBuffer.Len() > 0 {
			// Empty line marks end of an SSE event
			// Write the accumulated data as a JSON-RPC message
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.Write(dataBuffer.Bytes())
			dataBuffer.Reset()
		}
		// Ignore "event:" lines and other SSE fields
	}

	// Handle any remaining data (in case stream didn't end with empty line)
	if dataBuffer.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.Write(dataBuffer.Bytes())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan SSE: %w", err)
	}

	return result.Bytes(), nil
}

// writeJSONRPCError writes a JSON-RPC error response to the writer.
func writeJSONRPCError(w io.Writer, id interface{}, code int, message string) {
	errResp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(errResp)
	w.Write(data)
	w.Write([]byte("\n"))
}

// buildMigrationContextFromConfig creates a MigrationContext from the Mitto configuration.
func buildMigrationContextFromConfig(cfg *config.Config) *session.MigrationContext {
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
