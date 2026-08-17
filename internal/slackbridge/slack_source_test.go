package slackbridge

import (
	"encoding/json"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// newTestSocketmodeClient builds a socketmode.Client with no real network
// connection. Ack() only enqueues onto an internal buffered channel
// (capacity 20) that this test never drains, which is safe for a single Ack.
func newTestSocketmodeClient() *socketmode.Client {
	return socketmode.New(slack.New("xoxb-fake-test-token"))
}

func eventsAPIEvent(teamID string, innerType string, data any) socketmode.Event {
	payload, _ := json.Marshal(map[string]string{"event_id": "Ev1"})
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			TeamID:     teamID,
			InnerEvent: slackevents.EventsAPIInnerEvent{Type: innerType, Data: data},
		},
		Request: &socketmode.Request{Payload: payload},
	}
}

func TestSlackSource_HandleSocketEvent_PlainMessage_Emitted(t *testing.T) {
	src := NewSlackSource(Config{}, nil, nil)
	client := newTestSocketmodeClient()

	evt := eventsAPIEvent("T1", "message", &slackevents.MessageEvent{
		Channel: "C1", User: "U1", Text: "hello", TimeStamp: "100.1", ThreadTimeStamp: "99.1",
	})

	var got []Event
	src.handleSocketEvent(evt, client, "U-SELF", func(e Event) { got = append(got, e) })

	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1", len(got))
	}
	e := got[0]
	if e.EventID != "Ev1" || e.TeamID != "T1" || e.ChannelID != "C1" || e.AuthorID != "U1" ||
		e.Kind != "message" || e.Timestamp != "100.1" || e.ThreadTimestamp != "99.1" || e.Text != "hello" {
		t.Errorf("event = %#v, unexpected field values", e)
	}
}

func TestSlackSource_HandleSocketEvent_SelfMessage_Ignored(t *testing.T) {
	src := NewSlackSource(Config{}, nil, nil)
	client := newTestSocketmodeClient()

	evt := eventsAPIEvent("T1", "message", &slackevents.MessageEvent{Channel: "C1", User: "U-SELF", Text: "hi"})

	var got []Event
	src.handleSocketEvent(evt, client, "U-SELF", func(e Event) { got = append(got, e) })
	if len(got) != 0 {
		t.Errorf("emitted %d events, want 0 for a self-authored message", len(got))
	}
}

func TestSlackSource_HandleSocketEvent_BotMessage_Ignored(t *testing.T) {
	src := NewSlackSource(Config{}, nil, nil)
	client := newTestSocketmodeClient()

	evt := eventsAPIEvent("T1", "message", &slackevents.MessageEvent{Channel: "C1", User: "U1", BotID: "B1", Text: "hi"})

	var got []Event
	src.handleSocketEvent(evt, client, "U-SELF", func(e Event) { got = append(got, e) })
	if len(got) != 0 {
		t.Errorf("emitted %d events, want 0 for a bot message", len(got))
	}
}

func TestSlackSource_HandleSocketEvent_MessageSubtype_Ignored(t *testing.T) {
	src := NewSlackSource(Config{}, nil, nil)
	client := newTestSocketmodeClient()

	evt := eventsAPIEvent("T1", "message", &slackevents.MessageEvent{Channel: "C1", User: "U1", SubType: "message_changed", Text: "hi"})

	var got []Event
	src.handleSocketEvent(evt, client, "U-SELF", func(e Event) { got = append(got, e) })
	if len(got) != 0 {
		t.Errorf("emitted %d events, want 0 for a message subtype (edits etc.)", len(got))
	}
}

func TestSlackSource_HandleSocketEvent_AppMention_Emitted(t *testing.T) {
	src := NewSlackSource(Config{}, nil, nil)
	client := newTestSocketmodeClient()

	evt := eventsAPIEvent("T1", "app_mention", &slackevents.AppMentionEvent{
		Channel: "C1", User: "U1", Text: "<@BOT> help", TimeStamp: "200.1",
	})

	var got []Event
	src.handleSocketEvent(evt, client, "U-SELF", func(e Event) { got = append(got, e) })
	if len(got) != 1 || got[0].Kind != "app_mention" {
		t.Errorf("got = %#v, want a single app_mention event", got)
	}
}

func TestExtractEventID(t *testing.T) {
	if id := extractEventID(nil); id != "" {
		t.Errorf("extractEventID(nil) = %q, want empty", id)
	}
	req := &socketmode.Request{Payload: json.RawMessage(`{"event_id":"EvXYZ","team_id":"T1"}`)}
	if id := extractEventID(req); id != "EvXYZ" {
		t.Errorf("extractEventID() = %q, want EvXYZ", id)
	}
}
