package conversation

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestDuplicateWorkingDirs_SameDirDifferentACP(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-a1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-a2", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-b1", WorkingDir: "/w1", ACPServer: "B"},
		{UUID: "uuid-lone", WorkingDir: "/w2", ACPServer: "A"},
	})

	groups := sm.WorkspaceRegistry().DuplicateWorkingDirs()
	if len(groups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.WorkingDir != "/w1" {
		t.Errorf("WorkingDir = %q, want /w1", g.WorkingDir)
	}
	wantUUIDs := []string{"uuid-a1", "uuid-a2", "uuid-b1"}
	if !reflect.DeepEqual(g.UUIDs, wantUUIDs) {
		t.Errorf("UUIDs = %v, want %v", g.UUIDs, wantUUIDs)
	}
	wantACP := []string{"A", "B"}
	if !reflect.DeepEqual(g.ACPServers, wantACP) {
		t.Errorf("ACPServers = %v, want %v", g.ACPServers, wantACP)
	}
	if g.SameACP {
		t.Error("SameACP = true, want false (mixed A/B)")
	}
}

func TestDuplicateWorkingDirs_SameDirSameACP(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "/w1", ACPServer: "A"},
	})

	groups := sm.WorkspaceRegistry().DuplicateWorkingDirs()
	if len(groups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(groups))
	}
	g := groups[0]
	if !g.SameACP {
		t.Error("SameACP = false, want true")
	}
	if !reflect.DeepEqual(g.ACPServers, []string{"A"}) {
		t.Errorf("ACPServers = %v, want [A]", g.ACPServers)
	}
	if !reflect.DeepEqual(g.UUIDs, []string{"uuid-1", "uuid-2"}) {
		t.Errorf("UUIDs = %v, want [uuid-1 uuid-2]", g.UUIDs)
	}
}

func TestDuplicateWorkingDirs_NoDuplicates(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "/w2", ACPServer: "A"},
		{UUID: "uuid-3", WorkingDir: "/w3", ACPServer: "B"},
	})

	groups := sm.WorkspaceRegistry().DuplicateWorkingDirs()
	if len(groups) != 0 {
		t.Fatalf("expected 0 duplicate groups, got %d: %+v", len(groups), groups)
	}
}

func TestDuplicateWorkingDirs_IgnoresEmptyWorkingDir(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "", ACPServer: "B"},
	})

	groups := sm.WorkspaceRegistry().DuplicateWorkingDirs()
	if len(groups) != 0 {
		t.Fatalf("expected 0 duplicate groups for empty WorkingDir, got %d: %+v", len(groups), groups)
	}
}

func TestDuplicateWorkingDirs_SameACPWithEmptyACPServer(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: ""},
		{UUID: "uuid-2", WorkingDir: "/w1", ACPServer: ""},
	})

	groups := sm.WorkspaceRegistry().DuplicateWorkingDirs()
	if len(groups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(groups))
	}
	if groups[0].SameACP {
		t.Error("SameACP = true, want false (empty ACPServer does not count as same)")
	}
}

// newCaptureLogger returns a *slog.Logger whose output (including per-record
// level tokens) is captured in buf. Text handler is used so tests can assert on
// both the level tag (level=INFO / level=WARN) and the message text.
func newCaptureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestLogDuplicateWorkingDirs_MixedACPLogsInfo(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-a", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-b", WorkingDir: "/w1", ACPServer: "B"},
	})

	var buf bytes.Buffer
	sm.WorkspaceRegistry().LogDuplicateWorkingDirs(newCaptureLogger(&buf))

	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected INFO-level log for mixed ACP, got: %s", out)
	}
	if strings.Contains(out, "level=WARN") {
		t.Errorf("did not expect WARN for mixed ACP, got: %s", out)
	}
	if !strings.Contains(out, "multiple workspaces registered for folder") {
		t.Errorf("expected new INFO message, got: %s", out)
	}
	if strings.Contains(out, "workspace duplication detected") {
		t.Errorf("mixed-ACP case should not use the WARN message, got: %s", out)
	}
}

func TestLogDuplicateWorkingDirs_SameACPLogsWarn(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "/w1", ACPServer: "A"},
	})

	var buf bytes.Buffer
	sm.WorkspaceRegistry().LogDuplicateWorkingDirs(newCaptureLogger(&buf))

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected WARN-level log for same-ACP duplicate, got: %s", out)
	}
	if !strings.Contains(out, "workspace duplication detected") {
		t.Errorf("expected legacy WARN message for same-ACP duplicate, got: %s", out)
	}
	if strings.Contains(out, "level=INFO") {
		t.Errorf("did not expect INFO for same-ACP duplicate, got: %s", out)
	}
}

func TestLogDuplicateWorkingDirs_NoDuplicatesLogsNothing(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "/w2", ACPServer: "A"},
	})

	var buf bytes.Buffer
	sm.WorkspaceRegistry().LogDuplicateWorkingDirs(newCaptureLogger(&buf))

	if buf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", buf.String())
	}
}

func TestLogDuplicateWorkingDirs_NilLoggerNoOp(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "uuid-1", WorkingDir: "/w1", ACPServer: "A"},
		{UUID: "uuid-2", WorkingDir: "/w1", ACPServer: "B"},
	})
	// Must not panic.
	sm.WorkspaceRegistry().LogDuplicateWorkingDirs(nil)
}
