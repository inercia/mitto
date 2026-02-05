package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
)

// badgeClickRequest represents a request to execute the badge click action.
type badgeClickRequest struct {
	// WorkspacePath is the absolute path to the workspace directory.
	WorkspacePath string `json:"workspace_path"`
}

// badgeClickResponse represents the response from the badge click action.
type badgeClickResponse struct {
	// Success indicates whether the command was executed successfully.
	Success bool `json:"success"`
	// Error contains the error message if the command failed.
	Error string `json:"error,omitempty"`
}

// handleBadgeClick handles POST /api/badge-click.
// This endpoint executes the configured badge click action command.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary command execution from remote clients.
func (s *Server) handleBadgeClick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Security check: Only allow this endpoint from localhost (native macOS app).
	// This prevents remote attackers from executing arbitrary commands.
	clientIP := getClientIPWithProxyCheck(r)
	if !isLoopbackIP(clientIP) {
		if s.logger != nil {
			s.logger.Warn("Rejected badge-click request from non-localhost",
				"client_ip", clientIP,
			)
		}
		http.Error(w, "This endpoint is only available from localhost", http.StatusForbidden)
		return
	}

	// Parse request body
	var req badgeClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate workspace path
	if req.WorkspacePath == "" {
		http.Error(w, "workspace_path is required", http.StatusBadRequest)
		return
	}

	// Ensure the path is absolute to prevent path traversal attacks
	if !filepath.IsAbs(req.WorkspacePath) {
		http.Error(w, "workspace_path must be an absolute path", http.StatusBadRequest)
		return
	}

	// Get the badge click action configuration
	var enabled bool
	var command string

	mittoConfig := s.config.MittoConfig
	if mittoConfig != nil && mittoConfig.UI.Mac != nil && mittoConfig.UI.Mac.BadgeClickAction != nil {
		enabled = mittoConfig.UI.Mac.BadgeClickAction.GetEnabled()
		command = mittoConfig.UI.Mac.BadgeClickAction.GetCommand()
	} else {
		// Use defaults
		enabled = true
		command = "open ${WORKSPACE}"
	}

	if !enabled {
		writeJSONOK(w, badgeClickResponse{
			Success: false,
			Error:   "Badge click action is disabled",
		})
		return
	}

	// Replace ${WORKSPACE} placeholder with the actual path
	// Use quoted path to handle spaces and special characters safely
	finalCommand := strings.ReplaceAll(command, "${WORKSPACE}", req.WorkspacePath)

	// Execute the command using sh -c for shell interpretation
	// This allows users to use pipes, redirects, etc. in their commands
	cmd := exec.Command("sh", "-c", finalCommand)
	cmd.Dir = req.WorkspacePath

	if err := cmd.Start(); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to execute badge click command",
				"command", finalCommand,
				"workspace", req.WorkspacePath,
				"error", err,
			)
		}
		writeJSONOK(w, badgeClickResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to execute command: %v", err),
		})
		return
	}

	// Don't wait for the command to complete - it runs in the background
	// This allows commands like "open" to return immediately
	go func() {
		_ = cmd.Wait()
	}()

	if s.logger != nil {
		s.logger.Debug("Badge click command executed",
			"command", finalCommand,
			"workspace", req.WorkspacePath,
		)
	}

	writeJSONOK(w, badgeClickResponse{
		Success: true,
	})
}
