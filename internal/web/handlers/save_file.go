package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/web/middleware"
)

// SaveFileToPathRequest represents a request to save a file to a specific path.
type SaveFileToPathRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SaveFileToPathResponse represents the response from saving a file.
type SaveFileToPathResponse struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

// HandleCheckFileExists handles GET /api/check-file-exists?path=<absolutePath>
// Returns whether a file exists at the given path.
// SECURITY: This endpoint is restricted to localhost connections only.
func (h *Handlers) HandleCheckFileExists(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	if middleware.IsExternalConnection(r) {
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	// Security check 2: Verify this is a localhost connection
	if !middleware.IsLocalhostRequest(r) {
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "path query parameter is required")
		return
	}

	if !filepath.IsAbs(filePath) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Path must be absolute")
		return
	}

	cleanPath := filepath.Clean(filePath)
	if strings.Contains(cleanPath, "..") {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid path")
		return
	}

	_, err := os.Stat(cleanPath)
	exists := err == nil

	writeJSONOK(w, map[string]bool{"exists": exists})
}

// HandleSaveFileToPath handles POST /api/save-file-to-path
// This endpoint is used by the native macOS app to save files to arbitrary paths.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary file write attacks from remote clients.
func (h *Handlers) HandleSaveFileToPath(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	if middleware.IsExternalConnection(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected save-file-to-path request from external listener",
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	// Security check 2: Verify this is a localhost connection
	// This is redundant with check 1 but provides defense in depth
	if !middleware.IsLocalhostRequest(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected save-file-to-path request from non-localhost",
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	// Only allow POST
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Parse request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Failed to read request body")
		return
	}

	var req SaveFileToPathRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid JSON")
		return
	}

	// Validate path
	if req.Path == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Path is required")
		return
	}

	// Security check 3: Ensure path is absolute and doesn't contain path traversal
	if !filepath.IsAbs(req.Path) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Path must be absolute")
		return
	}

	cleanPath := filepath.Clean(req.Path)
	if strings.Contains(cleanPath, "..") {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid path")
		return
	}

	// Ensure parent directory exists
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to create directory", "dir", dir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", fmt.Sprintf("Failed to create directory: %v", err))
		return
	}

	// Write file
	if err := os.WriteFile(cleanPath, []byte(req.Content), 0644); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to write file", "path", cleanPath, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", fmt.Sprintf("Failed to write file: %v", err))
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("File saved successfully", "path", cleanPath, "size", len(req.Content))
	}

	// Return success response
	response := SaveFileToPathResponse{
		Success: true,
		Path:    cleanPath,
		Message: "File saved successfully",
	}

	writeJSONOK(w, response)
}
