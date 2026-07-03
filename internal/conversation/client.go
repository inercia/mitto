package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversion"
)

// WebClient implements acp.Client for web-based interaction.
// It sends streaming updates via callbacks instead of printing to terminal.
type WebClient struct {
	autoApprove bool
	seqProvider SeqProvider
	logger      *slog.Logger

	// isLoadingSession is set to true during LoadSession to suppress event processing.
	// During Load, the agent replays the entire conversation history as ACP notifications.
	// With large sessions (hundreds of exchanges), this produces thousands of events that
	// overwhelm the SDK's bounded notification queue (1024 entries) because the consumer
	// (markdown conversion, persistence, pruning) is slower than the producer.
	// When true, SessionUpdate returns immediately — the events are historical and already
	// persisted by Mitto, so discarding them is safe.
	isLoadingSession atomic.Bool

	// Callbacks for file operations (not buffered)
	onFileWrite          func(seq int64, path string, size int)
	onFileRead           func(seq int64, path string, size int)
	onPermission         func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	onAvailableCommands  func(commands []AvailableCommand)
	onCurrentModeChanged func(modeID string)
	// onMittoToolCall is called when any mitto_* tool call is detected.
	onMittoToolCall func(selfID string)
	// onContextUsageUpdate is called when the agent sends a context window usage update.
	onContextUsageUpdate func(size, used int)
	// onActivity is called on every streamed update from the agent (pre-buffering)
	// to signal liveness for the prompt inactivity watchdog.
	onActivity func()

	// Stream buffer for all streaming events (markdown, thoughts, tool calls, etc.)
	// This ensures correct ordering even when markdown content is buffered.
	streamBuffer *StreamBuffer
}

// Ensure WebClient implements acp.Client
var _ acp.Client = (*WebClient)(nil)

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
	// This allows registering the self_id for correlation with MCP requests.
	// The callback receives the self_id extracted from the tool call arguments.
	// All mitto_* tools use self_id for automatic session detection.
	OnMittoToolCall func(selfID string)
	// OnContextUsageUpdate is called when the agent sends context window usage data.
	OnContextUsageUpdate func(size, used int)
	// OnActivity is called on every streamed update received from the agent, before
	// any buffering. It signals that the agent is still alive and producing output,
	// used by the prompt inactivity watchdog to detect a live-but-unresponsive agent.
	OnActivity func()
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
		onContextUsageUpdate: config.OnContextUsageUpdate,
		onActivity:           config.OnActivity,
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

// SetLoadingSession controls whether the WebClient suppresses event processing.
// Set to true before calling LoadSession, and false after it returns.
// During Load, the agent replays the entire conversation history as notifications.
// Discarding them prevents the SDK's notification queue (1024 entries) from overflowing
// when the consumer (markdown conversion + persistence) can't keep up.
func (c *WebClient) SetLoadingSession(loading bool) {
	c.isLoadingSession.Store(loading)
}

// SessionUpdate handles streaming updates from the agent.
// Sequence numbers are assigned at emit time (not receive time) to ensure
// contiguous numbers without gaps from coalesced chunks.
//
// Non-markdown events (tool calls, thoughts, plans) are buffered when we're
// in the middle of a markdown block (list, table, code block) and emitted
// after the block completes. This prevents tool calls from breaking lists.
func (c *WebClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	// During LoadSession, discard all replayed history events to prevent
	// notification queue overflow. These events are historical — Mitto already
	// has them persisted in events.jsonl.
	if c.isLoadingSession.Load() {
		return nil
	}

	// Signal liveness to the prompt inactivity watchdog: any streamed update from
	// the agent (message, thought, tool call, plan, etc.) means it is still working.
	if c.onActivity != nil {
		c.onActivity()
	}

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
	case u.UsageUpdate != nil:
		eventType = "usage_update"
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
		// Register MCP correlation BEFORE StreamBuffer processing.
		// The MCP HTTP request arrives in parallel and polls for this correlation.
		// By registering first, we avoid the MCP handler waiting for StreamBuffer
		// to finish flushing markdown, persisting events, and notifying observers.
		if c.onMittoToolCall != nil && strings.Contains(u.ToolCall.Title, "mitto_") {
			selfID := extractMittoSelfID(u.ToolCall.RawInput)
			if selfID != "" {
				c.onMittoToolCall(selfID)
			} else if strings.Contains(u.ToolCall.Title, "get_current") {
				// Fallback for agents that don't include RawInput in ACP tool_call events
				// (e.g., Claude Code). Register with "init" — the documented default self_id
				// value — so the MCP server can correlate the request with this session.
				if c.logger != nil {
					c.logger.Debug("mitto get_current tool call detected without RawInput, using fallback",
						"tool_title", u.ToolCall.Title)
				}
				c.onMittoToolCall("init")
			}
		}

		// Seq is assigned at emit time by StreamBuffer.
		// Tool calls are buffered if we're in a markdown block, otherwise emitted immediately.
		status := string(u.ToolCall.Status)
		c.streamBuffer.AddToolCall(string(u.ToolCall.ToolCallId), u.ToolCall.Title, &status)

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

	case u.UsageUpdate != nil:
		// Context window usage update; notify immediately.
		if c.onContextUsageUpdate != nil {
			c.onContextUsageUpdate(u.UsageUpdate.Size, u.UsageUpdate.Used)
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
	if err := mittoAcp.DefaultFileSystem.WriteTextFile(params.Path, params.Content); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	// Assign seq AFTER success to avoid consuming a seq number on error,
	// which would create a gap in the sequence (e.g., seq jumps from 992 to 994).
	seq := c.getNextSeq()
	if c.onFileWrite != nil {
		c.onFileWrite(seq, params.Path, len(params.Content))
	}
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile handles file read requests from the agent.
func (c *WebClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	// Assign seq AFTER success to avoid consuming a seq number on error,
	// which would create a gap in the sequence (e.g., seq jumps from 992 to 994).
	seq := c.getNextSeq()
	if c.onFileRead != nil {
		c.onFileRead(seq, params.Path, len(content))
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// WebTerminalStub is the shared stub handler for terminal operations.
// It is exported so that multiplex_client (internal/web) can reuse it.
var WebTerminalStub = &mittoAcp.StubTerminalHandler{}

// CreateTerminal handles terminal creation requests.
func (c *WebClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return WebTerminalStub.CreateTerminal(ctx, params)
}

// TerminalOutput handles requests to get terminal output.
func (c *WebClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return WebTerminalStub.TerminalOutput(ctx, params)
}

// ReleaseTerminal handles terminal release requests.
func (c *WebClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return WebTerminalStub.ReleaseTerminal(ctx, params)
}

// WaitForTerminalExit handles requests to wait for terminal exit.
func (c *WebClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return WebTerminalStub.WaitForTerminalExit(ctx, params)
}

// KillTerminal handles requests to kill terminals.
func (c *WebClient) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return WebTerminalStub.KillTerminal(ctx, params)
}

// FlushMarkdown forces a flush of any buffered content (markdown and pending events).
func (c *WebClient) FlushMarkdown() {
	c.streamBuffer.Flush()
}

// Close cleans up resources.
func (c *WebClient) Close() {
	c.streamBuffer.Close()
}

// extractMittoSelfID extracts the self_id from a mitto_* tool call's RawInput.
// The RawInput can be a map[string]any, a JSON string, or a struct with a SelfID field.
// Returns empty string if self_id is not found or extraction fails.
func extractMittoSelfID(rawInput any) string {
	if rawInput == nil {
		return ""
	}

	// Try direct map access
	if m, ok := rawInput.(map[string]any); ok {
		if selfID, ok := m["self_id"].(string); ok {
			return selfID
		}
	}

	// Try JSON string
	if s, ok := rawInput.(string); ok {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			if selfID, ok := m["self_id"].(string); ok {
				return selfID
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
	if selfID, ok := m["self_id"].(string); ok {
		return selfID
	}

	return ""
}
