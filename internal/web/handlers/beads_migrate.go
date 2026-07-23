package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/beads"
)

// migrateRequestTimeout bounds the whole POST /api/beads/migrate request.
// It sits ABOVE the individual bd migrate/bootstrap subprocess timeouts (see
// beads/migrate.go) so the handler can surface a clean 503. This endpoint is
// exempted from the default request-timeout middleware via a server.go
// allowlist, so this constant is the only budget governing the request.
var migrateRequestTimeout = 6 * time.Minute

// migrateRequest is the POST /api/beads/migrate body. Mode selects which
// bd remediation to run: "migrate" (this clone is the designated migrator)
// or "adopt" (another clone already migrated; bootstrap from it).
type migrateRequest struct {
	WorkingDir string `json:"working_dir"`
	Mode       string `json:"mode"`
}

// migrateResponse is the successful shape returned to the frontend. Output
// carries bd's raw JSON (bd migrate schema --json) when available so the UI
// can render concrete migration details; Mode echoes the requested mode.
type migrateResponse struct {
	Ok     bool            `json:"ok"`
	Mode   string          `json:"mode"`
	Output json.RawMessage `json:"output,omitempty"`
}

// HandleBeadsMigrate handles POST /api/beads/migrate. It runs a bd schema
// migration or bootstrap on behalf of the user when the Beads panel surfaces
// a beads_schema_skew error, so the user does not have to leave Mitto for
// the fix. Enabled by default; the SchemaSkewDialog collects informed
// consent (mode radio + ack checkbox) before this endpoint is called. Admins
// can set web.beads.allow_migrate_from_ui: false as a kill-switch to
// forbid UI-initiated migrations (e.g. shared clones of a remote-backed DB
// where forking the schema is unacceptable). See mitto-erry.
func (h *Handlers) HandleBeadsMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	if !h.beadsMigrationAllowed() {
		// Emit code=migrate_from_ui_disabled (not the generic errCodeForbidden)
		// so the SchemaSkewDialog can render the tailored kill-switch copy
		// (BeadsView.js handleConfirm branches on this exact code). See
		// mitto-erry — the kill-switch UX is dead-code without this mapping.
		writeErrorJSON(w, http.StatusForbidden, "migrate_from_ui_disabled",
			"Beads migration from the UI has been disabled by the administrator (web.beads.allow_migrate_from_ui=false). Run the migration from a terminal on the designated clone.")
		return
	}

	var req migrateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.WorkingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "migrate"
	}
	if mode != "migrate" && mode != "adopt" {
		writeErrorJSON(w, http.StatusBadRequest, "", `mode must be "migrate" or "adopt"`)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), migrateRequestTimeout)
	defer cancel()

	var (
		out []byte
		err error
	)
	client := h.beadsClient()
	switch mode {
	case "migrate":
		out, err = client.MigrateRemote(ctx, req.WorkingDir)
	case "adopt":
		out, err = client.Bootstrap(ctx, req.WorkingDir)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeRetryableUnavailable(w, "Beads migration timed out. Try running it manually from a terminal.", 0)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("beads migration failed",
				"mode", mode, "working_dir", req.WorkingDir,
				"error", err, "stderr", beads.StderrOf(err),
				"exit_code", beads.ExitCodeOf(err))
		}
		details := map[string]any{"mode": mode, "working_dir": req.WorkingDir}
		if s := beads.StderrOf(err); s != "" {
			details["stderr"] = s
		}
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: errorBody{
			Code:    errCodeServerError,
			Message: err.Error(),
			Details: details,
		}})
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("beads migration succeeded", "mode", mode, "working_dir", req.WorkingDir)
	}

	resp := migrateResponse{Ok: true, Mode: mode}
	if len(out) > 0 && json.Valid(out) {
		resp.Output = out
	}
	writeJSONOK(w, resp)
}

// beadsMigrationAllowed reports whether the UI-initiated migration path is
// enabled via config. Tri-state: nil MittoConfig / nil Web.Beads / nil
// AllowMigrateFromUI all mean "allowed" (default on); only an explicit
// *false acts as an admin kill-switch. See mitto-erry.
func (h *Handlers) beadsMigrationAllowed() bool {
	if h.deps.MittoConfig == nil {
		return true
	}
	if h.deps.MittoConfig.Web.Beads == nil {
		return true
	}
	if h.deps.MittoConfig.Web.Beads.AllowMigrateFromUI == nil {
		return true
	}
	return *h.deps.MittoConfig.Web.Beads.AllowMigrateFromUI
}
