package beads

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// MigrateLocal — local mode must run exactly one migration command, bypassing
// the gate for a preserved dormant remote without any publish/bootstrap step.
// ---------------------------------------------------------------------------

func TestMigrateLocal_MigrationOnly(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{{stdout: []byte(`{"applied":2}`)}}}
	out, err := newClient(r).MigrateLocal(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("MigrateLocal() error = %v", err)
	}
	if string(out) != `{"applied":2}` {
		t.Errorf("out = %s, want migration JSON", out)
	}
	if len(r.calls) != 1 {
		t.Fatalf("runner calls = %d, want exactly 1", len(r.calls))
	}
	if got, want := strings.Join(r.calls[0].args, " "), "migrate schema --json"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if got, want := strings.Join(r.calls[0].env, " "), "BD_ALLOW_REMOTE_MIGRATE=1"; got != want {
		t.Errorf("env = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// MigrateRemote — mitto-cq2n.1: publish-stage (bd dolt push) failures must be
// distinguishable from migrate-stage (bd migrate schema) failures via
// CmdError.Stage / IsPublishFailure, while still returning a non-nil error
// (a failed publish is never reported as an overall success).
// ---------------------------------------------------------------------------

func TestMigrateRemote_MigrateStageFailure_NotPublishFailure(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stderr: "Error: schema migration exploded", err: errors.New("bd exited with non-zero status")},
	}}
	c := newClient(r)

	out, err := c.MigrateRemote(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("MigrateRemote() error = nil, want non-nil (migrate step failed)")
	}
	if out != nil {
		t.Errorf("out = %v, want nil on a migrate-stage failure", out)
	}
	if IsPublishFailure(err) {
		t.Error("IsPublishFailure(err) = true, want false for a migrate-stage failure")
	}
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("error is not a *CmdError: %v", err)
	}
	if ce.Stage != "" {
		t.Errorf("Stage = %q, want empty for a migrate-stage failure", ce.Stage)
	}
	if ce.Stderr != "Error: schema migration exploded" {
		t.Errorf("Stderr = %q, want the captured diagnostic", ce.Stderr)
	}
	// Only one call: the runner must never reach step 2 (bd dolt push) when
	// step 1 already failed.
	if len(r.calls) != 1 {
		t.Errorf("runner calls = %d, want 1 (must not attempt dolt push after a migrate-stage failure)", len(r.calls))
	}
}

func TestMigrateRemote_PublishStageFailure_IsPublishFailure(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(`{"applied":4,"from":49,"to":53}`)}, // step 1: bd migrate schema --json succeeds
		{stderr: "Error: push to origin/main: Error 1105: failed to get remote db; ERROR: Repository not found.", err: errors.New("bd exited with non-zero status")},
	}}
	c := newClient(r)

	out, err := c.MigrateRemote(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("MigrateRemote() error = nil, want non-nil — a failed publish must never be reported as overall success")
	}
	if !IsPublishFailure(err) {
		t.Error("IsPublishFailure(err) = false, want true for a bd-dolt-push failure")
	}
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("error is not a *CmdError: %v", err)
	}
	if ce.Stage != StagePublish {
		t.Errorf("Stage = %q, want %q", ce.Stage, StagePublish)
	}
	if want := "Error: push to origin/main: Error 1105: failed to get remote db; ERROR: Repository not found."; ce.Stderr != want {
		t.Errorf("Stderr = %q, want %q (preserved from the dolt-push step)", ce.Stderr, want)
	}
	// The step-1 (successful) migrate output must still be returned so the
	// caller can report exactly what the local migration applied.
	if string(out) != `{"applied":4,"from":49,"to":53}` {
		t.Errorf("out = %s, want the step-1 stdout preserved despite the step-2 failure", out)
	}
	if len(r.calls) != 2 {
		t.Errorf("runner calls = %d, want 2 (migrate schema, then dolt push)", len(r.calls))
	}
}

func TestMigrateRemote_Success_NoStage(t *testing.T) {
	r := &recordingRunner{responses: []runnerResp{
		{stdout: []byte(`{"applied":4}`)},
		{stdout: []byte(`up to date`)},
	}}
	c := newClient(r)

	out, err := c.MigrateRemote(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("MigrateRemote() error = %v, want nil", err)
	}
	if string(out) != `{"applied":4}` {
		t.Errorf("out = %s, want step-1 stdout", out)
	}
}

// TestIsPublishFailure_NonCmdError verifies IsPublishFailure fails safe (does
// not panic and returns false) for a nil error and for an error that is not
// a *CmdError at all — e.g. a context.DeadlineExceeded.
func TestIsPublishFailure_NonCmdError(t *testing.T) {
	if IsPublishFailure(nil) {
		t.Error("IsPublishFailure(nil) = true, want false")
	}
	if IsPublishFailure(errors.New("plain error")) {
		t.Error("IsPublishFailure(plain error) = true, want false")
	}
	if IsPublishFailure(context.DeadlineExceeded) {
		t.Error("IsPublishFailure(context.DeadlineExceeded) = true, want false")
	}
}
