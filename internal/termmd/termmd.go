// Package termmd renders markdown to ANSI-styled terminal output via
// charm.land/glamour/v2. It is owned by the CLI (internal/cmd and the
// mitto-pscc.7 chat TUI): internal/conversation must never import this
// package, so styling policy stays a CLI-only concern (mitto-pscc.8 DDR).
//
// Render is the single entry point. It is pure and deterministic for a given
// (markdown, Options) pair — callers resolve Mode and Width themselves (see
// ResolveMode and TerminalWidth) rather than Render sniffing the environment,
// which is what keeps the golden-file tests in this package stable in CI.
//
// Streaming is explicitly out of scope: re-rendering a whole in-flight
// message on every chunk is O(n^2) over a long answer. Render is meant to be
// called once per complete message body. A stable-prefix cache for streaming
// re-render is tracked separately (mitto-pscc.8.1), gated on the chat TUI
// showing the cost.
package termmd

import (
	"fmt"
	"sync"

	glamour "charm.land/glamour/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

// Mode selects how Render styles its output.
type Mode int

const (
	// ModeStyled renders full ANSI styling (color, bold, etc.) using
	// glamour's "dark" style. Selected for an interactive TTY.
	ModeStyled Mode = iota
	// ModePlain renders glamour's "notty" (ASCII-only, no ANSI) style.
	// Selected for piped stdout, --no-color, or $NO_COLOR.
	ModePlain
	// ModeDegraded routes through RenderHTMLFallback instead of glamour.
	// Never auto-selected by ResolveMode — a caller must request it
	// explicitly, for legacy events recorded before mitto-pscc.3 added
	// markdown persistence and that therefore carry HTML only.
	ModeDegraded
)

// Options configures a single Render call.
type Options struct {
	Mode Mode
	// Width is the wrap width in columns. Always supplied by the caller
	// (see TerminalWidth) — Render never queries the terminal itself, so
	// its output stays deterministic for golden-file testing. Non-positive
	// values fall back to glamour's own default (80).
	Width int
}

// rendererKey identifies a cached *glamour.TermRenderer by the (mode, width)
// pair that determines its output. ModeDegraded never reaches this cache.
type rendererKey struct {
	mode  Mode
	width int
}

var (
	rendererMu    sync.Mutex
	rendererCache = map[rendererKey]*glamour.TermRenderer{}
)

// glamourStyle maps a styled/plain Mode to glamour's builtin style name.
func glamourStyle(mode Mode) string {
	if mode == ModePlain {
		return glamourStyleNoTTY
	}
	return glamourStyleDark
}

// Builtin glamour style names (charm.land/glamour/v2/styles), duplicated here
// as untyped constants so this package does not need to import the styles
// subpackage just for two string literals.
const (
	glamourStyleDark  = "dark"
	glamourStyleNoTTY = "notty"
)

// termRenderer returns a cached *glamour.TermRenderer for (mode, width),
// creating one on first use. glamour's TermRenderer is stateful and not
// concurrency-safe (mitto-pscc.8 plan) — callers must render under the
// returned mutex, which is what Render below does.
func termRenderer(mode Mode, width int) (*glamour.TermRenderer, error) {
	key := rendererKey{mode: mode, width: width}

	rendererMu.Lock()
	defer rendererMu.Unlock()

	if r, ok := rendererCache[key]; ok {
		return r, nil
	}

	opts := []glamour.TermRendererOption{glamour.WithStylePath(glamourStyle(mode))}
	if width > 0 {
		opts = append(opts, glamour.WithWordWrap(width))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil, fmt.Errorf("termmd: create renderer: %w", err)
	}
	rendererCache[key] = r
	return r, nil
}

// Render renders body per opts. ModeStyled and ModePlain treat body as
// markdown and render it through glamour. ModeDegraded treats body as HTML
// and routes it through RenderHTMLFallback instead — the caller is expected
// to pass a legacy event's HTML in that case, since it has no markdown to
// give (see RenderHTMLFallback's doc comment).
//
// On a glamour render error, Render returns the original markdown unchanged
// rather than an error, so a rendering failure never blocks a caller from
// showing the agent's reply — degraded formatting beats no output.
func Render(body string, opts Options) string {
	if opts.Mode == ModeDegraded {
		return RenderHTMLFallback(body)
	}

	r, err := termRenderer(opts.Mode, opts.Width)
	if err != nil {
		return body
	}

	rendererMu.Lock()
	out, err := r.Render(body)
	rendererMu.Unlock()
	if err != nil {
		return body
	}

	if opts.Mode == ModePlain {
		// glamour emits OSC 8 hyperlink escapes for links unconditionally,
		// even under the "notty" style (which only strips color/bold). Strip
		// them explicitly so ModePlain output is genuinely ANSI-free — the
		// property this package's plain-mode contract (and its test) rely
		// on, since piped stdout/non-TTY consumers can't interpret OSC 8.
		out = xansi.Strip(out)
	}
	return out
}
