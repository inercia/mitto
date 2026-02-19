package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	logger      *slog.Logger

	// Callbacks for file operations (not buffered)
	onFileWrite          func(seq int64, path string, size int)
	onFileRead           func(seq int64, path string, size int)
	onPermission         func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	onAvailableCommands  func(commands []AvailableCommand)
	onCurrentModeChanged func(modeID string)
	// onMittoToolCall is called when any mitto_* tool call is detected.
	onMittoToolCall func(requestID string)

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

// SessionConfigOption represents a configurable session option.
// This type mirrors the ACP configOptions structure and supports both:
// - Legacy "modes" API (converted to configOptions with category "mode")
// - Newer "configOptions" API (used directly when available)
// See https://agentclientprotocol.com/protocol/session-config-options
type SessionConfigOption struct {
	// ID is the unique identifier for this option (e.g., "mode", "model").
	ID string `json:"id"`
	// Name is the human-readable label for the option (e.g., "Session Mode", "Model").
	Name string `json:"name"`
	// Description provides more details about what this option controls.
	Description string `json:"description,omitempty"`
	// Category is semantic metadata for UX (e.g., "mode", "model", "thought_level").
	// For legacy modes, this is always "mode".
	Category string `json:"category,omitempty"`
	// Type is the input control type. Currently only "select" is supported.
	Type string `json:"type"`
	// CurrentValue is the currently selected value for this option.
	CurrentValue string `json:"current_value"`
	// Options are the available values for this option.
	Options []SessionConfigOptionValue `json:"options"`
}

// SessionConfigOptionValue represents a selectable value for a config option.
type SessionConfigOptionValue struct {
	// Value is the identifier used when setting this option.
	Value string `json:"value"`
	// Name is the human-readable name to display.
	Name string `json:"name"`
	// Description explains what this value does.
	Description string `json:"description,omitempty"`
}

// ConfigOptionCategory constants for well-known categories.
const (
	ConfigOptionCategoryMode         = "mode"
	ConfigOptionCategoryModel        = "model"
	ConfigOptionCategoryThoughtLevel = "thought_level"
)

// ConfigOptionType constants for option types.
const (
	// ConfigOptionTypeSelect is a dropdown/select control.
	ConfigOptionTypeSelect = "select"
	// ConfigOptionTypeToggle is a boolean toggle control (future).
	ConfigOptionTypeToggle = "toggle"
)

// WebClientConfig holds configuration for creating a WebClient.
type WebClientConfig struct {
	AutoApprove bool
	// SeqProvider provides sequence numbers for event ordering.
	// Required for correct ordering of events when content is buffered.
	SeqProvider SeqProvider
	// Logger for debug logging of ACP SDK events (optional).
	// When set to DEBUG level, logs timing of events from ACP SDK.
	Logger *slog.Logger
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
	// OnMittoToolCall is called when any mitto_* tool call is detected.
	// This allows registering the request_id for correlation with MCP requests.
	// The callback receives the request_id extracted from the tool call arguments.
	// All mitto_* tools use request_id for automatic session detection.
	OnMittoToolCall func(requestID string)
	// FileLinksConfig configures file path detection and linking in agent messages.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
}

// NewWebClient creates a new web-based ACP client.
func NewWebClient(config WebClientConfig) *WebClient {
	c := &WebClient{
		autoApprove:          config.AutoApprove,
		seqProvider:          config.SeqProvider,
		logger:               config.Logger,
		onFileWrite:          config.OnFileWrite,
		onFileRead:           config.OnFileRead,
		onPermission:         config.OnPermission,
		onAvailableCommands:  config.OnAvailableCommands,
		onCurrentModeChanged: config.OnCurrentModeChanged,
		onMittoToolCall:      config.OnMittoToolCall,
	}

	// Create stream buffer that handles all streaming events.
	// This ensures correct ordering even when markdown content is buffered.
	// Non-markdown events (tool calls, thoughts) are buffered when we're in
	// the middle of a markdown block (list, table, code block) and emitted
	// after the block completes.
	// SeqProvider is passed to StreamBuffer so seq is assigned at emit time,
	// ensuring contiguous sequence numbers without gaps from coalesced chunks.
	c.streamBuffer = NewStreamBuffer(StreamBufferConfig{
		Callbacks: StreamBufferCallbacks{
			OnAgentMessage: config.OnAgentMessage,
			OnAgentThought: config.OnAgentThought,
			OnToolCall:     config.OnToolCall,
			OnToolUpdate:   config.OnToolUpdate,
			OnPlan:         config.OnPlan,
		},
		FileLinksConfig: config.FileLinksConfig,
		SeqProvider:     config.SeqProvider,
	})

	return c
}

// SessionUpdate handles streaming updates from the agent.
// Sequence numbers are assigned at emit time (not receive time) to ensure
// contiguous numbers without gaps from coalesced chunks.
//
// Non-markdown events (tool calls, thoughts, plans) are buffered when we're
// in the middle of a markdown block (list, table, code block) and emitted
// after the block completes. This prevents tool calls from breaking lists.
func (c *WebClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	// Log event arrival time from ACP SDK (DEBUG level only).
	// This helps diagnose whether events arrive in bursts from ACP or are delayed in our processing.
	receiveTime := time.Now()
	u := params.Update

	// Determine event type for logging
	var eventType string
	var eventDetail string
	switch {
	case u.AgentMessageChunk != nil:
		eventType = "agent_message_chunk"
		if u.AgentMessageChunk.Content.Text != nil {
			eventDetail = fmt.Sprintf("len=%d", len(u.AgentMessageChunk.Content.Text.Text))
		}
	case u.AgentThoughtChunk != nil:
		eventType = "agent_thought_chunk"
	case u.ToolCall != nil:
		eventType = "tool_call"
		eventDetail = u.ToolCall.Title
	case u.ToolCallUpdate != nil:
		eventType = "tool_call_update"
		eventDetail = string(u.ToolCallUpdate.ToolCallId)
	case u.Plan != nil:
		eventType = "plan"
	case u.AvailableCommandsUpdate != nil:
		eventType = "available_commands"
	case u.CurrentModeUpdate != nil:
		eventType = "current_mode"
	default:
		eventType = "unknown"
	}

	if c.logger != nil {
		c.logger.Debug("acp_sdk_event_received",
			"event_type", eventType,
			"event_detail", eventDetail,
			"receive_time_ns", receiveTime.UnixNano(),
			"receive_time", receiveTime.Format("15:04:05.000000"),
		)
	}

	switch {
	case u.AgentMessageChunk != nil:
		// Seq is assigned at emit time by StreamBuffer, not here.
		// This ensures contiguous seq numbers even when chunks are coalesced.
		content := u.AgentMessageChunk.Content
		if content.Text != nil {
			c.streamBuffer.WriteMarkdown(content.Text.Text)
		}

	case u.AgentThoughtChunk != nil:
		// Seq is assigned at emit time by StreamBuffer.
		// Thoughts are buffered if we're in a markdown block, otherwise emitted immediately.
		thought := u.AgentThoughtChunk.Content
		if thought.Text != nil {
			c.streamBuffer.AddThought(thought.Text.Text)
		}

	case u.ToolCall != nil:
		// Seq is assigned at emit time by StreamBuffer.
		// Tool calls are buffered if we're in a markdown block, otherwise emitted immediately.
		status := string(u.ToolCall.Status)
		c.streamBuffer.AddToolCall(string(u.ToolCall.ToolCallId), u.ToolCall.Title, &status)

		// Check if this is any mitto_* tool call and extract session_id for correlation.
		// The tool title contains the tool name (e.g., "mitto_get_current_session_mitto-debug").
		// All mitto_* tools use session_id for automatic session detection.
		if c.onMittoToolCall != nil && strings.Contains(u.ToolCall.Title, "mitto_") {
			if sessionID := extractMittoSessionID(u.ToolCall.RawInput); sessionID != "" {
				c.onMittoToolCall(sessionID)
			}
		}

	case u.ToolCallUpdate != nil:
		// Seq is assigned at emit time by StreamBuffer.
		var status *string
		if u.ToolCallUpdate.Status != nil {
			s := string(*u.ToolCallUpdate.Status)
			status = &s
		}
		c.streamBuffer.AddToolUpdate(string(u.ToolCallUpdate.ToolCallId), status)

	case u.Plan != nil:
		// Seq is assigned at emit time by StreamBuffer.
		// Convert ACP plan entries to our PlanEntry type
		entries := make([]PlanEntry, len(u.Plan.Entries))
		for i, e := range u.Plan.Entries {
			entries[i] = PlanEntry{
				Content:  e.Content,
				Priority: string(e.Priority),
				Status:   string(e.Status),
			}
		}
		c.streamBuffer.AddPlan(entries)

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

// extractMittoSessionID extracts the session_id from a mitto_* tool call's RawInput.
// The RawInput can be a map[string]any, a JSON string, or a struct with a SessionID field.
// Returns empty string if session_id is not found or extraction fails.
func extractMittoSessionID(rawInput any) string {
	if rawInput == nil {
		return ""
	}

	// Try direct map access
	if m, ok := rawInput.(map[string]any); ok {
		if sessionID, ok := m["session_id"].(string); ok {
			return sessionID
		}
	}

	// Try JSON string
	if s, ok := rawInput.(string); ok {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			if sessionID, ok := m["session_id"].(string); ok {
				return sessionID
			}
		}
	}

	// Try marshaling to JSON then unmarshaling to map
	data, err := json.Marshal(rawInput)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if sessionID, ok := m["session_id"].(string); ok {
		return sessionID
	}

	return ""
}
