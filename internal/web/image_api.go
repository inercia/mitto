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

// Image upload limits
const (
	maxUploadSize = 10 * 1024 * 1024 // 10 MB (matches session.MaxImageSize)
)

// ImageUploadResponse is the response for a successful image upload.
type ImageUploadResponse struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// handleSessionImages handles image operations for a session.
// Routes:
//   - POST /api/sessions/{id}/images - Upload an image
//   - POST /api/sessions/{id}/images/from-path - Upload images from file paths (native app)
//   - GET /api/sessions/{id}/images - List images
//   - GET /api/sessions/{id}/images/{imageId} - Serve an image
//   - DELETE /api/sessions/{id}/images/{imageId} - Delete an image
func (s *Server) handleSessionImages(w http.ResponseWriter, r *http.Request, sessionID string, imagePath string) {
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
	if imagePath == "from-path" {
		if r.Method == http.MethodPost {
			s.handleUploadImageFromPath(w, r, store, sessionID)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// If imagePath is empty, we're operating on the images collection
	if imagePath == "" {
		switch r.Method {
		case http.MethodPost:
			s.handleUploadImage(w, r, store, sessionID)
		case http.MethodGet:
			s.handleListImages(w, r, store, sessionID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Operating on a specific image
	switch r.Method {
	case http.MethodGet:
		s.handleServeImage(w, r, store, sessionID, imagePath)
	case http.MethodDelete:
		s.handleDeleteImage(w, r, store, sessionID, imagePath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUploadImage handles POST /api/sessions/{id}/images
func (s *Server) handleUploadImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "image_too_large", "Image exceeds 10MB limit")
			return
		}
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get the file from the form
	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusInternalServerError)
		return
	}

	// Determine MIME type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		// Try to detect from content
		mimeType = http.DetectContentType(data)
	}

	// Validate MIME type
	if !session.IsSupportedImageType(mimeType) {
		writeErrorJSON(w, http.StatusBadRequest, "unsupported_format", "Only PNG, JPEG, GIF, and WebP images are supported")
		return
	}

	// Save the image
	info, err := store.SaveImage(sessionID, data, mimeType, header.Filename)
	if err != nil {
		s.handleImageSaveError(w, err)
		return
	}

	// Build response
	response := ImageUploadResponse{
		ID:       info.ID,
		URL:      "/api/sessions/" + sessionID + "/images/" + info.ID,
		Name:     info.Name,
		MimeType: info.MimeType,
		Size:     info.Size,
	}

	writeJSONCreated(w, response)
}

// handleImageSaveError handles errors from SaveImage and returns appropriate HTTP responses.
func (s *Server) handleImageSaveError(w http.ResponseWriter, err error) {
	switch err {
	case session.ErrImageTooLarge:
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "image_too_large", "Image exceeds 10MB limit")
	case session.ErrUnsupportedFormat:
		writeErrorJSON(w, http.StatusBadRequest, "unsupported_format", "Only PNG, JPEG, GIF, and WebP images are supported")
	case session.ErrSessionImageLimit:
		writeErrorJSON(w, http.StatusBadRequest, "session_limit", "Session has reached the maximum of 50 images")
	case session.ErrSessionStorageLimit:
		writeErrorJSON(w, http.StatusBadRequest, "storage_limit", "Session has reached the maximum storage of 100MB for images")
	default:
		if s.logger != nil {
			s.logger.Error("Failed to save image", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "save_failed", "Failed to save image")
	}
}

// handleListImages handles GET /api/sessions/{id}/images
func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	images, err := store.ListImages(sessionID)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list images", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to list images", http.StatusInternalServerError)
		return
	}

	// Add URL to each image
	type ImageWithURL struct {
		session.ImageInfo
		URL string `json:"url"`
	}

	result := make([]ImageWithURL, len(images))
	for i, img := range images {
		result[i] = ImageWithURL{
			ImageInfo: img,
			URL:       "/api/sessions/" + sessionID + "/images/" + img.ID,
		}
	}

	writeJSONOK(w, result)
}

// handleServeImage handles GET /api/sessions/{id}/images/{imageId}
func (s *Server) handleServeImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, imageID string) {
	// Validate image ID to prevent path traversal
	if strings.Contains(imageID, "/") || strings.Contains(imageID, "..") {
		http.Error(w, "Invalid image ID", http.StatusBadRequest)
		return
	}

	imagePath, err := store.GetImagePath(sessionID, imageID)
	if err != nil {
		if err == session.ErrImageNotFound {
			http.Error(w, "Image not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get image path", "error", err, "session_id", sessionID, "image_id", imageID)
		}
		http.Error(w, "Failed to get image", http.StatusInternalServerError)
		return
	}

	// Open the file
	file, err := os.Open(imagePath)
	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Get file info for size
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusInternalServerError)
		return
	}

	// Determine content type from extension
	mimeType := session.GetMimeTypeFromExt(strings.ToLower(imageID[strings.LastIndex(imageID, "."):]))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Set headers
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Cache-Control", "private, max-age=86400") // Cache for 1 day

	// Serve the file
	http.ServeContent(w, r, imageID, stat.ModTime(), file)
}

// handleDeleteImage handles DELETE /api/sessions/{id}/images/{imageId}
func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, imageID string) {
	// Validate image ID to prevent path traversal
	if strings.Contains(imageID, "/") || strings.Contains(imageID, "..") {
		http.Error(w, "Invalid image ID", http.StatusBadRequest)
		return
	}

	err := store.DeleteImage(sessionID, imageID)
	if err != nil {
		if err == session.ErrImageNotFound {
			http.Error(w, "Image not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete image", "error", err, "session_id", sessionID, "image_id", imageID)
		}
		http.Error(w, "Failed to delete image", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UploadFromPathRequest is the request body for uploading images from file paths.
type UploadFromPathRequest struct {
	Paths []string `json:"paths"`
}

// handleUploadImageFromPath handles POST /api/sessions/{id}/images/from-path
// This endpoint is used by the native macOS app to upload images from file paths.
// SECURITY: This endpoint is restricted to localhost connections only to prevent
// arbitrary file read attacks from remote clients.
func (s *Server) handleUploadImageFromPath(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	// Security check: Only allow this endpoint from localhost (native macOS app).
	// This prevents remote attackers from reading arbitrary files on the server.
	clientIP := getClientIPWithProxyCheck(r)
	if !isLoopbackIP(clientIP) {
		if s.logger != nil {
			s.logger.Warn("Rejected from-path request from non-localhost",
				"client_ip", clientIP,
				"session_id", sessionID,
			)
		}
		http.Error(w, "This endpoint is only available from localhost", http.StatusForbidden)
		return
	}

	// Parse JSON body
	var req UploadFromPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(req.Paths) == 0 {
		http.Error(w, "No paths provided", http.StatusBadRequest)
		return
	}

	// Process each file path
	var responses []ImageUploadResponse
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
		if stat.Size() > maxUploadSize {
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

		// Validate MIME type
		if !session.IsSupportedImageType(mimeType) {
			if s.logger != nil {
				s.logger.Warn("Unsupported image type", "path", filePath, "mime_type", mimeType)
			}
			continue
		}

		// Get filename from path
		filename := filePath[strings.LastIndex(filePath, "/")+1:]

		// Save the image
		info, err := store.SaveImage(sessionID, data, mimeType, filename)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("Failed to save image", "path", filePath, "error", err)
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
