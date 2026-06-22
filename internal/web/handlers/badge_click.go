package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/web/middleware"
)

// badgeClickRequest represents a request to execute the badge click action.
type badgeClickRequest struct {
	// WorkspacePath is the absolute path to the workspace directory.
	WorkspacePath string `json:"workspace_path"`
	// Action specifies which action to perform: "folder" (default) or "terminal".
	Action string `json:"action,omitempty"`
}

// badgeClickResponse represents the response from the badge click action.
type badgeClickResponse struct {
	// Success indicates whether the command was executed successfully.
	Success bool `json:"success"`
	// Error contains the error message if the command failed.
	Error string `json:"error,omitempty"`
}

// HandleBadgeClick handles POST /api/badge-click.
// This endpoint executes the configured badge click action command.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary command execution from remote clients.
func (h *Handlers) HandleBadgeClick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Security check: Only allow this endpoint from localhost (native macOS app).
	// This prevents remote attackers from executing arbitrary commands.
	clientIP := middleware.GetClientIPWithProxyCheck(r)
	if !middleware.IsLoopbackIP(clientIP) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected badge-click request from non-localhost",
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

	// Get the action configuration based on the requested action type
	var enabled bool
	var command string

	mittoConfig := h.deps.MittoConfig
	if req.Action == "terminal" {
		// Use terminal action config
		if mittoConfig != nil && mittoConfig.UI.Mac != nil && mittoConfig.UI.Mac.TerminalAction != nil {
			enabled = mittoConfig.UI.Mac.TerminalAction.GetEnabled()
			command = mittoConfig.UI.Mac.TerminalAction.GetCommand()
		} else {
			enabled = true
			command = "open -a Terminal ${MITTO_WORKING_DIR}"
		}
	} else {
		// Default: use badge click (folder open) action config
		if mittoConfig != nil && mittoConfig.UI.Mac != nil && mittoConfig.UI.Mac.BadgeClickAction != nil {
			enabled = mittoConfig.UI.Mac.BadgeClickAction.GetEnabled()
			command = mittoConfig.UI.Mac.BadgeClickAction.GetCommand()
		} else {
			// Use defaults
			enabled = true
			command = "open ${MITTO_WORKING_DIR}"
		}
	}

	if !enabled {
		writeJSONOK(w, badgeClickResponse{
			Success: false,
			Error:   "Badge click action is disabled",
		})
		return
	}

	// Replace ${MITTO_WORKING_DIR} placeholder with the actual path
	// Use quoted path to handle spaces and special characters safely
	finalCommand := strings.ReplaceAll(command, "${MITTO_WORKING_DIR}", req.WorkspacePath)
	// Legacy: also support ${WORKSPACE} for backward compatibility
	finalCommand = strings.ReplaceAll(finalCommand, "${WORKSPACE}", req.WorkspacePath)

	// Execute the command using sh -c for shell interpretation
	// This allows users to use pipes, redirects, etc. in their commands
	cmd := exec.Command("sh", "-c", finalCommand)
	cmd.Dir = req.WorkspacePath

	// Capture stderr for error reporting
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to execute badge click command",
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

	// Wait briefly for the command to detect immediate failures (e.g., command not found)
	// Commands like "open" typically exit quickly on success
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			errMsg := stderrBuf.String()
			if errMsg == "" {
				errMsg = err.Error()
			}
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Badge click command failed",
					"command", finalCommand,
					"workspace", req.WorkspacePath,
					"error", errMsg,
				)
			}
			writeJSONOK(w, badgeClickResponse{
				Success: false,
				Error:   fmt.Sprintf("Command failed: %s", strings.TrimSpace(errMsg)),
			})
			return
		}
		// Command completed successfully
	case <-time.After(2 * time.Second):
		// Command is still running after 2s - assume it's a long-running process (e.g., terminal app)
		// and consider it successful
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("Badge click command executed",
			"command", finalCommand,
			"workspace", req.WorkspacePath,
		)
	}

	writeJSONOK(w, badgeClickResponse{
		Success: true,
	})
}
