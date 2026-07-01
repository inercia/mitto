package handlers

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// File upload limits
const (
	maxFileUploadSize = 50 * 1024 * 1024 // 50 MB (matches session.MaxFileSize)
)

// FileUploadResponse is the response for a successful file upload.
type FileUploadResponse struct {
	ID       string               `json:"id"`
	URL      string               `json:"url"`
	Name     string               `json:"name"`
	MimeType string               `json:"mime_type"`
	Size     int64                `json:"size"`
	Category session.FileCategory `json:"category"`
}

// HandleSessionFiles handles file operations for a session.
// Routes:
//   - POST /api/sessions/{id}/files - Upload a file
//   - POST /api/sessions/{id}/files/from-path - Upload files from file paths (native app)
//   - GET /api/sessions/{id}/files - List files
//   - GET /api/sessions/{id}/files/{fileId} - Serve a file
//   - DELETE /api/sessions/{id}/files/{fileId} - Delete a file
func (h *Handlers) HandleSessionFiles(w http.ResponseWriter, r *http.Request, sessionID string, filePath string) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	// Check if session exists
	if !store.Exists(sessionID) {
		writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
		return
	}

	// Handle from-path endpoint (for native macOS app)
	if filePath == "from-path" {
		if r.Method == http.MethodPost {
			h.handleUploadFileFromPath(w, r, store, sessionID)
		} else {
			methodNotAllowed(w)
		}
		return
	}

	// If filePath is empty, we're operating on the files collection
	if filePath == "" {
		switch r.Method {
		case http.MethodPost:
			h.handleUploadFile(w, r, store, sessionID)
		case http.MethodGet:
			h.handleListFiles(w, r, store, sessionID)
		default:
			methodNotAllowed(w)
		}
		return
	}

	// Operating on a specific file
	switch r.Method {
	case http.MethodGet:
		h.handleServeFile(w, r, store, sessionID, filePath)
	case http.MethodDelete:
		h.handleDeleteFile(w, r, store, sessionID, filePath)
	default:
		methodNotAllowed(w)
	}
}

// handleUploadFile handles POST /api/sessions/{id}/files
func (h *Handlers) handleUploadFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxFileUploadSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxFileUploadSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "file_too_large", "File exceeds 50MB limit")
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, "", "Failed to parse form")
		return
	}

	// Get the file from the form
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "No file provided")
		return
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to read file")
		return
	}

	// Determine MIME type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		// Try to detect from content
		mimeType = http.DetectContentType(data)
	}

	// For text files, also check by extension if detection failed
	if mimeType == "application/octet-stream" || mimeType == "text/plain" {
		if extMime := session.GetFileMimeTypeFromExt(strings.ToLower(getFileExtension(header.Filename))); extMime != "" {
			mimeType = extMime
		}
	}

	// Save the file
	info, err := store.SaveFile(sessionID, data, mimeType, header.Filename)
	if err != nil {
		h.handleFileSaveError(w, err)
		return
	}

	// Build response
	response := FileUploadResponse{
		ID:       info.ID,
		URL:      "/api/sessions/" + sessionID + "/files/" + info.ID,
		Name:     info.Name,
		MimeType: info.MimeType,
		Size:     info.Size,
		Category: info.Category,
	}

	writeJSONCreated(w, response)
}

// getFileExtension extracts the file extension from a filename.
func getFileExtension(filename string) string {
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		return filename[idx:]
	}
	return ""
}

// handleFileSaveError handles errors from SaveFile and returns appropriate HTTP responses.
func (h *Handlers) handleFileSaveError(w http.ResponseWriter, err error) {
	switch err {
	case session.ErrFileTooLarge:
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "file_too_large", "File exceeds size limit (50MB for binary, 1MB for text)")
	case session.ErrUnsupportedFileType:
		writeErrorJSON(w, http.StatusBadRequest, "unsupported_format", "Unsupported file type")
	case session.ErrSessionFileLimit:
		writeErrorJSON(w, http.StatusBadRequest, "session_limit", "Session has reached the maximum of 100 files")
	case session.ErrSessionFileStorageLimit:
		writeErrorJSON(w, http.StatusBadRequest, "storage_limit", "Session has reached the maximum storage of 500MB for files")
	default:
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save file", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "save_failed", "Failed to save file")
	}
}

// handleListFiles handles GET /api/sessions/{id}/files
func (h *Handlers) handleListFiles(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	files, err := store.ListFiles(sessionID)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list files", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list files")
		return
	}

	// Add URL to each file
	type FileWithURL struct {
		session.FileInfo
		URL string `json:"url"`
	}

	result := make([]FileWithURL, len(files))
	for i, f := range files {
		result[i] = FileWithURL{
			FileInfo: f,
			URL:      "/api/sessions/" + sessionID + "/files/" + f.ID,
		}
	}

	writeJSONOK(w, result)
}

// handleServeFile handles GET /api/sessions/{id}/files/{fileId}
func (h *Handlers) handleServeFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, fileID string) {
	// Validate file ID to prevent path traversal
	if strings.Contains(fileID, "/") || strings.Contains(fileID, "..") {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid file ID")
		return
	}

	filePath, err := store.GetFilePath(sessionID, fileID)
	if err != nil {
		if err == session.ErrFileNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "File not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get file path", "error", err, "session_id", sessionID, "file_id", fileID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get file")
		return
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "", "File not found")
		return
	}
	defer file.Close()

	// Get file info for size
	stat, err := file.Stat()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to read file")
		return
	}

	// Determine content type from extension
	ext := strings.ToLower(getFileExtension(fileID))
	mimeType := session.GetFileMimeTypeFromExt(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Set headers
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Cache-Control", "private, max-age=86400") // Cache for 1 day

	// Serve the file
	http.ServeContent(w, r, fileID, stat.ModTime(), file)
}

// handleDeleteFile handles DELETE /api/sessions/{id}/files/{fileId}
func (h *Handlers) handleDeleteFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, fileID string) {
	// Validate file ID to prevent path traversal
	if strings.Contains(fileID, "/") || strings.Contains(fileID, "..") {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid file ID")
		return
	}

	err := store.DeleteFile(sessionID, fileID)
	if err != nil {
		if err == session.ErrFileNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "File not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete file", "error", err, "session_id", sessionID, "file_id", fileID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to delete file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
