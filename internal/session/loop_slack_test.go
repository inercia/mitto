package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoopPromptOnSlackValidation(t *testing.T) {
	valid := SlackSubscription{InstallationID: "install-1", ChannelID: "channel-1"}

	t.Run("sole trigger with subscription", func(t *testing.T) {
		p := &LoopPrompt{Prompt: "inspect", Enabled: true, Triggers: []LoopTrigger{TriggerOnSlack}, SlackSubscriptions: []SlackSubscription{valid}}
		p.Normalize()
		if err := p.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		if p.Trigger != TriggerOnSlack {
			t.Errorf("legacy Trigger = %q, want %q", p.Trigger, TriggerOnSlack)
		}
	})

	t.Run("enabled requires subscription but disabled draft does not", func(t *testing.T) {
		enabled := &LoopPrompt{Prompt: "inspect", Enabled: true, Triggers: []LoopTrigger{TriggerOnSlack}}
		enabled.Normalize()
		if err := enabled.Validate(); !errors.Is(err, ErrSlackSubscriptionsRequired) {
			t.Fatalf("Validate() error = %v, want ErrSlackSubscriptionsRequired", err)
		}
		draft := &LoopPrompt{Enabled: false, Triggers: []LoopTrigger{TriggerOnSlack}}
		draft.Normalize()
		if err := draft.Validate(); err != nil {
			t.Fatalf("disabled draft Validate() error = %v", err)
		}
	})

	t.Run("invalid and duplicate subscriptions", func(t *testing.T) {
		cases := []struct {
			name string
			subs []SlackSubscription
		}{
			{"missing installation", []SlackSubscription{{ChannelID: "channel-1"}}},
			{"invalid mode", []SlackSubscription{{InstallationID: "install-1", ChannelID: "channel-1", EventMode: "bots"}}},
			{"invalid thread policy", []SlackSubscription{{InstallationID: "install-1", ChannelID: "channel-1", ThreadPolicy: "rootsAndBots"}}},
			{"duplicate reference", []SlackSubscription{valid, valid}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				p := &LoopPrompt{Enabled: false, Triggers: []LoopTrigger{TriggerOnSlack}, SlackSubscriptions: tc.subs}
				p.Normalize()
				if err := p.Validate(); !errors.Is(err, ErrInvalidSlackSubscription) {
					t.Fatalf("Validate() error = %v, want ErrInvalidSlackSubscription", err)
				}
			})
		}
	})
}

func TestLoopStoreOnSlackCanonicalPersistenceAndPatchSemantics(t *testing.T) {
	store := NewLoopStore(t.TempDir())
	p := &LoopPrompt{
		Prompt:   "inspect",
		Enabled:  true,
		Triggers: []LoopTrigger{TriggerOnSlack, TriggerOnCompletion},
		SlackSubscriptions: []SlackSubscription{
			{InstallationID: " z-install ", ChannelID: " c-two ", EventMode: SlackEventModeAppMention, ThreadPolicy: SlackThreadPolicyRepliesOnly},
			{InstallationID: "a-install", ChannelID: "c-one"},
		},
	}
	if err := store.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Trigger != TriggerOnSlack || len(got.Triggers) != 2 {
		t.Errorf("trigger compatibility shape = (%q, %v), want onSlack primary and two triggers", got.Trigger, got.Triggers)
	}
	if len(got.SlackSubscriptions) != 2 || got.SlackSubscriptions[0].InstallationID != "a-install" || got.SlackSubscriptions[1].InstallationID != "z-install" {
		t.Fatalf("SlackSubscriptions = %#v, want canonical installation order", got.SlackSubscriptions)
	}
	if got.SlackSubscriptions[0].EventMode != SlackEventModeAnyHumanMessage || got.SlackSubscriptions[0].ThreadPolicy != SlackThreadPolicyAny {
		t.Errorf("defaulted subscription = %#v", got.SlackSubscriptions[0])
	}

	newPrompt := "inspect again"
	if err := store.Update(LoopUpdate{Prompt: &newPrompt}); err != nil {
		t.Fatalf("unrelated Update() error = %v", err)
	}
	unchanged, _ := store.Get()
	if len(unchanged.SlackSubscriptions) != 2 {
		t.Fatalf("nil SlackSubscriptions update changed list: %#v", unchanged.SlackSubscriptions)
	}

	disabled := false
	empty := []SlackSubscription{}
	if err := store.Update(LoopUpdate{Enabled: &disabled, SlackSubscriptions: &empty}); err != nil {
		t.Fatalf("clear while disabling Update() error = %v", err)
	}
	cleared, _ := store.Get()
	if len(cleared.SlackSubscriptions) != 0 {
		t.Errorf("SlackSubscriptions after explicit clear = %#v, want empty", cleared.SlackSubscriptions)
	}
}

func TestLoopStoreRemoveSlackSubscriptions(t *testing.T) {
	t.Run("preserves unrelated subscriptions and triggers", func(t *testing.T) {
		store := NewLoopStore(t.TempDir())
		if err := store.Set(&LoopPrompt{
			Prompt:   "inspect",
			Enabled:  true,
			Triggers: []LoopTrigger{TriggerOnCompletion, TriggerOnSlack},
			SlackSubscriptions: []SlackSubscription{
				{InstallationID: "remove", ChannelID: "one"},
				{InstallationID: "keep", ChannelID: "two"},
			},
		}); err != nil {
			t.Fatal(err)
		}
		updated, changed, err := store.RemoveSlackSubscriptions([]string{"remove"})
		if err != nil || !changed {
			t.Fatalf("RemoveSlackSubscriptions() changed=%v err=%v", changed, err)
		}
		if len(updated.SlackSubscriptions) != 1 || updated.SlackSubscriptions[0].InstallationID != "keep" || !updated.IsOnSlack() || !updated.IsOnCompletion() {
			t.Fatalf("updated loop = %#v", updated)
		}
	})

	t.Run("removes onSlack but preserves another trigger", func(t *testing.T) {
		store := NewLoopStore(t.TempDir())
		if err := store.Set(&LoopPrompt{Prompt: "inspect", Enabled: true,
			Triggers:           []LoopTrigger{TriggerOnSlack, TriggerOnCompletion},
			SlackSubscriptions: []SlackSubscription{{InstallationID: "remove", ChannelID: "one"}},
		}); err != nil {
			t.Fatal(err)
		}
		updated, changed, err := store.RemoveSlackSubscriptions([]string{"remove"})
		if err != nil || !changed || updated == nil {
			t.Fatalf("RemoveSlackSubscriptions() updated=%#v changed=%v err=%v", updated, changed, err)
		}
		if updated.IsOnSlack() || !updated.IsOnCompletion() || len(updated.SlackSubscriptions) != 0 {
			t.Fatalf("updated loop = %#v", updated)
		}
	})

	t.Run("sole onSlack trigger becomes a regular conversation", func(t *testing.T) {
		store := NewLoopStore(t.TempDir())
		if err := store.Set(&LoopPrompt{Prompt: "inspect", Enabled: true,
			Triggers:           []LoopTrigger{TriggerOnSlack},
			SlackSubscriptions: []SlackSubscription{{InstallationID: "remove", ChannelID: "one"}},
		}); err != nil {
			t.Fatal(err)
		}
		updated, changed, err := store.RemoveSlackSubscriptions([]string{"remove"})
		if err != nil || !changed || updated != nil {
			t.Fatalf("RemoveSlackSubscriptions() updated=%#v changed=%v err=%v", updated, changed, err)
		}
		if _, err := store.Get(); !errors.Is(err, ErrLoopNotFound) {
			t.Fatalf("Get() error = %v, want ErrLoopNotFound", err)
		}
	})

	t.Run("preserves unknown triggers and fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, loopFileName)
		raw := `{"prompt":"future","enabled":true,"triggers":["onSlack","onWebhook"],"slack_subscriptions":[{"installation_id":"remove","channel_id":"c"}],"future_delivery":{"mode":"durable"}}`
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		updated, changed, err := NewLoopStore(dir).RemoveSlackSubscriptions([]string{"remove"})
		if err != nil || !changed || updated == nil || len(updated.Triggers) != 1 || updated.Triggers[0] != "onWebhook" {
			t.Fatalf("RemoveSlackSubscriptions() updated=%#v changed=%v err=%v", updated, changed, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"future_delivery"`) {
			t.Fatalf("future field was lost: %s", data)
		}
	})
}

func TestLoopStorePartialUpdatePreservesUnknownTriggerAndFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, loopFileName)
	raw := `{"prompt":"future","enabled":false,"trigger":"onSlack","triggers":["onSlack","onWebhook"],"slack_subscriptions":[{"installation_id":"i","channel_id":"c","event_mode":"anyHumanMessage","thread_policy":"any"}],"future_delivery":{"mode":"durable"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := NewLoopStore(dir)
	updatedPrompt := "future updated"
	if err := store.Update(LoopUpdate{Prompt: &updatedPrompt}); err != nil {
		t.Fatalf("partial Update() rejected preserved future shape: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"onWebhook"`) || !strings.Contains(string(data), `"future_delivery"`) {
		t.Errorf("partial update lost future trigger/field: %s", data)
	}

	badTriggers := []LoopTrigger{TriggerOnSlack, "onWebhook"}
	if err := store.Update(LoopUpdate{Triggers: &badTriggers}); !errors.Is(err, ErrInvalidTrigger) {
		t.Fatalf("explicit future trigger replacement error = %v, want ErrInvalidTrigger", err)
	}

	// A full replacement intentionally drops fields unknown to this binary.
	replacement := &LoopPrompt{Prompt: "known", Enabled: false, Triggers: []LoopTrigger{TriggerOnSlack}}
	if err := store.Set(replacement); err != nil {
		t.Fatalf("Set(replacement) error = %v", err)
	}
	data, _ = os.ReadFile(path)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("Unmarshal persisted replacement: %v", err)
	}
	if _, ok := obj["future_delivery"]; ok {
		t.Errorf("full replacement retained unknown field unexpectedly: %s", data)
	}
}
