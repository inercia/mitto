package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStore_SaveImage(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session first
	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create a simple PNG-like data (just for testing, not a real PNG)
	imageData := bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 100)

	// Save image
	info, err := store.SaveImage("test-session", imageData, "image/png", "test.png")
	if err != nil {
		t.Fatalf("SaveImage failed: %v", err)
	}

	if info.ID == "" {
		t.Error("expected non-empty image ID")
	}
	if !strings.HasPrefix(info.ID, "img_001_") {
		t.Errorf("expected image ID to start with 'img_001_', got %s", info.ID)
	}
	if !strings.HasSuffix(info.ID, ".png") {
		t.Errorf("expected image ID to end with '.png', got %s", info.ID)
	}
	if info.MimeType != "image/png" {
		t.Errorf("expected mime type 'image/png', got %s", info.MimeType)
	}
	if info.Size != int64(len(imageData)) {
		t.Errorf("expected size %d, got %d", len(imageData), info.Size)
	}
}

func TestStore_SaveImage_UnsupportedFormat(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.SaveImage("test-session", []byte("data"), "image/bmp", "test.bmp")
	if err != ErrUnsupportedFormat {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestStore_SaveImage_TooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create data larger than MaxImageSize
	largeData := make([]byte, MaxImageSize+1)
	_, err = store.SaveImage("test-session", largeData, "image/png", "large.png")
	if err != ErrImageTooLarge {
		t.Errorf("expected ErrImageTooLarge, got %v", err)
	}
}

func TestStore_GetImagePath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	imageData := []byte("fake image data")
	info, err := store.SaveImage("test-session", imageData, "image/jpeg", "photo.jpg")
	if err != nil {
		t.Fatalf("SaveImage failed: %v", err)
	}

	path, err := store.GetImagePath("test-session", info.ID)
	if err != nil {
		t.Fatalf("GetImagePath failed: %v", err)
	}

	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestStore_GetImagePath_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.GetImagePath("test-session", "nonexistent.png")
	if err != ErrImageNotFound {
		t.Errorf("expected ErrImageNotFound, got %v", err)
	}
}

func TestStore_ListImages(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Save multiple images
	for i := 0; i < 3; i++ {
		_, err := store.SaveImage("test-session", []byte("data"), "image/png", "test.png")
		if err != nil {
			t.Fatalf("SaveImage failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	images, err := store.ListImages("test-session")
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}

	if len(images) != 3 {
		t.Errorf("expected 3 images, got %d", len(images))
	}
}

func TestStore_DeleteImage(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	info, err := store.SaveImage("test-session", []byte("data"), "image/png", "test.png")
	if err != nil {
		t.Fatalf("SaveImage failed: %v", err)
	}

	// Delete the image
	if err := store.DeleteImage("test-session", info.ID); err != nil {
		t.Fatalf("DeleteImage failed: %v", err)
	}

	// Verify it's gone
	_, err = store.GetImagePath("test-session", info.ID)
	if err != ErrImageNotFound {
		t.Errorf("expected ErrImageNotFound after delete, got %v", err)
	}
}

func TestStore_SessionImageLimit(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Save MaxImagesPerSession images
	for i := 0; i < MaxImagesPerSession; i++ {
		_, err := store.SaveImage("test-session", []byte("data"), "image/png", "test.png")
		if err != nil {
			t.Fatalf("SaveImage %d failed: %v", i, err)
		}
	}

	// Try to save one more - should fail
	_, err = store.SaveImage("test-session", []byte("data"), "image/png", "test.png")
	if err != ErrSessionImageLimit {
		t.Errorf("expected ErrSessionImageLimit, got %v", err)
	}
}

func TestIsSupportedImageType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/bmp", false},
		{"image/tiff", false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		got := IsSupportedImageType(tt.mimeType)
		if got != tt.want {
			t.Errorf("IsSupportedImageType(%q) = %v, want %v", tt.mimeType, got, tt.want)
		}
	}
}

func TestGetMimeTypeExtension(t *testing.T) {
	tests := []struct {
		mimeType string
		wantExt  string
		wantOk   bool
	}{
		{"image/png", ".png", true},
		{"image/jpeg", ".jpg", true},
		{"image/gif", ".gif", true},
		{"image/webp", ".webp", true},
		{"image/bmp", "", false},
	}

	for _, tt := range tests {
		ext, ok := GetMimeTypeExtension(tt.mimeType)
		if ext != tt.wantExt || ok != tt.wantOk {
			t.Errorf("GetMimeTypeExtension(%q) = (%q, %v), want (%q, %v)",
				tt.mimeType, ext, ok, tt.wantExt, tt.wantOk)
		}
	}
}
