package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// WebClient implements acp.Client for web-based interaction.
// It sends streaming updates via callbacks instead of printing to terminal.
type WebClient struct {
	autoApprove bool

	// Callbacks for different event types
	onAgentMessage func(html string)
	onAgentThought func(text string)
	onToolCall     func(id, title, status string)
	onToolUpdate   func(id string, status *string)
	onPlan         func()
	onFileWrite    func(path string, size int)
	onFileRead     func(path string, size int)
	onPermission   func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)

	// Markdown buffer for streaming conversion
	mdBuffer *MarkdownBuffer
}

// Ensure WebClient implements acp.Client
var _ acp.Client = (*WebClient)(nil)

// WebClientConfig holds configuration for creating a WebClient.
type WebClientConfig struct {
	AutoApprove    bool
	OnAgentMessage func(html string)
	OnAgentThought func(text string)
	OnToolCall     func(id, title, status string)
	OnToolUpdate   func(id string, status *string)
	OnPlan         func()
	OnFileWrite    func(path string, size int)
	OnFileRead     func(path string, size int)
	OnPermission   func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

// NewWebClient creates a new web-based ACP client.
func NewWebClient(config WebClientConfig) *WebClient {
	c := &WebClient{
		autoApprove:    config.AutoApprove,
		onAgentMessage: config.OnAgentMessage,
		onAgentThought: config.OnAgentThought,
		onToolCall:     config.OnToolCall,
		onToolUpdate:   config.OnToolUpdate,
		onPlan:         config.OnPlan,
		onFileWrite:    config.OnFileWrite,
		onFileRead:     config.OnFileRead,
		onPermission:   config.OnPermission,
	}

	// Create markdown buffer that sends HTML via callback
	c.mdBuffer = NewMarkdownBuffer(func(html string) {
		if c.onAgentMessage != nil {
			c.onAgentMessage(html)
		}
	})

	return c
}

// SessionUpdate handles streaming updates from the agent.
func (c *WebClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update

	switch {
	case u.AgentMessageChunk != nil:
		// Stream text through markdown buffer
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.mdBuffer.Write(content.Text.Text)
		}

	case u.AgentThoughtChunk != nil:
		// Thoughts are sent as-is (not markdown)
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil && c.onAgentThought != nil {
			c.onAgentThought(thought.Text.Text)
		}

	case u.ToolCall != nil:
		if c.onToolCall != nil {
			c.onToolCall(string(u.ToolCall.ToolCallId), u.ToolCall.Title, string(u.ToolCall.Status))
		}

	case u.ToolCallUpdate != nil:
		if c.onToolUpdate != nil {
			var status *string
			if u.ToolCallUpdate.Status != nil {
				s := string(*u.ToolCallUpdate.Status)
				status = &s
			}
			c.onToolUpdate(string(u.ToolCallUpdate.ToolCallId), status)
		}

	case u.Plan != nil:
		if c.onPlan != nil {
			c.onPlan()
		}
	}

	return nil
}

// RequestPermission handles permission requests from the agent.
func (c *WebClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Flush any pending markdown before showing permission dialog
	c.mdBuffer.Flush()

	if c.autoApprove {
		return c.autoApprovePermission(params)
	}

	// Delegate to callback for interactive permission handling
	if c.onPermission != nil {
		return c.onPermission(ctx, params)
	}

	// No handler, cancel
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// autoApprovePermission automatically approves permission requests.
func (c *WebClient) autoApprovePermission(params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Prefer an allow option if present
	for _, o := range params.Options {
		if o.Kind == acp.PermissionOptionKindAllowOnce || o.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
				},
			}, nil
		}
	}
	// Otherwise choose the first option
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// WriteTextFile handles file write requests from the agent.
func (c *WebClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	dir := filepath.Dir(params.Path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return acp.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", params.Path, err)
	}
	if c.onFileWrite != nil {
		c.onFileWrite(params.Path, len(params.Content))
	}
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *WebClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acp.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", params.Path, err)
	}
	content := string(b)
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = min(max(*params.Line-1, 0), len(lines))
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 {
			if start+*params.Limit < end {
				end = start + *params.Limit
			}
		}
		content = strings.Join(lines[start:end], "\n")
	}
	if c.onFileRead != nil {
		c.onFileRead(params.Path, len(content))
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// CreateTerminal handles terminal creation requests.
func (c *WebClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{TerminalId: "term-1"}, nil
}

// TerminalOutput handles requests to get terminal output.
func (c *WebClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

// ReleaseTerminal handles terminal release requests.
func (c *WebClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

// WaitForTerminalExit handles requests to wait for terminal exit.
func (c *WebClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

// KillTerminalCommand handles requests to kill terminal commands.
func (c *WebClient) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, nil
}

// FlushMarkdown forces a flush of any buffered markdown content.
func (c *WebClient) FlushMarkdown() {
	c.mdBuffer.Flush()
}

// Close cleans up resources.
func (c *WebClient) Close() {
	c.mdBuffer.Close()
}
