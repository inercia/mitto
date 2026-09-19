package conversation

// mitto-mx9.3: parity coverage for the two intentional duplicate translators
// in backend_provider_acp.go (acpLeaseStopReasonToNeutral/FromNeutral,
// acpCapabilities) against their internal/acpbackend originals
// (ToNeutralStopReason in outcome.go, sessionCapabilities in
// capabilities.go).
//
// backend_provider_acp_test.go already pins acpLeaseStopReasonToNeutral,
// acpLeaseStopReasonFromNeutral, promptOutcomeToACPResponse, and
// acpCapabilities.Query against literal truth tables identical to
// internal/acpbackend's own tests (TestAcpLeaseStopReasonToNeutral_AllCases,
// TestAcpLeaseStopReasonFromNeutral_AllCases,
// TestPromptOutcomeToACPResponse_TokenAccountingParity/
// _NoUsageReported, TestACPCapabilities_Query) — that IS this bead's
// practical substitute for a literal cross-package parity test: a true
// side-by-side call of both packages' unexported functions is impossible
// (internal/acpbackend already imports internal/conversation, so this
// package cannot import it back without a cycle; the functions are also
// unexported, so even an external `conversation_test` package could not
// call them regardless of cycle legality). This file adds two things not
// already covered:
//
//  1. A round-trip test for the ToNeutral/FromNeutral stop-reason pair.
//  2. TestAcpCapabilities_Query_ParityWithSessionCapabilities_mitto_mx9_8,
//     which pins acpCapabilities.Query's answers for FeatureFiles,
//     FeaturePermissions, and empty-catalog ModelSelection/ModeSelection to
//     be identical to acpbackend's sessionCapabilities.Query — mitto-mx9.8
//     fixed three real behavioral divergences between the two "mirror"
//     translators that this test phase discovered (see the fix commit for
//     the before/after values).

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// TestAcpLeaseStopReasonFromNeutral_RoundTrip pins the documented lossy
// exception in acpLeaseStopReasonFromNeutral's doc-comment: every neutral
// value round-trips through ToNeutral(FromNeutral(x)) == x.
func TestAcpLeaseStopReasonFromNeutral_RoundTrip(t *testing.T) {
	for _, neutral := range []agentbackend.StopReason{
		agentbackend.StopReasonEndTurn,
		agentbackend.StopReasonCancelled,
		agentbackend.StopReasonMaxTokens,
		agentbackend.StopReasonRefusal,
	} {
		acpVal := acpLeaseStopReasonFromNeutral(neutral)
		if got := acpLeaseStopReasonToNeutral(acpVal); got != neutral {
			t.Errorf("round-trip via %q: got %q, want %q", acpVal, got, neutral)
		}
	}
}

// TestAcpCapabilities_Query_ParityWithSessionCapabilities_mitto_mx9_8 pins
// acpCapabilities.Query (this package) to answer the SAME canonical values
// as acpbackend's sessionCapabilities.Query
// (internal/acpbackend/capabilities.go) for the shared feature set — the
// values also independently chosen by internal/web/handlers/neutral_dto.go's
// neutralCapabilities. Before mitto-mx9.8's fix, acpCapabilities.Query
// diverged on all four assertions below (no Files/Permissions special
// case → Unknown instead of Supported; nil-pointer-only checks for
// Model/ModeSelection → Supported instead of Unsupported for a
// non-nil-but-empty catalog).
func TestAcpCapabilities_Query_ParityWithSessionCapabilities_mitto_mx9_8(t *testing.T) {
	c := &acpCapabilities{
		handle:      &SessionHandle{Models: &SessionModelState{}, Modes: &agentbackend.ModeState{}},
		processCaps: NewProcessCapabilities(&acp.AgentCapabilities{}),
	}

	// Canonical: Files/Permissions are constant host facts (Mitto's ACP
	// handshake always advertises Fs read/write, and always wires an
	// auto-approving permission handler) — sessionCapabilities hardcodes
	// both Supported; acpCapabilities must match.
	if got := c.Query(agentbackend.FeatureFiles); got != agentbackend.CapabilitySupported {
		t.Errorf("Files = %v, want Supported (parity with acpbackend's sessionCapabilities)", got)
	}
	if got := c.Query(agentbackend.FeaturePermissions); got != agentbackend.CapabilitySupported {
		t.Errorf("Permissions = %v, want Supported (parity with acpbackend's sessionCapabilities)", got)
	}

	// Canonical: a non-nil-but-empty catalog means the feature is
	// definitively NOT available (Unsupported), not merely Unknown/guessed
	// Supported — sessionCapabilities requires len(...) > 0.
	if got := c.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("ModelSelection (empty catalog) = %v, want Unsupported (parity with acpbackend's sessionCapabilities len()>0 check)", got)
	}
	if got := c.Query(agentbackend.FeatureModeSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("ModeSelection (empty catalog) = %v, want Unsupported (parity with acpbackend's sessionCapabilities len()>0 check)", got)
	}
}
