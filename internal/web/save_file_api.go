// Package web provides the web interface for Mitto.
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// handleCheckFileExists handles GET /api/check-file-exists?path=<absolutePath>
// Returns whether a file exists at the given path.
// SECURITY: This endpoint is restricted to localhost connections only.
func (s *Server) handleCheckFileExists(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	if IsExternalConnection(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Security check 2: Verify this is a localhost connection
	if !isLocalhostRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}

	if !filepath.IsAbs(filePath) {
		http.Error(w, "Path must be absolute", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(filePath)
	if strings.Contains(cleanPath, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	_, err := os.Stat(cleanPath)
	exists := err == nil

	writeJSONOK(w, map[string]bool{"exists": exists})
}

// handleSaveFileToPath handles POST /api/save-file-to-path
// This endpoint is used by the native macOS app to save files to arbitrary paths.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary file write attacks from remote clients.
func (s *Server) handleSaveFileToPath(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	if IsExternalConnection(r) {
		if s.logger != nil {
			s.logger.Warn("Rejected save-file-to-path request from external listener",
				"remote_addr", r.RemoteAddr,
			)
		}
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Security check 2: Verify this is a localhost connection
	// This is redundant with check 1 but provides defense in depth
	if !isLocalhostRequest(r) {
		if s.logger != nil {
			s.logger.Warn("Rejected save-file-to-path request from non-localhost",
				"remote_addr", r.RemoteAddr,
			)
		}
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Only allow POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req SaveFileToPathRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate path
	if req.Path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Security check 3: Ensure path is absolute and doesn't contain path traversal
	if !filepath.IsAbs(req.Path) {
		http.Error(w, "Path must be absolute", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(req.Path)
	if strings.Contains(cleanPath, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Ensure parent directory exists
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to create directory", "dir", dir, "error", err)
		}
		http.Error(w, fmt.Sprintf("Failed to create directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Write file
	if err := os.WriteFile(cleanPath, []byte(req.Content), 0644); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to write file", "path", cleanPath, "error", err)
		}
		http.Error(w, fmt.Sprintf("Failed to write file: %v", err), http.StatusInternalServerError)
		return
	}

	if s.logger != nil {
		s.logger.Info("File saved successfully", "path", cleanPath, "size", len(req.Content))
	}

	// Return success response
	response := SaveFileToPathResponse{
		Success: true,
		Path:    cleanPath,
		Message: "File saved successfully",
	}

	writeJSONOK(w, response)
}
