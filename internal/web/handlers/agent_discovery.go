package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/agents"
	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/fileutil"
)

// AgentScanResult wraps an AgentDefinition with its detection status.
type AgentScanResult struct {
	// DirName is the agent's directory name (e.g., "claude-code", "augment")
	DirName string `json:"dir_name"`
	// Source is the parent directory name (e.g., "builtin", "custom")
	Source string `json:"source"`
	// Metadata is the agent's parsed metadata.yaml content
	Metadata agents.AgentMetadata `json:"metadata"`
	// Status is the parsed output from status.sh (nil if not available or failed)
	Status *agents.AgentStatus `json:"status,omitempty"`
	// Available indicates whether the agent is installed and ready to use
	Available bool `json:"available"`
	// Error contains a human-readable error if detection failed
	Error string `json:"error,omitempty"`
}

// AgentConfirmRequest is the body for POST /api/agents/confirm.
type AgentConfirmRequest struct {
	// Agents is the list of agents selected by the user to add as ACP servers
	Agents []AgentConfirmEntry `json:"agents"`
}

// AgentConfirmEntry describes one agent the user wants to configure.
type AgentConfirmEntry struct {
	// Name is the display name for the new ACP server entry
	Name string `json:"name"`
	// Command is the full command string to start the ACP server
	Command string `json:"command"`
	// Type is an optional type identifier (used for prompt matching)
	Type string `json:"type,omitempty"`
	// DirName is the agent's directory name (e.g., "claude-code", "augment").
	// When present, the backend looks up the agent's metadata defaults by this name
	// and seeds them into the new ACPServerSettings entry.
	DirName string `json:"dir_name,omitempty"`
}

// seedACPServerDefaults applies agent metadata defaults onto a newly created
// ACP server settings entry. Only fields that are currently empty/unset on the
// settings are populated, so any user-provided values win.
// Both s and d may be nil, in which case the function is a no-op.
func seedACPServerDefaults(s *configPkg.ACPServerSettings, d *agents.AgentDefaults) {
	if s == nil || d == nil {
		return
	}
	if len(s.Env) == 0 && len(d.Env) > 0 {
		env := make(map[string]string, len(d.Env))
		for k, v := range d.Env {
			env[k] = v
		}
		s.Env = env
	}
	if len(s.Tags) == 0 && len(d.Tags) > 0 {
		tags := make([]string, len(d.Tags))
		copy(tags, d.Tags)
		s.Tags = tags
	}
	if s.Constraints == nil && len(d.Constraints) > 0 {
		constraints := make(map[string]*configPkg.ACPServerConstraint, len(d.Constraints))
		for k, spec := range d.Constraints {
			if spec == nil {
				continue
			}
			constraints[k] = &configPkg.ACPServerConstraint{
				MatchMode: spec.MatchMode,
				Pattern:   spec.Pattern,
			}
		}
		if len(constraints) > 0 {
			s.Constraints = constraints
		}
	}
	if s.ContextFlushCommand == "" && d.ContextFlushCommand != "" {
		s.ContextFlushCommand = d.ContextFlushCommand
	}
	s.AutoApprove = d.AutoApprove
}

// HandleScanAgents handles POST /api/agents/scan.
// It runs status.sh for all known agent definitions and returns the results.
func (h *Handlers) HandleScanAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get agents directory: "+err.Error())
		return
	}

	mgr := agents.NewManager(agentsDir, h.deps.Logger)
	allAgents, err := mgr.ListAgents()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list agents: "+err.Error())
		return
	}

	results := make([]AgentScanResult, 0, len(allAgents))
	for _, agent := range allAgents {
		result := AgentScanResult{
			DirName:  agent.DirName,
			Source:   agent.Source,
			Metadata: agent.Metadata,
		}

		if agent.HasCommand(agents.CommandStatus) {
			status, err := mgr.GetStatus(r.Context(), agent.DirName)
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Status = status
				result.Available = status.Installed
			}
		}

		results = append(results, result)
	}

	writeJSONOK(w, results)
}

// HandleConfirmAgents handles POST /api/agents/confirm.
// Saves the selected agents as ACP server entries in settings.json.
func (h *Handlers) HandleConfirmAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Reject saves when config is read-only (loaded from --config file)
	if h.deps.ConfigReadOnly {
		writeErrorJSON(w, http.StatusForbidden, "", "Configuration is read-only (loaded from config file)")
		return
	}

	var req AgentConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body: "+err.Error())
		return
	}

	if len(req.Agents) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "", "No agents selected")
		return
	}

	// Load current settings from disk
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get settings path: "+err.Error())
		return
	}

	var settings configPkg.Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to load settings: "+err.Error())
		return
	}

	// Build a set of existing server names to avoid duplicates
	existing := make(map[string]bool)
	for _, srv := range settings.ACPServers {
		existing[strings.ToLower(srv.Name)] = true
	}

	// Build agent manager for defaults seeding; skip defaults (not a fatal error) if unavailable.
	var mgr *agents.Manager
	if agentsDir, err := appdir.AgentsDir(); err == nil {
		mgr = agents.NewManager(agentsDir, h.deps.Logger)
	}

	// Append new servers from the confirmation request
	added := 0
	for _, entry := range req.Agents {
		if entry.Name == "" || entry.Command == "" {
			continue
		}
		if existing[strings.ToLower(entry.Name)] {
			continue
		}
		srv := configPkg.ACPServerSettings{
			Name:    entry.Name,
			Command: entry.Command,
			Type:    entry.Type,
			Source:  configPkg.SourceSettings,
		}
		if mgr != nil && entry.DirName != "" {
			if agent, err := mgr.GetAgent(entry.DirName); err == nil && agent != nil && agent.Metadata.Defaults != nil {
				seedACPServerDefaults(&srv, agent.Metadata.Defaults)
			}
		}
		settings.ACPServers = append(settings.ACPServers, srv)
		existing[strings.ToLower(entry.Name)] = true
		added++
	}

	if added == 0 {
		writeJSONOK(w, map[string]interface{}{
			"success": true,
			"message": "No new agents added (all already configured)",
			"added":   0,
		})
		return
	}

	// Persist updated settings
	if err := configPkg.SaveSettings(&settings); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save settings: "+err.Error())
		return
	}

	// Apply changes to the running server in-memory config
	if h.deps.MittoConfig != nil {
		newServers := make([]configPkg.ACPServer, len(settings.ACPServers))
		for i, srv := range settings.ACPServers {
			newServers[i] = configPkg.ACPServer(srv)
		}
		h.deps.MittoConfig.ACPServers = newServers
	}

	writeJSONOK(w, map[string]interface{}{
		"success": true,
		"message": "Agents added successfully",
		"added":   added,
	})
}
