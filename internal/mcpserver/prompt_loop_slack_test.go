package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestParseLoopTriggerListAcceptsOnSlack(t *testing.T) {
	got, err := parseLoopTriggerList("onSlack,onTasks")
	if err != nil {
		t.Fatalf("parseLoopTriggerList() error = %v", err)
	}
	if len(got) != 2 || got[0] != session.TriggerOnSlack || got[1] != session.TriggerOnTasks {
		t.Errorf("parseLoopTriggerList() = %v", got)
	}
}

func TestApplyPromptLoopSlackDefaultsToMCPInputs(t *testing.T) {
	pl := &config.PromptLoop{
		Trigger: []string{"onSlack"},
		OnSlack: &config.PromptLoopOnSlack{EventMode: "appMention", ThreadPolicy: "repliesOnly"},
	}

	t.Run("start input fills only missing filters", func(t *testing.T) {
		in := &ConversationStartInput{LoopSlackSubscriptions: []session.SlackSubscription{
			{InstallationID: "i1", ChannelID: "c1"},
			{InstallationID: "i2", ChannelID: "c2", EventMode: session.SlackEventModeAnyHumanMessage, ThreadPolicy: session.SlackThreadPolicyRootOnly},
		}}
		applyPromptLoopDefaultsToStartInput(in, pl, "seed")
		if in.LoopTrigger != "onSlack" {
			t.Errorf("LoopTrigger = %q", in.LoopTrigger)
		}
		if in.LoopSlackSubscriptions[0].EventMode != session.SlackEventModeAppMention || in.LoopSlackSubscriptions[0].ThreadPolicy != session.SlackThreadPolicyRepliesOnly {
			t.Errorf("defaulted subscription = %#v", in.LoopSlackSubscriptions[0])
		}
		if in.LoopSlackSubscriptions[1].EventMode != session.SlackEventModeAnyHumanMessage || in.LoopSlackSubscriptions[1].ThreadPolicy != session.SlackThreadPolicyRootOnly {
			t.Errorf("explicit caller filters were overwritten: %#v", in.LoopSlackSubscriptions[1])
		}
	})

	t.Run("update input mirrors start semantics", func(t *testing.T) {
		in := &ConversationUpdateInput{LoopSlackSubscriptions: []session.SlackSubscription{{InstallationID: "i", ChannelID: "c"}}}
		applyPromptLoopDefaultsToUpdateInput(in, pl)
		if in.LoopTrigger == nil || *in.LoopTrigger != "onSlack" {
			t.Errorf("LoopTrigger = %v", in.LoopTrigger)
		}
		if in.LoopSlackSubscriptions[0].EventMode != session.SlackEventModeAppMention || in.LoopSlackSubscriptions[0].ThreadPolicy != session.SlackThreadPolicyRepliesOnly {
			t.Errorf("defaulted subscription = %#v", in.LoopSlackSubscriptions[0])
		}
	})
}

func TestMCPOnSlackJSONSurfaceIsCredentialFree(t *testing.T) {
	in := ConversationUpdateInput{
		ConversationID: "s1",
		LoopSlackSubscriptions: []session.SlackSubscription{{
			InstallationID: "install-1", ChannelID: "channel-1",
		}},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"loop_slack_subscriptions"`) || !strings.Contains(body, `"installation_id":"install-1"`) {
		t.Errorf("MCP JSON missing canonical Slack fields: %s", body)
	}
	for _, forbidden := range []string{"token", "secret", "credential"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("MCP JSON contains forbidden credential-shaped field %q: %s", forbidden, body)
		}
	}
}
