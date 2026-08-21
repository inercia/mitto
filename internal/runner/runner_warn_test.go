package runner

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// captureHandler is a minimal slog.Handler that records every emitted record
// so tests can assert on the WARN advisory wired into NewRunner.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func newCaptureHandler() *captureHandler                           { return &captureHandler{} }
func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

// findAdvisory returns the first record whose message contains the
// additive-permits advisory substring, or nil if none was emitted.
func (h *captureHandler) findAdvisory() *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if strings.Contains(h.records[i].Message, "additive permits") {
			return &h.records[i]
		}
	}
	return nil
}

func attrMap(r *slog.Record) map[string]any {
	out := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.Any()
		return true
	})
	return out
}

func newTestLogger() (*slog.Logger, *captureHandler) {
	h := newCaptureHandler()
	return slog.New(h), h
}

// TestNewRunner_SandboxExec_WarnsOnAllowFolders — macOS only, because on other
// platforms sandbox-exec falls back to exec and the WARN gate closes by design.
func TestNewRunner_SandboxExec_WarnsOnAllowFolders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only available on macOS")
	}

	logger, cap := newTestLogger()
	allowNet := false
	cfgs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "sandbox-exec",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking:  &allowNet,
				AllowReadFolders: []string{"/tmp"},
			},
		},
	}

	r, err := NewRunner(nil, nil, cfgs, "/tmp", logger)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	if r.Type() != "sandbox-exec" {
		t.Skipf("runner resolved to %q instead of sandbox-exec (likely fallback)", r.Type())
	}

	rec := cap.findAdvisory()
	if rec == nil {
		t.Fatalf("expected additive-permits advisory WARN, got none. records=%d", len(cap.records))
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("expected advisory at WARN level, got %s", rec.Level)
	}
	attrs := attrMap(rec)
	if got, want := attrs["runner_type"], "sandbox-exec"; got != want {
		t.Errorf("runner_type: got %v, want %v", got, want)
	}
	if got, want := attrs["has_allow_read_folders"], true; got != want {
		t.Errorf("has_allow_read_folders: got %v, want %v", got, want)
	}
	// $MITTO_WORKING_DIR is auto-injected into AllowWriteFolders by NewRunner
	// for any non-exec runner, so has_allow_write_folders is expected true.
	if got, want := attrs["has_allow_write_folders"], true; got != want {
		t.Errorf("has_allow_write_folders: got %v, want %v", got, want)
	}
	if ref, ok := attrs["doc_ref"].(string); !ok || !strings.Contains(ref, "docs/config/restricted.md#semantics-additive-permits-vs-whitelist") {
		t.Errorf("doc_ref: got %v, want stable docs anchor", attrs["doc_ref"])
	}
}

// TestNewRunner_Exec_DoesNotWarn — the exec runner must never emit the advisory
// even when allow_* lists are present (they are inert for exec). Portable.
func TestNewRunner_Exec_DoesNotWarn(t *testing.T) {
	logger, cap := newTestLogger()
	cfgs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "exec",
			Restrictions: &config.RunnerRestrictions{
				AllowReadFolders:  []string{"/tmp"},
				AllowWriteFolders: []string{"/tmp"},
			},
		},
	}
	r, err := NewRunner(nil, nil, cfgs, "/tmp", logger)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	if r.Type() != "exec" {
		t.Fatalf("expected exec runner, got %q", r.Type())
	}
	if rec := cap.findAdvisory(); rec != nil {
		t.Errorf("exec runner must not emit additive-permits advisory, got: %q", rec.Message)
	}
}

// TestNewRunner_NilLogger_DoesNotPanic guards the `logger != nil` gate on the
// advisory site: nil logger must not crash even on the sandbox-exec path.
func TestNewRunner_NilLogger_DoesNotPanic(t *testing.T) {
	cfgs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "sandbox-exec",
			Restrictions: &config.RunnerRestrictions{
				AllowReadFolders: []string{"/tmp"},
			},
		},
	}
	if _, err := NewRunner(nil, nil, cfgs, "/tmp", nil); err != nil {
		t.Fatalf("NewRunner with nil logger failed: %v", err)
	}
}
