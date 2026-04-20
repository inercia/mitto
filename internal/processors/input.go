package processors

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProcessorInput provides context for processor execution.
type ProcessorInput struct {
	// Message is the user's message text.
	Message string `json:"message"`
	// IsFirstMessage indicates if this is the first message in the conversation.
	IsFirstMessage bool `json:"is_first_message"`
	// SessionID is the current session identifier.
	SessionID string `json:"session_id"`
	// WorkingDir is the session's working directory.
	WorkingDir string `json:"working_dir"`
	// History contains previous conversation turns (only for InputConversation).
	History []HistoryEntry `json:"history,omitempty"`

	// Session metadata for variable substitution
	// ParentSessionID is the parent conversation ID (empty if this is a root session).
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// ParentSessionName is the parent conversation title/name (empty if no parent or name not set).
	ParentSessionName string `json:"parent_session_name,omitempty"`
	// SessionName is the conversation title/name.
	SessionName string `json:"session_name,omitempty"`
	// ACPServer is the ACP server name (e.g., "claude-code").
	ACPServer string `json:"acp_server,omitempty"`
	// WorkspaceUUID is the workspace identifier.
	WorkspaceUUID string `json:"workspace_uuid,omitempty"`
	// AvailableACPServers lists the ACP servers that have workspaces configured for the
	// session's working directory. Mirrors the data reported by the MCP tool.
	// Each entry includes the server name, type, tags, and whether it is the current server.
	AvailableACPServers []AvailableACPServer `json:"available_acp_servers,omitempty"`
	// ChildSessions lists direct child sessions of the current session.
	// Each entry includes the session ID, name, and ACP server.
	ChildSessions []ChildSession `json:"child_sessions,omitempty"`
	// MCPToolNames is the list of MCP tool names available in the current workspace.
	// Used for enabledWhenMCP pattern matching and tools.* CEL context.
	// May be empty if tools haven't been fetched yet.
	MCPToolNames []string `json:"-"`
}

// AvailableACPServer describes an ACP server available in the session's workspace.
// It matches the structure reported by the mitto_conversation_get_current MCP tool.
type AvailableACPServer struct {
	// Name is the server identifier (e.g., "claude-code").
	Name string `json:"name"`
	// Type is the server type for prompt matching. Defaults to Name if not set.
	Type string `json:"type,omitempty"`
	// Tags contains optional categorization labels (e.g., ["coding", "fast-model"]).
	Tags []string `json:"tags,omitempty"`
	// Current is true if this is the active ACP server for the session.
	Current bool `json:"current,omitempty"`
}

// ChildSession describes a direct child session of the current session.
type ChildSession struct {
	// ID is the child session identifier.
	ID string `json:"id"`
	// Name is the child session title/name (may be empty if not yet set).
	Name string `json:"name,omitempty"`
	// ACPServer is the ACP server name used by the child session.
	ACPServer string `json:"acp_server,omitempty"`
	// IsAutoChild indicates the child was auto-created with the parent.
	IsAutoChild bool `json:"is_auto_child,omitempty"`
	// ChildOrigin indicates how the child was created: "auto", "mcp", or "human".
	ChildOrigin string `json:"child_origin,omitempty"`
}

// HistoryEntry represents a single turn in the conversation history.
type HistoryEntry struct {
	// Role is either "user" or "assistant".
	Role string `json:"role"`
	// Content is the message content.
	Content string `json:"content"`
}

// ProcessorOutput contains the result of processor execution.
type ProcessorOutput struct {
	// Message is the transformed message (for OutputTransform).
	Message string `json:"message,omitempty"`
	// Text is the text to prepend/append (for OutputPrepend/OutputAppend).
	Text string `json:"text,omitempty"`
	// Attachments contains files to attach to the message.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Error is an optional error message from the processor.
	Error string `json:"error,omitempty"`
	// Metadata contains optional data the processor wants to log.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Attachment represents a file attachment from a processor.
type Attachment struct {
	// Type is the attachment type: "image", "text", "file"
	Type string `json:"type"`
	// Path is the file path (resolved relative to working directory)
	Path string `json:"path,omitempty"`
	// Data is base64-encoded content (alternative to Path)
	Data string `json:"data,omitempty"`
	// MimeType is the MIME type (e.g., "image/png", "text/plain")
	MimeType string `json:"mime_type,omitempty"`
	// Name is the display name for the attachment
	Name string `json:"name,omitempty"`
}

// ResolveData resolves the attachment data, reading from file if necessary.
// Returns the resolved attachment data with base64-encoded content.
func (a *Attachment) ResolveData(workingDir string) (AttachmentData, error) {
	result := AttachmentData{
		Type:     a.Type,
		MimeType: a.MimeType,
		Name:     a.Name,
	}

	// If data is already provided, use it directly
	if a.Data != "" {
		result.Data = a.Data
		return result, nil
	}

	// Otherwise, read from file
	if a.Path == "" {
		return result, fmt.Errorf("attachment has neither data nor path")
	}

	// Resolve path relative to working directory
	path := a.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("failed to read file: %w", err)
	}

	result.Data = base64.StdEncoding.EncodeToString(data)

	// Set name from path if not provided
	if result.Name == "" {
		result.Name = filepath.Base(path)
	}

	// Detect MIME type if not provided
	if result.MimeType == "" {
		result.MimeType = detectMimeType(path, data)
	}

	return result, nil
}

// detectMimeType attempts to detect the MIME type from file extension or content.
func detectMimeType(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".rs":
		return "text/x-rust"
	case ".ts":
		return "text/typescript"
	case ".yaml", ".yml":
		return "text/yaml"
	default:
		// Check for common binary signatures
		if len(data) >= 8 {
			if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
				return "image/png"
			}
			if data[0] == 0xFF && data[1] == 0xD8 {
				return "image/jpeg"
			}
			if string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a" {
				return "image/gif"
			}
		}
		return "application/octet-stream"
	}
}
