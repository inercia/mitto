package cel

import "testing"

// TestNormalizeTasksUpstream covers the normalization rule from the
// mitto-w8jp.1 plan: lowercase, trim whitespace, and map "none" (any casing,
// with surrounding whitespace) to "".
func TestNormalizeTasksUpstream(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already lowercase", "jira", "jira"},
		{"uppercase", "JIRA", "jira"},
		{"mixed case", "GitHub", "github"},
		{"leading/trailing whitespace", "  gitlab  ", "gitlab"},
		{"none lowercase maps to empty", "none", ""},
		{"none uppercase maps to empty", "NONE", ""},
		{"none with whitespace maps to empty", "  None  ", ""},
		{"empty stays empty", "", ""},
		{"whitespace-only stays empty", "   ", ""},
		{"linear", "linear", "linear"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTasksUpstream(tt.in); got != tt.want {
				t.Errorf("NormalizeTasksUpstream(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
