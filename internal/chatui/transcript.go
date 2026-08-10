package chatui

import (
	"strings"

	viewport "charm.land/bubbles/v2/viewport"
	"github.com/inercia/mitto/internal/termmd"
)

// transcript is the imperative struct owning the ordered list of items and
// the viewport they are rendered into, following crush's pattern of plain
// stateful structs with methods the root Update calls directly (no nested
// tea.Model, no message routing between sub-components).
type transcript struct {
	vp    viewport.Model
	items []*item

	// byToolID maps an in-flight tool call's ID to its item, so a
	// tool_update event mutates the existing item instead of appending a
	// new one.
	byToolID map[string]*item

	width       int
	mode        termmd.Mode
	showThought bool

	styles *styles
}

func newTranscript(styles *styles, showThought bool) *transcript {
	return &transcript{
		vp:          viewport.New(),
		byToolID:    make(map[string]*item),
		showThought: showThought,
		styles:      styles,
	}
}

// SetSize resizes the viewport and invalidates every item's render cache
// (width changed), then re-renders.
func (t *transcript) SetSize(width, height int) {
	t.width = width
	t.vp.SetWidth(width)
	t.vp.SetHeight(height)
	for _, it := range t.items {
		it.invalidate()
	}
	t.refresh()
}

// SetMode updates the termmd render mode (styled/plain) applied to future
// and cached renders, invalidating the cache so it takes effect immediately.
func (t *transcript) SetMode(mode termmd.Mode) {
	if t.mode == mode {
		return
	}
	t.mode = mode
	for _, it := range t.items {
		it.invalidate()
	}
	t.refresh()
}

// AppendUser appends a completed user-prompt item.
func (t *transcript) AppendUser(text string) {
	it := newItem(itemUser)
	it.markdown = text
	t.append(it)
}

// AppendOrUpdateAgent appends a new agent-message item for seq, or updates
// the existing in-flight item for the same seq with the latest accumulated
// markdown/html (glamour re-renders the whole body on each chunk — a fence
// is not a fence until it closes, so incremental rendering is not possible).
func (t *transcript) AppendOrUpdateAgent(seq int64, markdown, html string) {
	if len(t.items) > 0 {
		last := t.items[len(t.items)-1]
		if last.kind == itemAgent && last.seq == seq {
			last.markdown = markdown
			last.html = html
			last.invalidate()
			t.refresh()
			return
		}
	}
	it := newItem(itemAgent)
	it.seq, it.markdown, it.html = seq, markdown, html
	t.append(it)
}

// AppendThought appends a thought item, dropped entirely when showThought
// is false (filtered at append time so the cache never has to model
// visibility, per the Plan decision).
func (t *transcript) AppendThought(text string) {
	if !t.showThought {
		return
	}
	it := newItem(itemThought)
	it.markdown = text
	t.append(it)
}

// AppendTool appends a new tool-call item, tracked by ID for later updates.
func (t *transcript) AppendTool(id, title, status string) {
	it := newItem(itemTool)
	it.toolID, it.title, it.status = id, title, status
	t.byToolID[id] = it
	t.append(it)
}

// UpdateTool mutates an existing tool item's status in place. If the ID is
// unknown (tool_call was never observed, e.g. mid-turn attach), a new item
// is synthesized so the status is not silently lost.
func (t *transcript) UpdateTool(id, status string) {
	if it, ok := t.byToolID[id]; ok {
		it.status = status
		t.refresh()
		return
	}
	t.AppendTool(id, id, status)
}

// AppendFileEvent appends a file_read/file_write line.
func (t *transcript) AppendFileEvent(verb, path string, size int) {
	it := newItem(itemTool)
	it.title, it.path, it.size = verb+" "+path, path, size
	it.status = ""
	t.append(it)
}

// AppendError appends an error line.
func (t *transcript) AppendError(message string) {
	it := newItem(itemError)
	it.title = message
	t.append(it)
}

func (t *transcript) append(it *item) {
	t.items = append(t.items, it)
	t.refresh()
}

// refresh re-renders every item (using each item's own cache) and feeds the
// joined content into the viewport, then scrolls to bottom if the viewport
// was already at the bottom before this update — preserving a manual
// scroll-back position instead of yanking it down on every new event.
func (t *transcript) refresh() {
	wasAtBottom := t.vp.AtBottom()
	lines := make([]string, 0, len(t.items))
	for _, it := range t.items {
		lines = append(lines, it.render(t.width, t.mode, t.styles))
	}
	t.vp.SetContent(strings.Join(lines, "\n\n"))
	if wasAtBottom {
		t.vp.GotoBottom()
	}
}

func (t *transcript) View() string {
	return t.vp.View()
}
