//go:build integration

package inprocess

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestWorkspacePrompts_BeadsDirGatesUseDirParamNotSession is an end-to-end
// regression test (through the real HTTP server) for the bug where beads-issue
// context-menu prompts disappeared. dir-based enabledWhen gates (dirExists) were
// evaluated against the active conversation's working dir instead of the `dir`
// query param. The frontend always appends &session_id=<activeConversation>, so
// when that conversation lived in a folder without ".beads", dirExists(".beads")
// evaluated false and every beads prompt was filtered out — an empty menu.
//
// Scenario reproduced here:
//   - Active conversation lives in the configured workspace (NO .beads).
//   - The Tasks/beads view is opened for a separate project dir (HAS .beads).
//   - GET /api/workspace-prompts?dir=<beadsDir>&session_id=<active>&item_*...
//
// Expectations (post-fix): the dir param is authoritative, so dir-gated prompts
// for beadsDir are returned even though the session's folder has no .beads. A
// negative control over a dir WITHOUT .beads proves the gate is genuinely
// evaluated (not fail-open).
func TestWorkspacePrompts_BeadsDirGatesUseDirParamNotSession(t *testing.T) {
	ts := SetupTestServer(t)

	// .mittorc shared by both dirs: a dir-gated prompt, an item-gated prompt,
	// and an ungated one.
	rcContent := `prompts:
  - name: "Decompose issue"
    prompt: "x"
    enabledWhen: 'dirExists(".beads")'
  - name: "Start work"
    prompt: "y"
    enabledWhen: 'item.status != "closed"'
  - name: "Show status"
    prompt: "z"
`

	// beadsDir: the project the Tasks view is opened for (HAS .beads).
	beadsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(beadsDir, ".beads"), 0755); err != nil {
		t.Fatalf("create .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, ".mittorc"), []byte(rcContent), 0644); err != nil {
		t.Fatalf("write beads .mittorc: %v", err)
	}

	// noBeadsDir: negative control, same prompts but NO .beads.
	noBeadsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(noBeadsDir, ".mittorc"), []byte(rcContent), 0644); err != nil {
		t.Fatalf("write no-beads .mittorc: %v", err)
	}

	// Active conversation lives in the configured workspace, which has no .beads.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "Active elsewhere"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)
	if sess.WorkingDir == beadsDir {
		t.Fatalf("precondition: active session must NOT live in beadsDir")
	}

	// fetchPrompts performs the real HTTP GET the frontend issues for the beads
	// context menu and returns the prompt names.
	fetchPrompts := func(t *testing.T, dir, sessionID, itemStatus string) []string {
		t.Helper()
		q := url.Values{}
		q.Set("dir", dir)
		q.Set("enabled_context", "workspace")
		if sessionID != "" {
			q.Set("session_id", sessionID)
		}
		q.Set("item_kind", "beadsIssue")
		q.Set("item_id", "mitto-1")
		q.Set("item_status", itemStatus)
		u := ts.HTTPServer.URL + "/mitto/api/workspace-prompts?" + q.Encode()
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET workspace-prompts: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET workspace-prompts: status %d: %s", resp.StatusCode, string(body))
		}
		var decoded struct {
			Prompts          []struct{ Name string } `json:"prompts"`
			EnabledEvaluated bool                    `json:"enabled_evaluated"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !decoded.EnabledEvaluated {
			t.Fatalf("enabled_evaluated = false, want true (gates must be applied)")
		}
		names := make([]string, 0, len(decoded.Prompts))
		for _, p := range decoded.Prompts {
			names = append(names, p.Name)
		}
		return names
	}

	has := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	// 1. dir=beadsDir (has .beads), open issue, active session in a no-.beads
	//    folder. Pre-fix this returned an empty list; post-fix all three show.
	open := fetchPrompts(t, beadsDir, sess.SessionID, "open")
	if !has(open, "Decompose issue") {
		t.Errorf("dir-gated prompt filtered out: dirExists(\".beads\") evaluated against the session's folder, not the dir param; got %v", open)
	}
	if !has(open, "Start work") {
		t.Errorf("item-gated prompt missing for open issue; got %v", open)
	}
	if !has(open, "Show status") {
		t.Errorf("ungated prompt missing; got %v", open)
	}

	// 2. Same dir/session but a closed issue: the item gate drops "Start work"
	//    while the dir-gated and ungated prompts remain.
	closed := fetchPrompts(t, beadsDir, sess.SessionID, "closed")
	if has(closed, "Start work") {
		t.Errorf("item-gated prompt should be hidden for closed issue; got %v", closed)
	}
	if !has(closed, "Decompose issue") || !has(closed, "Show status") {
		t.Errorf("dir-gated/ungated prompts must remain for closed issue; got %v", closed)
	}

	// 3. Negative control: dir without .beads. The dir gate genuinely evaluates
	//    against the dir param (not fail-open), so "Decompose issue" is hidden.
	nb := fetchPrompts(t, noBeadsDir, sess.SessionID, "open")
	if has(nb, "Decompose issue") {
		t.Errorf("dir gate is fail-open: dir-gated prompt returned for a dir without .beads; got %v", nb)
	}
	if !has(nb, "Show status") {
		t.Errorf("ungated prompt missing for no-beads dir; got %v", nb)
	}
}
