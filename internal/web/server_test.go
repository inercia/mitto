package web

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

func TestConfig_GetWorkspaces_WithWorkspaces(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1", ACPServer: "server1"},
			{WorkingDir: "/workspace2", ACPServer: "server2"},
		},
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 2 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 2", len(workspaces))
	}

	if workspaces[0].WorkingDir != "/workspace1" {
		t.Errorf("workspaces[0].WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/workspace1")
	}
}

func TestConfig_GetWorkspaces_LegacyFields(t *testing.T) {
	cfg := &Config{
		ACPServer:         "legacy-server",
		ACPCommand:        "legacy-command",
		DefaultWorkingDir: "/legacy/dir",
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 1 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 1", len(workspaces))
	}

	if workspaces[0].ACPServer != "legacy-server" {
		t.Errorf("ACPServer = %q, want %q", workspaces[0].ACPServer, "legacy-server")
	}
	// Legacy CLI command is stored as ACPCommandOverride (per-workspace override)
	if workspaces[0].ACPCommandOverride != "legacy-command" {
		t.Errorf("ACPCommandOverride = %q, want %q", workspaces[0].ACPCommandOverride, "legacy-command")
	}
	if workspaces[0].WorkingDir != "/legacy/dir" {
		t.Errorf("WorkingDir = %q, want %q", workspaces[0].WorkingDir, "/legacy/dir")
	}
}

func TestConfig_GetWorkspaces_EmptyWorkingDir(t *testing.T) {
	cfg := &Config{
		ACPServer:         "server",
		DefaultWorkingDir: "", // Empty - should use current directory
	}

	workspaces := cfg.GetWorkspaces()

	if len(workspaces) != 1 {
		t.Fatalf("GetWorkspaces() returned %d workspaces, want 1", len(workspaces))
	}

	// WorkingDir should be set to current directory (not empty)
	if workspaces[0].WorkingDir == "" {
		t.Error("WorkingDir should not be empty when DefaultWorkingDir is empty")
	}
}

func TestConfig_GetDefaultWorkspace(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/first", ACPServer: "server1"},
			{WorkingDir: "/second", ACPServer: "server2"},
		},
	}

	ws := cfg.GetDefaultWorkspace()

	if ws == nil {
		t.Fatal("GetDefaultWorkspace() returned nil")
	}

	if ws.WorkingDir != "/first" {
		t.Errorf("WorkingDir = %q, want %q", ws.WorkingDir, "/first")
	}
}

func TestConfig_GetDefaultWorkspace_Empty(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{},
	}

	// When Workspaces is empty, GetWorkspaces creates a legacy workspace
	ws := cfg.GetDefaultWorkspace()

	// Should return the legacy workspace, not nil
	if ws == nil {
		t.Fatal("GetDefaultWorkspace() returned nil")
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := &Config{
		AutoApprove:    true,
		Debug:          true,
		FromCLI:        true,
		ConfigReadOnly: true,
		RCFilePath:     "/path/to/rc",
	}

	if !cfg.AutoApprove {
		t.Error("AutoApprove should be true")
	}
	if !cfg.Debug {
		t.Error("Debug should be true")
	}
	if !cfg.FromCLI {
		t.Error("FromCLI should be true")
	}
	if !cfg.ConfigReadOnly {
		t.Error("ConfigReadOnly should be true")
	}
	if cfg.RCFilePath != "/path/to/rc" {
		t.Errorf("RCFilePath = %q, want %q", cfg.RCFilePath, "/path/to/rc")
	}
}

func TestConfig_GetWorkspaceByDir(t *testing.T) {
	cfg := &Config{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1", ACPServer: "server1"},
			{WorkingDir: "/workspace2", ACPServer: "server2"},
		},
	}

	// Find existing workspace
	ws := cfg.GetWorkspaceByDir("/workspace1")
	if ws == nil {
		t.Fatal("GetWorkspaceByDir returned nil for existing workspace")
	}
	if ws.ACPServer != "server1" {
		t.Errorf("ACPServer = %q, want %q", ws.ACPServer, "server1")
	}

	// Find non-existent workspace
	ws = cfg.GetWorkspaceByDir("/nonexistent")
	if ws != nil {
		t.Error("GetWorkspaceByDir should return nil for non-existent workspace")
	}
}

func TestConfig_GetWorkspaceByDir_Legacy(t *testing.T) {
	cfg := &Config{
		ACPServer:         "legacy-server",
		ACPCommand:        "legacy-cmd",
		DefaultWorkingDir: "/legacy/dir",
	}

	// Find legacy workspace
	ws := cfg.GetWorkspaceByDir("/legacy/dir")
	if ws == nil {
		t.Fatal("GetWorkspaceByDir returned nil for legacy workspace")
	}
	if ws.ACPServer != "legacy-server" {
		t.Errorf("ACPServer = %q, want %q", ws.ACPServer, "legacy-server")
	}
}

func TestServer_APIPrefix(t *testing.T) {
	server := &Server{
		apiPrefix: "/api/v1",
	}

	if server.APIPrefix() != "/api/v1" {
		t.Errorf("APIPrefix() = %q, want %q", server.APIPrefix(), "/api/v1")
	}
}

func TestServer_Store(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		store: store,
	}

	if server.Store() != store {
		t.Error("Store() should return the store")
	}
}

func TestServer_Store_Nil(t *testing.T) {
	server := &Server{
		store: nil,
	}

	if server.Store() != nil {
		t.Error("Store() should return nil when store is nil")
	}
}

func TestServer_IsShutdown(t *testing.T) {
	server := &Server{}

	// Initially not shutdown
	if server.IsShutdown() {
		t.Error("IsShutdown should return false initially")
	}

	// Set shutdown
	server.shutdown.Store(true)

	if !server.IsShutdown() {
		t.Error("IsShutdown should return true after setting")
	}
}

func TestServer_Logger(t *testing.T) {
	logger := slog.Default()
	server := &Server{
		logger: logger,
	}

	if server.Logger() != logger {
		t.Error("Logger() should return the logger")
	}
}

func TestServer_Logger_Nil(t *testing.T) {
	server := &Server{
		logger: nil,
	}

	if server.Logger() != nil {
		t.Error("Logger() should return nil when logger is nil")
	}
}

// =============================================================================
// conversation.BuildLoopUpdatedData tests
// =============================================================================

func TestBuildLoopUpdatedData_NilLoop(t *testing.T) {
	data := conversation.BuildLoopUpdatedData("s1", nil)
	if data["loop_configured"] != false {
		t.Errorf("loop_configured = %v, want false", data["loop_configured"])
	}
	if data["loop_enabled"] != false {
		t.Errorf("loop_enabled = %v, want false", data["loop_enabled"])
	}
	// New keys must NOT be present when there's no config.
	for _, key := range []string{"trigger", "delay_seconds", "max_duration_seconds"} {
		if _, ok := data[key]; ok {
			t.Errorf("key %q must be absent when loop is nil", key)
		}
	}
}

func TestBuildLoopUpdatedData_ScheduleLoop(t *testing.T) {
	p := &session.LoopPrompt{
		Prompt:             "Test",
		Frequency:          session.Frequency{Value: 30, Unit: session.FrequencyMinutes},
		Enabled:            true,
		Trigger:            session.TriggerSchedule,
		MaxIterations:      5,
		IterationCount:     2,
		DelaySeconds:       0,
		MaxDurationSeconds: 3600,
	}
	data := conversation.BuildLoopUpdatedData("s1", p)

	if data["loop_configured"] != true {
		t.Errorf("loop_configured = %v, want true", data["loop_configured"])
	}
	if data["trigger"] != "schedule" {
		t.Errorf("trigger = %v, want %q", data["trigger"], "schedule")
	}
	if data["delay_seconds"] != 0 {
		t.Errorf("delay_seconds = %v, want 0", data["delay_seconds"])
	}
	if data["max_duration_seconds"] != 3600 {
		t.Errorf("max_duration_seconds = %v, want 3600", data["max_duration_seconds"])
	}
	if data["max_iterations"] != 5 {
		t.Errorf("max_iterations = %v, want 5", data["max_iterations"])
	}
	if data["iteration_count"] != 2 {
		t.Errorf("iteration_count = %v, want 2", data["iteration_count"])
	}
}

func TestBuildLoopUpdatedData_EmptyTriggerReportsSchedule(t *testing.T) {
	// Trigger="" defaults to "schedule" via EffectiveTrigger().
	p := &session.LoopPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
		Trigger:   "", // empty — must be resolved to "schedule"
	}
	data := conversation.BuildLoopUpdatedData("s1", p)
	if data["trigger"] != "schedule" {
		t.Errorf("trigger = %v, want %q (empty trigger must resolve to 'schedule')", data["trigger"], "schedule")
	}
}

func TestBuildLoopUpdatedData_OnCompletionLoop(t *testing.T) {
	p := &session.LoopPrompt{
		Prompt:             "Test",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 7200,
	}
	data := conversation.BuildLoopUpdatedData("s1", p)

	if data["trigger"] != "onCompletion" {
		t.Errorf("trigger = %v, want %q", data["trigger"], "onCompletion")
	}
	if data["delay_seconds"] != 30 {
		t.Errorf("delay_seconds = %v, want 30", data["delay_seconds"])
	}
	if data["max_duration_seconds"] != 7200 {
		t.Errorf("max_duration_seconds = %v, want 7200", data["max_duration_seconds"])
	}
}

func TestBuildLoopUpdatedData_StoppedReasonPresent(t *testing.T) {
	p := &session.LoopPrompt{
		Prompt:        "Test",
		Frequency:     session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:       false,
		StoppedReason: session.StoppedReasonMaxDuration,
	}
	data := conversation.BuildLoopUpdatedData("s1", p)
	if data["loop_stopped_reason"] != "maxDuration" {
		t.Errorf("loop_stopped_reason = %v, want %q", data["loop_stopped_reason"], "maxDuration")
	}
	// trigger must still be present even when stopped.
	if data["trigger"] != "schedule" {
		t.Errorf("trigger = %v, want %q when stopped", data["trigger"], "schedule")
	}
}

// =============================================================================
// BroadcastACPStarted / BroadcastACPStopped dedupe tests
// =============================================================================

// newBroadcastTestServer builds a minimal Server with a real (empty)
// GlobalEventsManager and a slog logger writing to buf at Debug level so
// tests can observe emitted vs suppressed broadcasts by counting log lines.
func newBroadcastTestServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &Server{
		eventsManager: NewGlobalEventsManager(),
		logger:        logger,
	}, buf
}

func countLines(s, substr string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

func TestBroadcastACPStarted_DedupesWithinWindow(t *testing.T) {
	s, buf := newBroadcastTestServer(t)

	s.BroadcastACPStarted("sess-1")
	s.BroadcastACPStarted("sess-1")
	s.BroadcastACPStarted("sess-1")

	out := buf.String()
	if got := countLines(out, "Broadcast ACP started"); got != 1 {
		t.Errorf("emitted broadcasts = %d, want 1\nlogs:\n%s", got, out)
	}
	if got := countLines(out, "Suppressed duplicate ACP started broadcast"); got != 2 {
		t.Errorf("suppressed broadcasts = %d, want 2\nlogs:\n%s", got, out)
	}

	// Different session must not be affected by the recent entry for sess-1.
	buf.Reset()
	s.BroadcastACPStarted("sess-2")
	if got := countLines(buf.String(), "Broadcast ACP started"); got != 1 {
		t.Errorf("distinct-session broadcast = %d, want 1\nlogs:\n%s", got, buf.String())
	}
}

func TestBroadcastACPStopped_DedupesWithinWindow(t *testing.T) {
	s, buf := newBroadcastTestServer(t)

	s.BroadcastACPStopped("sess-1", "gc_suspended")
	s.BroadcastACPStopped("sess-1", "gc_suspended")
	s.BroadcastACPStopped("sess-1", "gc_suspended")

	out := buf.String()
	if got := countLines(out, "Broadcast ACP stopped"); got != 1 {
		t.Errorf("emitted broadcasts = %d, want 1\nlogs:\n%s", got, out)
	}
	if got := countLines(out, "Suppressed duplicate ACP stopped broadcast"); got != 2 {
		t.Errorf("suppressed broadcasts = %d, want 2\nlogs:\n%s", got, out)
	}
}

func TestBroadcastACPStarted_WindowExpires(t *testing.T) {
	s, buf := newBroadcastTestServer(t)

	// Seed the map with a stale entry to simulate the window having elapsed
	// without actually sleeping acpLifecycleWindow.
	s.recentStartsMu.Lock()
	s.recentStarts = map[string]time.Time{
		"sess-1": time.Now().Add(-(acpLifecycleWindow + 100*time.Millisecond)),
	}
	s.recentStartsMu.Unlock()

	s.BroadcastACPStarted("sess-1")

	if got := countLines(buf.String(), "Broadcast ACP started"); got != 1 {
		t.Errorf("post-window broadcast = %d, want 1\nlogs:\n%s", got, buf.String())
	}
	if got := countLines(buf.String(), "Suppressed duplicate ACP started broadcast"); got != 0 {
		t.Errorf("unexpected suppression = %d, want 0\nlogs:\n%s", got, buf.String())
	}
}

func TestBroadcastACPStopped_WindowExpires(t *testing.T) {
	s, buf := newBroadcastTestServer(t)

	s.recentStopsMu.Lock()
	s.recentStops = map[string]time.Time{
		"sess-1": time.Now().Add(-(acpLifecycleWindow + 100*time.Millisecond)),
	}
	s.recentStopsMu.Unlock()

	s.BroadcastACPStopped("sess-1", "archived")

	if got := countLines(buf.String(), "Broadcast ACP stopped"); got != 1 {
		t.Errorf("post-window broadcast = %d, want 1\nlogs:\n%s", got, buf.String())
	}
}

func TestBroadcastACPStarted_EvictsStaleEntries(t *testing.T) {
	s, _ := newBroadcastTestServer(t)

	// Seed several stale entries for other sessions plus a fresh one that
	// should be left alone. A broadcast on a new session should evict all
	// stale entries but keep the fresh one.
	stale := time.Now().Add(-(acpLifecycleWindow + time.Second))
	fresh := time.Now()
	s.recentStartsMu.Lock()
	s.recentStarts = map[string]time.Time{
		"old-a": stale,
		"old-b": stale,
		"old-c": stale,
		"fresh": fresh,
	}
	s.recentStartsMu.Unlock()

	s.BroadcastACPStarted("new")

	s.recentStartsMu.Lock()
	defer s.recentStartsMu.Unlock()
	if _, ok := s.recentStarts["old-a"]; ok {
		t.Error("stale entry old-a should have been evicted")
	}
	if _, ok := s.recentStarts["old-b"]; ok {
		t.Error("stale entry old-b should have been evicted")
	}
	if _, ok := s.recentStarts["old-c"]; ok {
		t.Error("stale entry old-c should have been evicted")
	}
	if _, ok := s.recentStarts["fresh"]; !ok {
		t.Error("fresh entry should have been retained")
	}
	if _, ok := s.recentStarts["new"]; !ok {
		t.Error("new entry should have been recorded")
	}
}

func TestBroadcastACPStarted_NilEventsManager(t *testing.T) {
	// Must not panic when eventsManager is nil (mirrors the guard in
	// BroadcastACPStartFailed).
	s := &Server{}
	s.BroadcastACPStarted("sess-1")
}

func TestBroadcastACPStopped_NilEventsManager(t *testing.T) {
	s := &Server{}
	s.BroadcastACPStopped("sess-1", "test")
}
