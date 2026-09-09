package acpbackend

import (
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// ToNeutralContentBlock translates a single ACP content block into its
// protocol-neutral counterpart. ResourceLink is translated to FileBlock since
// it is the closest existing analogue (a URI-addressed external reference),
// mirroring the reverse direction already used by
// internal/acp.BinaryFileAttachment. Content kinds without a neutral
// analogue yet (Audio, embedded Resource) are dropped rather than lossily
// coerced into TextBlock/FileBlock; ok is false in that case (and for an
// empty/unrecognized union) so callers can skip the block.
func ToNeutralContentBlock(b acp.ContentBlock) (block agentbackend.ContentBlock, ok bool) {
	switch {
	case b.Text != nil:
		return agentbackend.ContentBlock{Text: &agentbackend.TextBlock{Text: b.Text.Text}}, true
	case b.Image != nil:
		return agentbackend.ContentBlock{Image: &agentbackend.ImageBlock{Data: b.Image.Data, MimeType: b.Image.MimeType}}, true
	case b.ResourceLink != nil:
		mime := ""
		if b.ResourceLink.MimeType != nil {
			mime = *b.ResourceLink.MimeType
		}
		return agentbackend.ContentBlock{File: &agentbackend.FileBlock{Path: b.ResourceLink.Uri, MimeType: mime}}, true
	default:
		return agentbackend.ContentBlock{}, false
	}
}

// ToNeutralContentBlocks translates a slice of ACP content blocks, silently
// skipping any block ToNeutralContentBlock cannot represent (see its doc).
func ToNeutralContentBlocks(blocks []acp.ContentBlock) []agentbackend.ContentBlock {
	out := make([]agentbackend.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if nb, ok := ToNeutralContentBlock(b); ok {
			out = append(out, nb)
		}
	}
	return out
}

// FromNeutralContentBlock translates a single neutral content block into its
// ACP counterpart using the same content-block helpers internal/acp uses
// (acp.TextBlock / acp.ImageBlock / acp.ResourceLinkBlock), so the wire shape
// matches the existing non-adapter path. Returns ok=false for a zero-value
// ContentBlock (no field set).
func FromNeutralContentBlock(b agentbackend.ContentBlock) (out acp.ContentBlock, ok bool) {
	switch {
	case b.Text != nil:
		return acp.TextBlock(b.Text.Text), true
	case b.Image != nil:
		return acp.ImageBlock(b.Image.Data, b.Image.MimeType), true
	case b.File != nil:
		return acp.ResourceLinkBlock(b.File.Path, fileURI(b.File.Path)), true
	default:
		return acp.ContentBlock{}, false
	}
}

// FromNeutralContentBlocks translates a slice of neutral content blocks,
// skipping any zero-value block (see FromNeutralContentBlock).
func FromNeutralContentBlocks(blocks []agentbackend.ContentBlock) []acp.ContentBlock {
	out := make([]acp.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if ab, ok := FromNeutralContentBlock(b); ok {
			out = append(out, ab)
		}
	}
	return out
}

// fileURI returns path unchanged when it already carries a URI scheme
// (e.g. it round-trips a FileBlock built from an ACP ResourceLink's Uri),
// otherwise prefixes "file://" so a FileBlock built directly from a plain
// filesystem path still produces a valid ResourceLink Uri.
func fileURI(path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	return "file://" + path
}
