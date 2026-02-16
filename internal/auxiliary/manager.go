// Package auxiliary provides a hidden ACP session for utility tasks.
// This session is not persisted and not shown in the UI.
package auxiliary

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/coder/acp-go-sdk"
	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// Manager manages a hidden auxiliary ACP session for utility tasks.
// It is safe for concurrent use from multiple goroutines.
type Manager struct {
	mu        sync.Mutex
	command   string
	logger    *slog.Logger
	cmd       *exec.Cmd
	conn      *acp.ClientSideConnection
	sessionID string
	client    *auxiliaryClient
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	requestMu sync.Mutex // Serializes requests to the ACP server
}

// NewManager creates a new auxiliary session manager.
// The manager is lazy - it won't start the ACP server until the first request.
func NewManager(command string, logger *slog.Logger) *Manager {
	return &Manager{
		command: command,
		logger:  logger,
	}
}

// start initializes the ACP connection if not already started.
// Must be called with mu held.
func (m *Manager) start(ctx context.Context) error {
	if m.started {
		return nil
	}

	// Parse command using shell-aware tokenization
	args, err := mittoAcp.ParseCommand(m.command)
	if err != nil {
		return err
	}

	// Create a long-lived context for the auxiliary session
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// Start ACP process
	m.cmd = exec.CommandContext(m.ctx, args[0], args[1:]...)
	m.cmd.Stderr = os.Stderr

	stdin, err := m.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe error: %w", err)
	}
	stdout, err := m.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ACP server: %w", err)
	}

	// Create client that collects responses
	m.client = newAuxiliaryClient()

	// Wrap stdout with a JSON line filter to discard non-JSON output
	// (e.g., ANSI escape sequences, terminal UI from crashed agents)
	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, m.logger)

	// Create ACP connection
	m.conn = acp.NewClientSideConnection(m.client, stdin, filteredStdout)
	if m.logger != nil {
		m.conn.SetLogger(m.logger)
	}

	// Initialize connection
	_, err = m.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		m.cleanup()
		return fmt.Errorf("initialize error: %w", err)
	}

	// Create session
	cwd, _ := os.Getwd()
	sess, err := m.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		m.cleanup()
		return fmt.Errorf("new session error: %w", err)
	}

	m.sessionID = string(sess.SessionId)
	m.started = true

	if m.logger != nil {
		m.logger.Info("Auxiliary session started", "session_id", m.sessionID)
	}

	return nil
}

// cleanup releases resources. Must be called with mu held.
func (m *Manager) cleanup() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	m.started = false
	m.conn = nil
	m.client = nil
	m.sessionID = ""
}

// Close shuts down the auxiliary session.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup()
	return nil
}

// Prompt sends a message to the auxiliary session and returns the response.
// This method is safe for concurrent use - requests are serialized.
func (m *Manager) Prompt(ctx context.Context, message string) (string, error) {
	// Ensure the session is started
	m.mu.Lock()
	if err := m.start(ctx); err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("failed to start auxiliary session: %w", err)
	}
	conn := m.conn
	sessionID := m.sessionID
	client := m.client
	m.mu.Unlock()

	// Serialize requests to the ACP server
	m.requestMu.Lock()
	defer m.requestMu.Unlock()

	// Reset the response buffer
	client.reset()

	// Send prompt
	_, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: acp.SessionId(sessionID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
	})
	if err != nil {
		return "", fmt.Errorf("prompt error: %w", err)
	}

	// Return collected response
	return client.getResponse(), nil
}
