package acp

import (
	"encoding/base64"
	"os"

	"github.com/coder/acp-go-sdk"
)

// Attachment represents a file attachment (image, etc.) to be sent with a prompt.
type Attachment struct {
	// Type is the attachment type (e.g., "image")
	Type string `json:"type"`
	// Data is the base64-encoded content
	Data string `json:"data"`
	// MimeType is the MIME type (e.g., "image/png")
	MimeType string `json:"mime_type"`
	// Name is the original filename (optional)
	Name string `json:"name,omitempty"`
}

// ImageAttachmentFromFile creates an image attachment from a file path.
func ImageAttachmentFromFile(path, mimeType string) (Attachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
		Name:     path,
	}, nil
}

// ToContentBlock converts an attachment to an ACP ContentBlock.
func (a Attachment) ToContentBlock() acp.ContentBlock {
	switch a.Type {
	case "image":
		return acp.ImageBlock(a.Data, a.MimeType)
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
