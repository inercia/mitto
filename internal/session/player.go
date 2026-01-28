package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

// SessionID returns the session ID.
func (p *Player) SessionID() string {
	return p.sessionID
}

// Metadata returns the session metadata.
func (p *Player) Metadata() (Metadata, error) {
	return p.store.GetMetadata(p.sessionID)
}

// Events returns all events in the session.
func (p *Player) Events() []Event {
	return p.events
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
func DecodeEventData(event Event) (interface{}, error) {
	// If data is already the correct type, return it
	switch event.Type {
	case EventTypeUserPrompt:
		if data, ok := event.Data.(UserPromptData); ok {
			return data, nil
		}
	case EventTypeAgentMessage:
		if data, ok := event.Data.(AgentMessageData); ok {
			return data, nil
		}
	case EventTypeAgentThought:
		if data, ok := event.Data.(AgentThoughtData); ok {
			return data, nil
		}
	case EventTypeToolCall:
		if data, ok := event.Data.(ToolCallData); ok {
			return data, nil
		}
	case EventTypeToolCallUpdate:
		if data, ok := event.Data.(ToolCallUpdateData); ok {
			return data, nil
		}
	case EventTypePlan:
		if data, ok := event.Data.(PlanData); ok {
			return data, nil
		}
	case EventTypePermission:
		if data, ok := event.Data.(PermissionData); ok {
			return data, nil
		}
	case EventTypeFileRead, EventTypeFileWrite:
		if data, ok := event.Data.(FileOperationData); ok {
			return data, nil
		}
	case EventTypeError:
		if data, ok := event.Data.(ErrorData); ok {
			return data, nil
		}
	case EventTypeSessionStart:
		if data, ok := event.Data.(SessionStartData); ok {
			return data, nil
		}
	case EventTypeSessionEnd:
		if data, ok := event.Data.(SessionEndData); ok {
			return data, nil
		}
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

// truncateText truncates text to maxLen characters, adding "..." if truncated.
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// decodeMapToStruct converts a map to the appropriate struct type.
func decodeMapToStruct(eventType EventType, data interface{}) (interface{}, error) {
	// Re-marshal and unmarshal to convert map to struct
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	var result interface{}
	switch eventType {
	case EventTypeUserPrompt:
		var d UserPromptData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeAgentMessage:
		var d AgentMessageData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeAgentThought:
		var d AgentThoughtData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeToolCall:
		var d ToolCallData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeToolCallUpdate:
		var d ToolCallUpdateData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypePlan:
		var d PlanData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypePermission:
		var d PermissionData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeFileRead, EventTypeFileWrite:
		var d FileOperationData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeError:
		var d ErrorData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeSessionStart:
		var d SessionStartData
		err = json.Unmarshal(jsonData, &d)
		result = d
	case EventTypeSessionEnd:
		var d SessionEndData
		err = json.Unmarshal(jsonData, &d)
		result = d
	default:
		return data, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}
	return result, nil
}
