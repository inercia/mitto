package conversation

// bgsession_leaseops_test.go proves the mitto-mx9.1 hot-path routing at the
// BackgroundSession call sites themselves (flushContextInPlace's Prompt,
// cmSetSessionMode, cmSetSessionModel): when bs.lease exposes a working
// SessionOps, these methods MUST call through it instead of bs.sharedProcess
// directly, and MUST fall back to the pre-existing SharedProcess call
// byte-identically when leaseSessionOps() reports ok=false (no lease, or an
// unbound DeferSession lease). Cancel's routing (finishCancelledPromptTurn)
// shares the exact same bs.leaseSessionOps() helper exercised here and by
// backend_provider_acp_test.go's acpSessionPromptOps.Cancel tests, so it is
// not duplicated at the BackgroundSession level.

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

// recordingSessionPromptOps is a SessionPromptOps test double that records
// every call and returns preset responses/errors.
type recordingSessionPromptOps struct {
	promptCalls   []agentbackend.SessionRef
	promptContent [][]agentbackend.ContentBlock
	promptErr     error
	setModeCalls  []string
	setModeErr    error
	setModelCalls []string
	setModelErr   error
}

func (o *recordingSessionPromptOps) Prompt(_ context.Context, ref agentbackend.SessionRef, content []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error) {
	o.promptCalls = append(o.promptCalls, ref)
	o.promptContent = append(o.promptContent, content)
	return agentbackend.PromptOutcome{}, o.promptErr
}
func (o *recordingSessionPromptOps) Cancel(context.Context, agentbackend.SessionRef) error {
	return nil
}
func (o *recordingSessionPromptOps) SetModel(_ context.Context, _ agentbackend.SessionRef, modelID string) error {
	o.setModelCalls = append(o.setModelCalls, modelID)
	return o.setModelErr
}
func (o *recordingSessionPromptOps) SetMode(_ context.Context, _ agentbackend.SessionRef, modeID string) error {
	o.setModeCalls = append(o.setModeCalls, modeID)
	return o.setModeErr
}

// leaseWithOps is a minimal BackendLease test double whose SessionOps
// returns a preset (ops, ref, ok) triple, reusing spyBackendLease's no-op
// implementations for every other method this test never exercises.
type leaseWithOps struct {
	spyBackendLease
	ops SessionPromptOps
	ref agentbackend.SessionRef
	ok  bool
}

func (l *leaseWithOps) SessionOps() (SessionPromptOps, agentbackend.SessionRef, bool) {
	return l.ops, l.ref, l.ok
}

var _ BackendLease = (*leaseWithOps)(nil)

// TestCmSetSessionMode_LeaseBound_RoutesThroughSessionOps proves
// cmSetSessionMode calls ops.SetMode (not sharedProcess.SetSessionMode) when
// the lease reports a bound SessionOps.
func TestCmSetSessionMode_LeaseBound_RoutesThroughSessionOps(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &recordingSessionPromptOps{}
	ref := agentbackend.SessionRef{ProviderSession: "sess-1"}
	bs := &BackgroundSession{sharedProcess: proc, lease: &leaseWithOps{ops: ops, ref: ref, ok: true}, acpID: "acp-sess-1"}

	if err := bs.cmSetSessionMode(context.Background(), "plan"); err != nil {
		t.Fatalf("cmSetSessionMode: %v", err)
	}
	if len(ops.setModeCalls) != 1 || ops.setModeCalls[0] != "plan" {
		t.Fatalf("ops.setModeCalls = %v, want [plan]", ops.setModeCalls)
	}
	if len(proc.setModeCalls) != 0 {
		t.Errorf("sharedProcess.SetSessionMode calls = %v, want none (must route through SessionOps)", proc.setModeCalls)
	}
}

// TestCmSetSessionMode_NoLease_FallsBackToSharedProcess proves the
// pre-existing behavior is preserved byte-identically when bs.lease is nil.
func TestCmSetSessionMode_NoLease_FallsBackToSharedProcess(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	bs := &BackgroundSession{sharedProcess: proc, lease: nil, acpID: "acp-sess-1"}

	if err := bs.cmSetSessionMode(context.Background(), "plan"); err != nil {
		t.Fatalf("cmSetSessionMode: %v", err)
	}
	if len(proc.setModeCalls) != 1 || proc.setModeCalls[0].value != "plan" {
		t.Fatalf("sharedProcess.setModeCalls = %+v, want one call with value \"plan\"", proc.setModeCalls)
	}
}

// TestCmSetSessionModel_LeaseBound_RoutesThroughSessionOps mirrors the
// SetMode test above for cmSetSessionModel/SetModel.
func TestCmSetSessionModel_LeaseBound_RoutesThroughSessionOps(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &recordingSessionPromptOps{}
	ref := agentbackend.SessionRef{ProviderSession: "sess-1"}
	bs := &BackgroundSession{sharedProcess: proc, lease: &leaseWithOps{ops: ops, ref: ref, ok: true}, acpID: "acp-sess-1"}

	if err := bs.cmSetSessionModel(context.Background(), "gpt-5"); err != nil {
		t.Fatalf("cmSetSessionModel: %v", err)
	}
	if len(ops.setModelCalls) != 1 || ops.setModelCalls[0] != "gpt-5" {
		t.Fatalf("ops.setModelCalls = %v, want [gpt-5]", ops.setModelCalls)
	}
	if len(proc.setModelCalls) != 0 {
		t.Errorf("sharedProcess.SetSessionModel calls = %v, want none (must route through SessionOps)", proc.setModelCalls)
	}
}

// TestCmSetSessionMode_LeaseUnbound_FallsBackToSharedProcess proves an
// unbound lease (SessionOps ok=false, e.g. a DeferSession lease pre-Bind)
// falls back to the pre-existing SharedProcess call rather than erroring or
// silently dropping the request.
func TestCmSetSessionMode_LeaseUnbound_FallsBackToSharedProcess(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	bs := &BackgroundSession{sharedProcess: proc, lease: &leaseWithOps{ok: false}, acpID: "acp-sess-1"}

	if err := bs.cmSetSessionMode(context.Background(), "plan"); err != nil {
		t.Fatalf("cmSetSessionMode: %v", err)
	}
	if len(proc.setModeCalls) != 1 || proc.setModeCalls[0].value != "plan" {
		t.Fatalf("sharedProcess.setModeCalls = %+v, want one call with value \"plan\"", proc.setModeCalls)
	}
}

// TestFlushContextInPlace_LeaseBound_RoutesPromptThroughSessionOps proves
// flushContextInPlace's context-flush Prompt call routes through
// ops.Prompt with the flush command translated into a neutral text block,
// discarding the returned PromptOutcome (only err matters), and never calls
// sharedProcess.Prompt directly.
func TestFlushContextInPlace_LeaseBound_RoutesPromptThroughSessionOps(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &recordingSessionPromptOps{}
	ref := agentbackend.SessionRef{ProviderSession: "sess-1"}
	bs := &BackgroundSession{
		sharedProcess:       proc,
		lease:               &leaseWithOps{ops: ops, ref: ref, ok: true},
		acpID:               "acp-sess-1",
		contextFlushCommand: "/clear",
	}

	if err := bs.flushContextInPlace(context.Background()); err != nil {
		t.Fatalf("flushContextInPlace: %v", err)
	}
	if len(ops.promptCalls) != 1 || ops.promptCalls[0] != ref {
		t.Fatalf("ops.promptCalls = %v, want exactly [%+v]", ops.promptCalls, ref)
	}
	content := ops.promptContent[0]
	if len(content) != 1 || content[0].Text == nil || content[0].Text.Text != "/clear" {
		t.Fatalf("ops.promptContent[0] = %+v, want one text block \"/clear\"", content)
	}
	if len(proc.promptCalls) != 0 {
		t.Errorf("sharedProcess.Prompt calls = %d, want 0 (must route through SessionOps)", len(proc.promptCalls))
	}
}

// TestFlushContextInPlace_LeaseBound_PropagatesPromptError proves a Prompt
// error from the neutral seam is surfaced unchanged to the caller (the
// best-effort contract lives one level up in FlushContext, not here).
func TestFlushContextInPlace_LeaseBound_PropagatesPromptError(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	wantErr := errors.New("boom")
	ops := &recordingSessionPromptOps{promptErr: wantErr}
	bs := &BackgroundSession{
		sharedProcess:       proc,
		lease:               &leaseWithOps{ops: ops, ok: true},
		acpID:               "acp-sess-1",
		contextFlushCommand: "/clear",
	}

	if err := bs.flushContextInPlace(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("flushContextInPlace error = %v, want %v", err, wantErr)
	}
}
