package acpproc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

func TestCheckACPExecutable(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	ws := &config.WorkspaceSettings{UUID: "workspace-test", WorkingDir: "/work/project", ACPServer: "test-agent"}
	missing := "mitto-test-missing-acp-executable"

	tests := []struct {
		name    string
		command string
		runner  *runner.Runner
		wantErr bool
	}{
		{name: "missing bare executable", command: missing + " --flag", wantErr: true},
		{name: "runner bypass", command: missing, runner: &runner.Runner{}},
		{name: "path-bearing command bypass", command: "./" + missing},
		{name: "parse failure bypass", command: "'unterminated"},
		{name: "MITTO expansion identifies executable", command: "$MITTO_ACP_SERVER", wantErr: true},
	}
	ws.ACPServer = missing
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkACPExecutable(ws, tt.command, tt.runner)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("checkACPExecutable() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, mittoAcp.ErrACPExecutableUnavailable) {
				t.Fatalf("checkACPExecutable() error = %v, want unavailable sentinel", err)
			}
			var classified *mittoAcp.ACPClassifiedError
			if !errors.As(err, &classified) || classified.Class != mittoAcp.ACPErrorPermanent {
				t.Fatalf("error classification = %#v, want permanent ACPClassifiedError", classified)
			}
			for _, want := range []string{missing, "server", "workspace-test", "Mitto's process PATH", "absolute executable path"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestCheckACPExecutable_RecoveryAndQuotedExecutable(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	ws := &config.WorkspaceSettings{UUID: "workspace-recovery", WorkingDir: "/work", ACPServer: "test-agent"}
	executable := "agent with spaces"
	command := `"agent with spaces" --acp "$MITTO_WORKING_DIR"`

	if err := checkACPExecutable(ws, command, nil); !errors.Is(err, mittoAcp.ErrACPExecutableUnavailable) {
		t.Fatalf("missing executable error = %v, want unavailable sentinel", err)
	}
	path := filepath.Join(pathDir, executable)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkACPExecutable(ws, command, nil); err != nil {
		t.Fatalf("check after installing executable = %v, want nil", err)
	}
}

func TestACPProcessManager_MissingExecutableDoesNotConstructOrLogStartup(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	m := NewACPProcessManager(context.Background(), logger)
	defer m.Close()
	ws := &config.WorkspaceSettings{UUID: "workspace-missing", WorkingDir: t.TempDir(), ACPServer: "omp"}

	for range 3 {
		_, err := m.GetOrCreateProcess(ws, "mitto-test-manager-missing", ws.WorkingDir, nil, nil, true)
		if !errors.Is(err, mittoAcp.ErrACPExecutableUnavailable) {
			t.Fatalf("GetOrCreateProcess() error = %v, want unavailable sentinel", err)
		}
	}
	if got := m.ProcessCount(); got != 0 {
		t.Fatalf("ProcessCount() = %d, want 0", got)
	}
	for _, noisy := range []string{"Starting ACP process", "ACP process start failed", "GetOrCreateProcess failed to create process"} {
		if strings.Contains(logs.String(), noisy) {
			t.Errorf("startup log contains %q: %s", noisy, logs.String())
		}
	}
}
