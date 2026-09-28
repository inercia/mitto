package session

import (
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"reflect"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// htmlStripper strips all HTML tags, returning plain text.
// Agent message events store HTML (markdown converted to HTML by the web layer).
// When replaying conversation history as a text prompt, we must strip the HTML
// to avoid the agent learning to produce HTML output, which would then be
// double-converted (HTML→markdown parser→HTML) and stripped by goldmark's
// safe renderer.
var htmlStripper = bluemonday.StrictPolicy()

// stripHTML removes all HTML tags and decodes HTML entities to plain text.
func stripHTML(s string) string {
	return htmlpkg.UnescapeString(htmlStripper.Sanitize(s))
}

// StripHTML removes all HTML tags and decodes HTML entities to plain text.
// This is the exported version of stripHTML for use by other packages.
func StripHTML(s string) string {
	return stripHTML(s)
}

// eventDataTypes maps event types to their corresponding data struct types.
// This registry is used by DecodeEventData to avoid duplicate switch statements.
var eventDataTypes = map[EventType]reflect.Type{
	EventTypeUserPrompt:     reflect.TypeOf(UserPromptData{}),
	EventTypeAgentMessage:   reflect.TypeOf(AgentMessageData{}),
	EventTypeAgentThought:   reflect.TypeOf(AgentThoughtData{}),
	EventTypeToolCall:       reflect.TypeOf(ToolCallData{}),
	EventTypeToolCallUpdate: reflect.TypeOf(ToolCallUpdateData{}),
	EventTypePlan:           reflect.TypeOf(PlanData{}),
	EventTypePermission:     reflect.TypeOf(PermissionData{}),
	EventTypeFileRead:       reflect.TypeOf(FileOperationData{}),
	EventTypeFileWrite:      reflect.TypeOf(FileOperationData{}),
	EventTypeError:          reflect.TypeOf(ErrorData{}),
	EventTypeSessionStart:   reflect.TypeOf(SessionStartData{}),
	EventTypeSessionEnd:     reflect.TypeOf(SessionEndData{}),
	EventTypeSessionChange:  reflect.TypeOf(SessionChangeData{}),
	EventTypeProcessorRun:   reflect.TypeOf(ProcessorRunData{}),
}

// DecodeEventData decodes the event data into the appropriate type.
// If the data is already the correct struct type, it returns it directly.
// If the data is a map (from JSON unmarshaling), it converts it to the appropriate struct.
func DecodeEventData(event Event) (interface{}, error) {
	// Look up the expected type for this event
	expectedType, ok := eventDataTypes[event.Type]
	if !ok {
		// Unknown event type, return data as-is
		return event.Data, nil
	}

	// Check if data is already the correct type
	dataValue := reflect.ValueOf(event.Data)
	if dataValue.Type() == expectedType {
		return event.Data, nil
	}

	// Data is likely a map from JSON unmarshaling, convert it
	return decodeMapToStruct(event.Type, event.Data)
}

// BuildConversationHistory builds a text summary of the conversation from events.
// This is used to provide context when resuming a session with a new ACP process.
// It extracts user prompts and agent messages, limiting to the most recent exchanges.
func BuildConversationHistory(events []Event, maxTurns int) string {
	turns := extractConversationTurns(events)
	if len(turns) == 0 {
		return ""
	}

	// Limit to most recent turns
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}

	var sb strings.Builder
	sb.WriteString(conversationHistoryHeader)
	for i, t := range turns {
		sb.WriteString(renderConversationTurn(i+1, t))
	}
	sb.WriteString(conversationHistoryFooter)
	return sb.String()
}

// conversationHistoryHeader/Footer bracket the injected transcript in both
// BuildConversationHistory and BuildConversationHistoryCapped.
const (
	conversationHistoryHeader = "[CONVERSATION HISTORY - This is a resumed session. Previous context:]\n\n"
	conversationHistoryFooter = "[END OF HISTORY - Continue the conversation:]\n\n"
	// conversationHistoryTruncationMarker prefixes a turn block whose text had
	// to be cut from the beginning to fit a character budget (used only by
	// BuildConversationHistoryCapped when even the single most recent turn
	// overflows maxChars), so it's clear to both the reader and the agent that
	// earlier content in that block was removed rather than simply absent.
	conversationHistoryTruncationMarker = "[...earlier content truncated to fit context budget...]\n"
)

// conversationTurn is one user-prompt/agent-response exchange extracted from
// a session's persisted events, as used by BuildConversationHistory and
// BuildConversationHistoryCapped.
type conversationTurn struct {
	userMessage  string
	agentMessage string
}

// extractConversationTurns walks events and pairs each user prompt with the
// agent message text that followed it (concatenating multiple agent_message
// events belonging to the same turn), stripping HTML along the way. Order is
// preserved (oldest first). A trailing user prompt with no agent response yet
// is still included as its own (agent-less) turn.
func extractConversationTurns(events []Event) []conversationTurn {
	if len(events) == 0 {
		return nil
	}

	var turns []conversationTurn
	var current conversationTurn

	for _, event := range events {
		data, err := DecodeEventData(event)
		if err != nil {
			continue
		}

		switch event.Type {
		case EventTypeUserPrompt:
			// Start a new turn
			if current.userMessage != "" {
				turns = append(turns, current)
			}
			if d, ok := data.(UserPromptData); ok {
				current = conversationTurn{userMessage: d.Message}
			}
		case EventTypeAgentMessage:
			// Add to current turn (strip HTML — events store HTML, not markdown)
			if d, ok := data.(AgentMessageData); ok {
				current.agentMessage += stripHTML(d.Text)
			}
		}
	}

	// Don't forget the last turn
	if current.userMessage != "" {
		turns = append(turns, current)
	}

	return turns
}

// renderConversationTurn renders a single turn block, numbered idx (1-based),
// applying the standard per-message truncation used by both
// BuildConversationHistory and BuildConversationHistoryCapped.
func renderConversationTurn(idx int, t conversationTurn) string {
	// Truncate very long messages
	userMsg := truncateText(t.userMessage, 500)
	agentMsg := truncateText(t.agentMessage, 1000)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- Turn %d ---\n", idx)
	fmt.Fprintf(&sb, "USER: %s\n\n", userMsg)
	if agentMsg != "" {
		fmt.Fprintf(&sb, "ASSISTANT: %s\n\n", agentMsg)
	}
	return sb.String()
}

// BuildConversationHistoryCapped is like BuildConversationHistory but additionally
// bounds the total rendered size by maxChars (in addition to the maxTurns count),
// trimming the OLDEST turns first so the most recently exchanged context is always
// kept. This is used for the larger history budget injected on the first prompt
// after an agent move (mitto-f7yo.5) — a plain maxTurns cap is not enough there
// because the raised turn count (e.g. 20 vs the usual 5) could otherwise produce
// an unbounded prompt size.
//
// maxTurns <= 0 means "no turn-count limit" (same convention as
// BuildConversationHistory). maxChars <= 0 means "no character limit", in which
// case this degenerates to exactly BuildConversationHistory(events, maxTurns).
//
// If even the single most recent turn alone (after per-message truncation)
// exceeds maxChars, that turn is NOT dropped — its rendered text is truncated
// from the beginning (keeping the tail, which is typically the most relevant
// part of the exchange) and prefixed with conversationHistoryTruncationMarker,
// so at least some context always survives rather than emitting nothing.
func BuildConversationHistoryCapped(events []Event, maxTurns int, maxChars int) string {
	turns := extractConversationTurns(events)
	if len(turns) == 0 {
		return ""
	}
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	if maxChars <= 0 {
		var sb strings.Builder
		sb.WriteString(conversationHistoryHeader)
		for i, t := range turns {
			sb.WriteString(renderConversationTurn(i+1, t))
		}
		sb.WriteString(conversationHistoryFooter)
		return sb.String()
	}

	budget := maxChars - len(conversationHistoryHeader) - len(conversationHistoryFooter)
	if budget < 0 {
		budget = 0
	}

	// Render each (already turn-count-capped) turn once, using its FINAL
	// position for numbering so blocks don't need to be re-rendered after
	// deciding how many oldest ones to drop below.
	blocks := make([]string, len(turns))
	for i, t := range turns {
		blocks[i] = renderConversationTurn(i+1, t)
	}

	// Walk backwards from the most recent turn, including as many older
	// turns as still fit the budget. Always keeps at least the most recent
	// block, even if it alone exceeds budget (handled by the overflow
	// truncation below) — the most recent context must never be dropped
	// entirely in favor of older context.
	keepFrom := len(blocks) - 1
	total := len(blocks[keepFrom])
	for keepFrom > 0 && total+len(blocks[keepFrom-1]) <= budget {
		keepFrom--
		total += len(blocks[keepFrom])
	}

	var sb strings.Builder
	for _, b := range blocks[keepFrom:] {
		sb.WriteString(b)
	}
	body := sb.String()

	if len(body) > budget {
		// Even the kept (most recent) turn(s) alone overflow the budget: cut
		// from the beginning of the combined body rather than drop it, so the
		// tail — typically the latest assistant reply — survives intact.
		if budget > len(conversationHistoryTruncationMarker) {
			cut := len(body) - (budget - len(conversationHistoryTruncationMarker))
			body = conversationHistoryTruncationMarker + body[cut:]
		} else {
			body = conversationHistoryTruncationMarker
		}
	}

	return conversationHistoryHeader + body + conversationHistoryFooter
}

// GetLastAgentMessage extracts the last agent message text from a list of events.
// It finds the most recent user_prompt and collects all subsequent agent_message events
// until the next user_prompt or end of events.
// Returns an empty string if no agent message is found after the last user prompt.
func GetLastAgentMessage(events []Event) string {
	if len(events) == 0 {
		return ""
	}

	// Find the last user_prompt index
	lastUserPromptIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventTypeUserPrompt {
			lastUserPromptIdx = i
			break
		}
	}

	// If no user prompt found, there's no conversation context
	if lastUserPromptIdx == -1 {
		return ""
	}

	// Collect all agent messages after the last user prompt
	// (strip HTML — events store HTML, not markdown)
	var agentMessage strings.Builder
	for i := lastUserPromptIdx + 1; i < len(events); i++ {
		if events[i].Type == EventTypeAgentMessage {
			data, err := DecodeEventData(events[i])
			if err != nil {
				continue
			}
			if d, ok := data.(AgentMessageData); ok {
				agentMessage.WriteString(stripHTML(d.Text))
			}
		}
	}

	return agentMessage.String()
}

// GetLastUserPrompt extracts the last user prompt text from a list of events.
// Returns an empty string if no user prompt is found.
func GetLastUserPrompt(events []Event) string {
	if len(events) == 0 {
		return ""
	}

	// Find the last user_prompt
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventTypeUserPrompt {
			data, err := DecodeEventData(events[i])
			if err != nil {
				return ""
			}
			if d, ok := data.(UserPromptData); ok {
				return d.Message
			}
		}
	}

	return ""
}

// LastUserPromptInfo contains information about the last user prompt.
type LastUserPromptInfo struct {
	PromptID string // Client-generated prompt ID for delivery confirmation
	Seq      int64  // Sequence number of the prompt
	Found    bool   // Whether a user prompt was found
}

// GetLastUserPromptInfo extracts information about the last user prompt from a list of events.
// This is used to help clients determine if their pending prompt was actually delivered
// after reconnecting from a zombie WebSocket connection.
func GetLastUserPromptInfo(events []Event) LastUserPromptInfo {
	if len(events) == 0 {
		return LastUserPromptInfo{}
	}

	// Find the last user_prompt
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventTypeUserPrompt {
			data, err := DecodeEventData(events[i])
			if err != nil {
				return LastUserPromptInfo{}
			}
			if d, ok := data.(UserPromptData); ok {
				return LastUserPromptInfo{
					PromptID: d.PromptID,
					Seq:      events[i].Seq,
					Found:    true,
				}
			}
		}
	}

	return LastUserPromptInfo{}
}

// truncateText truncates text to maxLen characters, adding "..." if truncated.
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// decodeMapToStruct converts a map to the appropriate struct type using the eventDataTypes registry.
func decodeMapToStruct(eventType EventType, data interface{}) (interface{}, error) {
	// Look up the expected type for this event
	expectedType, ok := eventDataTypes[eventType]
	if !ok {
		// Unknown event type, return data as-is
		return data, nil
	}

	// Re-marshal and unmarshal to convert map to struct
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	// Create a new instance of the expected type and unmarshal into it
	resultPtr := reflect.New(expectedType).Interface()
	if err := json.Unmarshal(jsonData, resultPtr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	// Return the value (not the pointer)
	return reflect.ValueOf(resultPtr).Elem().Interface(), nil
}
