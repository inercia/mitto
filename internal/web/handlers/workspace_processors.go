package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
)

// WebProcessor represents a processor as returned by the workspace processors API.
type WebProcessor struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Enabled     bool                       `json:"enabled"`
	Source      processors.ProcessorSource `json:"source"`
	On          string                     `json:"on,omitempty"`
	Match       string                     `json:"match,omitempty"`
	Priority    int                        `json:"priority,omitempty"`
	FilePath    string                     `json:"file_path,omitempty"`
	Mode        string                     `json:"mode,omitempty"` // "text", "command", or "prompt"
}

// HandleWorkspaceProcessors handles GET /api/workspaces/{uuid}/processors.
// Returns all processors applicable to the workspace (global + workspace-local),
// with enabled state reflecting any .mittorc overrides.
func (h *Handlers) HandleWorkspaceProcessors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	uuid := r.PathValue("uuid")
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	workingDir := ws.WorkingDir

	// Get merged processor manager (global + workspace processors)
	procMgr := h.deps.SessionManager.GetWorkspaceProcessorManager(workingDir)
	if procMgr == nil {
		writeJSONOK(w, map[string]interface{}{"processors": []WebProcessor{}, "working_dir": workingDir})
		return
	}

	// Build override map from workspace .mittorc processors section.
	// Mirrors the prompts pattern: [{name, enabled}] entries override processor defaults.
	overrides := make(map[string]bool) // name → enabled
	for _, o := range h.deps.SessionManager.GetWorkspaceProcessorOverrides(workingDir) {
		if o.Enabled != nil {
			overrides[o.Name] = *o.Enabled
		}
	}

	// Build response list
	var result []WebProcessor
	for _, p := range procMgr.Processors() {
		// Skip config (text-mode) processors — they are not file-based and can't be toggled
		if p.Source == processors.ProcessorSourceConfig {
			continue
		}
		enabled := p.Enabled == nil || *p.Enabled
		// Apply workspace-level override from .mittorc processors section
		if override, ok := overrides[p.Name]; ok {
			enabled = override
		}
		mode := "command"
		if p.IsTextMode() {
			mode = "text"
		} else if p.IsPromptMode() {
			mode = "prompt"
		}
		result = append(result, WebProcessor{
			Name:        p.Name,
			Description: p.Description,
			Enabled:     enabled,
			Source:      p.Source,
			On:          string(p.When.On),
			Match:       string(p.When.Match),
			Priority:    p.Priority,
			FilePath:    p.FilePath,
			Mode:        mode,
		})
	}

	// Sort: workspace processors first, then global, then by name within each group
	sort.Slice(result, func(i, j int) bool {
		si, sj := sourceOrder(result[i].Source), sourceOrder(result[j].Source)
		if si != sj {
			return si < sj
		}
		return result[i].Name < result[j].Name
	})

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Returning workspace processors",
			"working_dir", workingDir,
			"count", len(result))
	}

	writeJSONOK(w, map[string]interface{}{
		"processors":  result,
		"working_dir": workingDir,
	})
}

// sourceOrder returns a sort priority for processor sources (lower = shown first).
func sourceOrder(src processors.ProcessorSource) int {
	switch src {
	case processors.ProcessorSourceWorkspace:
		return 0
	case processors.ProcessorSourceGlobal:
		return 1
	case processors.ProcessorSourceBuiltin:
		return 2
	default:
		return 3
	}
}

// HandleWorkspaceProcessorPatch handles PATCH /api/workspaces/{uuid}/processors/{name}.
//
// Routing logic:
//   - Workspace-local, single-document YAML file → update enabled field in-place.
//   - Multi-document YAML file, global, or builtin processor → record override in
//     the workspace .mittorc file (processors section), same as the global path.
//
// The processor is resolved by Name through the merged manager so that multi-doc
// files (where filename ≠ processor name) are handled correctly.
func (h *Handlers) HandleWorkspaceProcessorPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		methodNotAllowed(w)
		return
	}

	uuid := r.PathValue("uuid")
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	workingDir := ws.WorkingDir
	name := r.PathValue("name")
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "name is required")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid JSON: "+err.Error())
		return
	}

	// Resolve the processor by Name through the merged manager.
	// This works correctly for multi-document files where the filename does
	// not match the processor name.
	var resolvedFilePath string
	var resolvedSource processors.ProcessorSource
	if procMgr := h.deps.SessionManager.GetWorkspaceProcessorManager(workingDir); procMgr != nil {
		for _, p := range procMgr.Processors() {
			if p.Name == name {
				resolvedFilePath = p.FilePath
				resolvedSource = p.Source
				break
			}
		}
	}

	// Determine whether the processor can be edited in-place:
	//   1. It must be workspace-local (not global/builtin).
	//   2. Its file must be a single-document YAML file.
	useInPlace := false
	if resolvedFilePath != "" && resolvedSource == processors.ProcessorSourceWorkspace {
		multi, err := processors.IsMultiDocFile(resolvedFilePath)
		if err == nil && !multi {
			useInPlace = true
		}
	}

	// Fall back to the old filename-based lookup when the manager couldn't
	// resolve the processor (e.g. newly added file not yet loaded). Apply the
	// same single-document guard before allowing an in-place write.
	if !useInPlace && resolvedFilePath == "" {
		workspaceProcessorDirs := h.deps.SessionManager.GetWorkspaceAllProcessorDirs(workingDir)
		for _, dir := range workspaceProcessorDirs {
			for _, ext := range []string{".yaml", ".yml"} {
				candidate := filepath.Join(dir, name+ext)
				if _, err := os.Stat(candidate); err == nil {
					multi, err := processors.IsMultiDocFile(candidate)
					if err == nil && !multi {
						resolvedFilePath = candidate
						useInPlace = true
					}
					break
				}
			}
			if resolvedFilePath != "" {
				break
			}
		}
	}

	if useInPlace {
		// Single-document workspace file — update enabled field in-place.
		if err := processors.UpdateProcessorFileEnabled(resolvedFilePath, req.Enabled); err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "", "failed to update processor file: "+err.Error())
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Debug("Updated processor file enabled state", "path", resolvedFilePath, "enabled", req.Enabled)
		}
	} else {
		// Multi-document file, global/builtin, or unresolvable processor —
		// record override in the workspace .mittorc processors section.
		if err := configPkg.SaveWorkspaceRCProcessorEnabled(workingDir, name, req.Enabled); err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "", "failed to update workspace config: "+err.Error())
			return
		}
		// Invalidate cache so the next read picks up the change.
		if h.deps.SessionManager != nil {
			h.deps.SessionManager.InvalidateWorkspaceRC(workingDir)
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Debug("Updated .mittorc processor enabled state",
				"dir", workingDir, "name", name, "enabled", req.Enabled)
		}
	}

	writeJSONOK(w, map[string]interface{}{"ok": true})
}
