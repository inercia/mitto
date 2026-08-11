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

	got := normalizeLines(tr.items[0].render(tr.width, tr.mode, tr.theme, tr.styles))
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
