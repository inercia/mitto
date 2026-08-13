package chatui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/internal/termmd"
)

// streamedAgentCorpus is a small multi-construct markdown body (heading,
// prose, fenced code, list) used to drive AppendOrUpdateAgent chunk by
// chunk, exercising the same growing-Text streaming path a live agent
// message takes.
const streamedAgentCorpus = `# Heading

First paragraph of the answer, long enough to wrap across more than one
terminal-width line when rendered at a narrow width.

` + "```go\nfunc f() int { return 1 }\n```" + `

- item one
- item two

Closing paragraph.
`

// chunkPrefixes splits body into a sequence of growing prefixes of size
// chunkSize, mirroring how AppendOrUpdateAgent receives a streamed
// message's accumulated text on every flush.
func chunkPrefixes(body string, chunkSize int) []string {
	var chunks []string
	for end := chunkSize; end < len(body); end += chunkSize {
		chunks = append(chunks, body[:end])
	}
	chunks = append(chunks, body)
	return chunks
}

// normalizeLines strips ANSI escapes and drops blank/whitespace-only lines,
// so two renders can be compared on content and order without being
// sensitive to blank-line spacing differences at a stable-prefix boundary
// (mitto-pscc.8.1's documented byte-identity deviation).
func normalizeLines(s string) []string {
	stripped := xansi.Strip(s)
	var lines []string
	for _, line := range strings.Split(stripped, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestAppendOrUpdateAgent_StreamedRenderMatchesFullRenderStructurally is the
// chatui-level check named in the mitto-pscc.8.1 Implementation comment: an
// itemAgent built up via repeated AppendOrUpdateAgent chunks (the real
// streaming path, routed through item.render's *termmd.StreamRenderer) must
// end up structurally equivalent to a fresh termmd.Render of the same,
// complete body — same content and order, regardless of exact blank-line
// spacing at the cached boundary.
func TestAppendOrUpdateAgent_StreamedRenderMatchesFullRenderStructurally(t *testing.T) {
	tr := newTestTranscript(t)

	for _, chunk := range chunkPrefixes(streamedAgentCorpus, 13) {
		tr.AppendOrUpdateAgent(1, chunk, "")
	}

	if got := len(tr.items); got != 1 {
		t.Fatalf("len(items) = %d, want 1 (same seq must update in place)", got)
	}

	rendered := tr.items[0].render(tr.width, tr.mode, tr.theme, tr.styles)
	if got := strings.Count(xansi.Strip(rendered), "[assistant]"); got != 1 {
		t.Fatalf("assistant label count = %d, want 1\nrendered:\n%s", got, rendered)
	}
	_, body, ok := strings.Cut(rendered, "\n")
	if !ok {
		t.Fatalf("rendered agent item has no label separator: %q", rendered)
	}
	got := normalizeLines(body)
	want := normalizeLines(termmd.Render(streamedAgentCorpus, termmd.Options{Mode: tr.mode, Width: tr.width}))

	if len(got) != len(want) {
		t.Fatalf("streamed render has %d lines, want %d\ngot:\n%s\nwant:\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestItem_Render_LabelsEveryKindInStyledAndPlainModes(t *testing.T) {
	tests := []struct {
		name  string
		kind  itemKind
		label string
		setup func(*item)
	}{
		{name: "user", kind: itemUser, label: "[user]", setup: func(it *item) { it.markdown = "body" }},
		{name: "assistant", kind: itemAgent, label: "[assistant]", setup: func(it *item) { it.markdown = "body" }},
		{name: "thought", kind: itemThought, label: "[thought]", setup: func(it *item) { it.markdown = "body" }},
		{name: "tool", kind: itemTool, label: "[tool]", setup: func(it *item) { it.title, it.status = "Read file", "done" }},
		{name: "error", kind: itemError, label: "[error]", setup: func(it *item) { it.title = "failed" }},
		{name: "system", kind: itemSystem, label: "[system]", setup: func(it *item) { it.title = "local output" }},
	}
	modes := []struct {
		name  string
		mode  termmd.Mode
		theme termmd.Theme
	}{
		{name: "dark", mode: termmd.ModeStyled, theme: termmd.ThemeDark},
		{name: "light", mode: termmd.ModeStyled, theme: termmd.ThemeLight},
		{name: "plain", mode: termmd.ModePlain, theme: termmd.ThemeDark},
	}

	for _, mode := range modes {
		for _, tt := range tests {
			t.Run(mode.name+"/"+tt.name, func(t *testing.T) {
				styles := newStyles()
				styles.apply(mode.mode, mode.theme)
				it := newItem(tt.kind)
				tt.setup(it)

				got := it.render(80, mode.mode, mode.theme, styles)
				stripped := xansi.Strip(got)
				if !strings.Contains(stripped, tt.label) {
					t.Errorf("rendered item = %q, want label %q", stripped, tt.label)
				}
				if mode.mode == termmd.ModePlain && got != stripped {
					t.Errorf("plain item emitted ANSI: %q", got)
				}
				if tt.kind == itemUser || tt.kind == itemAgent || tt.kind == itemThought {
					if !strings.HasSuffix(got, it.rendered) {
						t.Errorf("Markdown body was wrapped or recolored outside its cached render:\n%q", got)
					}
				}
			})
		}
	}
}

func TestItem_Render_CachedAgentDoesNotDuplicateLabel(t *testing.T) {
	styles := newStyles()
	it := newItem(itemAgent)
	it.markdown = "first chunk"

	it.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	stream := it.stream
	second := it.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	if got := strings.Count(xansi.Strip(second), "[assistant]"); got != 1 {
		t.Fatalf("cached render label count = %d, want 1: %q", got, second)
	}
	if it.stream != stream {
		t.Fatal("cached render replaced the StreamRenderer")
	}

	it.markdown = "first chunk plus more"
	it.invalidate()
	third := it.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	if got := strings.Count(xansi.Strip(third), "[assistant]"); got != 1 {
		t.Fatalf("stream update label count = %d, want 1: %q", got, third)
	}
	if it.stream != stream {
		t.Fatal("stream update replaced the StreamRenderer")
	}
}

// TestItem_Render_ItemAgentLazilyCreatesStreamRenderer verifies an itemAgent
// gets its *termmd.StreamRenderer created on first render, and that a
// non-agent kind (e.g. itemUser) never gets one — the streaming cache is
// itemAgent-only.
func TestItem_Render_ItemAgentLazilyCreatesStreamRenderer(t *testing.T) {
	styles := newStyles()

	agent := newItem(itemAgent)
	agent.markdown = "hello"
	if agent.stream != nil {
		t.Fatal("stream must be nil before the first render")
	}
	agent.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	if agent.stream == nil {
		t.Error("render on an itemAgent must lazily create a StreamRenderer")
	}

	user := newItem(itemUser)
	user.markdown = "hi"
	user.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	if user.stream != nil {
		t.Error("render on a non-agent item must never create a StreamRenderer")
	}
}

// TestItem_Invalidate_DoesNotResetStreamCache pins the invariant items.go's
// stream field doc comment states: invalidate() (called on every streamed
// chunk via AppendOrUpdateAgent) must NOT reset the StreamRenderer's
// internal cache — only a real width/mode change through SetOptions (i.e.
// a subsequent render at different width/mode) may do that. Losing this
// would silently reintroduce the O(n^2) re-render cost invalidate() exists
// to avoid.
func TestItem_Invalidate_DoesNotResetStreamCache(t *testing.T) {
	styles := newStyles()
	it := newItem(itemAgent)
	it.markdown = "Para one.\n\nPara two.\n"
	it.render(80, termmd.ModeStyled, termmd.ThemeDark, styles)
	if it.stream == nil {
		t.Fatal("expected a StreamRenderer after the first render")
	}

	it.invalidate()
	if it.stream == nil {
		t.Error("invalidate() must not drop the StreamRenderer itself")
	}
}
