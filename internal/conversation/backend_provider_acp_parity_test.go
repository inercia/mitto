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
//  2. A test documenting two real behavioral DISCREPANCIES this test phase
//     discovered between acpCapabilities and acpbackend's
//     sessionCapabilities, despite both being commented as "mirrors" of
//     each other — filed here for the Review phase to triage.

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

// TestAcpCapabilities_Query_DiscrepanciesFromAcpbackendSessionCapabilities
// documents two behavioral differences this test phase discovered between
// acpCapabilities (this package) and acpbackend's sessionCapabilities,
// despite both being commented as "mirrors" of each other. Filed here for
// the Review phase to triage (fix now vs. follow-up bead) rather than
// silently papered over — mx9.3's acceptance criteria required the
// duplicates to be "pinned" against each other for the shared feature set,
// not that all features behave identically, so this is not treated as a
// blocking regression, but the discrepancy is real and worth a look:
//
//  1. FeatureFiles/FeaturePermissions: acpbackend's sessionCapabilities
//     hardcodes both as CapabilitySupported (a constant host fact,
//     independent of any inner capabilities snapshot). acpCapabilities has
//     no such special case for either feature — both fall through to
//     `default` and delegate to processCaps, which (acpProcessCapabilities)
//     doesn't recognize them either and returns CapabilityUnknown.
//  2. Empty-but-non-nil catalog: acpCapabilities treats "handle.Models is a
//     non-nil pointer" as sufficient for ModelSelection=Supported, even if
//     AvailableModels is empty. acpbackend's sessionCapabilities instead
//     requires len(AvailableModels) > 0, reporting Unsupported for a
//     non-nil-but-empty catalog. Same asymmetry for Modes/ModeSelection.
func TestAcpCapabilities_Query_DiscrepanciesFromAcpbackendSessionCapabilities(t *testing.T) {
	c := &acpCapabilities{
		handle:      &SessionHandle{Models: &SessionModelState{}, Modes: &agentbackend.ModeState{}},
		processCaps: NewProcessCapabilities(&acp.AgentCapabilities{}),
	}

	// Discrepancy 1: unlike sessionCapabilities, Files/Permissions are NOT
	// hardcoded Supported here — they delegate and come back Unknown.
	if got := c.Query(agentbackend.FeatureFiles); got != agentbackend.CapabilityUnknown {
		t.Errorf("Files = %v, want Unknown (acpCapabilities has no Files special-case, unlike acpbackend's sessionCapabilities)", got)
	}
	if got := c.Query(agentbackend.FeaturePermissions); got != agentbackend.CapabilityUnknown {
		t.Errorf("Permissions = %v, want Unknown (acpCapabilities has no Permissions special-case, unlike acpbackend's sessionCapabilities)", got)
	}

	// Discrepancy 2: a non-nil-but-empty catalog reports Supported here,
	// where acpbackend's sessionCapabilities would report Unsupported.
	if got := c.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilitySupported {
		t.Errorf("ModelSelection (empty catalog) = %v, want Supported (nil-pointer-only check, unlike acpbackend's len()>0 check)", got)
	}
}
