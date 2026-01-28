package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/logging"
)

// Image storage constants
const (
	imagesDirName = "images"

	// Limits
	MaxImageSize        = 10 * 1024 * 1024 // 10 MB
	MaxImagesPerSession = 50
	MaxTotalImageSize   = 100 * 1024 * 1024 // 100 MB per session
	ImageCleanupAge     = 30 * 24 * time.Hour
	ImagePreserveRecent = 7 * 24 * time.Hour
)

// Supported image MIME types
var supportedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var (
	ErrImageTooLarge       = errors.New("image exceeds maximum size")
	ErrUnsupportedFormat   = errors.New("unsupported image format")
	ErrSessionImageLimit   = errors.New("session has reached maximum image count")
	ErrSessionStorageLimit = errors.New("session has reached maximum image storage")
	ErrImageNotFound       = errors.New("image not found")
)

// ImageInfo contains metadata about a stored image.
type ImageInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MimeType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// ImageRef is a lightweight reference to an image for session events.
type ImageRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type"`
}

// imagesDir returns the images directory path for a session.
func (s *Store) imagesDir(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), imagesDirName)
}

// SaveImage saves an image to the session's images directory.
// Returns the image ID that can be used to retrieve it later.
func (s *Store) SaveImage(sessionID string, data []byte, mimeType string, originalName string) (ImageInfo, error) {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ImageInfo{}, ErrStoreClosed
	}

	// Validate MIME type
	ext, ok := supportedImageTypes[mimeType]
	if !ok {
		return ImageInfo{}, ErrUnsupportedFormat
	}

	// Validate size
	if len(data) > MaxImageSize {
		return ImageInfo{}, ErrImageTooLarge
	}

	// Check session exists
	if _, err := s.readMetadata(sessionID); err != nil {
		return ImageInfo{}, err
	}

	// Ensure images directory exists
	imagesDir := s.imagesDir(sessionID)
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return ImageInfo{}, fmt.Errorf("failed to create images directory: %w", err)
	}

	// Check session limits
	images, totalSize, err := s.listImagesInternal(sessionID)
	if err != nil && !os.IsNotExist(err) {
		return ImageInfo{}, fmt.Errorf("failed to list images: %w", err)
	}

	if len(images) >= MaxImagesPerSession {
		return ImageInfo{}, ErrSessionImageLimit
	}

	if totalSize+int64(len(data)) > MaxTotalImageSize {
		return ImageInfo{}, ErrSessionStorageLimit
	}

	// Generate unique image ID
	imageID := generateImageID(len(images)+1, ext)

	// Write image file
	imagePath := filepath.Join(imagesDir, imageID)
	if err := os.WriteFile(imagePath, data, 0644); err != nil {
		return ImageInfo{}, fmt.Errorf("failed to write image: %w", err)
	}

	info := ImageInfo{
		ID:        imageID,
		Name:      originalName,
		MimeType:  mimeType,
		Size:      int64(len(data)),
		CreatedAt: time.Now(),
	}

	log.Debug("image saved",
		"session_id", sessionID,
		"image_id", imageID,
		"size", len(data),
		"mime_type", mimeType)

	return info, nil
}

// GetImagePath returns the file path for an image.
func (s *Store) GetImagePath(sessionID, imageID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return "", ErrStoreClosed
	}

	imagePath := filepath.Join(s.imagesDir(sessionID), imageID)
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return "", ErrImageNotFound
	}

	return imagePath, nil
}

// ListImages returns all images for a session.
func (s *Store) ListImages(sessionID string) ([]ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	images, _, err := s.listImagesInternal(sessionID)
	return images, err
}

// listImagesInternal lists images without locking (caller must hold lock).
func (s *Store) listImagesInternal(sessionID string) ([]ImageInfo, int64, error) {
	imagesDir := s.imagesDir(sessionID)
	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var images []ImageInfo
	var totalSize int64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Determine MIME type from extension
		mimeType := GetMimeTypeFromExt(filepath.Ext(entry.Name()))
		if mimeType == "" {
			continue // Skip non-image files
		}

		images = append(images, ImageInfo{
			ID:        entry.Name(),
			Name:      entry.Name(),
			MimeType:  mimeType,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
		totalSize += info.Size()
	}

	// Sort by creation time (oldest first)
	sort.Slice(images, func(i, j int) bool {
		return images[i].CreatedAt.Before(images[j].CreatedAt)
	})

	return images, totalSize, nil
}

// DeleteImage removes an image from the session.
func (s *Store) DeleteImage(sessionID, imageID string) error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	imagePath := filepath.Join(s.imagesDir(sessionID), imageID)
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return ErrImageNotFound
	}

	if err := os.Remove(imagePath); err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	log.Debug("image deleted", "session_id", sessionID, "image_id", imageID)
	return nil
}

// CleanupOldImages removes old images from inactive sessions.
// It preserves images from sessions updated within preserveRecent duration.
// For older sessions, it removes images older than maxAge.
func (s *Store) CleanupOldImages(maxAge, preserveRecent time.Duration) (int, error) {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	sessions, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	now := time.Now()
	var totalRemoved int

	for _, sessionEntry := range sessions {
		if !sessionEntry.IsDir() {
			continue
		}

		sessionID := sessionEntry.Name()

		// Read session metadata to check last update time
		meta, err := s.readMetadata(sessionID)
		if err != nil {
			continue // Skip sessions with invalid metadata
		}

		// Skip recent sessions entirely
		if now.Sub(meta.UpdatedAt) < preserveRecent {
			continue
		}

		// For older sessions, remove old images
		imagesDir := s.imagesDir(sessionID)
		entries, err := os.ReadDir(imagesDir)
		if err != nil {
			continue // No images directory or error reading
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			if now.Sub(info.ModTime()) > maxAge {
				imagePath := filepath.Join(imagesDir, entry.Name())
				if err := os.Remove(imagePath); err == nil {
					totalRemoved++
					log.Debug("cleaned up old image",
						"session_id", sessionID,
						"image_id", entry.Name(),
						"age", now.Sub(info.ModTime()))
				}
			}
		}
	}

	if totalRemoved > 0 {
		log.Info("image cleanup completed", "removed_count", totalRemoved)
	}

	return totalRemoved, nil
}

// generateImageID creates a unique image ID.
// Format: img_{seq}_{random8chars}.{ext}
func generateImageID(seq int, ext string) string {
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)
	return fmt.Sprintf("img_%03d_%s%s", seq, randomHex, ext)
}

// GetMimeTypeFromExt returns the MIME type for a file extension.
func GetMimeTypeFromExt(ext string) string {
	ext = strings.ToLower(ext)
	for mimeType, e := range supportedImageTypes {
		if e == ext || (ext == ".jpeg" && e == ".jpg") {
			return mimeType
		}
	}
	return ""
}

// GetMimeTypeExtension returns the file extension for a MIME type.
func GetMimeTypeExtension(mimeType string) (string, bool) {
	ext, ok := supportedImageTypes[mimeType]
	return ext, ok
}

// IsSupportedImageType checks if a MIME type is supported.
func IsSupportedImageType(mimeType string) bool {
	_, ok := supportedImageTypes[mimeType]
	return ok
}
