package conversation

import (
	"testing"
	"time"
)

// TestTitleRetryPolicy_DefaultCadence pins the production retry schedule so a
// future refactor cannot silently change it (mitto-1vi).
func TestTitleRetryPolicy_DefaultCadence(t *testing.T) {
	policy := DefaultTitleRetryPolicy()
	if policy.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3", policy.MaxRetries)
	}
	if policy.BaseDelay != 30*time.Second {
		t.Fatalf("BaseDelay = %v, want 30s", policy.BaseDelay)
	}

	// attempt 0 is the initial attempt (no backoff); later attempts clamp at
	// MaxRetries so the process-busy retention path stays capped at 120s.
	want := []time.Duration{0, 30 * time.Second, 60 * time.Second, 120 * time.Second, 120 * time.Second}
	for attempt, wantDelay := range want {
		if got := policy.delay(attempt); got != wantDelay {
			t.Errorf("delay(%d) = %v, want %v", attempt, got, wantDelay)
		}
	}
}

// TestTitleRetryPolicy_NilNormalizesToDefaults verifies that callers which do
// not override the cadence (nil or zero policy) get the production schedule.
func TestTitleRetryPolicy_NilNormalizesToDefaults(t *testing.T) {
	var nilPolicy *TitleRetryPolicy
	if got, want := nilPolicy.normalized(), DefaultTitleRetryPolicy(); got != want {
		t.Fatalf("(*TitleRetryPolicy)(nil).normalized() = %+v, want %+v", got, want)
	}

	var zero TitleRetryPolicy
	if got, want := zero.normalized(), DefaultTitleRetryPolicy(); got != want {
		t.Fatalf("zero policy normalized() = %+v, want %+v", got, want)
	}
}

// TestTitleJob_NilIsSafe verifies the nil handle returned when no goroutine is
// started never blocks a caller that waits on it.
func TestTitleJob_NilIsSafe(t *testing.T) {
	var job *TitleJob
	job.Wait()
	select {
	case <-job.Done():
	case <-time.After(time.Second):
		t.Fatal("nil TitleJob.Done() did not yield a closed channel")
	}
}
