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

	// Callbacks for file operations (not buffered)
	onFileWrite          func(seq int64, path string, size int)
	onFileRead           func(seq int64, path string, size int)
	onPermission         func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	onAvailableCommands  func(commands []AvailableCommand)
	onCurrentModeChanged func(modeID string)

	// Stream buffer for all streaming events (markdown, thoughts, tool calls, etc.)
	// This ensures correct ordering even when markdown content is buffered.
	streamBuffer *StreamBuffer
}

// Ensure WebClient implements acp.Client
var _ acp.Client = (*WebClient)(nil)

// AvailableCommand represents a slash command that the agent can execute.
// This mirrors the ACP protocol's AvailableCommand structure.
type AvailableCommand struct {
	// Name is the command name (e.g., "web", "test", "plan").
	Name string `json:"name"`
	// Description is a human-readable description of what the command does.
	Description string `json:"description"`
	// InputHint is an optional hint to display when the input hasn't been provided yet.
	InputHint string `json:"input_hint,omitempty"`
}

// WebClientConfig holds configuration for creating a WebClient.
type WebClientConfig struct {
	AutoApprove bool
	// SeqProvider provides sequence numbers for event ordering.
	// Required for correct ordering of events when content is buffered.
	SeqProvider SeqProvider
	// Callbacks for different event types (all include seq for ordering)
	OnAgentMessage       func(seq int64, html string)
	OnAgentThought       func(seq int64, text string)
	OnToolCall           func(seq int64, id, title, status string)
	OnToolUpdate         func(seq int64, id string, status *string)
	OnPlan               func(seq int64, entries []PlanEntry)
	OnFileWrite          func(seq int64, path string, size int)
	OnFileRead           func(seq int64, path string, size int)
	OnPermission         func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	OnAvailableCommands  func(commands []AvailableCommand)
	OnCurrentModeChanged func(modeID string)
	// FileLinksConfig configures file path detection and linking in agent messages.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
}

// NewWebClient creates a new web-based ACP client.
func NewWebClient(config WebClientConfig) *WebClient {
	c := &WebClient{
		autoApprove:          config.AutoApprove,
		seqProvider:          config.SeqProvider,
		onFileWrite:          config.OnFileWrite,
		onFileRead:           config.OnFileRead,
		onPermission:         config.OnPermission,
		onAvailableCommands:  config.OnAvailableCommands,
		onCurrentModeChanged: config.OnCurrentModeChanged,
	}

	// Create stream buffer that handles all streaming events.
	// This ensures correct ordering even when markdown content is buffered.
	// Non-markdown events (tool calls, thoughts) are buffered when we're in
	// the middle of a markdown block (list, table, code block) and emitted
	// after the block completes.
	c.streamBuffer = NewStreamBuffer(StreamBufferConfig{
		Callbacks: StreamBufferCallbacks{
			OnAgentMessage: config.OnAgentMessage,
			OnAgentThought: config.OnAgentThought,
			OnToolCall:     config.OnToolCall,
			OnToolUpdate:   config.OnToolUpdate,
			OnPlan:         config.OnPlan,
		},
		FileLinksConfig: config.FileLinksConfig,
	})

	return c
}

// SessionUpdate handles streaming updates from the agent.
// Sequence numbers are assigned at receive time to ensure correct ordering,
// even when content is buffered (e.g., in MarkdownBuffer or StreamBuffer).
//
// Non-markdown events (tool calls, thoughts, plans) are buffered when we're
// in the middle of a markdown block (list, table, code block) and emitted
// after the block completes. This prevents tool calls from breaking lists.
func (c *WebClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update

	switch {
	case u.AgentMessageChunk != nil:
		// Assign seq NOW at receive time, before buffering.
		// This ensures correct ordering even if the buffer delays flushing.
		seq := c.getNextSeq()
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.streamBuffer.WriteMarkdown(seq, content.Text.Text)
		}

	case u.AgentThoughtChunk != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		// Thoughts are buffered if we're in a markdown block, otherwise emitted immediately.
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil {
			c.streamBuffer.AddThought(seq, thought.Text.Text)
		}

	case u.ToolCall != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		// Tool calls are buffered if we're in a markdown block, otherwise emitted immediately.
		status := string(u.ToolCall.Status)
		c.streamBuffer.AddToolCall(seq, string(u.ToolCall.ToolCallId), u.ToolCall.Title, &status)

	case u.ToolCallUpdate != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		var status *string
		if u.ToolCallUpdate.Status != nil {
			s := string(*u.ToolCallUpdate.Status)
			status = &s
		}
		c.streamBuffer.AddToolUpdate(seq, string(u.ToolCallUpdate.ToolCallId), status)

	case u.Plan != nil:
		// Assign seq NOW at receive time.
		seq := c.getNextSeq()
		// Convert ACP plan entries to our PlanEntry type
		entries := make([]PlanEntry, len(u.Plan.Entries))
		for i, e := range u.Plan.Entries {
			entries[i] = PlanEntry{
				Content:  e.Content,
				Priority: string(e.Priority),
				Status:   string(e.Status),
			}
		}
		c.streamBuffer.AddPlan(seq, entries)

	case u.AvailableCommandsUpdate != nil:
		// Available commands are not sequence-dependent; notify immediately.
		if c.onAvailableCommands != nil {
			commands := make([]AvailableCommand, len(u.AvailableCommandsUpdate.AvailableCommands))
			for i, cmd := range u.AvailableCommandsUpdate.AvailableCommands {
				inputHint := ""
				if cmd.Input != nil && cmd.Input.Unstructured != nil {
					inputHint = cmd.Input.Unstructured.Hint
				}
				commands[i] = AvailableCommand{
					Name:        cmd.Name,
					Description: cmd.Description,
					InputHint:   inputHint,
				}
			}
			c.onAvailableCommands(commands)
		}

	case u.CurrentModeUpdate != nil:
		// Current mode changed; notify immediately.
		if c.onCurrentModeChanged != nil {
			c.onCurrentModeChanged(string(u.CurrentModeUpdate.CurrentModeId))
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
	// Force flush all buffered content before showing permission dialog.
	// Permission dialogs are blocking, so we need to show all content first.
	c.streamBuffer.Flush()

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

// FlushMarkdown forces a flush of any buffered content (markdown and pending events).
func (c *WebClient) FlushMarkdown() {
	c.streamBuffer.Flush()
}

// Close cleans up resources.
func (c *WebClient) Close() {
	c.streamBuffer.Close()
}
