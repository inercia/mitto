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

// File storage constants
const (
	filesDirName = "files"

	// Limits
	MaxFileSize        = 50 * 1024 * 1024 // 50 MB per file
	MaxTextFileSize    = 1 * 1024 * 1024  // 1 MB for text files (inline content)
	MaxFilesPerSession = 100
	MaxTotalFileSize   = 500 * 1024 * 1024 // 500 MB per session
	FileCleanupAge     = 30 * 24 * time.Hour
	FilePreserveRecent = 7 * 24 * time.Hour
)

// FileCategory represents how a file should be handled by the ACP protocol.
type FileCategory string

const (
	// FileCategoryText files are sent inline as TextBlock content.
	FileCategoryText FileCategory = "text"
	// FileCategoryBinary files are sent as ResourceLinkBlock references.
	FileCategoryBinary FileCategory = "binary"
)

// Supported file MIME types and their extensions.
// Text files are sent inline, binary files are sent as path references.
var supportedFileTypes = map[string]struct {
	Extension string
	Category  FileCategory
}{
	// Text/Code files (sent inline)
	"text/plain":             {".txt", FileCategoryText},
	"text/markdown":          {".md", FileCategoryText},
	"text/x-markdown":        {".md", FileCategoryText},
	"application/json":       {".json", FileCategoryText},
	"application/x-yaml":     {".yaml", FileCategoryText},
	"text/yaml":              {".yaml", FileCategoryText},
	"text/xml":               {".xml", FileCategoryText},
	"application/xml":        {".xml", FileCategoryText},
	"text/csv":               {".csv", FileCategoryText},
	"text/x-log":             {".log", FileCategoryText},
	"text/x-python":          {".py", FileCategoryText},
	"text/javascript":        {".js", FileCategoryText},
	"application/javascript": {".js", FileCategoryText},
	"text/typescript":        {".ts", FileCategoryText},
	"application/typescript": {".ts", FileCategoryText},
	"text/x-go":              {".go", FileCategoryText},
	"text/x-java":            {".java", FileCategoryText},
	"text/x-c":               {".c", FileCategoryText},
	"text/x-c++":             {".cpp", FileCategoryText},
	"text/x-rust":            {".rs", FileCategoryText},
	"text/x-ruby":            {".rb", FileCategoryText},
	"text/html":              {".html", FileCategoryText},
	"text/css":               {".css", FileCategoryText},
	"application/x-sh":       {".sh", FileCategoryText},
	"text/x-shellscript":     {".sh", FileCategoryText},
	"application/toml":       {".toml", FileCategoryText},
	"text/x-toml":            {".toml", FileCategoryText},

	// Binary files (sent as path references)
	"application/pdf":    {".pdf", FileCategoryBinary},
	"application/zip":    {".zip", FileCategoryBinary},
	"application/x-tar":  {".tar", FileCategoryBinary},
	"application/gzip":   {".gz", FileCategoryBinary},
	"application/x-gzip": {".gz", FileCategoryBinary},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {".docx", FileCategoryBinary},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {".xlsx", FileCategoryBinary},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {".pptx", FileCategoryBinary},
	"application/msword":       {".doc", FileCategoryBinary},
	"application/vnd.ms-excel": {".xls", FileCategoryBinary},
}

// File-specific errors
var (
	ErrFileTooLarge            = errors.New("file exceeds maximum size")
	ErrUnsupportedFileType     = errors.New("unsupported file type")
	ErrSessionFileLimit        = errors.New("session has reached maximum file count")
	ErrSessionFileStorageLimit = errors.New("session has reached maximum file storage")
	ErrFileNotFound            = errors.New("file not found")
)

// FileInfo contains metadata about a stored file.
type FileInfo struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	MimeType  string       `json:"mime_type"`
	Size      int64        `json:"size"`
	Category  FileCategory `json:"category"`
	CreatedAt time.Time    `json:"created_at"`
}

// FileRef is a lightweight reference to a file for session events.
type FileRef struct {
	ID       string       `json:"id"`
	Name     string       `json:"name,omitempty"`
	MimeType string       `json:"mime_type"`
	Category FileCategory `json:"category"`
}

// filesDir returns the files directory path for a session.
func (s *Store) filesDir(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), filesDirName)
}

// GetFileCategory returns the category for a supported MIME type.
func GetFileCategory(mimeType string) FileCategory {
	if info, ok := supportedFileTypes[mimeType]; ok {
		return info.Category
	}
	return FileCategoryBinary // Default to binary for unknown types
}

// GetFileExtension returns the file extension for a MIME type.
func GetFileExtension(mimeType string) string {
	if info, ok := supportedFileTypes[mimeType]; ok {
		return info.Extension
	}
	return ""
}

// SaveFile saves a file to the session's files directory.
// Returns the file ID that can be used to retrieve it later.
func (s *Store) SaveFile(sessionID string, data []byte, mimeType string, originalName string) (FileInfo, error) {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return FileInfo{}, ErrStoreClosed
	}

	// Determine file category and validate
	category := GetFileCategory(mimeType)

	// Validate size based on category
	maxSize := MaxFileSize
	if category == FileCategoryText {
		maxSize = MaxTextFileSize
	}
	if len(data) > maxSize {
		return FileInfo{}, ErrFileTooLarge
	}

	// Check session exists
	if _, err := s.readMetadata(sessionID); err != nil {
		return FileInfo{}, err
	}

	// Ensure files directory exists
	filesDir := s.filesDir(sessionID)
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		return FileInfo{}, fmt.Errorf("failed to create files directory: %w", err)
	}

	// Check session limits
	files, totalSize, err := s.listFilesInternal(sessionID)
	if err != nil && !os.IsNotExist(err) {
		return FileInfo{}, fmt.Errorf("failed to list files: %w", err)
	}

	if len(files) >= MaxFilesPerSession {
		return FileInfo{}, ErrSessionFileLimit
	}

	if totalSize+int64(len(data)) > MaxTotalFileSize {
		return FileInfo{}, ErrSessionFileStorageLimit
	}

	// Generate unique file ID with extension
	ext := GetFileExtension(mimeType)
	if ext == "" {
		// Try to get extension from original filename
		if idx := strings.LastIndex(originalName, "."); idx >= 0 {
			ext = originalName[idx:]
		}
	}
	fileID := generateFileID(len(files)+1, ext)

	// Write file
	filePath := filepath.Join(filesDir, fileID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return FileInfo{}, fmt.Errorf("failed to write file: %w", err)
	}

	info := FileInfo{
		ID:        fileID,
		Name:      originalName,
		MimeType:  mimeType,
		Size:      int64(len(data)),
		Category:  category,
		CreatedAt: time.Now(),
	}

	log.Debug("file saved",
		"session_id", sessionID,
		"file_id", fileID,
		"size", len(data),
		"mime_type", mimeType,
		"category", category)

	return info, nil
}

// GetFilePath returns the file path for a stored file.
func (s *Store) GetFilePath(sessionID, fileID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return "", ErrStoreClosed
	}

	filePath := filepath.Join(s.filesDir(sessionID), fileID)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", ErrFileNotFound
	}

	return filePath, nil
}

// ListFiles returns all files for a session.
func (s *Store) ListFiles(sessionID string) ([]FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	files, _, err := s.listFilesInternal(sessionID)
	return files, err
}

// listFilesInternal lists files without locking (caller must hold lock).
func (s *Store) listFilesInternal(sessionID string) ([]FileInfo, int64, error) {
	filesDir := s.filesDir(sessionID)
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var files []FileInfo
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
		mimeType := GetFileMimeTypeFromExt(filepath.Ext(entry.Name()))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		files = append(files, FileInfo{
			ID:        entry.Name(),
			Name:      entry.Name(),
			MimeType:  mimeType,
			Size:      info.Size(),
			Category:  GetFileCategory(mimeType),
			CreatedAt: info.ModTime(),
		})
		totalSize += info.Size()
	}

	// Sort by creation time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].CreatedAt.Before(files[j].CreatedAt)
	})

	return files, totalSize, nil
}

// DeleteFile removes a file from the session.
func (s *Store) DeleteFile(sessionID, fileID string) error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	filePath := filepath.Join(s.filesDir(sessionID), fileID)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return ErrFileNotFound
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	log.Debug("file deleted", "session_id", sessionID, "file_id", fileID)
	return nil
}

// CleanupOldFiles removes old files from inactive sessions.
// It preserves files from sessions updated within preserveRecent duration.
// For older sessions, it removes files older than maxAge.
func (s *Store) CleanupOldFiles(maxAge, preserveRecent time.Duration) (int, error) {
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

		// For older sessions, remove old files
		filesDir := s.filesDir(sessionID)
		entries, err := os.ReadDir(filesDir)
		if err != nil {
			continue // No files directory or error reading
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
				filePath := filepath.Join(filesDir, entry.Name())
				if err := os.Remove(filePath); err == nil {
					totalRemoved++
					log.Debug("cleaned up old file",
						"session_id", sessionID,
						"file_id", entry.Name(),
						"age", now.Sub(info.ModTime()))
				}
			}
		}
	}

	if totalRemoved > 0 {
		log.Info("file cleanup completed", "removed_count", totalRemoved)
	}

	return totalRemoved, nil
}

// generateFileID creates a unique file ID.
// Format: file_{seq}_{random8chars}.{ext}
func generateFileID(seq int, ext string) string {
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)
	if ext == "" {
		return fmt.Sprintf("file_%03d_%s", seq, randomHex)
	}
	return fmt.Sprintf("file_%03d_%s%s", seq, randomHex, ext)
}

// GetFileMimeTypeFromExt returns the MIME type for a file extension.
func GetFileMimeTypeFromExt(ext string) string {
	ext = strings.ToLower(ext)
	for mimeType, info := range supportedFileTypes {
		if info.Extension == ext {
			return mimeType
		}
	}
	// Common extensions not in the map
	switch ext {
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".xml":
		return "application/xml"
	case ".py":
		return "text/x-python"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".go":
		return "text/x-go"
	case ".java":
		return "text/x-java"
	case ".c":
		return "text/x-c"
	case ".cpp", ".cc", ".cxx":
		return "text/x-c++"
	case ".rs":
		return "text/x-rust"
	case ".rb":
		return "text/x-ruby"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".sh":
		return "application/x-sh"
	case ".toml":
		return "application/toml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return ""
}
