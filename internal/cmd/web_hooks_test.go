package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWebSkipsUpHookWhenExternalListenerAbsent reproduces mitto-qljn: the
// `up` hook (and, transitively, anything it launches — e.g. a Cloudflare
// tunnel) must NOT run when the external listener did not start. Today it
// runs unconditionally whenever web.hooks.up.command is configured, using
// whatever "hookPort" falls back to (the local, auth-exempt 127.0.0.1
// listener) — see internal/cmd/web.go:
//
//	hookPort := actualPort
//	if actualExternalPort > 0 {
//	    hookPort = actualExternalPort
//	}
//	...
//	upHook = hooks.StartUp(cfg.Web.Hooks.Up, hookPort, onFailure)  // fires unconditionally
//
// This spawns a real "mitto web" subprocess (mirroring
// TestWebRejectsUnauthenticatedNonLoopbackBind's re-exec pattern) with
// external access intentionally disabled (--port-external -1) and an up
// hook that writes a marker file. The marker file must NEVER appear.
func TestWebSkipsUpHookWhenExternalListenerAbsent(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "up-hook-fired.marker")
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `acp:
  - test:
      command: "true"
web:
  hooks:
    up:
      command: "printf fired > \"$MITTO_QLJN_MARKER\""
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWebUpHookHelper$")
	cmd.Env = append(os.Environ(),
		"MITTO_QLJN_HELPER=1",
		"MITTO_QLJN_CONFIG="+configPath,
		"MITTO_QLJN_MARKER="+markerFile,
		"MITTO_DIR="+t.TempDir(),
	)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper subprocess: %v", err)
	}

	// Poll for the marker file while the server starts up.
	deadline := time.Now().Add(5 * time.Second)
	markerSeen := false
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(markerFile); statErr == nil {
			markerSeen = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	_ = cmd.Wait()

	output := outBuf.String()
	if !strings.Contains(output, "Local URL:") {
		t.Fatalf("helper subprocess never reached a successful local listener startup "+
			"(cannot trust the negative result) — output:\n%s", output)
	}
	if markerSeen {
		t.Fatalf("mitto-qljn: up hook fired even though the external listener never started "+
			"(external access was intentionally disabled via --port-external -1) — the hook "+
			"must be skipped whenever there is no running external listener to point it at.\n"+
			"helper output:\n%s", output)
	}
}

// TestWebUpHookHelper is not a real test; it is re-executed as a subprocess
// by TestWebSkipsUpHookWhenExternalListenerAbsent to run "mitto web" with a
// configured up hook and external access disabled.
func TestWebUpHookHelper(t *testing.T) {
	if os.Getenv("MITTO_QLJN_HELPER") != "1" {
		return
	}

	rootCmd.SetArgs([]string{
		"web",
		"--config", os.Getenv("MITTO_QLJN_CONFIG"),
		"--host", "127.0.0.1",
		"--port", "0",
		"--port-external", "-1",
	})
	_ = Execute()
}
