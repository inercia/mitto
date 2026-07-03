package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/session"
)

// HandleAgentTypes handles GET /api/agents/types.
// Returns the list of available agent definitions by reading subdirectory names
// from the agents directory (both builtin and user-created).
func (h *Handlers) HandleAgentTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"agent_types": []string{}})
		return
	}

	// Collect unique agent type names from all subdirectories
	typeSet := make(map[string]bool)

	// Walk top-level subdirectories (e.g., "builtin")
	topEntries, err := os.ReadDir(agentsDir)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"agent_types": []string{}})
		return
	}

	for _, topEntry := range topEntries {
		if !topEntry.IsDir() {
			continue
		}
		subDir := filepath.Join(agentsDir, topEntry.Name())
		entries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				typeSet[entry.Name()] = true
			}
		}
	}

	// Convert to sorted slice
	types := make([]string, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(types)

	writeJSONOK(w, map[string]interface{}{"agent_types": types})
}

// HandleRunnerDefaults handles GET /api/runner-defaults.
// Returns default runner configuration values. Currently a stub returning an empty object.
func (h *Handlers) HandleRunnerDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSONOK(w, map[string]interface{}{})
}

// HandleAdvancedFlags handles GET /api/advanced-flags.
// Returns the list of available advanced setting flags that can be configured per-session,
// along with the configured default values from the config file.
func (h *Handlers) HandleAdvancedFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Get configured default flags from config
	configuredDefaults := make(map[string]bool)
	if h.deps.MittoConfig != nil && h.deps.MittoConfig.Conversations != nil {
		configuredDefaults = h.deps.MittoConfig.Conversations.DefaultFlags
	}

	// Build response with both available flags and configured defaults
	response := map[string]interface{}{
		"flags":               session.AvailableFlags,
		"configured_defaults": configuredDefaults,
	}

	writeJSONOK(w, response)
}
