package handlers

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/beads"
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
// A schema-version skew (bd refusing to auto-migrate a remote-backed database)
// is distinguished from a genuine internal error: it surfaces as an
// actionable HTTP 409 "needs migration" response with the DB path and a
// remediation hint, rather than a bare 500. Every other failure keeps the
// existing HTTP 500 behavior.
func (h *Handlers) writeBeadsError(w http.ResponseWriter, r *http.Request, err error) {
	if beads.IsSchemaSkew(err) {
		dbPath := beads.SchemaSkewDBPath(err)
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("beads schema needs migration", "db_path", dbPath, "stderr", beads.StderrOf(err), "path", r.URL.Path)
		}
		hint := "This beads database is behind the bd binary's schema and is remote-backed, so bd will not auto-migrate it. Reconcile it once (e.g. `BD_ALLOW_REMOTE_MIGRATE=1 bd migrate && bd dolt push` on the designated migrator clone, or `bd bootstrap` if another clone already migrated), then reload."
		details := map[string]any{"hint": hint}
		if dbPath != "" {
			details["db_path"] = dbPath
		}
		if s := beads.StderrOf(err); s != "" {
			details["stderr"] = s
		}
		msg := "The beads database schema needs migration"
		if dbPath != "" {
			msg = "The beads database at " + dbPath + " needs migration"
		}
		writeJSON(w, http.StatusConflict, errorEnvelope{Error: errorBody{Code: errCodeBeadsSchemaSkew, Message: msg, Details: details}})
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Error("beads command failed", "error", err, "stderr", beads.StderrOf(err), "path", r.URL.Path)
	}
	var details map[string]any
	if s := beads.StderrOf(err); s != "" {
		details = map[string]any{"stderr": s}
	}
	writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: errorBody{Code: errCodeServerError, Message: err.Error(), Details: details}})
}

// runBeadsRead executes a read-only bd query with bounded retries on transient
// failures. It stops retrying on context cancellation and on not-found errors,
// since retrying either would be pointless (the former can never succeed, the
// latter is a genuine result rather than a transient failure).
func (h *Handlers) runBeadsRead(ctx context.Context, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	var out []byte
	var err error
	for attempt := 0; attempt <= beadsReadRetries; attempt++ {
		out, err = fn(ctx)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || beads.IsNotFound(err) {
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
		h.writeBeadsError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}
