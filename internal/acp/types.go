package acp

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coder/acp-go-sdk"
)

// Attachment type constants
const (
	AttachmentTypeImage      = "image"
	AttachmentTypeTextFile   = "text_file"
	AttachmentTypeBinaryFile = "binary_file"
)

// Attachment represents a file attachment (image, text file, etc.) to be sent with a prompt.
type Attachment struct {
	// Type is the attachment type: "image", "text_file", or "binary_file"
	Type string `json:"type"`
	// Data is the base64-encoded content (for images) or plain text content (for text files)
	Data string `json:"data"`
	// MimeType is the MIME type (e.g., "image/png", "text/plain")
	MimeType string `json:"mime_type"`
	// Name is the original filename (optional)
	Name string `json:"name,omitempty"`
	// FilePath is the absolute path to the file (for binary files that are referenced, not embedded)
	FilePath string `json:"file_path,omitempty"`
}

// ImageAttachmentFromFile creates an image attachment from a file path.
func ImageAttachmentFromFile(path, mimeType string) (Attachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{
		Type:     AttachmentTypeImage,
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
		Name:     filepath.Base(path),
	}, nil
}

// TextFileAttachmentFromFile creates a text file attachment from a file path.
// The file content is read and included inline as text.
func TextFileAttachmentFromFile(path, mimeType string) (Attachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{
		Type:     AttachmentTypeTextFile,
		Data:     string(data), // Plain text, not base64
		MimeType: mimeType,
		Name:     filepath.Base(path),
		FilePath: path,
	}, nil
}

// BinaryFileAttachment creates a binary file attachment that references a file path.
// The file content is NOT embedded; the agent is expected to read it via filesystem access.
func BinaryFileAttachment(path, mimeType string) Attachment {
	return Attachment{
		Type:     AttachmentTypeBinaryFile,
		MimeType: mimeType,
		Name:     filepath.Base(path),
		FilePath: path,
	}
}

// ToContentBlock converts an attachment to an ACP ContentBlock.
func (a Attachment) ToContentBlock() acp.ContentBlock {
	switch a.Type {
	case AttachmentTypeImage:
		return acp.ImageBlock(a.Data, a.MimeType)
	case AttachmentTypeTextFile:
		// Include file content inline with a header showing the filename
		header := fmt.Sprintf("=== File: %s ===\n", a.Name)
		footer := fmt.Sprintf("\n=== End of %s ===", a.Name)
		return acp.TextBlock(header + a.Data + footer)
	case AttachmentTypeBinaryFile:
		// For binary files, use ResourceLinkBlock to reference the file
		// The agent can read the file via its filesystem access
		return acp.ResourceLinkBlock(a.Name, "file://"+a.FilePath)
	default:
		// Fallback to text block with description
		return acp.TextBlock("[Attachment: " + a.Name + "]")
	}
}

// BuildContentBlocks creates a slice of ContentBlocks from a message and attachments.
// Images are placed before the text message to provide context.
func BuildContentBlocks(message string, attachments []Attachment) []acp.ContentBlock {
	blocks := make([]acp.ContentBlock, 0, len(attachments)+1)

	// Add attachments first (images provide context for the question)
	for _, att := range attachments {
		blocks = append(blocks, att.ToContentBlock())
	}

	// Add text message
	if message != "" {
		blocks = append(blocks, acp.TextBlock(message))
	}

	return blocks
}
