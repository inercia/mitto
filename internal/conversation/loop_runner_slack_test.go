package conversation

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundSlackEventsEnforcesCountBytesAndUntrustedProvenance(t *testing.T) {
	events := make([]PromptSlackEvent, MaxSlackEventsPerDispatch+5)
	for i := range events {
		events[i] = PromptSlackEvent{
			InstallationID: "install", EventID: strings.Repeat("e", 600), ChannelID: "channel",
			Kind: "message", AuthorID: "author", Timestamp: "123.456", Text: strings.Repeat("🙂", 3000),
		}
	}
	bounded := boundSlackEvents(events)
	if len(bounded) > MaxSlackEventsPerDispatch {
		t.Fatalf("len(boundSlackEvents) = %d, max %d", len(bounded), MaxSlackEventsPerDispatch)
	}
	total := 0
	for i, event := range bounded {
		if !event.Untrusted {
			t.Errorf("event %d Untrusted = false", i)
		}
		if len(event.EventID) > 512 || len(event.Kind) > 64 {
			t.Errorf("event %d metadata was not bounded: %#v", i, event)
		}
		if !utf8.ValidString(event.Text) {
			t.Errorf("event %d text is invalid UTF-8", i)
		}
		total += len(event.InstallationID) + len(event.EventID) + len(event.ChannelID) + len(event.Kind) + len(event.AuthorID) + len(event.Timestamp) + len(event.ThreadTimestamp) + len(event.Text)
	}
	if total > MaxSlackEventBatchBytes {
		t.Errorf("bounded batch uses %d bytes, max %d", total, MaxSlackEventBatchBytes)
	}
	if events[0].Untrusted {
		t.Error("boundSlackEvents mutated caller-owned input")
	}
}

func TestPromptSlackEventCredentialFreeShape(t *testing.T) {
	typ := reflect.TypeOf(PromptSlackEvent{})
	want := map[string]bool{
		"InstallationID": true, "EventID": true, "ChannelID": true, "Kind": true,
		"AuthorID": true, "Timestamp": true, "ThreadTimestamp": true, "Untrusted": true, "Text": true,
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !want[name] {
			t.Errorf("unexpected PromptSlackEvent field %q; normalized DTO must stay credential/SDK-free", name)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Errorf("PromptSlackEvent missing canonical fields: %v", want)
	}
}
