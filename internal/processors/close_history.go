package processors

import (
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/inercia/mitto/internal/session"
)

const (
	closeHistoryMaxEvents = 50
	closeHistoryMaxText   = 2048
)

type closeHistorySnapshot struct {
	ConversationID string              `json:"conversation_id"`
	CapturedAt     string              `json:"captured_at"`
	TotalEvents    int                 `json:"total_events"`
	FilteredEvents int                 `json:"filtered_events"`
	ReturnedEvents int                 `json:"returned_events"`
	Events         []closeHistoryEvent `json:"events"`
}

type closeHistoryEvent struct {
	Seq       int64  `json:"seq"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

// BuildCloseHistorySnapshot captures the same source event classes used by the
// builtin close processors: the most recent 50 user prompts and agent messages,
// with each text field bounded to the conversation-history tool's 2 KiB limit.
func BuildCloseHistorySnapshot(sessionID string, events []session.Event) (string, error) {
	filtered := make([]closeHistoryEvent, 0, len(events))
	for _, event := range events {
		data, err := session.DecodeEventData(event)
		if err != nil {
			continue
		}
		var text string
		switch event.Type {
		case session.EventTypeUserPrompt:
			if prompt, ok := data.(session.UserPromptData); ok {
				text = prompt.Message
			}
		case session.EventTypeAgentMessage:
			if message, ok := data.(session.AgentMessageData); ok {
				text = message.Markdown
				if text == "" {
					text = session.StripHTML(message.Text)
				}
			}
		default:
			continue
		}
		filtered = append(filtered, closeHistoryEvent{
			Seq: event.Seq, Type: string(event.Type),
			Timestamp: event.Timestamp.Format(time.RFC3339Nano),
			Text:      truncateCloseHistoryText(text),
		})
	}

	filteredCount := len(filtered)
	if filteredCount > closeHistoryMaxEvents {
		filtered = filtered[filteredCount-closeHistoryMaxEvents:]
	}
	snapshot := closeHistorySnapshot{
		ConversationID: sessionID,
		CapturedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		TotalEvents:    len(events),
		FilteredEvents: filteredCount,
		ReturnedEvents: len(filtered),
		Events:         filtered,
	}
	return marshalCloseHistorySnapshot(snapshot)
}

func marshalCloseHistorySnapshot(snapshot closeHistorySnapshot) (string, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func truncateCloseHistoryText(text string) string {
	if len(text) <= closeHistoryMaxText {
		return text
	}
	cut := closeHistoryMaxText - 3
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}
