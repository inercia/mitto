package agentbackend

import "testing"

func TestPermissionDecision_UnknownFailsClosed(t *testing.T) {
	var d PermissionDecision // zero value = PermissionUnknown
	if d != PermissionUnknown {
		t.Fatalf("zero value = %v, want PermissionUnknown", d)
	}
	if d.Approved() {
		t.Fatal("PermissionUnknown.Approved() = true, want false (fail closed)")
	}
}

func TestPermissionDecision_DeniedFailsClosed(t *testing.T) {
	if PermissionDenied.Approved() {
		t.Fatal("PermissionDenied.Approved() = true, want false")
	}
}

func TestPermissionDecision_OnlyApprovedGrants(t *testing.T) {
	if !PermissionApproved.Approved() {
		t.Fatal("PermissionApproved.Approved() = false, want true")
	}
}

func TestPermissionFence_AcceptsCurrentGeneration(t *testing.T) {
	f := &PermissionFence{}
	gen := f.Next()
	if !f.Accept(gen) {
		t.Fatal("Accept(current generation) = false, want true")
	}
}

func TestPermissionFence_RejectsSupersededGeneration(t *testing.T) {
	// Models a responder that answers an earlier, now-superseded request
	// (e.g. one that raced a reconnect which re-issued the same logical
	// permission request under a new generation).
	f := &PermissionFence{}
	stale := f.Next()
	fresh := f.Next()
	if f.Accept(stale) {
		t.Fatal("Accept(stale generation) = true, want false (superseded response must be dropped)")
	}
	if !f.Accept(fresh) {
		t.Fatal("Accept(fresh generation) = false, want true")
	}
}

func TestPermissionFence_RejectsZeroGeneration(t *testing.T) {
	// gen 0 never comes from Next (which starts at 1), so it must never be
	// accepted even before any request has been issued — a caller that
	// forgot to call Next (or lost track of its generation) must not be
	// able to smuggle in an implicit approval via the zero value.
	f := &PermissionFence{}
	if f.Accept(0) {
		t.Fatal("Accept(0) = true, want false")
	}
	f.Next()
	if f.Accept(0) {
		t.Fatal("Accept(0) = true after Next(), want false")
	}
}

func TestPermissionFence_CompetingResponders_OnlyOneWins(t *testing.T) {
	// Models two competing responders answering the SAME outstanding
	// request: only the one tagged with the still-current generation may be
	// honored, regardless of arrival order.
	f := &PermissionFence{}
	gen := f.Next()

	// A duplicate responder for the same request also tags gen; both are
	// accepted (this fence doesn't dedupe by call count, only by
	// generation) — dedup-by-first-writer is the caller's job once Accept
	// says the generation is still live.
	if !f.Accept(gen) {
		t.Fatal("first responder: Accept(gen) = false, want true")
	}
	if !f.Accept(gen) {
		t.Fatal("second responder for the same still-current gen: Accept(gen) = false, want true")
	}

	// Once a new request supersedes gen, neither an old nor a duplicate
	// stale responder may win anymore.
	f.Next()
	if f.Accept(gen) {
		t.Fatal("Accept(gen) after supersession = true, want false")
	}
}
