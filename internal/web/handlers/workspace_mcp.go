package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/inercia/mitto/internal/agents"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/mcpserver"
)

// HandleWorkspaceMCPTools handles GET /api/workspace-mcp-tools?acp_server=...&dir=...
// Returns MCP tools available for the workspace's ACP server type by running
// the agent's mcp-list.sh script.
func (h *Handlers) HandleWorkspaceMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	acpServerName := r.URL.Query().Get("acp_server")
	workingDir := r.URL.Query().Get("dir")

	if acpServerName == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "acp_server query parameter is required")
		return
	}

	// Live Mitto MCP server URL, exposed so the UI can offer a one-click install.
	// Defaults to the well-known port and is overridden with the actual runtime
	// port when the server is running (handles dynamic / fallback ports).
	mcpURL := fmt.Sprintf("http://127.0.0.1:%d/mcp", mcpserver.DefaultPort)
	if h.deps.MCPServerURL != nil {
		mcpURL = h.deps.MCPServerURL()
	}

	// Resolve ACP server type from config
	var acpType string
	if h.deps.MittoConfig != nil {
		acpType = h.deps.MittoConfig.GetServerType(acpServerName)
	}
	if acpType == "" {
		acpType = acpServerName // fallback
	}

	// Get agents directory
	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		writeJSONOK(w, map[string]interface{}{
			"servers":        []interface{}{},
			"error":          "Failed to get agents directory: " + err.Error(),
			"agent_name":     "",
			"has_mcp_remove": false,
		})
		return
	}

	// Find agent by ACP ID
	mgr := agents.NewManager(agentsDir, h.deps.Logger)
	agent, err := mgr.GetAgentByACPId(acpType)
	if err != nil {
		// No matching agent found - not an error, just no MCP tools
		writeJSONOK(w, map[string]interface{}{
			"servers":        []interface{}{},
			"agent_name":     "",
			"message":        fmt.Sprintf("No agent definition found for ACP type %q", acpType),
			"has_mcp_remove": false,
		})
		return
	}

	// Compute MCP scopes from agent metadata (always an array, never null)
	mcpScopes := []string{}
	if agent.Metadata.MCP != nil {
		mcpScopes = agent.Metadata.MCP.Scopes
	}

	// Check if agent has mcp-list command
	if !agent.HasCommand(agents.CommandMCPList) {
		writeJSONOK(w, map[string]interface{}{
			"servers":         []interface{}{},
			"agent_name":      agent.Metadata.DisplayName,
			"message":         "Agent does not support MCP listing",
			"mcp_scopes":      mcpScopes,
			"mcp_url":         mcpURL,
			"has_mcp_install": agent.HasCommand(agents.CommandMCPInstall),
			"has_mcp_remove":  agent.HasCommand(agents.CommandMCPRemove),
		})
		return
	}

	// Run mcp-list.sh with workspace path
	input := &agents.MCPListInput{}
	if workingDir != "" {
		input.Path = workingDir
	}

	output, err := mgr.ListMCPServers(r.Context(), agent.DirName, input)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{
			"servers":         []interface{}{},
			"agent_name":      agent.Metadata.DisplayName,
			"error":           "Failed to list MCP servers: " + err.Error(),
			"mcp_scopes":      mcpScopes,
			"mcp_url":         mcpURL,
			"has_mcp_install": agent.HasCommand(agents.CommandMCPInstall),
			"has_mcp_remove":  agent.HasCommand(agents.CommandMCPRemove),
		})
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"servers":         output.Servers,
		"agent_name":      agent.Metadata.DisplayName,
		"mcp_scopes":      mcpScopes,
		"mcp_url":         mcpURL,
		"has_mcp_install": agent.HasCommand(agents.CommandMCPInstall),
		"has_mcp_remove":  agent.HasCommand(agents.CommandMCPRemove),
	})
}

// HandleWorkspaceMCPRemove handles POST /api/workspace-mcp-remove
// Removes an MCP server from a workspace's ACP agent by running mcp-remove.sh.
func (h *Handlers) HandleWorkspaceMCPRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	type mcpRemoveRequest struct {
		ACPServer string `json:"acp_server"`
		Dir       string `json:"dir"`
		Scope     string `json:"scope"`
		Name      string `json:"name"`
	}

	var req mcpRemoveRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.ACPServer == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "acp_server is required")
		return
	}
	if req.Name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "name is required")
		return
	}

	// Resolve ACP server type from config
	var acpType string
	if h.deps.MittoConfig != nil {
		acpType = h.deps.MittoConfig.GetServerType(req.ACPServer)
	}
	if acpType == "" {
		acpType = req.ACPServer
	}

	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get agents directory: "+err.Error())
		return
	}

	mgr := agents.NewManager(agentsDir, h.deps.Logger)
	agent, err := mgr.GetAgentByACPId(acpType)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("No agent definition found for ACP type %q", acpType))
		return
	}

	if !agent.HasCommand(agents.CommandMCPRemove) {
		writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("Agent %q does not support MCP removal", agent.Metadata.DisplayName))
		return
	}

	// Validate scope if agent declares supported scopes
	if agent.Metadata.MCP != nil && len(agent.Metadata.MCP.Scopes) > 0 && req.Scope != "" {
		validScope := false
		for _, sc := range agent.Metadata.MCP.Scopes {
			if sc == req.Scope {
				validScope = true
				break
			}
		}
		if !validScope {
			writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("Invalid scope %q; valid scopes for %s: %v", req.Scope, agent.Metadata.DisplayName, agent.Metadata.MCP.Scopes))
			return
		}
	}

	input := &agents.MCPRemoveInput{
		Name:  req.Name,
		Scope: req.Scope,
		Path:  req.Dir,
	}

	output, err := mgr.RemoveMCPServer(r.Context(), agent.DirName, input)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{
			"success": false,
			"message": err.Error(),
			"name":    req.Name,
		})
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"success": output.Success,
		"message": output.Message,
		"name":    output.Name,
	})
}

// HandleWorkspaceMCPInstall handles POST /api/workspace-mcp-install
// Installs MCP servers for a workspace's ACP agent by running mcp-install.sh.
func (h *Handlers) HandleWorkspaceMCPInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	type mcpServerEntry struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		URL     string            `json:"url"`
		Env     map[string]string `json:"env"`
	}

	type mcpInstallRequest struct {
		ACPServer  string `json:"acp_server"`
		Dir        string `json:"dir"`
		Scope      string `json:"scope"`
		Definition struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"definition"`
	}

	var req mcpInstallRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.ACPServer == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "acp_server is required")
		return
	}

	if len(req.Definition.MCPServers) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "", "definition.mcpServers must contain at least one entry")
		return
	}

	// Resolve ACP server type from config
	var acpType string
	if h.deps.MittoConfig != nil {
		acpType = h.deps.MittoConfig.GetServerType(req.ACPServer)
	}
	if acpType == "" {
		acpType = req.ACPServer // fallback
	}

	// Get agents directory
	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get agents directory: "+err.Error())
		return
	}

	// Find agent by ACP ID
	mgr := agents.NewManager(agentsDir, h.deps.Logger)
	agent, err := mgr.GetAgentByACPId(acpType)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("No agent definition found for ACP type %q", acpType))
		return
	}

	// Check that the agent supports mcp-install
	if !agent.HasCommand(agents.CommandMCPInstall) {
		writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("Agent %q does not support MCP installation", agent.Metadata.DisplayName))
		return
	}

	// Validate scope if the agent declares supported scopes
	if agent.Metadata.MCP != nil && len(agent.Metadata.MCP.Scopes) > 0 {
		if req.Scope == "" {
			writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("scope is required; valid scopes for %s: %v", agent.Metadata.DisplayName, agent.Metadata.MCP.Scopes))
			return
		}
		validScope := false
		for _, sc := range agent.Metadata.MCP.Scopes {
			if sc == req.Scope {
				validScope = true
				break
			}
		}
		if !validScope {
			writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("Invalid scope %q; valid scopes for %s: %v", req.Scope, agent.Metadata.DisplayName, agent.Metadata.MCP.Scopes))
			return
		}
	}

	type installResult struct {
		Name    string `json:"name"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	results := make([]installResult, 0, len(req.Definition.MCPServers))

	for serverName, rawEntry := range req.Definition.MCPServers {
		var entry mcpServerEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			results = append(results, installResult{
				Name:    serverName,
				Success: false,
				Message: "Failed to parse server definition: " + err.Error(),
			})
			continue
		}

		input := &agents.MCPInstallInput{
			Name:    serverName,
			Command: entry.Command,
			Args:    entry.Args,
			URL:     entry.URL,
			Env:     entry.Env,
			Scope:   req.Scope,
			Path:    req.Dir,
		}

		output, err := mgr.InstallMCPServer(r.Context(), agent.DirName, input)
		if err != nil {
			results = append(results, installResult{
				Name:    serverName,
				Success: false,
				Message: err.Error(),
			})
			continue
		}

		results = append(results, installResult{
			Name:    serverName,
			Success: output.Success,
			Message: output.Message,
		})
	}

	writeJSONOK(w, map[string]interface{}{
		"results": results,
	})
}
