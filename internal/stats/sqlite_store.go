package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	// modernc.org/sqlite registers the "sqlite" database/sql driver on import.
	// Pure Go, cgo-free — safe for cross-compilation.
	_ "modernc.org/sqlite"
)

// SQLiteStore is the persistent Store used in production. Backed by a single
// on-disk SQLite database file, opened in WAL mode with a single writer.
//
// Safe for concurrent use: modernc.org/sqlite + a single-connection pool
// serialises writes internally, and reads still observe committed data.
type SQLiteStore struct {
	db     *sql.DB
	path   string
	closed atomic.Bool
}

// Compile-time assertion that SQLiteStore satisfies the Store interface.
var _ Store = (*SQLiteStore)(nil)

// Open creates or opens the SQLite database at path, applies pending
// migrations, and returns a ready-to-use store. The parent directory must
// already exist — callers use appdir.StatsDir() and MkdirAll it themselves.
//
// The DSN forces WAL journalling, a 5s busy-timeout, foreign_keys=on, and
// txlock=immediate so BEGIN grabs the write lock up front (avoids upgrade
// deadlocks on concurrent write attempts).
func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("stats: Open: path is empty")
	}

	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("stats: sql.Open: %w", err)
	}
	// v1: single connection so writes are trivially serialised; WAL still
	// permits external readers on the same DB file.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("stats: ping: %w", err)
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db, path: path}, nil
}

// buildDSN encodes path + our required PRAGMAs into a modernc.org/sqlite DSN.
// The `file:` prefix and query string is the standard SQLite URI form.
func buildDSN(path string) string {
	q := url.Values{}
	q.Set("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// Path reports the on-disk path this store was opened against. Exposed for
// tests and observability.
func (s *SQLiteStore) Path() string { return s.path }

// Close closes the underlying database. Idempotent: subsequent method calls
// on this store return ErrClosed.
func (s *SQLiteStore) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.db.Close()
}

// UpsertDeltas idempotently applies each delta's Value to its (ts_bucket,
// metric, session_id, workspace) row. Zero-valued deltas are skipped. Within
// a single call, duplicate keys are collapsed (last-write-wins on Value); the
// resulting rows are the last-write-wins REPLACE the epic requires for the
// "double-insert = no double-count" acceptance test.
func (s *SQLiteStore) UpsertDeltas(ctx context.Context, deltas []Delta) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if len(deltas) == 0 {
		return nil
	}

	// Collapse duplicate keys in-batch — last one wins. Guarantees no
	// primary-key conflict inside the single INSERT ... ON CONFLICT loop.
	type key struct {
		ts        int64
		metric    string
		sessionID string
		workspace string
	}
	dedup := make(map[key]Delta, len(deltas))
	for _, d := range deltas {
		if d.Value == 0 {
			continue
		}
		k := key{d.TSBucket.UTC().Unix(), d.Metric, d.SessionID, d.Workspace}
		dedup[k] = d
	}
	if len(dedup) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `INSERT INTO stats_events
		(ts_bucket, metric, session_id, workspace, working_dir, acp_server, value)
		VALUES (?, ?, ?, ?, '', '', ?)
		ON CONFLICT(ts_bucket, metric, session_id, workspace) DO UPDATE SET
			value = excluded.value`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, d := range dedup {
		if _, err := stmt.ExecContext(ctx,
			k.ts, k.metric, k.sessionID, k.workspace, d.Value,
		); err != nil {
			return fmt.Errorf("stats: upsert delta: %w", err)
		}
	}
	return tx.Commit()
}

// GetCursor returns the persisted cursor for sessionID. When no cursor row
// exists, the returned Cursor has SessionID set and every other field zero,
// with err == ErrNotFound (mirrors NoopStore's contract).
func (s *SQLiteStore) GetCursor(ctx context.Context, sessionID string) (Cursor, error) {
	if s.closed.Load() {
		return Cursor{}, ErrClosed
	}

	var (
		lastSeq   int64
		lastTS    int64
		estVer    int
		updatedAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT last_seq, last_ts, estimator_version, updated_at
		   FROM stats_cursors WHERE session_id = ?`, sessionID,
	).Scan(&lastSeq, &lastTS, &estVer, &updatedAt)
	if err == sql.ErrNoRows {
		return Cursor{SessionID: sessionID}, ErrNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("stats: GetCursor: %w", err)
	}
	return Cursor{
		SessionID:        sessionID,
		LastEventSeq:     lastSeq,
		LastEventAt:      time.Unix(lastTS, 0).UTC(),
		EstimatorVersion: estVer,
		UpdatedAt:        time.Unix(updatedAt, 0).UTC(),
	}, nil
}

// SetCursor persists cur monotonically: last_seq and last_ts never regress —
// an out-of-order or replayed backfill call cannot roll a session's cursor
// backwards. estimator_version and updated_at always take the caller's value.
func (s *SQLiteStore) SetCursor(ctx context.Context, cur Cursor) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if cur.SessionID == "" {
		return errors.New("stats: SetCursor: SessionID is empty")
	}
	lastTS := int64(0)
	if !cur.LastEventAt.IsZero() {
		lastTS = cur.LastEventAt.UTC().Unix()
	}
	updatedAt := int64(0)
	if !cur.UpdatedAt.IsZero() {
		updatedAt = cur.UpdatedAt.UTC().Unix()
	}
	estVer := cur.EstimatorVersion
	if estVer == 0 {
		estVer = EstimatorVersion
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stats_cursors (session_id, last_seq, last_ts, estimator_version, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
			last_seq          = MAX(last_seq,          excluded.last_seq),
			last_ts           = MAX(last_ts,           excluded.last_ts),
			estimator_version = excluded.estimator_version,
			updated_at        = excluded.updated_at`,
		cur.SessionID, cur.LastEventSeq, lastTS, estVer, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("stats: SetCursor: %w", err)
	}
	return nil
}

// Query returns rows for the requested window aggregated at hourly granularity
// (day-bucket rollup is stats.7's concern and is not implemented here).
// Points are returned sorted by (TS, Metric). Empty ranges return an empty
// slice, never ErrNotFound — matching NoopStore's permissive shape.
func (s *SQLiteStore) Query(ctx context.Context, q Query) ([]Point, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}

	// Build the WHERE clause dynamically to keep placeholders positional.
	where := []string{"ts_bucket >= ?", "ts_bucket < ?"}
	args := []any{q.RangeFrom.UTC().Unix(), q.RangeTo.UTC().Unix()}

	if q.Workspace != "" {
		where = append(where, "workspace = ?")
		args = append(args, q.Workspace)
	}
	if len(q.Metrics) > 0 {
		placeholders := ""
		for i, m := range q.Metrics {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, m)
		}
		where = append(where, "metric IN ("+placeholders+")")
	}

	sqlText := `SELECT ts_bucket, metric, SUM(value) AS value
		FROM stats_events
		WHERE ` + joinAnd(where) + `
		GROUP BY ts_bucket, metric
		ORDER BY ts_bucket ASC, metric ASC`

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("stats: Query: %w", err)
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var (
			ts     int64
			metric string
			value  int64
		)
		if err := rows.Scan(&ts, &metric, &value); err != nil {
			return nil, fmt.Errorf("stats: Query scan: %w", err)
		}
		out = append(out, Point{
			TS:     time.Unix(ts, 0).UTC(),
			Metric: metric,
			Value:  value,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Prune deletes hourly rows strictly older than olderThan (comparison is
// bucket-start < cutoff). Returns the number of rows removed.
func (s *SQLiteStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM stats_events WHERE ts_bucket < ?`, olderThan.UTC().Unix())
	if err != nil {
		return 0, fmt.Errorf("stats: Prune: %w", err)
	}
	return res.RowsAffected()
}

// joinAnd is a tiny helper (avoids pulling in strings just for one Join).
func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}
