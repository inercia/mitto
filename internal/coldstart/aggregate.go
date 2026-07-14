package coldstart

import (
	"math"
	"sort"
	"time"
)

// OutcomeClass buckets a cold-start Outcome (and its final phase) into a
// small set of diagnostic categories usable by aggregators, health checks,
// and UI grouping. Values are stable, machine-readable strings.
type OutcomeClass string

const (
	ClassSuccess         OutcomeClass = "success"
	ClassTimeout         OutcomeClass = "timeout"
	ClassNewFailed       OutcomeClass = "new_failed"
	ClassResumeFailed    OutcomeClass = "resume_failed"
	ClassHandshakeFailed OutcomeClass = "handshake_failed"
	ClassStale           OutcomeClass = "stale"
	ClassGateWait        OutcomeClass = "gate_wait"
	ClassOther           OutcomeClass = "other"
)

// ClassifyOutcome maps a cold-start (outcome, lastPhaseName) pair to a class.
// lastPhaseName may be "" when unknown; the classifier degrades gracefully.
func ClassifyOutcome(outcome, lastPhaseName string) OutcomeClass {
	switch outcome {
	case "ready", "ok":
		return ClassSuccess
	case "deferred_handshake_failed", "prompt_failed":
		return ClassTimeout
	case "session_new_failed":
		return ClassNewFailed
	case "shared_resume_failed", "shared_resume_retry_failed":
		return ClassResumeFailed
	case "acp_start_failed", "initialize_failed",
		"shared_prepare_failed", "shared_restart_failed", "spawn_failed":
		return ClassHandshakeFailed
	}
	switch lastPhaseName {
	case "mcp_init_wait_begin":
		return ClassGateWait
	case "session_load_stale":
		return ClassStale
	}
	return ClassOther
}

// IsFailureOutcome returns true when the outcome represents a failed cold
// start (i.e. not a success terminal outcome).
func IsFailureOutcome(outcome string) bool {
	return ClassifyOutcome(outcome, "") != ClassSuccess
}

// WorkspaceColdStats summarises cold-start behaviour for one workspace over
// the recent-summary window it was computed from.
type WorkspaceColdStats struct {
	WorkspaceUUID string       `json:"workspace_uuid"`
	Total         int          `json:"total"`
	Failures      int          `json:"failures"`
	FailureRate   float64      `json:"failure_rate"`
	P50Ms         int64        `json:"p50_ms"`
	P95Ms         int64        `json:"p95_ms"`
	LastOutcome   string       `json:"last_outcome"`
	LastClass     OutcomeClass `json:"last_class"`
	LastAt        time.Time    `json:"last_at"`
}

// AggregateByWorkspace groups summaries by WorkspaceUUID and returns one
// WorkspaceColdStats per UUID. Result is sorted highest FailureRate first,
// tiebreak by highest Total, then by WorkspaceUUID for determinism.
// Empty input returns a non-nil empty slice.
func AggregateByWorkspace(summaries []Summary) []WorkspaceColdStats {
	out := []WorkspaceColdStats{}
	if len(summaries) == 0 {
		return out
	}

	groups := make(map[string][]Summary)
	for _, s := range summaries {
		groups[s.WorkspaceUUID] = append(groups[s.WorkspaceUUID], s)
	}

	for uuid, items := range groups {
		total := len(items)
		failures := 0
		durations := make([]int64, 0, total)
		var last Summary
		for i, it := range items {
			if IsFailureOutcome(it.Outcome) {
				failures++
			}
			durations = append(durations, it.TotalMs)
			if i == 0 || it.At.After(last.At) {
				last = it
			}
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

		lastPhase := ""
		if n := len(last.Phases); n > 0 {
			lastPhase = last.Phases[n-1].Name
		}

		out = append(out, WorkspaceColdStats{
			WorkspaceUUID: uuid,
			Total:         total,
			Failures:      failures,
			FailureRate:   float64(failures) / float64(total),
			P50Ms:         percentileNearestRank(durations, 0.50),
			P95Ms:         percentileNearestRank(durations, 0.95),
			LastOutcome:   last.Outcome,
			LastClass:     ClassifyOutcome(last.Outcome, lastPhase),
			LastAt:        last.At,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].FailureRate != out[j].FailureRate {
			return out[i].FailureRate > out[j].FailureRate
		}
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].WorkspaceUUID < out[j].WorkspaceUUID
	})

	return out
}

// percentileNearestRank returns the nearest-rank percentile p (0..1) of the
// pre-sorted (ascending) values. Returns 0 for an empty slice.
func percentileNearestRank(sorted []int64, p float64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	return sorted[idx]
}
