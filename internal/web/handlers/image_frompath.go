package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web/middleware"
)

// UploadFromPathRequest is the request body for uploading images from file paths.
type UploadFromPathRequest struct {
	Paths []string `json:"paths"`
}

// handleUploadImageFromPath handles POST /api/sessions/{id}/images/from-path
// This endpoint is used by the native macOS app to upload images from file paths.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary file read attacks from remote clients.
func (h *Handlers) handleUploadImageFromPath(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	// Even if an attacker spoofs X-Forwarded-For to appear as localhost, this check
	// will block them because external listener requests are marked at the handler level.
	if middleware.IsExternalConnection(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected from-path request from external listener",
				"session_id", sessionID,
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "This endpoint is only available from localhost")
		return
	}

	// Security check 2: Only allow this endpoint from localhost (native macOS app).
	// This prevents remote attackers from reading arbitrary files on the server.
	clientIP := middleware.GetClientIPWithProxyCheck(r)
	if !middleware.IsLoopbackIP(clientIP) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected from-path request from non-localhost",
				"client_ip", clientIP,
				"session_id", sessionID,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "This endpoint is only available from localhost")
		return
	}

	// Parse JSON body
	var req UploadFromPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid JSON body")
		return
	}

	if len(req.Paths) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "", "No paths provided")
		return
	}

	// Process each file path
	var responses []ImageUploadResponse
	for _, filePath := range req.Paths {
		// Validate the path exists and is a file
		stat, err := os.Stat(filePath)
		if err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("File not found", "path", filePath, "error", err)
			}
			continue // Skip invalid paths
		}
		if stat.IsDir() {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Path is a directory", "path", filePath)
			}
			continue
		}

		// Check file size
		if stat.Size() > maxUploadSize {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("File too large", "path", filePath, "size", stat.Size())
			}
			continue
		}

		// Read the file
		data, err := os.ReadFile(filePath)
		if err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to read file", "path", filePath, "error", err)
			}
			continue
		}

		// Detect MIME type
		mimeType := http.DetectContentType(data)

		// Validate MIME type
		if !session.IsSupportedImageType(mimeType) {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Unsupported image type", "path", filePath, "mime_type", mimeType)
			}
			continue
		}

		// Get filename from path
		filename := filePath[strings.LastIndex(filePath, "/")+1:]

		// Save the image
		info, err := store.SaveImage(sessionID, data, mimeType, filename)
		if err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to save image", "path", filePath, "error", err)
			}
			continue
		}

		responses = append(responses, ImageUploadResponse{
			ID:       info.ID,
			URL:      "/api/sessions/" + sessionID + "/images/" + info.ID,
			Name:     info.Name,
			MimeType: info.MimeType,
			Size:     info.Size,
		})
	}

	if len(responses) > 0 {
		writeJSONCreated(w, responses)
	} else {
		writeJSONOK(w, responses)
	}
}
