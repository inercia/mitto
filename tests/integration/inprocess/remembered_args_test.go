//go:build integration

package inprocess

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/web"
)

// TestRememberedArgs_EnqueueThenGET is the end-to-end regression test for the
// per-argument "remember last value" feature (mitto-x8v). It:
//  1. Registers a workspace prompt with two parameters — one remember:folder,
//     one without.
//  2. Enqueues that prompt via POST /api/sessions/{id}/queue with both args.
//  3. GETs /api/workspace-prompts/remembered-args and asserts only the
//     remember:folder arg is returned, at the value we just submitted.
//  4. Enqueues a second time with a new value and asserts the value updates.
func TestRememberedArgs_EnqueueThenGET(t *testing.T) {
	trueP := true
	remPrompt := config.WebPrompt{
		Name:   "commit",
		Prompt: "commit {{ .Args.Msg }}",
		Parameters: []config.PromptParameter{
			{Name: "Msg", Type: "text", Remember: prompts.RememberFolder, Required: &trueP},
			{Name: "Secret", Type: "text"},
		},
	}
	ts := SetupTestServer(t, func(cfg *web.Config) {
		if cfg.MittoConfig == nil {
			cfg.MittoConfig = &config.Config{}
		}
		cfg.MittoConfig.Prompts = append(cfg.MittoConfig.Prompts, remPrompt)
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "remember-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Step 1: enqueue with args.
	if _, err := ts.Client.AddToQueueNamedWithArgs(sess.SessionID, "commit",
		map[string]string{"Msg": "first commit", "Secret": "hunter2"}); err != nil {
		t.Fatalf("AddToQueueNamedWithArgs: %v", err)
	}

	// Step 2: GET remembered args.
	got := fetchRememberedArgs(t, ts, sess.WorkingDir, "commit")
	if got["Msg"] != "first commit" {
		t.Errorf("after first enqueue: Msg = %q, want %q", got["Msg"], "first commit")
	}
	if _, has := got["Secret"]; has {
		t.Errorf("Secret should NOT be remembered (no remember:folder), got %v", got)
	}

	// Step 3: enqueue again with a new value.
	if _, err := ts.Client.AddToQueueNamedWithArgs(sess.SessionID, "commit",
		map[string]string{"Msg": "second commit"}); err != nil {
		t.Fatalf("AddToQueueNamedWithArgs (2nd): %v", err)
	}
	got = fetchRememberedArgs(t, ts, sess.WorkingDir, "commit")
	if got["Msg"] != "second commit" {
		t.Errorf("after second enqueue: Msg = %q, want %q", got["Msg"], "second commit")
	}

	// Step 4: unknown prompt returns empty (still 200).
	got = fetchRememberedArgs(t, ts, sess.WorkingDir, "no-such-prompt")
	if len(got) != 0 {
		t.Errorf("unknown prompt should return empty, got %v", got)
	}
}

// fetchRememberedArgs performs a real HTTP GET to /api/workspace-prompts/
// remembered-args and returns the `arguments` map. Fails the test on any
// non-200 response.
func fetchRememberedArgs(t *testing.T, ts *TestServer, workingDir, promptName string) map[string]string {
	t.Helper()
	q := url.Values{}
	q.Set("working_dir", workingDir)
	q.Set("prompt", promptName)
	u := ts.HTTPServer.URL + "/mitto/api/workspace-prompts/remembered-args?" + q.Encode()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET remembered-args: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET remembered-args status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode remembered-args: %v", err)
	}
	if out.Arguments == nil {
		return map[string]string{}
	}
	return out.Arguments
}
