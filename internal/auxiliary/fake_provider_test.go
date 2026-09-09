package auxiliary

import (
	"context"
	"fmt"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

// fakeNonProcessProvider is a non-process ProcessProvider used to prove that
// auxiliary dispatch works through the ProcessProvider seam with NO process,
// PID, GC, or pin assumptions (mitto-lrt.10 acceptance criteria: "Non-process
// fake exercises auxiliary dispatch or a deliberate unsupported outcome with
// no process pin/GC assumptions"). Unlike acpproc.ACPProcessManager, this
// fake never spawns anything and never touches a workspace's ACP process
// lifecycle — it is pure in-memory bookkeeping. unsupportedPurposes lists
// purposes this fake deliberately refuses, returning an
// *agentbackend.UnsupportedError instead of faking success.
type fakeNonProcessProvider struct {
	responses           map[string]string
	unsupportedPurposes map[string]bool
	closed              []string
	calls               []string
}

func newFakeNonProcessProvider() *fakeNonProcessProvider {
	return &fakeNonProcessProvider{
		responses:           make(map[string]string),
		unsupportedPurposes: make(map[string]bool),
	}
}

func (p *fakeNonProcessProvider) PromptAuxiliary(_ context.Context, _, purpose, _ string) (string, error) {
	p.calls = append(p.calls, purpose)
	if p.unsupportedPurposes[purpose] {
		return "", &agentbackend.UnsupportedError{Feature: agentbackend.Feature(purpose)}
	}
	if resp, ok := p.responses[purpose]; ok {
		return resp, nil
	}
	return "", fmt.Errorf("fakeNonProcessProvider: no response configured for purpose %q", purpose)
}

func (p *fakeNonProcessProvider) PromptAuxiliaryAsync(ctx context.Context, workspaceUUID, purpose, message string) error {
	_, err := p.PromptAuxiliary(ctx, workspaceUUID, purpose, message)
	return err
}

func (p *fakeNonProcessProvider) CloseWorkspaceAuxiliary(workspaceUUID string) error {
	p.closed = append(p.closed, workspaceUUID)
	return nil
}

// SupportsAuxiliaryPurpose implements the optional CapabilityProvider seam.
func (p *fakeNonProcessProvider) SupportsAuxiliaryPurpose(purpose string) bool {
	return !p.unsupportedPurposes[purpose]
}

var (
	_ ProcessProvider    = (*fakeNonProcessProvider)(nil)
	_ CapabilityProvider = (*fakeNonProcessProvider)(nil)
)

// TestFakeNonProcessProvider_DispatchesAuxiliaryWork proves a non-process
// backend can service ordinary auxiliary dispatch (here, title generation)
// through WorkspaceAuxiliaryManager with no process/PID/GC/pin concept
// anywhere in the fake.
func TestFakeNonProcessProvider_DispatchesAuxiliaryWork(t *testing.T) {
	provider := newFakeNonProcessProvider()
	provider.responses[PurposeImprovePrompt] = "A clearer, more specific prompt."

	mgr := NewWorkspaceAuxiliaryManager(provider, nil)

	got, err := mgr.ImprovePrompt(context.Background(), "ws-1", "do the thing")
	if err != nil {
		t.Fatalf("ImprovePrompt() error = %v", err)
	}
	if got != "A clearer, more specific prompt." {
		t.Errorf("ImprovePrompt() = %q, want %q", got, "A clearer, more specific prompt.")
	}
	if len(provider.calls) != 1 || provider.calls[0] != PurposeImprovePrompt {
		t.Errorf("provider.calls = %v, want single call for purpose %q", provider.calls, PurposeImprovePrompt)
	}
}

// TestFakeNonProcessProvider_DeliberateUnsupportedOutcome proves a
// non-process backend can deliberately decline an auxiliary purpose, and
// that the resulting error classifies as OutcomeUnsupported (permanent, not
// retryable) via ClassifyOutcome — distinguishing it from a transient
// busy/saturated condition.
func TestFakeNonProcessProvider_DeliberateUnsupportedOutcome(t *testing.T) {
	provider := newFakeNonProcessProvider()
	provider.unsupportedPurposes[PurposeImprovePrompt] = true

	if provider.SupportsAuxiliaryPurpose(PurposeImprovePrompt) {
		t.Fatal("SupportsAuxiliaryPurpose() = true, want false for a deliberately unsupported purpose")
	}

	mgr := NewWorkspaceAuxiliaryManager(provider, nil)

	_, err := mgr.ImprovePrompt(context.Background(), "ws-1", "do the thing")
	if err == nil {
		t.Fatal("ImprovePrompt() error = nil, want an unsupported-outcome error")
	}
	if got := ClassifyOutcome(err); got != OutcomeUnsupported {
		t.Errorf("ClassifyOutcome(err) = %v, want %v (err = %v)", got, OutcomeUnsupported, err)
	}
}
