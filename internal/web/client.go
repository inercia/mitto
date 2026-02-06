package web

import (
	"context"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversion"
)

// SeqProvider provides sequence numbers for event ordering.
// Sequence numbers are assigned when events are received from ACP,
// ensuring correct ordering even when content is buffered (e.g., in MarkdownBuffer).
type SeqProvider interface {
	// GetNextSeq returns the next sequence number and increments the counter.
	GetNextSeq() int64
}

// WebClient implements acp.Client for web-based interaction.
// It sends streaming updates via callbacks instead of printing to terminal.
type WebClient struct {
	autoApprove bool
	seqProvider SeqProvider

	// Callbacks for different event types (all include seq for ordering)
	onAgentMessage func(seq int64, html string)
	onAgentThought func(seq int64, text string)
	onToolCall     func(seq int64, id, title, status string)
	onToolUpdate   func(seq int64, id string, status *string)
	onPlan         func(seq int64)
	onFileWrite    func(seq int64, path string, size int)
	onFileRead     func(seq int64, path string, size int)
	onPermission   func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)

	// Markdown buffer for streaming conversion
	mdBuffer *MarkdownBuffer
}

// Ensure WebClient implements acp.Client
var _ acp.Client = (*WebClient)(nil)

// WebClientConfig holds configuration for creating a WebClient.
type WebClientConfig struct {
	AutoApprove bool
	// SeqProvider provides sequence numbers for event ordering.
	// Required for correct ordering of events when content is buffered.
	SeqProvider SeqProvider
	// Callbacks for different event types (all include seq for ordering)
	OnAgentMessage func(seq int64, html string)
	OnAgentThought func(seq int64, text string)
	OnToolCall     func(seq int64, id, title, status string)
	OnToolUpdate   func(seq int64, id string, status *string)
	OnPlan         func(seq int64)
	OnFileWrite    func(seq int64, path string, size int)
	OnFileRead     func(seq int64, path string, size int)
	OnPermission   func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	// FileLinksConfig configures file path detection and linking in agent messages.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
}

// NewWebClient creates a new web-based ACP client.
func NewWebClient(config WebClientConfig) *WebClient {
	c := &WebClient{
		autoApprove:    config.AutoApprove,
		seqProvider:    config.SeqProvider,
		onAgentMessage: config.OnAgentMessage,
		onAgentThought: config.OnAgentThought,
		onToolCall:     config.OnToolCall,
		onToolUpdate:   config.OnToolUpdate,
		onPlan:         config.OnPlan,
		onFileWrite:    config.OnFileWrite,
		onFileRead:     config.OnFileRead,
		onPermission:   config.OnPermission,
	}

	// Create markdown buffer that sends HTML via callback.
	// The seq is passed through the buffer to maintain correct ordering.
	c.mdBuffer = NewMarkdownBufferWithConfig(MarkdownBufferConfig{
		OnFlush: func(seq int64, html string) {
			if c.onAgentMessage != nil {
				c.onAgentMessage(seq, html)
			}
		},
		FileLinksConfig: config.FileLinksConfig,
	})

	return c
}

// SessionUpdate handles streaming updates from the agent.
// Sequence numbers are assigned at receive time to ensure correct ordering,
// even when content is buffered (e.g., in MarkdownBuffer).
func (c *WebClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update

	switch {
	case u.AgentMessageChunk != nil:
		// Assign seq NOW at receive time, before buffering.
		// This ensures correct ordering even if the buffer delays flushing.
		seq := c.getNextSeq()
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.mdBuffer.Write(seq, content.Text.Text)
		}

	case u.AgentThoughtChunk != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		// A thought signals the agent has finished its current text block.
		c.mdBuffer.Flush()
		// Thoughts are sent as-is (not markdown)
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil && c.onAgentThought != nil {
			c.onAgentThought(seq, thought.Text.Text)
		}

	case u.ToolCall != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		// A tool call signals the agent has finished its current text block.
		c.mdBuffer.Flush()
		if c.onToolCall != nil {
			c.onToolCall(seq, string(u.ToolCall.ToolCallId), u.ToolCall.Title, string(u.ToolCall.Status))
		}

	case u.ToolCallUpdate != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		if c.onToolUpdate != nil {
			var status *string
			if u.ToolCallUpdate.Status != nil {
				s := string(*u.ToolCallUpdate.Status)
				status = &s
			}
			c.onToolUpdate(seq, string(u.ToolCallUpdate.ToolCallId), status)
		}

	case u.Plan != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		if c.onPlan != nil {
			c.onPlan(seq)
		}
	}

	return nil
}

// getNextSeq returns the next sequence number from the provider.
// Returns 0 if no provider is configured (for backward compatibility in tests).
func (c *WebClient) getNextSeq() int64 {
	if c.seqProvider == nil {
		return 0
	}
	return c.seqProvider.GetNextSeq()
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
	// Assign seq NOW at receive time.
	seq := c.getNextSeq()
	if err := mittoAcp.DefaultFileSystem.WriteTextFile(params.Path, params.Content); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if c.onFileWrite != nil {
		c.onFileWrite(seq, params.Path, len(params.Content))
	}
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *WebClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	// Assign seq NOW at receive time.
	seq := c.getNextSeq()
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	if c.onFileRead != nil {
		c.onFileRead(seq, params.Path, len(content))
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
