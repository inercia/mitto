package web

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
}

// handleScanAgents handles POST /api/agents/scan.
// It runs status.sh for all known agent definitions and returns the results.
func (s *Server) handleScanAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		http.Error(w, "Failed to get agents directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	mgr := agents.NewManager(agentsDir, s.logger)
	allAgents, err := mgr.ListAgents()
	if err != nil {
		http.Error(w, "Failed to list agents: "+err.Error(), http.StatusInternalServerError)
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

// handleConfirmAgents handles POST /api/agents/confirm.
// Saves the selected agents as ACP server entries in settings.json.
func (s *Server) handleConfirmAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Reject saves when config is read-only (loaded from --config file)
	if s.config.ConfigReadOnly {
		http.Error(w, "Configuration is read-only (loaded from config file)", http.StatusForbidden)
		return
	}

	var req AgentConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Agents) == 0 {
		http.Error(w, "No agents selected", http.StatusBadRequest)
		return
	}

	// Load current settings from disk
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		http.Error(w, "Failed to get settings path: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var settings configPkg.Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		http.Error(w, "Failed to load settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build a set of existing server names to avoid duplicates
	existing := make(map[string]bool)
	for _, srv := range settings.ACPServers {
		existing[strings.ToLower(srv.Name)] = true
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
		settings.ACPServers = append(settings.ACPServers, configPkg.ACPServerSettings{
			Name:    entry.Name,
			Command: entry.Command,
			Type:    entry.Type,
			Source:  configPkg.SourceSettings,
		})
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
		http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply changes to the running server in-memory config
	if s.config.MittoConfig != nil {
		newServers := make([]configPkg.ACPServer, len(settings.ACPServers))
		for i, srv := range settings.ACPServers {
			newServers[i] = configPkg.ACPServer(srv)
		}
		s.config.MittoConfig.ACPServers = newServers
	}

	writeJSONOK(w, map[string]interface{}{
		"success": true,
		"message": "Agents added successfully",
		"added":   added,
	})
}
