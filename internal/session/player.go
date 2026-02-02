package session

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

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
}

// Player provides session playback functionality.
type Player struct {
	store     *Store
	sessionID string
	events    []Event
	position  int
}

// NewPlayer creates a new session player.
func NewPlayer(store *Store, sessionID string) (*Player, error) {
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read events: %w", err)
	}

	return &Player{
		store:     store,
		sessionID: sessionID,
		events:    events,
		position:  0,
	}, nil
}

// EventCount returns the total number of events.
func (p *Player) EventCount() int {
	return len(p.events)
}

// Position returns the current playback position.
func (p *Player) Position() int {
	return p.position
}

// HasNext returns true if there are more events to play.
func (p *Player) HasNext() bool {
	return p.position < len(p.events)
}

// Next returns the next event and advances the position.
func (p *Player) Next() (Event, bool) {
	if !p.HasNext() {
		return Event{}, false
	}
	event := p.events[p.position]
	p.position++
	return event, true
}

// Peek returns the next event without advancing the position.
func (p *Player) Peek() (Event, bool) {
	if !p.HasNext() {
		return Event{}, false
	}
	return p.events[p.position], true
}

// Reset resets the playback position to the beginning.
func (p *Player) Reset() {
	p.position = 0
}

// Seek sets the playback position to a specific index.
func (p *Player) Seek(position int) error {
	if position < 0 || position > len(p.events) {
		return fmt.Errorf("position out of range: %d (max: %d)", position, len(p.events))
	}
	p.position = position
	return nil
}

// EventsOfType returns all events of a specific type.
func (p *Player) EventsOfType(eventType EventType) []Event {
	var result []Event
	for _, event := range p.events {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
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
	if len(events) == 0 {
		return ""
	}

	// Extract conversation turns (user prompt + agent response pairs)
	type turn struct {
		userMessage  string
		agentMessage string
	}
	var turns []turn
	var currentTurn turn

	for _, event := range events {
		data, err := DecodeEventData(event)
		if err != nil {
			continue
		}

		switch event.Type {
		case EventTypeUserPrompt:
			// Start a new turn
			if currentTurn.userMessage != "" {
				turns = append(turns, currentTurn)
			}
			if d, ok := data.(UserPromptData); ok {
				currentTurn = turn{userMessage: d.Message}
			}
		case EventTypeAgentMessage:
			// Add to current turn
			if d, ok := data.(AgentMessageData); ok {
				currentTurn.agentMessage += d.Text
			}
		}
	}

	// Don't forget the last turn
	if currentTurn.userMessage != "" {
		turns = append(turns, currentTurn)
	}

	if len(turns) == 0 {
		return ""
	}

	// Limit to most recent turns
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}

	// Build the history text
	var sb strings.Builder
	sb.WriteString("[CONVERSATION HISTORY - This is a resumed session. Previous context:]\n\n")

	for i, t := range turns {
		// Truncate very long messages
		userMsg := truncateText(t.userMessage, 500)
		agentMsg := truncateText(t.agentMessage, 1000)

		sb.WriteString(fmt.Sprintf("--- Turn %d ---\n", i+1))
		sb.WriteString(fmt.Sprintf("USER: %s\n\n", userMsg))
		if agentMsg != "" {
			sb.WriteString(fmt.Sprintf("ASSISTANT: %s\n\n", agentMsg))
		}
	}

	sb.WriteString("[END OF HISTORY - Continue the conversation:]\n\n")
	return sb.String()
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
	var agentMessage strings.Builder
	for i := lastUserPromptIdx + 1; i < len(events); i++ {
		if events[i].Type == EventTypeAgentMessage {
			data, err := DecodeEventData(events[i])
			if err != nil {
				continue
			}
			if d, ok := data.(AgentMessageData); ok {
				agentMessage.WriteString(d.Text)
			}
		}
	}

	return agentMessage.String()
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
