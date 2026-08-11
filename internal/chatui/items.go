package chatui

import (
	"github.com/inercia/mitto/internal/termmd"
)

// itemKind identifies the kind of transcript item, driving which style is
// applied in item.render.
type itemKind int

const (
	itemUser itemKind = iota
	itemAgent
	itemThought
	itemTool
	itemError
	// itemSystem is local-only output from a slash command (e.g. /help),
	// never sent to or received from the agent (mitto-pscc.11).
	itemSystem
)

// item is one entry in the transcript, owning its own render cache
// (crush chat/messages.go cachedMessageItem). rendered/renderedWidth are
// invalidated whenever markdown, html, or the render width changes —
// Update never re-renders on every frame, only on the events that actually
// change what an item looks like.
type item struct {
	kind itemKind

	// seq is the server sequence number for agent/user items, used to
	// detect the "same in-flight message, new chunk" case (a streamed
	// agent message re-arrives with a growing Text on the same Seq).
	seq int64

	// markdown is the raw markdown body (agent/user items). Non-empty
	// markdown always wins over html (see renderModeFor).
	markdown string
	// html is the sanitized HTML fallback body, populated for pre-.3
	// history events that carry no markdown.
	html string

	// toolID/title/status describe a tool-call item, updated in place by
	// tool_update events keyed on toolID (never a new item).
	toolID string
	title  string
	status string

	// path/size describe a file_read/file_write item.
	path string
	size int

	// rendered is the cached render of this item at renderedWidth, in the
	// current renderMode. Empty rendered with renderedWidth == -1 means
	// "never rendered yet".
	rendered      string
	renderedWidth int
	renderedMode  termmd.Mode
}

func newItem(kind itemKind) *item {
	return &item{kind: kind, renderedWidth: -1}
}

// renderModeFor picks ModeStyled/ModePlain vs ModeDegraded for an
// agent/user item: non-empty markdown renders through glamour, otherwise a
// pre-mitto-pscc.3 history event's HTML falls back to the degraded path
// (Plan decision — confirmed against pkg/api/stream.go: Event.Text is the
// raw markdown field .3 added, Event.HTML the sanitized HTML).
func renderModeFor(base termmd.Mode, markdown string) termmd.Mode {
	if markdown == "" {
		return termmd.ModeDegraded
	}
	return base
}

// render returns the styled string for this item at width, using the cache
// when neither width nor content changed since the last call.
func (it *item) render(width int, baseMode termmd.Mode, styles *styles) string {
	switch it.kind {
	case itemTool:
		return styles.renderTool(it.title, it.status)
	case itemError:
		return styles.errorStyle.Render(it.title)
	case itemSystem:
		return it.title
	}

	mode := renderModeFor(baseMode, it.markdown)
	if it.rendered != "" && it.renderedWidth == width && it.renderedMode == mode {
		return it.decorate(it.rendered, styles)
	}

	body := it.markdown
	if mode == termmd.ModeDegraded {
		body = it.html
	}
	out := termmd.Render(body, termmd.Options{Mode: mode, Width: width})

	it.rendered = out
	it.renderedWidth = width
	it.renderedMode = mode
	return it.decorate(out, styles)
}

// decorate applies the per-kind wrapper style (dimming for thoughts, a
// sender label for user items) around the already-rendered markdown body.
func (it *item) decorate(body string, styles *styles) string {
	switch it.kind {
	case itemThought:
		return styles.thoughtStyle.Render(body)
	case itemUser:
		return styles.userStyle.Render(body)
	default:
		return body
	}
}

// invalidate clears the render cache, forcing the next render call to
// re-render regardless of width. Used when in-place content (markdown/html)
// changes, e.g. a streamed chunk growing an in-flight agent message.
func (it *item) invalidate() {
	it.rendered = ""
	it.renderedWidth = -1
}
