package web

import (
	"context"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversion"
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
	// FileLinksConfig configures file path detection and linking in agent messages.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
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
	c.mdBuffer = NewMarkdownBufferWithConfig(MarkdownBufferConfig{
		OnFlush: func(html string) {
			if c.onAgentMessage != nil {
				c.onAgentMessage(html)
			}
		},
		FileLinksConfig: config.FileLinksConfig,
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
		// Try to flush any buffered message before thought to maintain event order.
		// Use SafeFlush to avoid breaking tables/lists/code blocks - if we're in
		// the middle of one, the thought will appear after the structure completes.
		c.mdBuffer.SafeFlush()
		// Thoughts are sent as-is (not markdown)
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil && c.onAgentThought != nil {
			c.onAgentThought(thought.Text.Text)
		}

	case u.ToolCall != nil:
		// Try to flush any buffered message before tool call to maintain event order.
		// Use SafeFlush to avoid breaking tables/lists/code blocks - if we're in
		// the middle of one, the tool call will appear after the structure completes.
		c.mdBuffer.SafeFlush()
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
	// Try to flush any pending markdown before showing permission dialog.
	// Use SafeFlush to avoid breaking tables/lists/code blocks.
	c.mdBuffer.SafeFlush()

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
	return mittoAcp.AutoApprovePermission(params.Options), nil
}

// WriteTextFile handles file write requests from the agent.
func (c *WebClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := mittoAcp.DefaultFileSystem.WriteTextFile(params.Path, params.Content); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if c.onFileWrite != nil {
		c.onFileWrite(params.Path, len(params.Content))
	}
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *WebClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	if c.onFileRead != nil {
		c.onFileRead(params.Path, len(content))
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// webTerminalStub is the shared stub handler for terminal operations.
var webTerminalStub = &mittoAcp.StubTerminalHandler{}

// CreateTerminal handles terminal creation requests.
func (c *WebClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return webTerminalStub.CreateTerminal(ctx, params)
}

// TerminalOutput handles requests to get terminal output.
func (c *WebClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return webTerminalStub.TerminalOutput(ctx, params)
}

// ReleaseTerminal handles terminal release requests.
func (c *WebClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return webTerminalStub.ReleaseTerminal(ctx, params)
}

// WaitForTerminalExit handles requests to wait for terminal exit.
func (c *WebClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return webTerminalStub.WaitForTerminalExit(ctx, params)
}

// KillTerminalCommand handles requests to kill terminal commands.
func (c *WebClient) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return webTerminalStub.KillTerminalCommand(ctx, params)
}

// FlushMarkdown forces a flush of any buffered markdown content.
func (c *WebClient) FlushMarkdown() {
	c.mdBuffer.Flush()
}

// Close cleans up resources.
func (c *WebClient) Close() {
	c.mdBuffer.Close()
}
