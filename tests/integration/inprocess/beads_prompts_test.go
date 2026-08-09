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
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// TestWorkspacePrompts_BeadsDirGatesUseDirParamNotSession is an end-to-end
// regression test (through the real HTTP server) for the bug where beads-issue
// context-menu prompts disappeared. dir-based enabledWhen gates (DirExists) were
// evaluated against the active conversation's working dir instead of the
// `working_dir` query param. The frontend always appends &session_id=<activeConversation>,
// so when that conversation lived in a folder without ".beads", DirExists(".beads")
// evaluated false and every beads prompt was filtered out — an empty menu.
//
// Scenario reproduced here:
//   - Active conversation lives in the configured workspace (NO .beads).
//   - The Tasks/beads view is opened for a separate project dir (HAS .beads).
//   - GET /api/workspace-prompts?working_dir=<beadsDir>&session_id=<active>&item_*...
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
    enabledWhen: 'DirExists(".beads")'
  - name: "Start work"
    prompt: "y"
    enabledWhen: 'Item.Status != "closed"'
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
		q.Set("working_dir", dir)
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
		t.Errorf("dir-gated prompt filtered out: DirExists(\".beads\") evaluated against the session's folder, not the working_dir param; got %v", open)
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

// TestWorkspacePrompts_BuiltinDeployAdvancesLastModified is a regression test
// for mitto-tf9: GET /api/workspace-prompts derived its Last-Modified header
// (and the If-Modified-Since 304 short-circuit) solely from the workspace
// .mittorc mtime. A prompt deployed to another source — here a builtin under
// MITTO_DIR/prompts/builtin, exactly as `mitto prompts update-builtin` does —
// left .mittorc untouched, so the browser's conditional revalidation kept
// getting 304 and never saw the new prompt until a hard reload or a `.mittorc`
// touch.
//
// Post-fix the endpoint folds every prompt source's mtime into Last-Modified,
// so deploying a builtin advances it: a stale conditional request (cached
// before the deploy) returns 200 with the new prompt, while the conditional
// logic itself still 304s for a client that is genuinely up to date.
func TestWorkspacePrompts_BuiltinDeployAdvancesLastModified(t *testing.T) {
	// Wire a global PromptsCache so the endpoint actually serves prompts from
	// MITTO_DIR/prompts/ (the default test server leaves it nil). The cache
	// resolves its default dir lazily to appdir.PromptsDir(), which is under the
	// MITTO_DIR that SetupTestServer has already pointed at the temp dir.
	ts := SetupTestServer(t, func(c *web.Config) {
		c.PromptsCache = config.NewPromptsCache()
	})

	// The configured test workspace lives at <MITTO_DIR>/workspace; NOTE we never
	// touch its .mittorc below — the point is that a non-.mittorc source changes.
	workspaceDir := filepath.Join(ts.TempDir, "workspace")

	// Deploy an initial builtin prompt so the global prompts dir exists and the
	// endpoint emits a non-zero Last-Modified baseline to revalidate against.
	builtinDir := filepath.Join(ts.TempDir, "prompts", "builtin")
	if err := os.MkdirAll(builtinDir, 0755); err != nil {
		t.Fatalf("mkdir builtin: %v", err)
	}
	alpha := "name: \"Builtin alpha\"\ngroup: \"Support\"\nprompt: \"a\"\n"
	if err := os.WriteFile(filepath.Join(builtinDir, "alpha.prompt.yaml"), []byte(alpha), 0644); err != nil {
		t.Fatalf("write alpha builtin: %v", err)
	}

	promptsURL := ts.HTTPServer.URL + "/mitto/api/workspace-prompts?" +
		url.Values{"working_dir": {workspaceDir}}.Encode()

	// fetch issues the GET with an optional If-Modified-Since header and returns
	// the status, the Last-Modified response header, and the prompt names.
	fetch := func(t *testing.T, ifModifiedSince string) (int, string, []string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, promptsURL, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if ifModifiedSince != "" {
			req.Header.Set("If-Modified-Since", ifModifiedSince)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET workspace-prompts: %v", err)
		}
		defer resp.Body.Close()
		lastMod := resp.Header.Get("Last-Modified")
		if resp.StatusCode == http.StatusNotModified {
			return resp.StatusCode, lastMod, nil
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		var decoded struct {
			Prompts []struct{ Name string } `json:"prompts"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		names := make([]string, 0, len(decoded.Prompts))
		for _, p := range decoded.Prompts {
			names = append(names, p.Name)
		}
		return resp.StatusCode, lastMod, names
	}

	has := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	// Baseline: 200 with a non-zero Last-Modified and the initial builtin present.
	status, baseline, names := fetch(t, "")
	if status != http.StatusOK {
		t.Fatalf("baseline: status %d, want 200", status)
	}
	if baseline == "" {
		t.Fatalf("baseline: missing Last-Modified header (folded mtime was zero)")
	}
	if !has(names, "Builtin alpha") {
		t.Fatalf("baseline: initial builtin prompt not returned; got %v", names)
	}
	baseTime, err := time.Parse(http.TimeFormat, baseline)
	if err != nil {
		t.Fatalf("parse baseline Last-Modified %q: %v", baseline, err)
	}

	// Deploy a NEW builtin prompt WITHOUT touching .mittorc. Stamp it strictly
	// after the baseline so the folded Last-Modified advances deterministically
	// (HTTP time has second precision, so a +2s bump is unambiguous).
	betaPath := filepath.Join(builtinDir, "beta.prompt.yaml")
	beta := "name: \"Builtin beta\"\ngroup: \"Support\"\nprompt: \"b\"\n"
	if err := os.WriteFile(betaPath, []byte(beta), 0644); err != nil {
		t.Fatalf("write beta builtin: %v", err)
	}
	newer := baseTime.Add(2 * time.Second)
	if err := os.Chtimes(betaPath, newer, newer); err != nil {
		t.Fatalf("chtimes beta builtin: %v", err)
	}

	// The stale conditional request (If-Modified-Since = pre-deploy baseline)
	// must now return 200 with the newly deployed prompt — the mitto-tf9 fix.
	status, newLastMod, names := fetch(t, baseline)
	if status != http.StatusOK {
		t.Fatalf("post-deploy revalidation: status %d, want 200 (stale-304 bug not fixed)", status)
	}
	if !has(names, "Builtin beta") {
		t.Errorf("post-deploy: newly deployed builtin prompt missing; got %v", names)
	}
	if newLastMod == "" {
		t.Errorf("post-deploy: missing Last-Modified header")
	} else if nt, perr := time.Parse(http.TimeFormat, newLastMod); perr == nil && !nt.After(baseTime) {
		t.Errorf("post-deploy: Last-Modified %q did not advance past baseline %q", newLastMod, baseline)
	}

	// The conditional logic still works with the folded mtime: a client that is
	// genuinely up to date (If-Modified-Since well past every source) gets 304.
	future := newer.Add(time.Hour).UTC().Format(http.TimeFormat)
	if status, _, _ := fetch(t, future); status != http.StatusNotModified {
		t.Errorf("up-to-date revalidation: status %d, want 304", status)
	}
}
