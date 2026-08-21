package beads

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecRunner_BlocksUnsafeBDVersionBeforeWorkspaceCommand reproduces
// mitto-dz97: bd v1.2.1 must be rejected before it can open and migrate a
// workspace database.
func TestExecRunner_BlocksUnsafeBDVersionBeforeWorkspaceCommand(t *testing.T) {
	binDir := t.TempDir()
	callsPath := filepath.Join(binDir, "calls")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
  --version|-V|version) printf 'bd version 1.2.1 (test)\n' ;;
  *) printf '[]\n' ;;
esac
`, callsPath)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := NewClient().List(context.Background(), initializedDir(t))
	if err == nil {
		t.Error("List() error = nil, want unsafe bd version 1.2.1 rejection")
	} else if !strings.Contains(err.Error(), "1.2.1") {
		t.Errorf("List() error = %q, want diagnostic naming unsafe version 1.2.1", err)
	}

	calls, readErr := os.ReadFile(callsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "version") {
		t.Errorf("bd calls = %q, want only a version probe and no workspace command", lines)
	}
}
