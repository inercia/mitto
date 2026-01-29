package acp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// Connection manages an ACP server process and its communication channel.
type Connection struct {
	cmd          *exec.Cmd
	conn         *acp.ClientSideConnection
	session      *acp.NewSessionResponse
	client       *Client
	logger       *slog.Logger
	capabilities *acp.AgentCapabilities
}

// NewConnection starts an ACP server process and establishes a connection.
func NewConnection(ctx context.Context, command string, autoApprove bool, output func(string), logger *slog.Logger) (*Connection, error) {
	// Parse command into args
	args := strings.Fields(command)
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe error: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ACP server: %w", err)
	}

	client := NewClient(autoApprove, output)
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	if logger != nil {
		conn.SetLogger(logger)
	}

	c := &Connection{
		cmd:    cmd,
		conn:   conn,
		client: client,
		logger: logger,
	}

	return c, nil
}

// Initialize establishes the ACP protocol connection.
func (c *Connection) Initialize(ctx context.Context) error {
	initResp, err := c.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize error: %w", err)
	}

	// Store agent capabilities for later use
	c.capabilities = &initResp.AgentCapabilities

	c.client.print("✅ Connected (protocol v%v)\n", initResp.ProtocolVersion)
	return nil
}

// HasImageSupport returns true if the agent supports image content in prompts.
func (c *Connection) HasImageSupport() bool {
	if c.capabilities == nil {
		return false
	}
	return c.capabilities.PromptCapabilities.Image
}

// HasLoadSessionSupport returns true if the agent supports loading/resuming sessions.
func (c *Connection) HasLoadSessionSupport() bool {
	if c.capabilities == nil {
		return false
	}
	return c.capabilities.LoadSession
}

// NewSession creates a new ACP session.
func (c *Connection) NewSession(ctx context.Context, cwd string) error {
	sess, err := c.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return fmt.Errorf("new session error: %w", err)
	}

	c.session = &sess
	c.client.print("📝 Created session: %s\n", sess.SessionId)
	return nil
}

// LoadSession loads an existing ACP session by ID.
// This allows resuming a previous conversation if the agent supports it.
// Returns an error if the agent doesn't support session loading or if the session doesn't exist.
func (c *Connection) LoadSession(ctx context.Context, sessionID, cwd string) error {
	if !c.HasLoadSessionSupport() {
		return fmt.Errorf("agent does not support session loading")
	}

	resp, err := c.conn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  acp.SessionId(sessionID),
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return fmt.Errorf("load session error: %w", err)
	}

	// Create a NewSessionResponse-like structure to store the session
	c.session = &acp.NewSessionResponse{
		SessionId: acp.SessionId(sessionID),
		Models:    resp.Models,
		Modes:     resp.Modes,
	}
	c.client.print("📝 Resumed session: %s\n", sessionID)
	return nil
}

// NewOrLoadSession creates a new session or loads an existing one.
// If acpSessionID is provided and the agent supports session loading, it attempts to load that session.
// Otherwise, it creates a new session.
// Returns the ACP session ID and whether a new session was created.
func (c *Connection) NewOrLoadSession(ctx context.Context, acpSessionID, cwd string) (string, bool, error) {
	// Try to load existing session if we have an ACP session ID and the agent supports it
	if acpSessionID != "" && c.HasLoadSessionSupport() {
		err := c.LoadSession(ctx, acpSessionID, cwd)
		if err == nil {
			return acpSessionID, false, nil
		}
		// Log the error but fall through to create a new session
		c.client.print("⚠️ Could not resume session: %v (creating new session)\n", err)
	}

	// Create a new session
	if err := c.NewSession(ctx, cwd); err != nil {
		return "", false, err
	}
	return c.SessionID(), true, nil
}

// Prompt sends a message to the agent and waits for the response.
func (c *Connection) Prompt(ctx context.Context, message string) error {
	return c.PromptWithContent(ctx, []acp.ContentBlock{acp.TextBlock(message)})
}

// PromptWithContent sends content blocks to the agent and waits for the response.
// This allows sending images and other content types along with text.
func (c *Connection) PromptWithContent(ctx context.Context, content []acp.ContentBlock) error {
	if c.session == nil {
		return fmt.Errorf("no active session")
	}

	_, err := c.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: c.session.SessionId,
		Prompt:    content,
	})
	return err
}

// Cancel cancels the current operation.
func (c *Connection) Cancel(ctx context.Context) error {
	if c.session == nil {
		return nil
	}
	return c.conn.Cancel(ctx, acp.CancelNotification{SessionId: c.session.SessionId})
}

// Close terminates the ACP server process.
func (c *Connection) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// Done returns a channel that's closed when the connection is done.
func (c *Connection) Done() <-chan struct{} {
	return c.conn.Done()
}

// SessionID returns the current session ID.
func (c *Connection) SessionID() string {
	if c.session == nil {
		return ""
	}
	return string(c.session.SessionId)
}
