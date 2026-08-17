package prompts

import (
	"strings"
	"testing"
	"time"
)

func TestParsePromptFileOnSlackDefaults(t *testing.T) {
	data := []byte(`name: "Slack loop"
loop:
  trigger: [onSlack, onCompletion]
  onSlack:
    eventMode: appMention
    threadPolicy: repliesOnly
  onCompletion:
    delay: 30
prompt: inspect Slack events
`)
	p, err := ParsePromptFile("slack.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile() error = %v", err)
	}
	if p.Loop == nil || p.Loop.SlackEventMode() != "appMention" || p.Loop.SlackThreadPolicy() != "repliesOnly" {
		t.Fatalf("Loop.OnSlack = %#v, want authored behavior defaults", p.Loop)
	}
	if got := p.Loop.Triggers(); len(got) != 2 || got[0] != "onSlack" || got[1] != "onCompletion" {
		t.Errorf("Triggers() = %v", got)
	}

	sole := []byte("name: sole\nloop:\n  trigger: [onSlack]\nprompt: inspect\n")
	if _, err := ParsePromptFile("sole.prompt.yaml", sole, time.Now()); err != nil {
		t.Fatalf("onSlack sole trigger should parse: %v", err)
	}
}

func TestParsePromptFileOnSlackRejectsInvalidOrDeploymentSpecificFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid event mode", "eventMode: bots", "eventMode"},
		{"invalid thread policy", "threadPolicy: rootsAndBots", "threadPolicy"},
		{"installation reference", "installationID: install-1", "installationID"},
		{"credential", "token: xoxb-redacted", "token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("name: bad\nloop:\n  trigger: [onSlack]\n  onSlack:\n    " + tc.body + "\nprompt: inspect\n")
			_, err := ParsePromptFile("bad.prompt.yaml", data, time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParsePromptFile() error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}
