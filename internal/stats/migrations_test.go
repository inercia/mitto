package stats

// Tests for the migration ladder (mitto-1pv / parent epic mitto-5r5 phase 1).
// The critical guarantee validated here is that v1 → v2 preserves every
// existing row with model='' — the "unknown provenance" bucket — so early
// installations do not lose historical stats when the model dimension lands.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// openDBAtVersion opens a fresh DB and applies migrations up to (and
// including) the given target version, then bumps schema_version. It is used
// to build a database that looks like an older Mitto release before the head
// migration runs.
func openDBAtVersion(t *testing.T, target int) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	db, err := sql.Open("sqlite", buildDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	for _, m := range migrations {
		if m.version > target {
			break
		}
		if err := applyOne(ctx, db, m); err != nil {
			t.Fatalf("applyOne v%d: %v", m.version, err)
		}
	}
	return db, dbPath
}

// TestMigrationV2_PreservesExistingRows seeds a v1 database with a handful of
// stats_events rows, runs applyMigrations to bring it to head, and verifies
// every seeded row is still readable — now with model=” — via the standard
// SQLiteStore query path.
func TestMigrationV2_PreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db, dbPath := openDBAtVersion(t, 1)

	// Confirm we started at v1 (i.e. no `model` column yet).
	v, err := currentSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("currentSchemaVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("schema_version after v1 apply = %d, want 1", v)
	}

	// Seed rows using the v1 column list (no model column).
	seedTS := int64(1735689600) // 2025-01-01T00:00:00Z, hour-aligned
	rows := []struct {
		metric, session, workspace string
		value                      int64
	}{
		{MetricPrompts, "s1", "w1", 3},
		{MetricInputTokensEst, "s1", "w1", 42},
		{MetricOutputTokensEst, "s1", "w1", 88},
		{MetricToolCallsTotal, "s2", "w2", 7},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO stats_events (ts_bucket, metric, session_id, workspace, working_dir, acp_server, value)
			 VALUES (?, ?, ?, ?, '', '', ?)`,
			seedTS, r.metric, r.session, r.workspace, r.value,
		); err != nil {
			t.Fatalf("seed insert %+v: %v", r, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("intermediate close: %v", err)
	}

	// Reopen via the public path so the full migration ladder runs.
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (post-seed): %v", err)
	}
	defer store.Close()

	// schema_version must now be at head (>= 2).
	v2, err := currentSchemaVersion(ctx, store.db)
	if err != nil {
		t.Fatalf("currentSchemaVersion post-migrate: %v", err)
	}
	if v2 < 2 {
		t.Fatalf("schema_version after Open = %d, want >= 2", v2)
	}

	// Every seeded row must be present with model=''.
	for _, r := range rows {
		var value int64
		var model string
		err := store.db.QueryRowContext(ctx,
			`SELECT value, model FROM stats_events
			 WHERE ts_bucket=? AND metric=? AND session_id=? AND workspace=?`,
			seedTS, r.metric, r.session, r.workspace,
		).Scan(&value, &model)
		if err != nil {
			t.Errorf("row %+v missing after migration: %v", r, err)
			continue
		}
		if value != r.value {
			t.Errorf("row %+v value = %d, want %d", r, value, r.value)
		}
		if model != "" {
			t.Errorf("row %+v model = %q, want empty (v1 rows land in unknown-provenance bucket)", r, model)
		}
	}

	// Row count sanity: exactly the seeded rows remain (no dupes from the
	// rebuild-and-rename dance).
	var n int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stats_events`).Scan(&n); err != nil {
		t.Fatalf("COUNT(*): %v", err)
	}
	if n != len(rows) {
		t.Errorf("stats_events row count after migrate = %d, want %d", n, len(rows))
	}

	// The v2 primary key must include model: inserting an identical row with
	// a non-empty model must land as a distinct row (not conflict with the
	// carried-over model='' row).
	if err := store.UpsertDeltas(ctx, []Delta{{
		TSBucket:  time.Unix(seedTS, 0).UTC(),
		Metric:    MetricInputTokensEst,
		SessionID: "s1",
		Workspace: "w1",
		Model:     "modelA",
		Value:     100,
	}}); err != nil {
		t.Fatalf("post-migrate upsert with model: %v", err)
	}
	var m int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stats_events
		 WHERE ts_bucket=? AND metric=? AND session_id=? AND workspace=?`,
		seedTS, MetricInputTokensEst, "s1", "w1",
	).Scan(&m); err != nil {
		t.Fatalf("COUNT(*) post-model-insert: %v", err)
	}
	if m != 2 {
		t.Errorf("rows for (bucket,metric,session,workspace) after model insert = %d, want 2 (model='' + modelA)", m)
	}
}
