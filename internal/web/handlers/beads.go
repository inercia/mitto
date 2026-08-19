package handlers

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/workspaces"
)

// beadsReadRetries is the number of extra attempts for read-only bd queries
// when they fail transiently (dolt store warm-up / instability). Read queries
// are idempotent so retrying is safe. It is a var only so tests can adjust it.
var beadsReadRetries = 2

// beadsRetryBackoff is the base backoff between read retries. It is a var
// only so tests can shorten it.
var beadsRetryBackoff = 150 * time.Millisecond

// beadsClient returns the injectable beads Client. When the handlers were
// constructed without an explicit client (e.g. in tests), it falls back to a
// default client backed by the real bd binary.
func (h *Handlers) beadsClient() beads.Client {
	if h.deps.BeadsClient != nil {
		return h.deps.BeadsClient
	}
	return beads.NewClient()
}

// isKnownWorkspaceDir returns true if workingDir matches any configured workspace.
func (h *Handlers) isKnownWorkspaceDir(workingDir string) bool {
	if h.deps.SessionManager == nil {
		return false
	}
	for _, ws := range h.deps.SessionManager.GetWorkspaces() {
		if ws.WorkingDir == workingDir {
			return true
		}
	}
	return false
}

// isValidBeadsIssueRef reports whether s is a safe issue reference: non-empty,
// not flag-like (no leading '-'), and composed only of letters, digits, '.',
// '-', '_', and ':'. The colon permits external references of the form
// external:<project>:<capability>. This prevents flag injection into the bd
// argument list.
func isValidBeadsIssueRef(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == ':':
		default:
			return false
		}
	}
	return true
}

// writeBeadsError reports a bd-command failure using the canonical error
// envelope, carrying any captured stderr under error.details.stderr. It also
// logs the failure (nil-guarded) so failures are never silent in mitto.log.
//
// A schema-version skew is distinguished from a genuine internal error: it
// surfaces as an actionable HTTP 409 with direction-appropriate remediation,
// rather than a bare 500. Every other failure keeps the existing HTTP 500
// behavior.
func (h *Handlers) writeBeadsError(w http.ResponseWriter, r *http.Request, err error) {
	if beads.IsSchemaSkew(err) {
		info := beads.SchemaSkewInfo(err)
		databaseAhead := info.DBVersion > 0 && info.BinaryVersion > 0 && info.DBVersion > info.BinaryVersion
		databaseMode := workspaces.BeadsDatabaseModeShared
		if workingDir := r.URL.Query().Get("working_dir"); workingDir != "" {
			if resolved, resolveErr := beads.ResolveDatabaseMode(r.Context(), h.beadsClient(), workingDir); resolveErr == nil {
				databaseMode = resolved
			} else if h.deps.Logger != nil {
				h.deps.Logger.Warn("could not resolve beads database mode for schema-skew guidance", "working_dir", workingDir, "error", resolveErr)
			}
		}
		logMessage := "beads schema needs migration"
		if databaseAhead {
			logMessage = "beads database newer than bd binary"
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Warn(logMessage, "db_path", info.DBPath, "db_version", info.DBVersion, "binary_version", info.BinaryVersion, "stderr", beads.StderrOf(err), "path", r.URL.Path)
		}
		hint := "This beads database is behind the bd binary's schema and is remote-backed, so bd will not auto-migrate it. Reconcile it once (e.g. `BD_ALLOW_REMOTE_MIGRATE=1 bd migrate && bd dolt push` on the designated migrator clone, or `bd bootstrap` if another clone already migrated), then reload."
		allowMigrate := h.beadsMigrationAllowed()
		if databaseMode == workspaces.BeadsDatabaseModeLocal {
			hint = "This local-only beads database is behind the bd binary's schema. Run the local migration once; Mitto will not push, pull, bootstrap, or publish it to a Dolt remote."
			info.Options = []beads.SchemaSkewOption{{Mode: "migrate", Description: "Apply pending migrations to this local-only database", Command: "BD_ALLOW_REMOTE_MIGRATE=1 bd migrate schema --json"}}
		}
		if databaseAhead {
			hint = "This beads database is newer than the installed bd binary. Follow the bd recovery guide at https://github.com/gastownhall/beads/blob/v1.2.2/docs/RECOVERY-1.2.1.md: upgrade every clone to bd 1.2.2, stop database users, take a backup, then perform the documented schema-cursor recovery. The ignore-schema-skew flag is only a temporary stopgap."
			allowMigrate = false
		}
		details := map[string]any{
			"hint":                  hint,
			"allow_migrate_from_ui": allowMigrate,
			"database_mode":         databaseMode,
		}
		if info.DBPath != "" {
			details["db_path"] = info.DBPath
		}
		if info.DBVersion != 0 {
			details["db_version"] = info.DBVersion
		}
		if info.BinaryVersion != 0 {
			details["binary_version"] = info.BinaryVersion
		}
		if len(info.Options) > 0 {
			details["options"] = info.Options
		}
		if s := beads.StderrOf(err); s != "" {
			details["stderr"] = s
		}
		msg := "The beads database schema needs migration"
		if databaseAhead {
			msg = "The beads database is newer than the installed bd binary"
		} else if info.DBPath != "" {
			msg = "The beads database at " + info.DBPath + " needs migration"
		}
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: errorBody{Code: errCodeBeadsSchemaSkew, Message: msg, Details: details}})
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Error("beads command failed", "error", err, "stderr", beads.StderrOf(err), "exit_code", beads.ExitCodeOf(err), "path", r.URL.Path)
	}
	var details map[string]any
	if s := beads.StderrOf(err); s != "" {
		details = map[string]any{"stderr": s}
	}
	writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: errorBody{Code: errCodeServerError, Message: err.Error(), Details: details}})
}

// runBeadsRead executes a read-only bd query with bounded retries on transient
// failures. It stops retrying on context cancellation, on not-found errors,
// and on schema-skew failures, since retrying any of those would be pointless
// (context cancellation can never succeed, not-found is a genuine result
// rather than a transient failure, and a schema skew is deterministic — bd
// will refuse identically on every attempt until the schema is reconciled
// out-of-band, so retrying only adds latency and needlessly multiplies bd
// spawns per request; mitto-292).
func (h *Handlers) runBeadsRead(ctx context.Context, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	var out []byte
	var err error
	for attempt := 0; attempt <= beadsReadRetries; attempt++ {
		out, err = fn(ctx)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || beads.IsNotFound(err) || beads.IsSchemaSkew(err) {
			return out, err
		}
		if attempt < beadsReadRetries {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("beads read failed, retrying", "attempt", attempt+1, "error", err)
			}
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(beadsRetryBackoff * time.Duration(attempt+1)):
			}
		}
	}
	return out, err
}

// HandleBeadsList handles GET /api/issues?working_dir=...
// Runs "bd list --json --all -n 0" in the workspace directory.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()
	out, err := h.runBeadsRead(ctx, func(ctx context.Context) ([]byte, error) {
		return h.beadsClient().List(ctx, workingDir)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeRetryableUnavailable(w, "Task service is busy. Please try again in a few seconds.", 5)
			return
		}
		h.writeBeadsError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// HandleBeadsStats handles GET /api/issues/stats?working_dir=...
// Runs "bd status --json --no-activity" in the workspace directory, returning an
// aggregate summary of issue counts by state (open, in_progress, ready, blocked,
// closed, ...). Used by the sidebar to render a per-folder Tasks stats line.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()
	out, err := h.runBeadsRead(ctx, func(ctx context.Context) ([]byte, error) {
		return h.beadsClient().Status(ctx, workingDir)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeRetryableUnavailable(w, "Task service is busy. Please try again in a few seconds.", 5)
			return
		}
		h.writeBeadsError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// HandleBeadsShow handles GET /api/issues/{id}?working_dir=...
// Runs "bd show <id> --json --include-comments" in the workspace directory,
// returning the full issue including its comments and dependencies. The id is
// read from the URL path via r.PathValue("id"); working_dir remains a query
// parameter.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("working_dir")
	id := r.PathValue("id")

	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "id is required")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()
	out, err := h.runBeadsRead(ctx, func(ctx context.Context) ([]byte, error) {
		return h.beadsClient().Show(ctx, workingDir, id)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeRetryableUnavailable(w, "Task service is busy. Please try again in a few seconds.", 5)
			return
		}
		if beads.IsNotFound(err) {
			writeErrorJSON(w, http.StatusNotFound, "", "Issue not found")
			return
		}
		// bd sometimes exits non-zero for a missing issue without printing any
		// diagnostic (empty stdout AND empty stderr). Treat that as a not-found
		// rather than surfacing an opaque 500, but log a warning so the silent
		// failure is still discoverable during triage. ExitCodeOf > 0 confirms
		// this was a real subprocess exit (not a timeout or context cancellation).
		var ce *beads.CmdError
		if errors.As(err, &ce) &&
			strings.TrimSpace(beads.StderrOf(err)) == "" &&
			beads.ExitCodeOf(err) != 0 {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("beads show exited non-zero with empty output; treating as not found",
					"id", id, "exit_code", beads.ExitCodeOf(err), "path", r.URL.Path)
			}
			writeErrorJSON(w, http.StatusNotFound, "", "Issue not found")
			return
		}
		h.writeBeadsError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}
