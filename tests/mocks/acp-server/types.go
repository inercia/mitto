package main

import "encoding/json"

// JSON-RPC message types
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ACP Protocol types
type InitializeParams struct {
	ProtocolVersion int `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion int `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities struct {
		Streaming bool `json:"streaming"`
	} `json:"capabilities"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
}

type AgentCapabilities struct {
	Streaming bool `json:"streaming"`
}

type NewSessionParams struct {
	Cwd              string `json:"cwd"`
	WorkingDirectory string `json:"workingDirectory"` // Legacy field
}

type NewSessionResult struct {
	SessionID string            `json:"sessionId"`
	Modes     *SessionModeState `json:"modes,omitempty"`
}

// SessionModeState represents the set of modes and the one currently active.
type SessionModeState struct {
	AvailableModes []SessionMode `json:"availableModes"`
	CurrentModeID  string        `json:"currentModeId"`
}

// SessionMode represents a single mode option.
type SessionMode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// SetSessionModeParams is the request to change the session mode.
type SetSessionModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// SetSessionModeResult is the response after changing the session mode.
type SetSessionModeResult struct {
	// Empty response on success
}

// SessionCurrentModeUpdate is sent when the current mode changes.
type SessionCurrentModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	CurrentModeID string `json:"currentModeId"`
}

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	// Message is kept for backward compatibility but deprecated
	Message string `json:"message,omitempty"`
}

type PromptResult struct {
	Success bool `json:"success"`
}

// PromptResponse matches the SDK's PromptResponse type with stopReason
type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

// Session notification types
type SessionNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		SessionID string        `json:"sessionId"`
		Update    SessionUpdate `json:"update"`
	} `json:"params"`
}

type SessionUpdate struct {
	AgentMessageChunk *AgentMessageChunk        `json:"-"` // Tagged union - use custom marshal
	AgentThoughtChunk *AgentThoughtChunk        `json:"-"` // Tagged union - use custom marshal
	ToolCall          *ToolCall                 `json:"-"` // Tagged union - use custom marshal
	ToolCallUpdate    *ToolCallUpdate           `json:"-"` // Tagged union - use custom marshal
	Plan              *Plan                     `json:"-"` // Tagged union - use custom marshal
	CurrentModeUpdate *SessionCurrentModeUpdate `json:"-"` // Tagged union - use custom marshal
}

// MarshalJSON implements the ACP SDK's tagged union format.
// Each variant includes a "sessionUpdate" discriminator field.
func (u SessionUpdate) MarshalJSON() ([]byte, error) {
	if u.AgentMessageChunk != nil {
		return json.Marshal(struct {
			SessionUpdate string       `json:"sessionUpdate"`
			Content       ContentBlock `json:"content"`
		}{
			SessionUpdate: "agent_message_chunk",
			Content:       u.AgentMessageChunk.Content,
		})
	}
	if u.AgentThoughtChunk != nil {
		return json.Marshal(struct {
			SessionUpdate string       `json:"sessionUpdate"`
			Content       ContentBlock `json:"content"`
		}{
			SessionUpdate: "agent_thought_chunk",
			Content:       u.AgentThoughtChunk.Content,
		})
	}
	if u.ToolCall != nil {
		return json.Marshal(struct {
			SessionUpdate string `json:"sessionUpdate"`
			ToolCallID    string `json:"toolCallId"`
			Title         string `json:"title"`
			Status        string `json:"status"`
			RawInput      any    `json:"rawInput,omitempty"`
		}{
			SessionUpdate: "tool_call",
			ToolCallID:    u.ToolCall.ToolCallID,
			Title:         u.ToolCall.Title,
			Status:        u.ToolCall.Status,
			RawInput:      u.ToolCall.RawInput,
		})
	}
	if u.ToolCallUpdate != nil {
		return json.Marshal(struct {
			SessionUpdate string  `json:"sessionUpdate"`
			ToolCallID    string  `json:"toolCallId"`
			Status        *string `json:"status,omitempty"`
		}{
			SessionUpdate: "tool_call_update",
			ToolCallID:    u.ToolCallUpdate.ToolCallID,
			Status:        u.ToolCallUpdate.Status,
		})
	}
	if u.Plan != nil {
		return json.Marshal(struct {
			SessionUpdate string `json:"sessionUpdate"`
			Description   string `json:"description"`
		}{
			SessionUpdate: "plan",
			Description:   u.Plan.Description,
		})
	}
	if u.CurrentModeUpdate != nil {
		return json.Marshal(struct {
			SessionUpdate string `json:"sessionUpdate"`
			CurrentModeID string `json:"currentModeId"`
		}{
			SessionUpdate: "current_mode_update",
			CurrentModeID: u.CurrentModeUpdate.CurrentModeID,
		})
	}
	return []byte("{}"), nil
}

type AgentMessageChunk struct {
	Content ContentBlock `json:"content"`
}

type AgentThoughtChunk struct {
	Content ContentBlock `json:"content"`
}

// ContentBlock represents both prompt content (flat format) and notification content.
// Prompt format from SDK: {"type": "text", "text": "hello"}
// Notification format: {"type": "text", "text": "hello"} or legacy {"text": {"text": "hello"}}
type ContentBlock struct {
	// Flat format used by SDK
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
	// Nested format for backward compatibility
	TextContent *TextContent `json:"textContent,omitempty"`
}

// TextContent is the nested text format used in some older protocol versions.
type TextContent struct {
	Text string `json:"text"`
}

// GetText extracts the text content regardless of format.
func (c *ContentBlock) GetText() string {
	if c.Text != "" {
		return c.Text
	}
	if c.TextContent != nil {
		return c.TextContent.Text
	}
	return ""
}

type ToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	RawInput   any    `json:"rawInput,omitempty"` // For mitto_* tools, includes session_id for correlation
}

type ToolCallUpdate struct {
	ToolCallID string  `json:"toolCallId"`
	Status     *string `json:"status,omitempty"`
}

type Plan struct {
	Description string   `json:"description,omitempty"`
	Steps       []string `json:"steps,omitempty"`
}

// Scenario types for test fixtures
type Scenario struct {
	Name        string     `json:"scenario"`
	Description string     `json:"description"`
	Responses   []Response `json:"responses"`
}

type Response struct {
	Trigger Trigger  `json:"trigger"`
	Actions []Action `json:"actions"`
}

type Trigger struct {
	Type    string `json:"type"`
	Pattern string `json:"pattern"`
}

type Action struct {
	Type     string   `json:"type"`
	DelayMs  int      `json:"delay_ms,omitempty"`
	Chunks   []string `json:"chunks,omitempty"`
	Text     string   `json:"text,omitempty"`
	ID       string   `json:"id,omitempty"`
	Title    string   `json:"title,omitempty"`
	Status   string   `json:"status,omitempty"`
	Message  string   `json:"message,omitempty"`
	File     string   `json:"file,omitempty"`      // For replay action: path to events.jsonl file
	RawInput any      `json:"raw_input,omitempty"` // For tool_call action: tool input parameters
}

// ReplayEvent represents an event from an events.jsonl file for replay.
type ReplayEvent struct {
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// ReplayAgentMessageData is the data for agent_message events in replay files.
type ReplayAgentMessageData struct {
	Text string `json:"text"` // Raw markdown text (not HTML)
}

// ReplayUserPromptData is the data for user_prompt events in replay files.
type ReplayUserPromptData struct {
	Message string `json:"message"`
}
