package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain installs a deterministic fake `bd` so rendering prompts that call
// bead-inspecting template funcs (TerminalBeads, BeadHasLabels, ...) never
// shells out to a real beads database. Unknown subcommands fail, matching the
// fail-open behaviour of an environment without bd.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakebd")
	if err != nil {
		panic(err)
	}
	script := `#!/bin/sh
[ "$1" = "--version" ] && { echo "bd version 1.3.0"; exit 0; }
[ "$1" = "--readonly" ] && shift
case "$1" in
list)
  case "$*" in
  *"--label fixed"*) echo '[{"id":"fake-1","issue_type":"bug","status":"open"}]';;
  *) echo '[]';;
  esac;;
history) echo '[{"CommitDate":"2026-01-01T00:00:00Z","Issue":{"status":"open"}}]';;
show)
  case "$2" in
  mitto-driver-closed) echo '[{"id":"mitto-driver-closed","issue_type":"task","status":"closed","parent":"epic-1"}]';;
  epic-1) echo '[{"id":"epic-1","issue_type":"epic","status":"open"}]';;
  *) echo '[{"id":"fake-1","status":"open","labels":["fixed"],"issue_type":"bug","metadata":{"claimed_at":"2026-01-01T00:00:00Z"}}]';;
  esac;;
children)
  case "$2" in
  epic-1) echo '[{"id":"mitto-driver-closed","status":"closed"}]';;
  *) echo '[]';;
  esac;;
*) exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		panic(err)
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
