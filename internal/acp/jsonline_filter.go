package acp

import (
	"bufio"
	"bytes"
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
	pending      []byte // buffered data from a valid line
	pendingIndex int    // current read position in pending
}

// NewJSONLineFilterReader creates a new filtering reader that wraps the given reader.
// Lines that don't start with '{' are logged at DEBUG level and discarded.
// If logger is nil, non-JSON lines are silently discarded.
func NewJSONLineFilterReader(r io.Reader, logger *slog.Logger) *JSONLineFilterReader {
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
			// Valid JSON line - add newline and buffer it
			f.pending = make([]byte, len(line)+1)
			copy(f.pending, line)
			f.pending[len(line)] = '\n'
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
