package web

import (
	"encoding/json"
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

// handleSessionFiles handles file operations for a session.
// Routes:
//   - POST /api/sessions/{id}/files - Upload a file
//   - POST /api/sessions/{id}/files/from-path - Upload files from file paths (native app)
//   - GET /api/sessions/{id}/files - List files
//   - GET /api/sessions/{id}/files/{fileId} - Serve a file
//   - DELETE /api/sessions/{id}/files/{fileId} - Delete a file
func (s *Server) handleSessionFiles(w http.ResponseWriter, r *http.Request, sessionID string, filePath string) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Check if session exists
	if !store.Exists(sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Handle from-path endpoint (for native macOS app)
	if filePath == "from-path" {
		if r.Method == http.MethodPost {
			s.handleUploadFileFromPath(w, r, store, sessionID)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// If filePath is empty, we're operating on the files collection
	if filePath == "" {
		switch r.Method {
		case http.MethodPost:
			s.handleUploadFile(w, r, store, sessionID)
		case http.MethodGet:
			s.handleListFiles(w, r, store, sessionID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Operating on a specific file
	switch r.Method {
	case http.MethodGet:
		s.handleServeFile(w, r, store, sessionID, filePath)
	case http.MethodDelete:
		s.handleDeleteFile(w, r, store, sessionID, filePath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUploadFile handles POST /api/sessions/{id}/files
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxFileUploadSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxFileUploadSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "file_too_large", "File exceeds 50MB limit")
			return
		}
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get the file from the form
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
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
		s.handleFileSaveError(w, err)
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
func (s *Server) handleFileSaveError(w http.ResponseWriter, err error) {
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
		if s.logger != nil {
			s.logger.Error("Failed to save file", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "save_failed", "Failed to save file")
	}
}

// handleListFiles handles GET /api/sessions/{id}/files
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	files, err := store.ListFiles(sessionID)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list files", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to list files", http.StatusInternalServerError)
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
func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, fileID string) {
	// Validate file ID to prevent path traversal
	if strings.Contains(fileID, "/") || strings.Contains(fileID, "..") {
		http.Error(w, "Invalid file ID", http.StatusBadRequest)
		return
	}

	filePath, err := store.GetFilePath(sessionID, fileID)
	if err != nil {
		if err == session.ErrFileNotFound {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get file path", "error", err, "session_id", sessionID, "file_id", fileID)
		}
		http.Error(w, "Failed to get file", http.StatusInternalServerError)
		return
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Get file info for size
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
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
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, fileID string) {
	// Validate file ID to prevent path traversal
	if strings.Contains(fileID, "/") || strings.Contains(fileID, "..") {
		http.Error(w, "Invalid file ID", http.StatusBadRequest)
		return
	}

	err := store.DeleteFile(sessionID, fileID)
	if err != nil {
		if err == session.ErrFileNotFound {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete file", "error", err, "session_id", sessionID, "file_id", fileID)
		}
		http.Error(w, "Failed to delete file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UploadFileFromPathRequest is the request body for uploading files from file paths.
type UploadFileFromPathRequest struct {
	Paths []string `json:"paths"`
}

// handleUploadFileFromPath handles POST /api/sessions/{id}/files/from-path
// This endpoint is used by the native macOS app to upload files from file paths.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary file read attacks from remote clients.
func (s *Server) handleUploadFileFromPath(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Security check 1 (defense-in-depth): Reject ALL requests from the external listener.
	if IsExternalConnection(r) {
		if s.logger != nil {
			s.logger.Warn("Rejected file from-path request from external listener",
				"session_id", sessionID,
				"remote_addr", r.RemoteAddr,
			)
		}
		http.Error(w, "This endpoint is only available from localhost", http.StatusForbidden)
		return
	}

	// Security check 2: Only allow this endpoint from localhost (native macOS app).
	clientIP := getClientIPWithProxyCheck(r)
	if !isLoopbackIP(clientIP) {
		if s.logger != nil {
			s.logger.Warn("Rejected file from-path request from non-localhost",
				"client_ip", clientIP,
				"session_id", sessionID,
			)
		}
		http.Error(w, "This endpoint is only available from localhost", http.StatusForbidden)
		return
	}

	// Parse JSON body
	var req UploadFileFromPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.Paths) == 0 {
		http.Error(w, "No paths provided", http.StatusBadRequest)
		return
	}

	// Process each file path
	var responses []FileUploadResponse
	for _, filePath := range req.Paths {
		// Validate the path exists and is a file
		stat, err := os.Stat(filePath)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("File not found", "path", filePath, "error", err)
			}
			continue // Skip invalid paths
		}
		if stat.IsDir() {
			if s.logger != nil {
				s.logger.Warn("Path is a directory", "path", filePath)
			}
			continue
		}

		// Check file size
		if stat.Size() > maxFileUploadSize {
			if s.logger != nil {
				s.logger.Warn("File too large", "path", filePath, "size", stat.Size())
			}
			continue
		}

		// Read the file
		data, err := os.ReadFile(filePath)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("Failed to read file", "path", filePath, "error", err)
			}
			continue
		}

		// Detect MIME type
		mimeType := http.DetectContentType(data)

		// For text files, also check by extension if detection failed
		if mimeType == "application/octet-stream" || mimeType == "text/plain" {
			ext := strings.ToLower(getFileExtension(filePath))
			if extMime := session.GetFileMimeTypeFromExt(ext); extMime != "" {
				mimeType = extMime
			}
		}

		// Get filename from path
		filename := filePath[strings.LastIndex(filePath, "/")+1:]

		// Save the file
		info, err := store.SaveFile(sessionID, data, mimeType, filename)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("Failed to save file", "path", filePath, "error", err)
			}
			continue
		}

		responses = append(responses, FileUploadResponse{
			ID:       info.ID,
			URL:      "/api/sessions/" + sessionID + "/files/" + info.ID,
			Name:     info.Name,
			MimeType: info.MimeType,
			Size:     info.Size,
			Category: info.Category,
		})
	}

	if len(responses) > 0 {
		writeJSONCreated(w, responses)
	} else {
		writeJSONOK(w, responses)
	}
}
