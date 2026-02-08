package acp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/runner"
)

// Connection manages an ACP server process and its communication channel.
type Connection struct {
	cmd          *exec.Cmd
	conn         *acp.ClientSideConnection
	session      *acp.NewSessionResponse
	client       *Client
	logger       *slog.Logger
	capabilities *acp.AgentCapabilities
	wait         func() error // cleanup function from runner.RunWithPipes or cmd.Wait
}

// NewConnection starts an ACP server process and establishes a connection.
//
// If r is provided, the process is started through the restricted runner.
// If r is nil, the process is started directly (no restrictions).
func NewConnection(
	ctx context.Context,
	command string,
	autoApprove bool,
	output func(string),
	logger *slog.Logger,
	r *runner.Runner, // optional restricted runner
) (*Connection, error) {
	// Parse command into args
	args := strings.Fields(command)
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd
	var err error

	// Create command through runner or directly
	if r != nil {
		// Use restricted runner with RunWithPipes
		if logger != nil {
			logger.Info("starting ACP process through restricted runner",
				"runner_type", r.Type(),
				"command", command)
		}
		stdin, stdout, stderr, wait, err = r.RunWithPipes(ctx, args[0], args[1:], os.Environ())
		if err != nil {
			return nil, fmt.Errorf("failed to start with runner: %w", err)
		}

		// Monitor stderr in background
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					os.Stderr.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		}()
	} else {
		// Direct execution (no restrictions)
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stderr = os.Stderr

		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("stdin pipe error: %w", err)
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("stdout pipe error: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start ACP server: %w", err)
		}

		// Create wait function for direct execution
		wait = func() error {
			return cmd.Wait()
		}
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
		wait:   wait,
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

// Close terminates the ACP server process and cleans up resources.
func (c *Connection) Close() error {
	// Kill the process first
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}

	// Call wait() to clean up resources (from runner.RunWithPipes or cmd.Wait)
	if c.wait != nil {
		// Ignore error from wait() since we already killed the process
		c.wait()
	}

	return nil
}

// Done returns a channel that's closed when the connection is done.
func (c *Connection) Done() <-chan struct{} {
	return c.conn.Done()
}
