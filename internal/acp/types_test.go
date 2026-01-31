package acp

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestImageAttachment(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("fake image data"))
	mimeType := "image/png"
	name := "screenshot.png"

	att := ImageAttachment(data, mimeType, name)

	if att.Type != "image" {
		t.Errorf("Type = %q, want %q", att.Type, "image")
	}
	if att.Data != data {
		t.Errorf("Data = %q, want %q", att.Data, data)
	}
	if att.MimeType != mimeType {
		t.Errorf("MimeType = %q, want %q", att.MimeType, mimeType)
	}
	if att.Name != name {
		t.Errorf("Name = %q, want %q", att.Name, name)
	}
}

func TestImageAttachmentFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.png")
	content := []byte("fake PNG content")

	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	att, err := ImageAttachmentFromFile(path, "image/png")
	if err != nil {
		t.Fatalf("ImageAttachmentFromFile failed: %v", err)
	}

	if att.Type != "image" {
		t.Errorf("Type = %q, want %q", att.Type, "image")
	}
	if att.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", att.MimeType, "image/png")
	}
	if att.Name != path {
		t.Errorf("Name = %q, want %q", att.Name, path)
	}

	// Verify data is base64 encoded
	decoded, err := base64.StdEncoding.DecodeString(att.Data)
	if err != nil {
		t.Fatalf("failed to decode base64 data: %v", err)
	}
	if string(decoded) != string(content) {
		t.Errorf("decoded data = %q, want %q", string(decoded), string(content))
	}
}

func TestImageAttachmentFromFile_NotFound(t *testing.T) {
	_, err := ImageAttachmentFromFile("/nonexistent/file.png", "image/png")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestAttachment_ToContentBlock_Image(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("fake image"))
	att := Attachment{
		Type:     "image",
		Data:     data,
		MimeType: "image/jpeg",
		Name:     "photo.jpg",
	}

	block := att.ToContentBlock()

	// The block should be an image block
	if block.Image == nil {
		t.Fatal("expected Image block, got nil")
	}
	if block.Image.Data != data {
		t.Errorf("Image data = %q, want %q", block.Image.Data, data)
	}
	if block.Image.MimeType != "image/jpeg" {
		t.Errorf("Image mime type = %q, want %q", block.Image.MimeType, "image/jpeg")
	}
}

func TestAttachment_ToContentBlock_Unknown(t *testing.T) {
	att := Attachment{
		Type: "unknown",
		Name: "file.dat",
	}

	block := att.ToContentBlock()

	// Unknown types should fall back to text block
	if block.Text == nil {
		t.Fatal("expected Text block for unknown type, got nil")
	}
	if block.Text.Text != "[Attachment: file.dat]" {
		t.Errorf("Text = %q, want %q", block.Text.Text, "[Attachment: file.dat]")
	}
}

func TestBuildContentBlocks_MessageOnly(t *testing.T) {
	blocks := BuildContentBlocks("Hello, world!", nil)

	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Text == nil {
		t.Fatal("expected Text block")
	}
	if blocks[0].Text.Text != "Hello, world!" {
		t.Errorf("Text = %q, want %q", blocks[0].Text.Text, "Hello, world!")
	}
}

func TestBuildContentBlocks_EmptyMessage(t *testing.T) {
	blocks := BuildContentBlocks("", nil)

	if len(blocks) != 0 {
		t.Errorf("got %d blocks, want 0 for empty message", len(blocks))
	}
}

func TestBuildContentBlocks_WithAttachments(t *testing.T) {
	attachments := []Attachment{
		ImageAttachment("data1", "image/png", "img1.png"),
		ImageAttachment("data2", "image/jpeg", "img2.jpg"),
	}

	blocks := BuildContentBlocks("What's in these images?", attachments)

	// Should have 2 image blocks + 1 text block = 3 total
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(blocks))
	}

	// First two should be images (attachments come first)
	if blocks[0].Image == nil {
		t.Error("block[0] should be an image")
	}
	if blocks[1].Image == nil {
		t.Error("block[1] should be an image")
	}

	// Last should be text
	if blocks[2].Text == nil {
		t.Error("block[2] should be text")
	}
	if blocks[2].Text.Text != "What's in these images?" {
		t.Errorf("Text = %q, want %q", blocks[2].Text.Text, "What's in these images?")
	}
}
