package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSupportPhaseHandoffContract(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	load := func(file string) *PromptFile {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin/support", file))
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := ParsePromptFile(file, data, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return prompt
	}

	watch := load("watch-channel.prompt.yaml")
	research := load("investigate.prompt.yaml")
	compose := load("compose-draft.prompt.yaml")
	review := load("reply-to-user.prompt.yaml")

	for _, text := range []string{"channel history", "thread replies", "slack_thread_ts", "Support: investigate", "Can Send Prompt"} {
		if !strings.Contains(strings.ToLower(watch.Content), strings.ToLower(text)) {
			t.Errorf("watcher missing discovery, reconciliation, or dispatch contract %q", text)
		}
	}
	for _, text := range []string{"research only", "no DRAFT", "`inconclusive`", "customer changed the ask", "thread fetch for the bead succeeded", "no queued prompts", "an unchanged thread leaves those", "states untouched", "reused <child-id>", "deferred: dispatch failed"} {
		if !strings.Contains(watch.Content, text) {
			t.Errorf("watcher missing phase or safe-recovery guard %q", text)
		}
	}
	for _, text := range []string{"mitto_conversation_send_prompt", "Support: compose draft", "state:gathering-info", "mitto_ui_notify"} {
		if !strings.Contains(research.Content, text) {
			t.Errorf("investigation missing phase handoff %q", text)
		}
	}
	if strings.Contains(research.Content, "bd comment <id> --stdin") && strings.Contains(research.Content, "--- BEGIN REPLY ---") {
		t.Error("investigation must not author a reply draft before the separate composition turn")
	}
	for _, text := range []string{"--- BEGIN REPLY ---", "--- END REPLY ---", "state:drafting", "Support: reply to user", "slack_thread_ts"} {
		if !strings.Contains(compose.Content, text) {
			t.Errorf("composition missing draft or review boundary %q", text)
		}
	}
	for _, text := range []string{"any unposted DRAFT", "do not", "thread cannot be read", "state:need-info"} {
		if !strings.Contains(compose.Content, text) {
			t.Errorf("composition missing stale/draft safety contract %q", text)
		}
	}
	if !strings.Contains(review.Content, "explicit user review and approval") {
		t.Error("reply-to-user must retain explicit human approval before posting")
	}
}
