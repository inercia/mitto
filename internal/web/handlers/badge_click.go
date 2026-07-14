package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web/middleware"
)

// badgeClickRequest represents a request to execute the badge click action.
type badgeClickRequest struct {
	// WorkspacePath is the absolute path to the workspace directory.
	WorkspacePath string `json:"workspace_path"`
	// Action specifies which action to perform. Must be "open" (only supported
	// value); resolves TargetID against MacUIConfig.EffectiveOpenTargets().
	Action string `json:"action,omitempty"`
	// TargetID selects which OpenTarget to run.
	TargetID string `json:"target_id,omitempty"`
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
		writeErrorJSON(w, http.StatusForbidden, "", "This endpoint is only available from localhost")
		return
	}

	// Parse request body
	var req badgeClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	// Validate workspace path
	if req.WorkspacePath == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "workspace_path is required")
		return
	}

	// Ensure the path is absolute to prevent path traversal attacks
	if !filepath.IsAbs(req.WorkspacePath) {
		writeErrorJSON(w, http.StatusBadRequest, "", "workspace_path must be an absolute path")
		return
	}

	// Only action="open" is supported; TargetID selects the OpenTarget to run.
	if req.Action != "open" {
		writeErrorJSON(w, http.StatusBadRequest, "", "action must be \"open\"")
		return
	}
	if req.TargetID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "target_id is required when action=open")
		return
	}

	mittoConfig := h.deps.MittoConfig
	var targets []config.OpenTarget
	if mittoConfig != nil && mittoConfig.UI.Mac != nil {
		targets = mittoConfig.UI.Mac.EffectiveOpenTargets()
	} else {
		targets = config.DefaultOpenTargets()
	}
	var found *config.OpenTarget
	for i := range targets {
		if targets[i].ID == req.TargetID {
			found = &targets[i]
			break
		}
	}
	if found == nil {
		writeErrorJSON(w, http.StatusNotFound, "", fmt.Sprintf("Unknown target_id: %s", req.TargetID))
		return
	}
	if !found.GetEnabled() {
		writeJSONOK(w, badgeClickResponse{
			Success: false,
			Error:   "Target is disabled",
		})
		return
	}
	command := found.Command

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
