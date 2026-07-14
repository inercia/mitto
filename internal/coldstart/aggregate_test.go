package coldstart

import (
	"testing"
	"time"
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		outcome string
		phase   string
		want    OutcomeClass
	}{
		{"ready", "", ClassSuccess},
		{"ok", "", ClassSuccess},
		{"session_new_failed", "", ClassNewFailed},
		{"shared_resume_failed", "", ClassResumeFailed},
		{"shared_resume_retry_failed", "", ClassResumeFailed},
		{"shared_restart_failed", "", ClassHandshakeFailed},
		{"shared_prepare_failed", "", ClassHandshakeFailed},
		{"acp_start_failed", "", ClassHandshakeFailed},
		{"deferred_handshake_failed", "", ClassTimeout},
		{"initialize_failed", "", ClassHandshakeFailed},
		{"spawn_failed", "", ClassHandshakeFailed},
		{"prompt_failed", "", ClassTimeout},
		{"", "mcp_init_wait_begin", ClassGateWait},
		{"", "session_load_stale", ClassStale},
		{"", "session_new", ClassOther},
		{"", "", ClassOther},
		{"weird_new_outcome", "", ClassOther},
		{"ready", "session_load_stale", ClassSuccess}, // outcome wins over phase
	}
	for _, tc := range cases {
		got := ClassifyOutcome(tc.outcome, tc.phase)
		if got != tc.want {
			t.Errorf("ClassifyOutcome(%q,%q) = %q, want %q", tc.outcome, tc.phase, got, tc.want)
		}
	}
}

func TestIsFailureOutcome(t *testing.T) {
	if IsFailureOutcome("ready") {
		t.Error("ready should not be failure")
	}
	if IsFailureOutcome("ok") {
		t.Error("ok should not be failure")
	}
	for _, o := range []string{"session_new_failed", "shared_resume_failed", "spawn_failed"} {
		if !IsFailureOutcome(o) {
			t.Errorf("%q should be failure", o)
		}
	}
}

func TestAggregateByWorkspaceEmpty(t *testing.T) {
	got := AggregateByWorkspace([]Summary{})
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected len 0, got %d", len(got))
	}
}

func TestAggregateByWorkspaceBasic(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	sums := []Summary{
		{WorkspaceUUID: "ws-A", Outcome: "ready", TotalMs: 100, At: base.Add(1 * time.Minute)},
		{WorkspaceUUID: "ws-A", Outcome: "ready", TotalMs: 200, At: base.Add(2 * time.Minute)},
		{WorkspaceUUID: "ws-A", Outcome: "session_new_failed", TotalMs: 300, At: base.Add(3 * time.Minute)},
		{WorkspaceUUID: "ws-A", Outcome: "ok", TotalMs: 400, At: base.Add(4 * time.Minute)},
		{WorkspaceUUID: "ws-A", Outcome: "shared_resume_failed", TotalMs: 500,
			Phases: []PhaseRecord{{Name: "session_load_stale"}}, At: base.Add(5 * time.Minute)},
		{WorkspaceUUID: "ws-B", Outcome: "ready", TotalMs: 50, At: base.Add(10 * time.Minute)},
	}

	out := AggregateByWorkspace(sums)
	if len(out) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(out))
	}

	byUUID := map[string]WorkspaceColdStats{}
	for _, s := range out {
		byUUID[s.WorkspaceUUID] = s
	}

	a, ok := byUUID["ws-A"]
	if !ok {
		t.Fatal("missing ws-A")
	}
	if a.Total != 5 || a.Failures != 2 {
		t.Errorf("ws-A totals: got Total=%d Failures=%d, want 5,2", a.Total, a.Failures)
	}
	if a.FailureRate != 2.0/5.0 {
		t.Errorf("ws-A FailureRate = %v, want 0.4", a.FailureRate)
	}
	if a.LastOutcome != "shared_resume_failed" {
		t.Errorf("ws-A LastOutcome = %q, want shared_resume_failed", a.LastOutcome)
	}
	if !a.LastAt.Equal(base.Add(5 * time.Minute)) {
		t.Errorf("ws-A LastAt = %v, want %v", a.LastAt, base.Add(5*time.Minute))
	}
	if a.LastClass != ClassResumeFailed {
		t.Errorf("ws-A LastClass = %q, want %q (outcome must win over stale phase)",
			a.LastClass, ClassResumeFailed)
	}

	b, ok := byUUID["ws-B"]
	if !ok {
		t.Fatal("missing ws-B")
	}
	if b.Total != 1 || b.Failures != 0 || b.FailureRate != 0 {
		t.Errorf("ws-B totals: got Total=%d Failures=%d Rate=%v, want 1,0,0",
			b.Total, b.Failures, b.FailureRate)
	}
	if b.LastOutcome != "ready" {
		t.Errorf("ws-B LastOutcome = %q, want ready", b.LastOutcome)
	}
}

func TestAggregateByWorkspacePercentiles(t *testing.T) {
	base := time.Now()
	sums := make([]Summary, 10)
	for i := 0; i < 10; i++ {
		sums[i] = Summary{
			WorkspaceUUID: "ws-P",
			Outcome:       "ready",
			TotalMs:       int64((i + 1) * 10),
			At:            base.Add(time.Duration(i) * time.Second),
		}
	}
	out := AggregateByWorkspace(sums)
	if len(out) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(out))
	}
	if out[0].P50Ms != 50 {
		t.Errorf("P50 = %d, want 50", out[0].P50Ms)
	}
	if out[0].P95Ms != 100 {
		t.Errorf("P95 = %d, want 100", out[0].P95Ms)
	}

	single := AggregateByWorkspace([]Summary{
		{WorkspaceUUID: "ws-Q", Outcome: "ready", TotalMs: 42, At: base},
	})
	if len(single) != 1 || single[0].P50Ms != 42 || single[0].P95Ms != 42 {
		t.Errorf("single-entry percentiles = %+v, want P50==P95==42", single)
	}
}

func TestAggregateByWorkspaceOrdering(t *testing.T) {
	base := time.Now()
	sums := []Summary{
		// ws-low: 1 fail / 4 total = 0.25
		{WorkspaceUUID: "ws-low", Outcome: "ready", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-low", Outcome: "ready", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-low", Outcome: "ready", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-low", Outcome: "session_new_failed", TotalMs: 10, At: base},
		// ws-hi: 3 fail / 4 total = 0.75
		{WorkspaceUUID: "ws-hi", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-hi", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-hi", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-hi", Outcome: "ready", TotalMs: 10, At: base},
		// ws-tie: 3 fail / 4 total = 0.75, same rate as ws-hi, smaller Total tiebreak (loses)
		{WorkspaceUUID: "ws-tie", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-tie", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-tie", Outcome: "session_new_failed", TotalMs: 10, At: base},
		{WorkspaceUUID: "ws-tie", Outcome: "ready", TotalMs: 10, At: base},
	}
	out := AggregateByWorkspace(sums)
	if len(out) != 3 {
		t.Fatalf("expected 3 workspaces, got %d", len(out))
	}
	if out[0].WorkspaceUUID != "ws-hi" {
		t.Errorf("out[0] = %q, want ws-hi (highest rate + higher total)", out[0].WorkspaceUUID)
	}
	if out[1].WorkspaceUUID != "ws-tie" {
		t.Errorf("out[1] = %q, want ws-tie", out[1].WorkspaceUUID)
	}
	if out[2].WorkspaceUUID != "ws-low" {
		t.Errorf("out[2] = %q, want ws-low", out[2].WorkspaceUUID)
	}
}
