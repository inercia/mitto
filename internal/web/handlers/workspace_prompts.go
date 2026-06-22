package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
)

// HandleWorkspacePromptsToggleEnabled handles PUT /api/workspace-prompts/toggle-enabled.
// If the prompt file exists in .mitto/prompts/, updates the enabled field in the YAML file.
// Otherwise, records the enabled state in the workspace .mittorc file.
func (h *Handlers) HandleWorkspacePromptsToggleEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}

	var req struct {
		Dir     string `json:"dir"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Check if a dedicated prompt file exists in .mitto/prompts/
	slug := configPkg.SlugifyPromptName(req.Name)
	promptsDir := appdir.WorkspacePromptsDir(req.Dir)
	filePath := filepath.Join(promptsDir, slug+".prompt.yaml")

	if _, err := os.Stat(filePath); err == nil {
		// File exists — update its enabled field
		if err := configPkg.UpdatePromptFileEnabled(filePath, req.Enabled); err != nil {
			http.Error(w, "failed to update prompt file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Debug("Updated prompt file enabled state", "path", filePath, "enabled", req.Enabled)
		}
	} else {
		// File doesn't exist — record in .mittorc
		if err := configPkg.SaveWorkspaceRCPromptEnabled(req.Dir, req.Name, req.Enabled); err != nil {
			http.Error(w, "failed to update workspace config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Debug("Updated .mittorc prompt enabled state", "dir", req.Dir, "name", req.Name, "enabled", req.Enabled)
		}
	}

	writeJSONOK(w, map[string]interface{}{"ok": true})
}

// HandleWorkspacePromptsPOST handles POST /api/workspace-prompts
// Creates or updates a workspace prompt file in .mitto/prompts/<slug>.prompt.yaml.
func (h *Handlers) HandleWorkspacePromptsPOST(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir             string `json:"dir"`
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		Description     string `json:"description"`
		BackgroundColor string `json:"backgroundColor"`
		Group           string `json:"group"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Create the prompts directory if needed
	promptsDir := appdir.WorkspacePromptsDir(req.Dir)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		http.Error(w, "failed to create prompts directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slug := configPkg.SlugifyPromptName(req.Name)
	if slug == "" {
		slug = "prompt"
	}
	filePath := filepath.Join(promptsDir, slug+".prompt.yaml")

	pf := &configPkg.PromptFile{
		Name:            req.Name,
		Description:     req.Description,
		BackgroundColor: req.BackgroundColor,
		Group:           req.Group,
		Enabled:         req.Enabled,
		Content:         req.Prompt,
	}
	yamlBytes, err := yaml.Marshal(pf)
	if err != nil {
		http.Error(w, "failed to marshal prompt file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filePath, yamlBytes, 0o644); err != nil {
		http.Error(w, "failed to write prompt file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Created workspace prompt file", "path", filePath, "name", req.Name)
	}
	writeJSONOK(w, map[string]interface{}{"ok": true, "path": filePath})
}

// HandleWorkspacePromptsDELETE handles DELETE /api/workspace-prompts?dir=...&name=...
// Finds and deletes a workspace prompt file by name from .mitto/prompts/.
func (h *Handlers) HandleWorkspacePromptsDELETE(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("dir")
	promptName := r.URL.Query().Get("name")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if promptName == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	promptsDir := appdir.WorkspacePromptsDir(workingDir)
	rawPrompts, err := configPkg.LoadPromptsFromDir(promptsDir)
	if err != nil {
		http.Error(w, "failed to read prompts directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the prompt by name
	var targetPath string
	for _, p := range rawPrompts {
		if strings.EqualFold(p.Name, promptName) {
			targetPath = filepath.Join(promptsDir, p.Path)
			break
		}
	}
	if targetPath == "" {
		http.Error(w, "prompt not found: "+promptName, http.StatusNotFound)
		return
	}

	if err := os.Remove(targetPath); err != nil {
		http.Error(w, "failed to delete prompt file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Deleted workspace prompt file", "path", targetPath, "name", promptName)
	}
	writeJSONOK(w, map[string]interface{}{"ok": true})
}

// HandleWorkspacePromptsGETIncludeGlobal handles the include_global=true variant of the GET endpoint.
// It loads builtin prompts and workspace prompts, merges them (workspace overrides builtin by name),
// and returns all prompts including disabled ones (so the UI can render enable/disable toggles).
func (h *Handlers) HandleWorkspacePromptsGETIncludeGlobal(w http.ResponseWriter, r *http.Request, workingDir string) {
	// Load builtin prompts and tag them as source="builtin"
	var builtinPrompts []configPkg.WebPrompt
	if builtinDir, err := appdir.BuiltinPromptsDir(); err == nil {
		rawBuiltin, _ := configPkg.LoadPromptsFromDir(builtinDir)
		for _, p := range rawBuiltin {
			wp := p.ToWebPrompt()
			wp.Source = configPkg.PromptSourceBuiltin
			builtinPrompts = append(builtinPrompts, wp)
		}
	}

	// Load workspace prompts from .mitto/prompts/ and tag them as source="workspace"
	var workspacePrompts []configPkg.WebPrompt
	workspacePromptsDir := appdir.WorkspacePromptsDir(workingDir)
	rawWorkspace, _ := configPkg.LoadPromptsFromDir(workspacePromptsDir)
	for _, p := range rawWorkspace {
		wp := p.ToWebPrompt()
		wp.Source = configPkg.PromptSourceWorkspace
		workspacePrompts = append(workspacePrompts, wp)
	}

	// Load inline prompts from .mittorc. Separate them into:
	// - disable-only entries (no prompt text, enabled=false): applied as overrides on builtins
	// - full prompts with content: treated as workspace prompts
	disableOverrides := make(map[string]bool) // prompt name → disabled
	inlinePrompts := h.deps.SessionManager.GetWorkspacePrompts(workingDir)
	for _, p := range inlinePrompts {
		isDisableOnly := p.Prompt == "" && p.Enabled != nil && !*p.Enabled
		if isDisableOnly {
			disableOverrides[p.Name] = true
		} else {
			p.Source = configPkg.PromptSourceWorkspace
			workspacePrompts = append(workspacePrompts, p)
		}
	}

	// Merge: workspace overrides builtin by name.
	// Unlike MergePrompts, we do NOT filter out disabled prompts — the UI needs to see them.
	seen := make(map[string]bool)
	var merged []configPkg.WebPrompt
	for _, p := range workspacePrompts {
		if p.Name != "" && !seen[p.Name] {
			merged = append(merged, p)
			seen[p.Name] = true
		}
	}
	for _, p := range builtinPrompts {
		if p.Name != "" && !seen[p.Name] {
			// Apply disable-only overrides from .mittorc: keep builtin source/content
			// but mark as disabled so the UI shows the toggle correctly.
			if disableOverrides[p.Name] {
				f := false
				p.Enabled = &f
			}
			merged = append(merged, p)
			seen[p.Name] = true
		}
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Returning workspace prompts (include_global)",
			"working_dir", workingDir,
			"builtin_count", len(builtinPrompts),
			"workspace_count", len(workspacePrompts),
			"merged_count", len(merged))
	}

	writeJSONOK(w, map[string]interface{}{
		"prompts":     merged,
		"working_dir": workingDir,
	})
}

// HandleWorkspacePromptsGET handles GET /api/workspace-prompts?dir=...
// Returns the prompts from the workspace's .mittorc file and prompts_dirs.
// Prompts are filtered by the workspace's ACP server if specified in the prompt's acps field.
// Supports conditional requests via If-Modified-Since header.
// When include_global=true, also loads builtin prompts and returns all (including disabled).
func (h *Handlers) HandleWorkspacePromptsGET(w http.ResponseWriter, r *http.Request) {

	workingDir := r.URL.Query().Get("dir")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Migrate any legacy .md prompt files in this workspace to the new
	// .prompt.yaml format before loading. Idempotent: once migrated, subsequent
	// fetches find the .prompt.yaml already present and report nothing.
	var migrated []configPkg.MigratedPrompt
	if h.deps.MigrateWorkspacePrompts != nil {
		migrated = h.deps.MigrateWorkspacePrompts(workingDir)
	}

	// Get the ACP server type for this workspace (used for filtering prompts).
	// We use the server type (not name) because prompts target types,
	// and servers with the same type share prompts (e.g., auggie-fast and auggie-smart
	// can both have type "auggie" to share prompts with acps: auggie).
	var acpServerType string
	var acpServerName string
	if ws := h.deps.SessionManager.GetWorkspace(workingDir); ws != nil {
		acpServerName = ws.ACPServer
	} else if defaultWs := h.deps.SessionManager.GetDefaultWorkspace(); defaultWs != nil {
		acpServerName = defaultWs.ACPServer
	}
	// Look up the server type from config (falls back to name if type is not set)
	if acpServerName != "" && h.deps.MittoConfig != nil {
		acpServerType = h.deps.MittoConfig.GetServerType(acpServerName)
	}
	if acpServerType == "" {
		// Fallback: use name as type if server not found in config
		acpServerType = acpServerName
	}

	// Get the file's last modification time for conditional requests
	lastModified := h.deps.SessionManager.GetWorkspaceRCLastModified(workingDir)

	// Check If-Modified-Since header for conditional request.
	// Skip the 304 short-circuit when we just migrated files: the client must
	// receive the fresh prompt list and the one-time migration notice.
	if !lastModified.IsZero() && len(migrated) == 0 {
		// Set Last-Modified header
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))

		// Check if client has fresh data
		if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
			if t, err := time.Parse(http.TimeFormat, ifModifiedSince); err == nil {
				// HTTP time has second precision, so truncate for comparison
				if !lastModified.Truncate(time.Second).After(t) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
	} else if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}

	// When include_global=true, load builtin + workspace prompts and return all (including disabled).
	// This is used by the WorkspacesDialog to show the full list with enable/disable controls.
	includeGlobal := r.URL.Query().Get("include_global")
	if includeGlobal == "true" || includeGlobal == "1" || includeGlobal == "t" {
		h.HandleWorkspacePromptsGETIncludeGlobal(w, r, workingDir)
		return
	}

	// === Load prompts from ALL sources and merge into a single list ===
	// Priority (lowest to highest):
	// 1. Global file prompts (MITTO_DIR/prompts/*.prompt.yaml)
	// 2. Settings file prompts (config.Prompts)
	// 3. ACP server-specific prompts (prompts with acps: field targeting this server)
	// 4. Workspace directory prompts (.mitto/prompts/*.prompt.yaml)
	// 5. Workspace inline prompts (.mittorc prompts section) — highest priority

	// 1. Global file prompts
	var globalFilePrompts []configPkg.WebPrompt
	if h.deps.PromptsCache != nil {
		var err error
		globalFilePrompts, err = h.deps.PromptsCache.GetWebPrompts()
		if err != nil && h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to load global file prompts", "error", err)
		}
	}

	// 2. Settings file prompts
	var settingsPrompts []configPkg.WebPrompt
	if h.deps.MittoConfig != nil {
		settingsPrompts = h.deps.MittoConfig.Prompts
	}

	// 3. ACP server-specific file prompts (prompts with acps: field targeting this server)
	var serverPrompts []configPkg.WebPrompt
	if acpServerType != "" && h.deps.PromptsCache != nil {
		sp, err := h.deps.PromptsCache.GetWebPromptsSpecificToACP(acpServerType)
		if err != nil && h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to load ACP-specific file prompts",
				"acp_server", acpServerName, "acp_type", acpServerType, "error", err)
		}
		serverPrompts = sp
	}

	// Also include inline per-server prompts from config
	if acpServerName != "" && h.deps.MittoConfig != nil {
		for _, srv := range h.deps.MittoConfig.ACPServers {
			if srv.Name == acpServerName {
				serverPrompts = append(serverPrompts, srv.Prompts...)
				break
			}
		}
	}

	// 4. Workspace directory prompts (.mitto/prompts/*.prompt.yaml)
	var workspacePromptsDirs []string
	defaultWorkspacePromptsDir := appdir.WorkspacePromptsDir(workingDir)
	workspacePromptsDirs = append(workspacePromptsDirs, defaultWorkspacePromptsDir)
	promptsDirs := h.deps.SessionManager.GetWorkspacePromptsDirs(workingDir)
	workspacePromptsDirs = append(workspacePromptsDirs, promptsDirs...)
	var dirPrompts []configPkg.WebPrompt
	if h.deps.LoadPromptsFromDirs != nil {
		dirPrompts = h.deps.LoadPromptsFromDirs(workingDir, workspacePromptsDirs)
	}

	// 5. Workspace inline prompts (.mittorc)
	inlinePrompts := h.deps.SessionManager.GetWorkspacePrompts(workingDir)

	// Merge all sources. MergePrompts takes (global, settings, workspace) and filters disabled.
	// We merge in two steps: first global+settings, then server+workspace on top.
	globalMerged := configPkg.MergePromptsKeepDisabled(globalFilePrompts, settingsPrompts, nil)
	// Server prompts override global; workspace dir prompts override server; inline overrides all.
	allWorkspace := configPkg.MergePromptsKeepDisabled(nil, dirPrompts, inlinePrompts)
	prompts := configPkg.MergePromptsKeepDisabled(globalMerged, serverPrompts, allWorkspace)

	// Filter out disabled prompts (workspace enabled:false suppresses same-named global prompts)
	var filtered []configPkg.WebPrompt
	for _, p := range prompts {
		if p.Enabled == nil || *p.Enabled {
			filtered = append(filtered, p)
		}
	}
	prompts = filtered

	// Filter by enabledWhen expressions. Approach B (mitto-gns): prefer the active
	// session's context (real per-session permission flags + session.isChild) so
	// gates like "Start work" stay visible when the current conversation can send
	// prompts; fall back to a session-less workspace context only when the caller
	// opts in via enabled_context=workspace and no session is available. The
	// workspace fallback is what makes the beads menus actually evaluate the full
	// gates (commandExists/dirExists/!session.isChild/tools/permissions) instead of
	// returning everything unfiltered. item.* params (sent per-row by the beads
	// view) are populated onto whichever context is used so item-gated prompts are
	// evaluated against the opened row.
	query := r.URL.Query()
	sessionID := query.Get("session_id")
	itemKind := query.Get("item_kind")

	var enabledCtx *configPkg.PromptEnabledContext
	if sessionID != "" && h.deps.BuildPromptEnabledContext != nil {
		enabledCtx = h.deps.BuildPromptEnabledContext(sessionID)
		// The dir query param is authoritative for the workspace these prompts
		// belong to. The session only supplies session.*/permissions.*/parent.*/
		// children.* (approach B); its working dir may differ from the requested
		// dir (e.g. the Tasks/beads view is opened for one project while the
		// active conversation is in another, or a worktree). Override the
		// workspace/ACP/tools namespaces so dir-based gates (dirExists/fileExists
		// via workspace.folder), tools.hasPattern, and acp.* evaluate against the
		// requested dir, not the session's folder (mitto-gns follow-up).
		if enabledCtx != nil && h.deps.ApplyWorkspaceNamespace != nil {
			h.deps.ApplyWorkspaceNamespace(enabledCtx, workingDir)
		}
	}
	if enabledCtx == nil && query.Get("enabled_context") == "workspace" && h.deps.BuildWorkspacePromptEnabledContext != nil {
		enabledCtx = h.deps.BuildWorkspacePromptEnabledContext(workingDir)
	}
	if enabledCtx != nil {
		if itemKind != "" {
			enabledCtx.Item = configPkg.ItemContext{
				Id:       query.Get("item_id"),
				Status:   query.Get("item_status"),
				Type:     query.Get("item_type"),
				Priority: query.Get("item_priority"),
				Kind:     itemKind,
			}
		}
		if h.deps.FilterPromptsByEnabled != nil {
			prompts = h.deps.FilterPromptsByEnabled(prompts, enabledCtx)
		}
	}
	enabledEvaluated := enabledCtx != nil

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Returning workspace prompts (all sources merged)",
			"working_dir", workingDir,
			"acp_server", acpServerName,
			"acp_server_type", acpServerType,
			"prompt_count", len(prompts),
			"global_file_count", len(globalFilePrompts),
			"settings_count", len(settingsPrompts),
			"server_count", len(serverPrompts),
			"dir_prompt_count", len(dirPrompts),
			"inline_prompt_count", len(inlinePrompts),
			"prompts_dirs", workspacePromptsDirs,
			"last_modified", lastModified,
			"session_id", sessionID,
			"item_kind", itemKind,
			"enabled_evaluated", enabledEvaluated)
	}

	resp := map[string]interface{}{
		"prompts":           prompts,
		"working_dir":       workingDir,
		"enabled_evaluated": enabledEvaluated,
	}
	if len(migrated) > 0 {
		migratedNames := make([]string, 0, len(migrated))
		for _, m := range migrated {
			migratedNames = append(migratedNames, m.Name)
		}
		resp["migrated"] = migratedNames
	}
	writeJSONOK(w, resp)
}
