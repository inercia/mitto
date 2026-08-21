package stats

import (
	"regexp"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestEstimateTokensText(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"a", 1},
		{"hi", 1},
		{"hell", 1},                      // 4 bytes → (4+3)/4 = 1
		{"hello", 2},                     // 5 bytes → (5+3)/4 = 2
		{string(make([]byte, 400)), 100}, // 400 bytes → 100
		// UTF-8 counts by BYTES, not runes: "héllo" is 6 bytes → (6+3)/4 = 2.
		{"héllo", 2},
	}
	for _, c := range cases {
		if got := EstimateTokensText(c.in); got != c.want {
			t.Errorf("EstimateTokensText(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestEstimateTokensText_Monotonic covers the bead's "fuzz-ish: monotonic in
// input length" acceptance criterion by growing a mixed ASCII+multibyte
// string one rune at a time and asserting the estimate never decreases.
func TestEstimateTokensText_Monotonic(t *testing.T) {
	runes := []rune("The quick brown fox jumps over the lazy dog. héllo wörld — 日本語テスト")
	var acc string
	prev := int64(-1)
	for i, r := range runes {
		acc += string(r)
		got := EstimateTokensText(acc)
		if got < prev {
			t.Fatalf("non-monotonic at step %d (rune %q): %d < %d for %q", i, r, got, prev, acc)
		}
		prev = got
	}
	// Also assert the byte-prefix chain is monotonic (stronger form).
	full := acc
	prev = -1
	for i := 0; i <= len(full); i++ {
		got := EstimateTokensText(full[:i])
		if got < prev {
			t.Fatalf("non-monotonic on byte-prefix at len %d: %d < %d", i, got, prev)
		}
		prev = got
	}
}

func TestEstimateTokensImage(t *testing.T) {
	if got := EstimateTokensImage(); got != 64 {
		t.Errorf("EstimateTokensImage() = %d, want 64", got)
	}
	if ImageTokenCost != 64 {
		t.Errorf("ImageTokenCost = %d, want 64", ImageTokenCost)
	}
}

func TestEstimateTokensFile(t *testing.T) {
	cases := []struct {
		size int64
		want int64
	}{
		{-1, 0},
		{0, 0},
		{1, 1},
		{3, 1},
		{4, 1},
		{5, 2},
		{1024, 256},
	}
	for _, c := range cases {
		if got := EstimateTokensFile(c.size); got != c.want {
			t.Errorf("EstimateTokensFile(%d) = %d, want %d", c.size, got, c.want)
		}
	}
}

func TestEstimateTokensFileRef(t *testing.T) {
	// name-length + 16 overhead, rounded up by /4, capped at 4096.
	cases := []struct {
		name string
		want int64
	}{
		{"", 4},                          // 0+16=16 → 4
		{"a.txt", 6},                     // 5+16=21 → 6
		{"a-very-long-file-name.md", 10}, // 24+16=40 → 10
	}
	for _, c := range cases {
		if got := EstimateTokensFileRef(session.FileRef{Name: c.name}); got != c.want {
			t.Errorf("EstimateTokensFileRef(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestIsMCPCall(t *testing.T) {
	cases := []struct {
		title string
		kind  string
		want  bool
	}{
		// Rule 1: Kind wins irrespective of title.
		{"Read file", "mcp", true},
		// Rule 2: mitto_ prefix (case-sensitive).
		{"mitto_conversation_new", "", true},
		// Rule 3: default regex covers spec prefixes (case-sensitive).
		{"github-create-issue", "", true},
		{"slack_post_message", "", true},
		{"jira-issue-create", "", true},
		{"linear_issues", "", true},
		{"fj-search", "", true},
		{"puppeteer-navigate", "", true},
		{"splunk_search", "", true},
		// Rule 4: negatives.
		{"Read file: main.go", "", false},
		{"Bash", "", false},
		{"", "", false},
		{"Github-x", "", false}, // case-sensitive: must be lowercase github
		{"MITTO_x", "", false},  // case-sensitive: must be lowercase mitto_
		{"launch-process", "", false},
	}
	for _, c := range cases {
		if got := IsMCPCall(session.ToolCallData{Title: c.title, Kind: c.kind}); got != c.want {
			t.Errorf("IsMCPCall({Title:%q, Kind:%q}) = %v, want %v", c.title, c.kind, got, c.want)
		}
	}
}

func TestIsMCPCall_ExtensibleRegexps(t *testing.T) {
	// The MCPTitleRegexps slice is mutable so callers can extend without config.
	// Snapshot + restore to avoid cross-test pollution.
	orig := MCPTitleRegexps
	t.Cleanup(func() { MCPTitleRegexps = orig })

	MCPTitleRegexps = append(orig, regexp.MustCompile(`^notion_`))
	if !IsMCPCall(session.ToolCallData{Title: "notion_search"}) {
		t.Errorf("after extending MCPTitleRegexps, IsMCPCall(notion_search) = false, want true")
	}
	if IsMCPCall(session.ToolCallData{Title: "Notion_search"}) {
		t.Errorf("case-sensitive extension: IsMCPCall(Notion_search) = true, want false")
	}
}
