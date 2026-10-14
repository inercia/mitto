package conversation

import "testing"

func TestEnrichToolTitle(t *testing.T) {
	tests := []struct {
		title string
		raw   any
		want  string
	}{
		{"shell", map[string]any{"command": "ls  -la"}, "shell: ls -la"},
		{"shell", nil, "shell"},
		{"Read /a/b", map[string]any{"filePath": "/a/b"}, "Read /a/b"},
		{"", map[string]any{"command": "ls"}, ""},
	}
	for _, tt := range tests {
		if got := enrichToolTitle(tt.title, tt.raw); got != tt.want {
			t.Errorf("enrichToolTitle(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
