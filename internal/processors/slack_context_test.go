package processors

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCELContextCanonicalOnSlackBatchAndCompatibilityAlias(t *testing.T) {
	events := []TriggerSlackEvent{
		{InstallationID: "install-1", EventID: "event-1", ChannelID: "channel-1", Kind: "message", AuthorID: "user-1", Timestamp: "1.0", Untrusted: true, Text: "external one"},
		{InstallationID: "install-2", EventID: "event-2", ChannelID: "channel-2", Kind: "appMention", AuthorID: "user-2", Timestamp: "2.0", ThreadTimestamp: "1.0", Untrusted: true, Text: "external two"},
	}
	ctx := BuildCELContext(&ProcessorInput{SessionID: "s", TriggerOnSlackEvents: events})
	if ctx.Trigger == nil || ctx.Trigger.OnSlack == nil {
		t.Fatalf("Trigger.OnSlack = %#v", ctx.Trigger)
	}
	if len(ctx.Trigger.OnSlack.Events) != 2 || ctx.Trigger.OnSlack.Events[1].EventID != "event-2" {
		t.Errorf("Trigger.OnSlack.Events = %#v", ctx.Trigger.OnSlack.Events)
	}
	if ctx.Trigger.Slack == nil || ctx.Trigger.Slack.EventID != "event-1" {
		t.Errorf("legacy Trigger.Slack alias = %#v, want first event", ctx.Trigger.Slack)
	}
	if !ctx.Trigger.OnSlack.Events[0].Untrusted {
		t.Error("canonical event lost Untrusted provenance")
	}
}

func TestProcessorInputDoesNotSerializeSlackEventContent(t *testing.T) {
	in := ProcessorInput{
		SessionID:            "s",
		TriggerOnSlackEvents: []TriggerSlackEvent{{EventID: "private-event-id", Text: "untrusted-private-text"}},
		TriggerSlackEvent:    &TriggerSlackEvent{EventID: "legacy-private-event-id", Text: "legacy-private-text"},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	body := string(data)
	for _, forbidden := range []string{"private-event-id", "untrusted-private-text", "legacy-private"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("external processor JSON leaked Slack trigger content %q: %s", forbidden, body)
		}
	}
}
