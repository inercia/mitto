package conversation

import "strings"

const maxToolTitleDetail = 120

// toolTitleDetailKeys lists RawInput fields, in priority order, that best
// describe what a tool call is doing.
var toolTitleDetailKeys = []string{
	"command", "cmd", "description", "filePath", "file_path", "path",
	"pattern", "query", "url", "prompt",
}

// enrichToolTitle appends a short detail from the tool's raw input when the
// agent sent only a bare tool name (e.g. OpenCode sends just "shell").
func enrichToolTitle(title string, rawInput any) string {
	m, ok := rawInput.(map[string]any)
	if !ok || strings.TrimSpace(title) == "" {
		return title
	}
	for _, k := range toolTitleDetailKeys {
		s, ok := m[k].(string)
		if !ok {
			continue
		}
		s = strings.Join(strings.Fields(s), " ")
		if s == "" || strings.Contains(title, s) {
			continue
		}
		if r := []rune(s); len(r) > maxToolTitleDetail {
			s = string(r[:maxToolTitleDetail]) + "…"
		}
		return title + ": " + s
	}
	return title
}
