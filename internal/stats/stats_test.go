package stats

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNoopStore_UpsertDeltas(t *testing.T) {
	store := &NoopStore{}
	if err := store.UpsertDeltas(context.Background(), []Delta{{Metric: MetricPrompts, Value: 1}}); err != nil {
		t.Errorf("NoopStore.UpsertDeltas() error = %v, want nil", err)
	}
}

func TestNoopStore_GetCursor(t *testing.T) {
	store := &NoopStore{}
	cur, err := store.GetCursor(context.Background(), "sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("NoopStore.GetCursor() error = %v, want ErrNotFound", err)
	}
	if cur.SessionID != "sess-1" {
		t.Errorf("NoopStore.GetCursor() SessionID = %q, want %q", cur.SessionID, "sess-1")
	}
	if cur.LastEventSeq != 0 || !cur.LastEventAt.IsZero() {
		t.Errorf("NoopStore.GetCursor() returned non-zero cursor fields: %+v", cur)
	}
}

func TestNoopStore_SetCursor(t *testing.T) {
	store := &NoopStore{}
	err := store.SetCursor(context.Background(), Cursor{SessionID: "sess-1", LastEventSeq: 42})
	if err != nil {
		t.Errorf("NoopStore.SetCursor() error = %v, want nil", err)
	}
}

func TestNoopStore_Query(t *testing.T) {
	store := &NoopStore{}
	points, err := store.Query(context.Background(), Query{Bucket: BucketHour})
	if err != nil {
		t.Errorf("NoopStore.Query() error = %v, want nil", err)
	}
	if len(points) != 0 {
		t.Errorf("NoopStore.Query() returned %d points, want 0", len(points))
	}
}

func TestNoopStore_Prune(t *testing.T) {
	store := &NoopStore{}
	rows, err := store.Prune(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Errorf("NoopStore.Prune() error = %v, want nil", err)
	}
	if rows != 0 {
		t.Errorf("NoopStore.Prune() rows = %d, want 0", rows)
	}
}

func TestNoopStore_Close(t *testing.T) {
	store := &NoopStore{}
	if err := store.Close(); err != nil {
		t.Errorf("NoopStore.Close() error = %v, want nil", err)
	}

	// Every method must return ErrClosed after Close.
	if err := store.UpsertDeltas(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("UpsertDeltas after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.GetCursor(context.Background(), "sess"); !errors.Is(err, ErrClosed) {
		t.Errorf("GetCursor after Close: error = %v, want ErrClosed", err)
	}
	if err := store.SetCursor(context.Background(), Cursor{SessionID: "sess"}); !errors.Is(err, ErrClosed) {
		t.Errorf("SetCursor after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.Query(context.Background(), Query{}); !errors.Is(err, ErrClosed) {
		t.Errorf("Query after Close: error = %v, want ErrClosed", err)
	}
	if _, err := store.Prune(context.Background(), time.Now()); !errors.Is(err, ErrClosed) {
		t.Errorf("Prune after Close: error = %v, want ErrClosed", err)
	}

	// Close is idempotent.
	if err := store.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

// TestConstants pins the exported metric-name string values. Any accidental
// rename here would break the API JSON schema and the chart legend, so it
// must break a test rather than a downstream consumer.
func TestConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"MetricInputTokensEst", MetricInputTokensEst, "input_tokens_est"},
		{"MetricOutputTokensEst", MetricOutputTokensEst, "output_tokens_est"},
		{"MetricPrompts", MetricPrompts, "prompts"},
		{"MetricAgentTurnsCompleted", MetricAgentTurnsCompleted, "agent_turns_completed"},
		{"MetricToolCallsTotal", MetricToolCallsTotal, "tool_calls_total"},
		{"MetricMCPCalls", MetricMCPCalls, "mcp_calls"},
		{"MetricPermissionsPrompted", MetricPermissionsPrompted, "permissions_prompted"},
		{"MetricErrors", MetricErrors, "errors"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if BucketHour != "hour" {
		t.Errorf("BucketHour = %q, want %q", BucketHour, "hour")
	}
	if BucketDay != "day" {
		t.Errorf("BucketDay = %q, want %q", BucketDay, "day")
	}
	if EstimatorVersion != 1 {
		t.Errorf("EstimatorVersion = %d, want 1", EstimatorVersion)
	}
}
