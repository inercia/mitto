package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
)

// JSONLineFilterReader wraps an io.Reader and filters out lines that are not
// valid JSON-RPC messages. This is used to filter stdout from ACP agents that
// may output terminal UI (ANSI escape sequences, box-drawing characters) when
// they crash or encounter errors.
//
// A line is considered a potential JSON-RPC message if it starts with '{'.
// Other lines (empty lines, terminal output) are logged at DEBUG level and discarded.
type JSONLineFilterReader struct {
	scanner      *bufio.Scanner
	logger       *slog.Logger
	filter       func([]byte) bool
	pending      []byte // buffered data from a valid line
	pendingIndex int    // current read position in pending
}

// NewJSONLineFilterReader creates a new filtering reader that wraps the given reader.
// Lines that don't start with '{' are logged at DEBUG level and discarded.
// If logger is nil, non-JSON lines are silently discarded.
func NewJSONLineFilterReader(r io.Reader, logger *slog.Logger) *JSONLineFilterReader {
	return NewJSONLineFilterReaderWithFilter(r, logger, nil)
}

// NewJSONLineFilterReaderWithFilter creates a filtering reader with an optional
// predicate that can discard JSON-RPC lines before they reach the ACP SDK.
// The predicate receives a trimmed line and returns true to discard it.
func NewJSONLineFilterReaderWithFilter(r io.Reader, logger *slog.Logger, filter func([]byte) bool) *JSONLineFilterReader {
	const (
		initialBufSize = 1024 * 1024      // 1MB initial buffer (same as SDK)
		maxBufSize     = 10 * 1024 * 1024 // 10MB max (same as SDK)
	)

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, initialBufSize)
	scanner.Buffer(buf, maxBufSize)

	return &JSONLineFilterReader{
		scanner: scanner,
		logger:  logger,
		filter:  filter,
	}
}

// Read implements io.Reader by returning only valid JSON lines.
// Non-JSON lines are filtered out and logged.
func (f *JSONLineFilterReader) Read(p []byte) (n int, err error) {
	// If we have pending data from a previous line, return it first
	if f.pendingIndex < len(f.pending) {
		n = copy(p, f.pending[f.pendingIndex:])
		f.pendingIndex += n
		if f.pendingIndex >= len(f.pending) {
			f.pending = nil
			f.pendingIndex = 0
		}
		return n, nil
	}

	// Read lines until we find a valid JSON line or reach EOF
	for f.scanner.Scan() {
		line := f.scanner.Bytes()

		// Skip empty lines
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		// Check if line starts with '{' (potential JSON-RPC message)
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			if f.filter != nil && f.filter(trimmed) {
				continue
			}

			outLine := line
			if sanitized, changed := sanitizeToolCallDiffOldText(trimmed); changed {
				outLine = sanitized
			}

			// Valid JSON line - add newline and buffer it
			f.pending = make([]byte, len(outLine)+1)
			copy(f.pending, outLine)
			f.pending[len(outLine)] = '\n'
			f.pendingIndex = 0

			// Return as much as fits in p
			n = copy(p, f.pending)
			f.pendingIndex = n
			if f.pendingIndex >= len(f.pending) {
				f.pending = nil
				f.pendingIndex = 0
			}
			return n, nil
		}

		// Non-JSON line - log at DEBUG level and skip
		// This catches ANSI escape sequences, box-drawing characters,
		// and other terminal UI output from crashed agents
		if f.logger != nil {
			// Truncate very long lines to avoid log spam
			logLine := string(line)
			if len(logLine) > 200 {
				logLine = logLine[:100] + "..." + logLine[len(logLine)-50:]
			}
			f.logger.Debug("filtered non-JSON line from agent stdout",
				"line", logLine,
				"length", len(line))
		}
	}

	// Scanner finished - check for error
	if err := f.scanner.Err(); err != nil {
		return 0, err
	}

	// EOF
	return 0, io.EOF
}

// sanitizeToolCallDiffOldText rewrites a raw session/update JSON-RPC line so
// that a tool_call diff content block whose oldText is a JSON object (rather
// than the ACP spec's string) does not get the whole notification dropped.
//
// See mitto-5jnw: some agents (observed via Auggie routing to Claude on
// Vertex AI) emit ToolCallContentDiff.oldText as an object. acp-go-sdk's
// generated ToolCallContentDiff.OldText is strictly typed as *string, so the
// SDK's per-variant UnmarshalJSON rejects the entire session/update
// notification with -32602 Invalid params before any Mitto callback ever
// sees it — losing every content block in that tool_call, not just the
// malformed diff. Running this ahead of the SDK's decoder (both ACP process
// wiring paths funnel stdout through JSONLineFilterReader.Read) lets a
// malformed oldText degrade to a best-effort string instead.
//
// Returns the original line unchanged (changed=false) unless an
// object-shaped oldText was actually found and rewritten.
func sanitizeToolCallDiffOldText(line []byte) (out []byte, changed bool) {
	// Cheap pre-check: skip the JSON round-trip for the overwhelming
	// majority of lines, which cannot contain an object-shaped oldText.
	if !bytes.Contains(line, []byte(`"oldText":{`)) {
		return line, false
	}

	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		return line, false
	}
	if method, _ := msg["method"].(string); method != "session/update" {
		return line, false
	}
	params, _ := msg["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	content, _ := update["content"].([]any)
	if content == nil {
		return line, false
	}

	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "diff" {
			continue
		}
		if obj, ok := block["oldText"].(map[string]any); ok {
			block["oldText"] = stringifyOldTextObject(obj)
			changed = true
		}
	}
	if !changed {
		return line, false
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return line, false
	}
	return raw, true
}

// stringifyOldTextObject best-effort converts an object-shaped oldText into
// a plain string: prefer a string-typed "text" field (the shape observed in
// production), else fall back to the object's own JSON representation so no
// information is silently discarded.
func stringifyOldTextObject(obj map[string]any) string {
	if text, ok := obj["text"].(string); ok {
		return text
	}
	if raw, err := json.Marshal(obj); err == nil {
		return string(raw)
	}
	return ""
}
