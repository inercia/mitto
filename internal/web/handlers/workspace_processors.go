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

// WebProcessorParameter represents one declared parameter of a prompt-mode processor
// as returned by the workspace processors API.
type WebProcessorParameter struct {
	// Name is the parameter identifier used in ${NAME} placeholders.
	Name string `json:"name"`
	// Type is one of the known parameter types (see config.KnownPromptParameterTypes).
	Type string `json:"type"`
	// Description is a human-readable explanation of the parameter.
	Description string `json:"description,omitempty"`
	// Default is the value declared in the processor YAML (always present).
	Default string `json:"default"`
	// Value is the effective value: the per-workspace override from .mittorc if set and
	// non-empty, otherwise the declared Default.
	Value string `json:"value"`
}

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
	// Parameters is non-empty only for prompt-mode processors that declare parameters.
	// Each entry includes the declared default and the effective per-workspace value.
	Parameters []WebProcessorParameter `json:"parameters,omitempty"`
	// Error is non-empty only for processors that failed to load or validate.
	// Valid processors leave this field empty (omitted from JSON).
	Error string `json:"error,omitempty"`
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

	// Build override maps from workspace .mittorc processors section.
	// Mirrors the prompts pattern: [{name, enabled?, arguments?}] entries override processor defaults.
	enabledOverrides := make(map[string]bool)            // name → enabled
	argOverrides := make(map[string]map[string]string)  // name → {paramName → value}
	for _, o := range h.deps.SessionManager.GetWorkspaceProcessorOverrides(workingDir) {
		if o.Enabled != nil {
			enabledOverrides[o.Name] = *o.Enabled
		}
		if len(o.Arguments) > 0 {
			argOverrides[o.Name] = o.Arguments
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
		if override, ok := enabledOverrides[p.Name]; ok {
			enabled = override
		}
		mode := "command"
		if p.IsTextMode() {
			mode = "text"
		} else if p.IsPromptMode() {
			mode = "prompt"
		}
		wp := WebProcessor{
			Name:        p.Name,
			Description: p.Description,
			Enabled:     enabled,
			Source:      p.Source,
			On:          string(p.When.On),
			Match:       string(p.When.Match),
			Priority:    p.Priority,
			FilePath:    p.FilePath,
			Mode:        mode,
		}
		// Populate parameters for prompt-mode processors with declared parameters.
		// The effective value is the workspace override (if set and non-empty) or the
		// declared default — following the same overlay pattern as argument substitution.
		if p.IsPromptMode() && len(p.Parameters) > 0 {
			pArgs := argOverrides[p.Name]
			params := make([]WebProcessorParameter, 0, len(p.Parameters))
			for _, param := range p.Parameters {
				value := param.Default
				if v, ok := pArgs[param.Name]; ok && v != "" {
					value = v
				}
				params = append(params, WebProcessorParameter{
					Name:        param.Name,
					Type:        param.Type,
					Description: param.Description,
					Default:     param.Default,
					Value:       value,
				})
			}
			wp.Parameters = params
		}
		result = append(result, wp)
	}

	// Surface load/validation errors so invalid processors appear in the tab.
	// De-dupe by (source, file_path, name).
	seenErr := make(map[string]bool)
	for _, le := range procMgr.LoadErrors() {
		name := le.Name
		if name == "" {
			name = filepath.Base(le.FilePath) // file-level parse error: identify by basename
		}
		key := string(le.Source) + "\x00" + le.FilePath + "\x00" + name
		if seenErr[key] {
			continue
		}
		seenErr[key] = true
		result = append(result, WebProcessor{
			Name:     name,
			Source:   le.Source,
			FilePath: le.FilePath,
			Error:    le.Error,
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

// HandleWorkspaceProcessorArguments handles PUT /api/workspaces/{uuid}/processors/{name}/arguments.
// It saves per-workspace argument value overrides for a prompt-mode processor to the workspace
// .mittorc file, then returns the updated effective parameter values.
//
// Request body: {"arguments": {"paramName": "value", ...}}
//   - An empty string for a value clears the override (reverts to the declared default).
//   - Unknown parameter names are rejected with a 400 error.
//
// Only prompt-mode processors with declared parameters support argument overrides.
// Non-prompt-mode or unknown processors are rejected with the appropriate error.
func (h *Handlers) HandleWorkspaceProcessorArguments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
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

	// Resolve the processor by name through the merged manager.
	var proc *processors.Processor
	if procMgr := h.deps.SessionManager.GetWorkspaceProcessorManager(workingDir); procMgr != nil {
		for _, p := range procMgr.Processors() {
			if p.Name == name {
				proc = p
				break
			}
		}
	}
	if proc == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Processor not found: "+name)
		return
	}
	if !proc.IsPromptMode() {
		writeErrorJSON(w, http.StatusBadRequest, "", "Processor '"+name+"' is not prompt-mode; arguments are only supported for prompt-mode processors")
		return
	}

	var req struct {
		Arguments map[string]string `json:"arguments"`
	}
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Validate: reject keys that don't correspond to declared parameters.
	knownParams := make(map[string]configPkg.PromptParameter, len(proc.Parameters))
	for _, p := range proc.Parameters {
		knownParams[p.Name] = p
	}
	for k := range req.Arguments {
		if _, ok := knownParams[k]; !ok {
			writeErrorJSON(w, http.StatusBadRequest, "", "unknown parameter: "+k)
			return
		}
	}

	// Persist to .mittorc (empty values clear the override; non-empty values set it).
	if err := configPkg.SaveWorkspaceRCProcessorArguments(workingDir, name, req.Arguments); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save processor arguments", "dir", workingDir, "name", name, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "failed to save arguments: "+err.Error())
		return
	}

	// Invalidate cache so subsequent reads pick up the change.
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.InvalidateWorkspaceRC(workingDir)
	}
	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Updated .mittorc processor arguments", "dir", workingDir, "name", name)
	}

	// Re-read fresh overrides from disk (cache was just invalidated) and return
	// the updated effective parameter values so the frontend can refresh.
	savedArgs := make(map[string]string)
	for _, o := range h.deps.SessionManager.GetWorkspaceProcessorOverrides(workingDir) {
		if o.Name == name {
			savedArgs = o.Arguments
			break
		}
	}

	params := make([]WebProcessorParameter, 0, len(proc.Parameters))
	for _, param := range proc.Parameters {
		value := param.Default
		if v, ok := savedArgs[param.Name]; ok && v != "" {
			value = v
		}
		params = append(params, WebProcessorParameter{
			Name:        param.Name,
			Type:        param.Type,
			Description: param.Description,
			Default:     param.Default,
			Value:       value,
		})
	}

	writeJSONOK(w, map[string]interface{}{
		"processor":  name,
		"parameters": params,
	})
}
