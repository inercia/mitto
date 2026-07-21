package stats

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is a single ordered schema change applied inside one transaction.
// Additions are append-only: never edit or reorder a released version.
type migration struct {
	version int
	stmts   []string
}

// migrations lists every schema step in order. The store's applyMigrations loop
// executes every migration whose version is > the currently recorded
// schema_version, and bumps schema_version at the end of each one.
var migrations = []migration{
	{
		version: 1,
		stmts: []string{
			// stats_meta must exist first so applyMigrations can seed and read
			// schema_version, estimator_version and last_full_backfill_at.
			`CREATE TABLE IF NOT EXISTS stats_meta (
				key   TEXT PRIMARY KEY NOT NULL,
				value TEXT NOT NULL
			) WITHOUT ROWID`,

			// Hourly buckets keyed by (ts_bucket, metric, session_id, workspace).
			// working_dir and acp_server are session-constant labels denormalised
			// into every row so workspace-scoped queries can filter without a
			// join. v1 writes empty strings until stats.3/4 wire up the source.
			`CREATE TABLE IF NOT EXISTS stats_events (
				ts_bucket   INTEGER NOT NULL,
				metric      TEXT NOT NULL,
				session_id  TEXT NOT NULL,
				workspace   TEXT NOT NULL,
				working_dir TEXT NOT NULL DEFAULT '',
				acp_server  TEXT NOT NULL DEFAULT '',
				value       INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (ts_bucket, metric, session_id, workspace)
			) WITHOUT ROWID`,

			// Range-scan indexes: the global time range (query API), and the
			// workspace-scoped time range (dashboard).
			`CREATE INDEX IF NOT EXISTS idx_stats_events_ts    ON stats_events(ts_bucket)`,
			`CREATE INDEX IF NOT EXISTS idx_stats_events_ws_ts ON stats_events(workspace, ts_bucket)`,

			// Per-session ingest cursors so restarts / mixed live+backfill do
			// not double-count events.
			`CREATE TABLE IF NOT EXISTS stats_cursors (
				session_id        TEXT PRIMARY KEY NOT NULL,
				last_seq          INTEGER NOT NULL DEFAULT 0,
				last_ts           INTEGER NOT NULL DEFAULT 0,
				estimator_version INTEGER NOT NULL DEFAULT 1,
				updated_at        INTEGER NOT NULL DEFAULT 0
			) WITHOUT ROWID`,

			// Seed meta rows. schema_version is set to the current migration's
			// version at the end of applyMigrations; here we just install the
			// invariant sibling rows.
			`INSERT OR IGNORE INTO stats_meta(key, value) VALUES ('estimator_version', '1')`,
			`INSERT OR IGNORE INTO stats_meta(key, value) VALUES ('last_full_backfill_at', '')`,
		},
	},
}

// currentSchemaVersion reads the stats_meta.schema_version row. Returns 0 when
// stats_meta does not yet exist or has no schema_version row; both are treated
// as a fresh database.
func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	// Detect the presence of stats_meta first — a plain SELECT on a missing
	// table raises "no such table" which is not a useful error at this layer.
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='stats_meta'`).Scan(&name)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stats: probing stats_meta: %w", err)
	}

	var v int
	err = db.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM stats_meta WHERE key='schema_version'`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stats: reading schema_version: %w", err)
	}
	return v, nil
}

// applyMigrations brings the database up to the latest schema version. Safe to
// call on every Open: no-ops when already at head.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	current, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return fmt.Errorf("stats: applying migration v%d: %w", m.version, err)
		}
	}
	return nil
}

// applyOne runs a single migration transactionally: all statements + the
// schema_version bump commit together, or nothing does.
func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// If any statement fails, rollback; the deferred rollback after a
	// successful commit is a no-op (sql.ErrTxDone), which is fine.
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("stmt %q: %w", firstLine(stmt), err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO stats_meta(key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		fmt.Sprintf("%d", m.version),
	); err != nil {
		return fmt.Errorf("bumping schema_version: %w", err)
	}
	return tx.Commit()
}

// firstLine returns the first non-empty trimmed line of s, useful for
// migration-error context without dumping a whole CREATE TABLE.
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
