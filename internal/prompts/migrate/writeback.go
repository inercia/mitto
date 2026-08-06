package migrate

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateYAML runs every registered migration against the document parsed
// from original, and — if the document's top-level loop: block changed as a
// result — returns new file bytes with ONLY that block's lines replaced.
// Every other byte of the original file (the "loop:" key line itself,
// unrelated top-level keys, comments, blank lines, and any multi-line prompt
// body) is left byte-for-byte untouched: re-marshalling the whole document
// would reflow indentation, block-scalar style and blank lines, which is
// unacceptable for long hand-authored prompt bodies (mitto-r6j.3 decision).
//
// If original is not valid YAML, or has no top-level loop: mapping, this is
// a no-op: it returns the original bytes unchanged and a zero Result so
// callers can skip any write. A migration bug (Apply returning an error)
// is surfaced as an error with the original bytes still returned, so a
// caller can safely fall back to parsing the unmigrated content.
func MigrateYAML(original []byte) ([]byte, Result, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		// Malformed YAML: let the normal parse path surface the real error.
		return original, Result{}, nil
	}

	loopKey, loopVal := findTopLevelPair(&doc, "loop")
	hadLoop := loopVal != nil && loopVal.Kind == yaml.MappingNode
	var startLine, endLine, indent int
	if hadLoop {
		startLine, endLine = loopBlockLineSpan(loopVal)
		indent = loopBlockIndent(loopVal)
	}

	res, err := Migrate(&doc)
	if err != nil {
		return original, Result{}, err
	}
	if !res.Changed || !hadLoop {
		return original, res, nil
	}

	_, loopValAfter := findTopLevelPair(&doc, "loop")
	rendered, err := renderIndented(loopValAfter, indent)
	if err != nil {
		return original, Result{}, fmt.Errorf("render migrated %q block: %w", loopKey.Value, err)
	}

	return spliceLines(original, startLine, endLine, rendered), res, nil
}

// loopBlockLineSpan computes the 1-indexed [start,end] line span (inclusive)
// occupied by the loop: value node in the ORIGINAL source, including any
// HeadComment lines attached to its first child and any FootComment lines
// trailing its last (possibly nested) child. Must be called BEFORE Migrate
// mutates the node, since mutation can reorder/replace children and lose the
// original first/last position.
func loopBlockLineSpan(val *yaml.Node) (start, end int) {
	start = val.Line
	if len(val.Content) > 0 {
		first := val.Content[0]
		start = first.Line - countCommentLines(first.HeadComment)
	} else if val.HeadComment != "" {
		start -= countCommentLines(val.HeadComment)
	}
	end = lastLineOf(val)
	return start, end
}

// loopBlockIndent returns the number of leading spaces the loop: block's
// children are indented by in the original source (i.e. one nesting level
// under the "loop:" key), so the re-rendered replacement can be shifted to
// match.
func loopBlockIndent(val *yaml.Node) int {
	if len(val.Content) > 0 {
		return val.Content[0].Column - 1
	}
	return val.Column - 1
}

// countCommentLines returns the number of source lines a HeadComment/
// FootComment string occupies (each source comment line is joined by "\n").
func countCommentLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// lastLineOf returns the greatest source line number spanned by n's subtree,
// including any trailing FootComment lines. Nodes synthesized by a migration
// (Line == 0) never win the max, so only original source positions count.
func lastLineOf(n *yaml.Node) int {
	max := n.Line
	for _, c := range n.Content {
		if l := lastLineOf(c); l > max {
			max = l
		}
	}
	if n.FootComment != "" {
		max += countCommentLines(n.FootComment)
	}
	return max
}

// renderIndented encodes node as a standalone YAML mapping (2-space indent,
// matching this codebase's prompt files) and shifts every non-blank line
// right by indent spaces so it can be spliced back at its original nesting
// level.
func renderIndented(node *yaml.Node, indent int) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	text := strings.TrimRight(buf.String(), "\n")
	if indent <= 0 {
		return text, nil
	}
	prefix := strings.Repeat(" ", indent)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n"), nil
}

// spliceLines replaces the 1-indexed, inclusive [startLine, endLine] span of
// original with replacement, leaving every other line untouched. Assumes LF
// line endings, matching this codebase's prompt files.
func spliceLines(original []byte, startLine, endLine int, replacement string) []byte {
	lines := strings.Split(string(original), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return original
	}
	out := make([]string, 0, len(lines)+strings.Count(replacement, "\n"))
	out = append(out, lines[:startLine-1]...)
	out = append(out, strings.Split(replacement, "\n")...)
	out = append(out, lines[endLine:]...)
	return []byte(strings.Join(out, "\n"))
}

// WriteBackIfNeeded persists migrated to path via an atomic temp-file+rename
// in the same directory when result.Changed is true. Any failure (read-only
// filesystem, permission denied, missing directory, ...) is logged as a
// single WARN and swallowed: migration always degrades to "applied in
// memory only" rather than failing the caller's load (mitto-r6j.3 decision).
// Returns whether the write actually happened.
func WriteBackIfNeeded(path string, migrated []byte, result Result) bool {
	if !result.Changed {
		return false
	}
	if err := atomicWrite(path, migrated); err != nil {
		slog.Warn("prompt file migration could not be written back to disk; applied in memory only",
			"path", path, "migrations", strings.Join(result.Fired, ","), "error", err)
		return false
	}
	return true
}

// atomicWrite writes data to path via a temp file in the same directory
// followed by os.Rename, so a reader never observes a partially-written
// file. Best-effort preserves the original file's permission bits.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".prompt-migrate-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if info, statErr := os.Stat(path); statErr == nil {
		_ = os.Chmod(tmpPath, info.Mode())
	}
	return os.Rename(tmpPath, path)
}
