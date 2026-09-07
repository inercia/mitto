package agentbackend

// ContentBlock is neutral prompt/response content, replacing protocol-specific
// content block types (e.g. acp.ContentBlock). Exactly one field is non-nil;
// this mirrors the codebase's existing nil-pointer discrimination convention
// for content blocks (see internal/conversation's ACP ContentBlock handling)
// rather than introducing a separate Type() discriminator.
type ContentBlock struct {
	Text  *TextBlock
	Image *ImageBlock
	File  *FileBlock
}

// TextBlock is plain text content.
type TextBlock struct {
	Text string
}

// ImageBlock is inline image content.
type ImageBlock struct {
	// Data is the base64-encoded image payload.
	Data string
	// MimeType is the image's MIME type (e.g. "image/png").
	MimeType string
}

// FileBlock references a file relevant to the prompt (e.g. an attachment).
type FileBlock struct {
	// Path is the file's path as understood by the backend.
	Path string
	// MimeType is the file's MIME type, when known.
	MimeType string
}
