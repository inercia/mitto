package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/config/configpath"
	"github.com/inercia/mitto/internal/config/configsvc"
)

// configPatchOp is one caller-supplied {path, value} write operation, using
// the same dotted/indexed path syntax as `mitto config set`
// (docs/config/config-cli.md), e.g. "web.port" or "task_label_colors[0].color".
type configPatchOp struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// configPatchRequest is the JSON body for POST /api/config/patch.
type configPatchRequest struct {
	Ops []configPatchOp `json:"ops"`
	// Revision, when set, must match a previously-read snapshot's revision
	// (optimistic concurrency); a stale value fails the whole batch with 409.
	Revision string `json:"revision,omitempty"`
	// DryRun validates (and reports) the batch without persisting anything.
	DryRun bool `json:"dry_run,omitempty"`
}

// Per-key result status strings surfaced in configPatchKeyResult.Status.
const (
	configPatchStatusApplied         = "applied"
	configPatchStatusRestartRequired = "restart_required"
	configPatchStatusApplyFailed     = "apply_failed"
	configPatchStatusWouldApply      = "would_apply"
)

// configPatchKeyResult reports one op's outcome after a successful batch
// validate(+persist). Status is one of the configPatchStatus* constants.
type configPatchKeyResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// configPatchResponse is the JSON envelope for a successful patch (or
// dry-run) response.
type configPatchResponse struct {
	DryRun   bool                   `json:"dry_run"`
	Applied  []configPatchKeyResult `json:"applied"`
	Revision string                 `json:"revision,omitempty"`
}

// HandleConfigPatch handles POST /api/config/patch (mitto-4rz.3): an
// instance-bearer-gated (see Deps.ValidateInstanceBearer), validated batch
// write against settings.json, backed by configsvc's live-safe
// (MutateRequest.InProcess) Mutate — the offline Mutate entry point refuses
// to run while a server is up, which this handler, running INSIDE that
// server, must not trip.
func (h *Handlers) HandleConfigPatch(w http.ResponseWriter, r *http.Request) {
	if !h.validateInstanceBearer(w, r) {
		return
	}
	if h.deps.ConfigReadOnly {
		writeErrorJSON(w, http.StatusForbidden, "", "Configuration is read-only (loaded from a custom config file)")
		return
	}

	var req configPatchRequest
	if !parseJSONBody(w, r, &req) {
		return
	}
	if len(req.Ops) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "", "At least one op is required")
		return
	}

	ops := make([]configpath.Op, 0, len(req.Ops))
	for _, o := range req.Ops {
		p, err := configpath.ParsePath(o.Path)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid path: "+err.Error())
			return
		}
		ops = append(ops, configpath.Op{Path: p, Value: configpath.Value{Kind: configpath.KindJSON, JSON: o.Value}})
	}

	result, err := configsvc.Mutate(configsvc.MutateRequest{
		Set:       configpath.OpSet{Ops: ops},
		Revision:  req.Revision,
		DryRun:    req.DryRun,
		InProcess: true,
	})
	if err != nil {
		writeConfigsvcError(w, err)
		return
	}

	resp := configPatchResponse{DryRun: req.DryRun}
	for _, af := range result.AppliedFields {
		resp.Applied = append(resp.Applied, configPatchKeyResult{
			Path:   af.Path,
			Status: h.applyPatchedField(af, req.DryRun),
		})
	}
	if !req.DryRun {
		if snap, snapErr := configsvc.ReadSnapshot(); snapErr == nil {
			resp.Revision = snap.Revision
		}
	}

	writeJSONOK(w, resp)
}

// applyPatchedField classifies (and, for LivenessLive fields, actually
// applies) one persisted op per its registry Liveness:
//   - LivenessLive: refresh the matching in-memory MittoConfig field (and,
//     for task_label_colors, broadcast the existing change event) so a
//     later full Settings save cannot clobber this write with a stale
//     in-memory copy. Mirrors the existing dedicated-resource handlers
//     (global_task_label_colors.go, global_shortcuts.go).
//   - LivenessRequiresRestart: report restart-required; this bead never
//     performs an automatic restart.
//   - anything else (LivenessUnspecified): the field took effect purely by
//     being persisted (no in-memory mirror to refresh).
//
// Dry-run never applies anything; it only reports what WOULD happen.
func (h *Handlers) applyPatchedField(af configsvc.AppliedField, dryRun bool) string {
	if dryRun {
		return configPatchStatusWouldApply
	}
	switch af.Liveness {
	case configsvc.LivenessLive:
		if h.refreshLiveField(af.Field) {
			return configPatchStatusApplied
		}
		return configPatchStatusApplyFailed
	case configsvc.LivenessRequiresRestart:
		return configPatchStatusRestartRequired
	default:
		return configPatchStatusApplied
	}
}

// refreshLiveField re-reads one LivenessLive registry field (identified by
// its canonical Field, e.g. "task_label_colors") from disk into
// Deps.MittoConfig, broadcasting the field's existing WS event where one
// exists. Returns false (apply_failed) only when h.deps.MittoConfig is nil
// (nothing to refresh) or the field is not one of the known live fields —
// an absent broadcast closure is tolerated, mirroring the existing
// dedicated-resource handlers which work the same way in tests.
func (h *Handlers) refreshLiveField(field string) bool {
	if h.deps.MittoConfig == nil {
		return false
	}
	switch field {
	case "task_label_colors":
		h.deps.MittoConfig.TaskLabelColors = config.GlobalTaskLabelColors()
		if h.deps.BroadcastTaskLabelColorsUpdated != nil {
			h.deps.BroadcastTaskLabelColorsUpdated()
		}
		return true
	case "shortcuts":
		h.deps.MittoConfig.Shortcuts = config.GlobalShortcuts()
		return true
	case "ui":
		h.deps.MittoConfig.UI = config.GlobalUI()
		return true
	default:
		return false
	}
}
