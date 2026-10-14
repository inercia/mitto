package cel

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeBd(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestTerminalBeads_ClassifiesStaleAndCurrent(t *testing.T) {
	fakeBd(t, `
[ "$1" = "--version" ] && { echo "bd version 1.3.0"; exit 0; }
shift # --readonly
case "$1 $2" in
"list --type") echo '[{"id":"a","issue_type":"bug"},{"id":"b","issue_type":"bug"}]';;
"list --status") echo '[]';;
"history a") echo '[{"CommitDate":"2026-01-02T00:00:00Z","Issue":{"status":"open"}},{"CommitDate":"2026-01-01T00:00:00Z","Issue":{"status":"closed"}}]';;
"history b") echo '[{"CommitDate":"2026-01-01T00:00:00Z","Issue":{"status":"open"}}]';;
"show a") echo '[{"status":"open","labels":["fixed"],"comments":[{"text":"Fix [tier: x] old","created_at":"2025-12-31T00:00:00Z"}]}]';;
"show b") echo '[{"status":"open","labels":["fixed"],"metadata":{"claimed_at":"t0"}}]';;
"children b") echo '[{"id":"c","status":"closed"}]';;
esac
`)
	got := terminalBeads("", true, true)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].Stale || got[0].ID != "a" {
		t.Errorf("a should be stale: %+v", got[0])
	}
	if got[1].Stale || !got[1].Closable() || got[1].WorkStarted != "t0" {
		t.Errorf("b should be closable with started: %+v", got[1])
	}
}

func TestReapableEpics(t *testing.T) {
	fakeBd(t, `
[ "$1" = "--version" ] && { echo "bd version 1.3.0"; exit 0; }
shift
case "$1 $2" in
"show leaf") echo '[{"id":"leaf","issue_type":"task","parent":"ep"}]';;
"show ep") echo '[{"id":"ep","issue_type":"epic","status":"open"}]';;
"children ep") echo '[{"id":"leaf","status":"open"},{"id":"x","status":"closed"}]';;
esac
`)
	if got := reapableEpics("", []string{"leaf"}, nil, 20); len(got) != 0 {
		t.Errorf("unexpected %v", got)
	}
	if got := reapableEpics("", []string{"leaf"}, []string{"leaf"}, 20); len(got) != 1 || got[0] != "ep" {
		t.Errorf("got %v", got)
	}
}

func TestTerminalLabelCurrent_ReopenedComments(t *testing.T) {
	for _, tc := range []struct {
		comment string
		want    bool
	}{
		{"2026-08-18T08:59:00Z", false},
		{"2026-08-18T10:01:00Z", true},
	} {
		fakeBd(t, `
shift
case "$1" in
history) echo '[{"CommitDate":"2026-08-18T10:00:00Z","Issue":{"status":"open"}},{"CommitDate":"2026-08-18T09:00:00Z","Issue":{"status":"closed"}}]';;
show) echo '[{"status":"open","labels":["fixed"],"comments":[{"created_at":"`+tc.comment+`","text":"Fix [tier: Coding]: x"}]}]';;
esac
`)
		if got, _ := terminalLabelCurrent("", "x", "fixed"); got != tc.want {
			t.Errorf("comment %s: current=%v want %v", tc.comment, got, tc.want)
		}
	}
}
