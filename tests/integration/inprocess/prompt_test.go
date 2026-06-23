//go:build integration

package inprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestSendPromptAndReceiveResponse tests the complete prompt/response flow.
func TestSendPromptAndReceiveResponse(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track events
	var (
		mu             sync.Mutex
		connected      bool
		promptReceived bool
		promptComplete bool
		agentMessages  []string
		agentThoughts  []string
		toolCalls      []string
		promptID       string
	)

	callbacks := client.SessionCallbacks{
		OnConnected: func(sid, cid, acp string) {
			mu.Lock()
			defer mu.Unlock()
			connected = true
			t.Logf("Connected: session=%s, client=%s", sid, cid)
		},
		OnPromptReceived: func(pid string) {
			mu.Lock()
			defer mu.Unlock()
			promptReceived = true
			promptID = pid
			t.Logf("Prompt received: %s", pid)
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			defer mu.Unlock()
			agentMessages = append(agentMessages, html)
		},
		OnAgentThought: func(text string) {
			mu.Lock()
			defer mu.Unlock()
			agentThoughts = append(agentThoughts, text)
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			defer mu.Unlock()
			toolCalls = append(toolCalls, title)
			t.Logf("Tool call: %s (%s)", title, status)
		},
	}

	// Connect to the session
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connected
	}, "connection")

	// Client must send load_events to register as an observer
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Send a prompt
	err = ws.SendPrompt("Hello, this is a test message")
	if err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for prompt to complete (mock ACP should respond quickly)
	// Note: prompt_received may or may not be sent depending on server implementation
	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete")

	// Verify we got some response
	mu.Lock()
	defer mu.Unlock()

	if promptReceived && promptID == "" {
		t.Error("Prompt ID should not be empty when prompt_received was called")
	}

	// The mock ACP server should have sent some response
	totalContent := len(agentMessages) + len(agentThoughts) + len(toolCalls)
	if totalContent == 0 {
		t.Log("Warning: No agent content received (mock ACP may not have responded)")
	} else {
		t.Logf("Received: %d messages, %d thoughts, %d tool calls",
			len(agentMessages), len(agentThoughts), len(toolCalls))
	}

	// Check that agent message contains expected content from mock
	fullMessage := strings.Join(agentMessages, "")
	if len(fullMessage) > 0 {
		t.Logf("Agent message preview: %s...", truncate(fullMessage, 100))
	}
}

// =============================================================================
// Template-render integration tests (mitto-m7sb.11)
// These tests exercise Go text/template rendering through the REAL send pipeline
// using the mock ACP harness. They use setupDeferredConfigServer so the rendered
// text delivered to the agent is captured in the RPC-order file.
// =============================================================================

// writeTemplatePrompt writes a .prompt.yaml file to the workspace .mitto/prompts/
// directory so the named-prompt resolver finds it.
func writeTemplatePrompt(t *testing.T, ts *TestServer, slug, name, body string) {
	t.Helper()
	promptsDir := filepath.Join(ts.TempDir, "workspace", ".mitto", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("mkdir workspace prompts: %v", err)
	}
	yaml := "name: " + `"` + name + `"` + "\nprompt: |\n"
	for _, line := range strings.Split(body, "\n") {
		yaml += "  " + line + "\n"
	}
	path := filepath.Join(promptsDir, slug+".prompt.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
}

// runTemplatePromptAndWait creates a session with a named prompt + optional args,
// connects, waits for prompt completion, and returns the RPC-order lines.
func runTemplatePromptAndWait(t *testing.T, ts *TestServer, orderFile, promptName string, args map[string]string) []string {
	t.Helper()
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: promptName,
		Arguments:         args,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	var (
		mu             sync.Mutex
		promptComplete bool
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(_ int) { mu.Lock(); promptComplete = true; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	waitFor(t, 20*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return promptComplete }, "prompt complete")
	return readRPCOrder(t, orderFile)
}

// promptLineFor returns the `prompt\t<text>` detail that contains needle, or "".
func promptLineFor(lines []string, needle string) string {
	for _, ln := range lines {
		if strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, needle) {
			return strings.TrimPrefix(ln, "prompt\t")
		}
	}
	return ""
}

// TestTemplateRender_NamedPrompt_SessionID verifies that {{ .Session.ID }} in a
// named-prompt body is rendered to the real session ID before reaching the agent.
func TestTemplateRender_NamedPrompt_SessionID(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)
	writeTemplatePrompt(t, ts, "tmpl-sessid", "tmpl-sessid", "Session: {{ .Session.ID }}")

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{InitialPromptName: "tmpl-sessid"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	var (
		mu             sync.Mutex
		promptComplete bool
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(_ int) { mu.Lock(); promptComplete = true; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	waitFor(t, 20*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return promptComplete }, "prompt complete")

	lines := readRPCOrder(t, orderFile)
	want := "Session: " + sess.SessionID
	rendered := promptLineFor(lines, want)
	if rendered == "" {
		t.Fatalf("expected RPC order line containing %q; got lines: %v", want, lines)
	}
	if strings.Contains(rendered, "{{") {
		t.Errorf("literal {{ remains in rendered prompt: %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}

// TestTemplateRender_ArgsAndVarOrdering proves template runs BEFORE ${VAR} substitution:
// {{ .Args.NAME }} is filled by template, then the emitted ${CITY} is resolved by
// SubstituteArguments in the legacy pass.
func TestTemplateRender_ArgsAndVarOrdering(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)
	// {{ "${CITY}" }} emits the literal string ${CITY} from the template;
	// SubstituteArguments then resolves ${CITY} → Paris.
	writeTemplatePrompt(t, ts, "tmpl-args-order", "tmpl-args-order",
		`Hi {{ .Args.NAME }} from {{ "${CITY}" }}`)

	lines := runTemplatePromptAndWait(t, ts, orderFile, "tmpl-args-order",
		map[string]string{"NAME": "Alice", "CITY": "Paris"})

	const want = "Hi Alice from Paris"
	rendered := promptLineFor(lines, want)
	if rendered == "" {
		t.Fatalf("expected %q in RPC order; got lines: %v", want, lines)
	}
	if strings.Contains(rendered, "Alice") && !strings.Contains(rendered, "Paris") {
		t.Errorf("arg substitution pass did not fire: %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}

// TestTemplateRender_Conditional verifies {{ if .Session.IsChild }} for a root session
// renders the else branch (ROOT, not CHILD).
func TestTemplateRender_Conditional(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)
	writeTemplatePrompt(t, ts, "tmpl-cond", "tmpl-cond",
		`{{ if .Session.IsChild }}CHILD{{ else }}ROOT{{ end }}`)

	lines := runTemplatePromptAndWait(t, ts, orderFile, "tmpl-cond", nil)

	rendered := promptLineFor(lines, "ROOT")
	if rendered == "" {
		t.Fatalf("expected ROOT in RPC order; got lines: %v", lines)
	}
	if strings.Contains(rendered, "CHILD") {
		t.Errorf("CHILD rendered for root session: %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}

// TestTemplateRender_Gating verifies fileExists/commandExists template functions.
// Writes marker.txt to the workspace dir; asserts HASFILE and HASSH appear,
// BADCMD does not.
func TestTemplateRender_Gating(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)

	// Write the marker file that fileExists will find at send time.
	markerPath := filepath.Join(ts.TempDir, "workspace", "marker.txt")
	if err := os.WriteFile(markerPath, []byte("exists"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	writeTemplatePrompt(t, ts, "tmpl-gating", "tmpl-gating",
		`{{ if fileExists "marker.txt" }}HASFILE{{ end }}`+
			`{{ if commandExists "definitely-not-real-cmd-zzz" }}BADCMD{{ end }}`+
			`{{ if commandExists "sh" }}HASSH{{ end }}`)

	lines := runTemplatePromptAndWait(t, ts, orderFile, "tmpl-gating", nil)

	rendered := promptLineFor(lines, "HASFILE")
	if rendered == "" {
		rendered = promptLineFor(lines, "HASSH")
		if rendered != "" {
			t.Errorf("HASFILE missing but HASSH present — fileExists may not see workspace folder; rendered: %q", rendered)
		}
		t.Fatalf("expected HASFILE in RPC order; got lines: %v", lines)
	}
	if strings.Contains(rendered, "BADCMD") {
		t.Errorf("BADCMD appeared for nonexistent command: %q", rendered)
	}
	if !strings.Contains(rendered, "HASSH") {
		t.Errorf("HASSH missing (commandExists(sh) should be true): %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}

// TestTemplateRender_CoexistWithMitto verifies that a body mixing a Go template
// token ({{ .Session.ID }}) and a legacy keep-list @mitto: token both resolve
// in the same send. @mitto:children is used (resolves to "" in test harness since
// there are no child sessions, but SubstituteVariables removes the literal token).
func TestTemplateRender_CoexistWithMitto(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)
	// @mitto:children resolves to "" (no child sessions in test harness).
	writeTemplatePrompt(t, ts, "tmpl-coexist", "tmpl-coexist",
		`ID={{ .Session.ID }} CHILDREN=@mitto:children END`)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{InitialPromptName: "tmpl-coexist"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	var (
		mu             sync.Mutex
		promptComplete bool
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(_ int) { mu.Lock(); promptComplete = true; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	waitFor(t, 20*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return promptComplete }, "prompt complete")

	lines := readRPCOrder(t, orderFile)
	// Find the prompt line that contains our session ID
	wantID := "ID=" + sess.SessionID
	rendered := promptLineFor(lines, wantID)
	if rendered == "" {
		t.Fatalf("expected line with %q in RPC order; got lines: %v", wantID, lines)
	}
	// Template resolved .Session.ID and legacy pass resolved @mitto:children.
	if strings.Contains(rendered, "@mitto:") {
		t.Errorf("literal @mitto: token remains in rendered prompt (legacy pass did not fire): %q", rendered)
	}
	if !strings.Contains(rendered, sess.SessionID) {
		t.Errorf("session ID not in rendered prompt: %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}

// TestTemplateRender_FailClosed_RawMessage verifies full-pipeline fail-closed behavior:
// a raw SendPrompt with an invalid template (struct-field typo) fires OnError and
// does NOT reach the mock ACP agent.
func TestTemplateRender_FailClosed_RawMessage(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	var (
		mu     sync.Mutex
		errors []string
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnError: func(msg string) { mu.Lock(); errors = append(errors, msg); mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}

	// Send a raw message with a struct-field typo — render must fail closed.
	if err := ws.SendPrompt("Bad: {{ .Session.NoSuchField }}"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	// Wait for OnError broadcast.
	waitFor(t, 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(errors) > 0
	}, "OnError from render failure")

	mu.Lock()
	gotErrors := append([]string(nil), errors...)
	mu.Unlock()
	t.Logf("OnError messages: %v", gotErrors)

	// At least one error message must mention the render failure.
	found := false
	for _, e := range gotErrors {
		if strings.Contains(e, "render error") || strings.Contains(e, "NoSuchField") || strings.Contains(e, "template") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no render-error message in OnError callbacks; got: %v", gotErrors)
	}

	// The aborted send must NOT have reached the mock agent.
	lines := readRPCOrder(t, orderFile)
	for _, ln := range lines {
		if strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "NoSuchField") {
			t.Errorf("aborted send reached the agent: %q", ln)
		}
	}
}

// waitFor waits for a condition to become true.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for %s", description)
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// TestTemplateRender_PeriodicRun verifies that Go template rendering works correctly
// on the periodic-run dispatch path: .Session.IsPeriodic == true and
// .Session.IsPeriodicForced == true when triggered via RunPeriodicNow (manual "run now").
func TestTemplateRender_PeriodicRun(t *testing.T) {
	ts, orderFile := setupDeferredConfigServer(t)

	// Write a named prompt whose body uses the periodic context fields.
	writeTemplatePrompt(t, ts, "tmpl-periodic", "tmpl-periodic",
		`PeriodicMarker: {{ if .Session.IsPeriodic }}PERIODIC{{ else }}ONESHOT{{ end }}{{ if .Session.IsPeriodicForced }}-FORCED{{ end }}`)

	// Create session without an initial prompt (avoids a concurrent-prompt 409 during SetPeriodic).
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "periodic-template-test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	// Connect WebSocket and count completions (use counter so we wait for exactly run 1).
	var (
		mu        sync.Mutex
		completes int
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(_ int) { mu.Lock(); completes++; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}

	// Configure periodic with the named template prompt.
	cfg, err := ts.Client.SetPeriodic(sess.SessionID, client.SetPeriodicRequest{
		PromptName: "tmpl-periodic",
		Frequency:  client.PeriodicFrequency{Value: 1, Unit: "hours"},
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("SetPeriodic: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("expected enabled=true after SetPeriodic, got false")
	}

	// Trigger run 1 via RunPeriodicNow (the manual "run now" path; forced=true → IsPeriodicForced=true).
	if err := ts.Client.RunPeriodicNow(sess.SessionID, true); err != nil {
		t.Fatalf("RunPeriodicNow: %v", err)
	}

	// Wait for the periodic prompt to complete.
	waitFor(t, 25*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return completes >= 1
	}, "periodic prompt complete")

	// Inspect the rendered text the mock agent received.
	lines := readRPCOrder(t, orderFile)
	got := promptLineFor(lines, "PeriodicMarker:")
	if got == "" {
		t.Fatalf("PeriodicMarker: line not found in RPC order; all lines: %v", lines)
	}
	t.Logf("captured line: %q", got)

	// Core acceptance: template must see IsPeriodic == true.
	if !strings.Contains(got, "PERIODIC") {
		t.Errorf("expected PERIODIC in rendered line, got %q", got)
	}
	if strings.Contains(got, "ONESHOT") {
		t.Errorf("ONESHOT rendered — IsPeriodic was false; got %q", got)
	}

	// RunPeriodicNow sets IsPeriodicForced=true (periodic_runner.go:TriggerNow forced=true).
	if !strings.Contains(got, "-FORCED") {
		t.Errorf("expected -FORCED in rendered line (RunPeriodicNow sets IsPeriodicForced=true); got %q", got)
	}
}

// TestAfterPhaseProcessor_SentinelFile verifies that an on: agentResponded processor
// fires after a prompt completes and produces a side effect (sentinel file).
//
// Strategy:
//  1. Write a processor YAML to the workspace's .mitto/processors/ directory before
//     creating any session (processors are loaded lazily at session-creation time).
//  2. Send a prompt and wait for prompt_complete.
//  3. Assert the sentinel file exists.
func TestAfterPhaseProcessor_SentinelFile(t *testing.T) {
	ts := SetupTestServer(t)

	// Determine sentinel path inside the test's temp dir (auto-cleaned).
	sentinelPath := filepath.Join(ts.TempDir, "after-phase-fired.txt")

	// Write the processor YAML before creating a session.
	// The processor runs sh -c '...' and writes to the sentinel path.
	processorsDir := filepath.Join(ts.TempDir, "workspace", ".mitto", "processors")
	if err := os.MkdirAll(processorsDir, 0755); err != nil {
		t.Fatalf("Failed to create processors dir: %v", err)
	}

	processorYAML := fmt.Sprintf(`name: after-test-sentinel
when:
  on: agentResponded
  match: all
command: sh
args: ["-c", "echo fired >> %s"]
output: discard
`, sentinelPath)

	yamlPath := filepath.Join(processorsDir, "after-test-sentinel.yaml")
	if err := os.WriteFile(yamlPath, []byte(processorYAML), 0644); err != nil {
		t.Fatalf("Failed to write processor YAML: %v", err)
	}
	t.Logf("Processor YAML written to %s", yamlPath)
	t.Logf("Sentinel path: %s", sentinelPath)

	// Create a session (processor is loaded at this point).
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	var (
		mu             sync.Mutex
		promptComplete bool
	)

	callbacks := client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("Hello, test the after-phase processor"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for prompt_complete.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete")

	// Give the after-phase processor a moment to finish writing the file.
	// The processor runs synchronously in the prompt goroutine, so by the time
	// prompt_complete is broadcast the processor should already be done.
	// A brief sleep guards against any timing edge cases.
	time.Sleep(500 * time.Millisecond)

	// Assert the sentinel file was created.
	if _, err := os.Stat(sentinelPath); os.IsNotExist(err) {
		t.Errorf("After-phase processor did not fire: sentinel file %s does not exist", sentinelPath)
	} else if err != nil {
		t.Errorf("Error checking sentinel file: %v", err)
	} else {
		content, _ := os.ReadFile(sentinelPath)
		t.Logf("Sentinel file content: %q", string(content))
	}
}
