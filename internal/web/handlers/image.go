package handlers

import (
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

// HandleSessionImages handles image operations for a session.
// Routes:
//   - POST /api/sessions/{id}/images - Upload an image
//   - POST /api/sessions/{id}/images/from-path - Upload images from file paths (native app)
//   - GET /api/sessions/{id}/images - List images
//   - GET /api/sessions/{id}/images/{imageId} - Serve an image
//   - DELETE /api/sessions/{id}/images/{imageId} - Delete an image
func (h *Handlers) HandleSessionImages(w http.ResponseWriter, r *http.Request, sessionID string, imagePath string) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
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
			h.handleUploadImageFromPath(w, r, store, sessionID)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// If imagePath is empty, we're operating on the images collection
	if imagePath == "" {
		switch r.Method {
		case http.MethodPost:
			h.handleUploadImage(w, r, store, sessionID)
		case http.MethodGet:
			h.handleListImages(w, r, store, sessionID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Operating on a specific image
	switch r.Method {
	case http.MethodGet:
		h.handleServeImage(w, r, store, sessionID, imagePath)
	case http.MethodDelete:
		h.handleDeleteImage(w, r, store, sessionID, imagePath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUploadImage handles POST /api/sessions/{id}/images
func (h *Handlers) handleUploadImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
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
	if mimeType == "" || mimeType == "application/octet-stream" {
		// Try to detect from content (multipart form files often default to application/octet-stream)
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
		h.handleImageSaveError(w, err)
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
func (h *Handlers) handleImageSaveError(w http.ResponseWriter, err error) {
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
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save image", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "save_failed", "Failed to save image")
	}
}

// handleListImages handles GET /api/sessions/{id}/images
func (h *Handlers) handleListImages(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID string) {
	images, err := store.ListImages(sessionID)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list images", "error", err, "session_id", sessionID)
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
func (h *Handlers) handleServeImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, imageID string) {
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
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get image path", "error", err, "session_id", sessionID, "image_id", imageID)
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
func (h *Handlers) handleDeleteImage(w http.ResponseWriter, r *http.Request, store *session.Store, sessionID, imageID string) {
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
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete image", "error", err, "session_id", sessionID, "image_id", imageID)
		}
		http.Error(w, "Failed to delete image", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
