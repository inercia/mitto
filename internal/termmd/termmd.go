// Package termmd renders markdown to ANSI-styled terminal output via
// charm.land/glamour/v2. It is owned by the CLI (internal/cmd and the
// mitto-pscc.7 chat TUI): internal/conversation must never import this
// package, so styling policy stays a CLI-only concern (mitto-pscc.8 DDR).
//
// Render is the single entry point. It is pure and deterministic for a given
// (markdown, Options) pair — callers resolve Mode, Theme, and Width
// themselves (see ResolveMode, ResolveTheme, and TerminalWidth) rather than
// Render sniffing the environment, which is what keeps the golden-file tests
// in this package stable in CI.
//
// Render is meant to be called once per complete message body. A caller
// that must re-render a growing in-flight message on every streamed chunk
// — re-rendering the whole accumulated body each time is O(n^2) over a
// long answer — should drive a StreamRenderer instead (mitto-pscc.8.1): it
// caches the render of a "stable prefix" (a point after a blank line where
// no markdown construct is open) and only re-renders the trailing partial
// on each flush. See StreamRenderer's doc comment for the correctness
// rules and the byte-identity caveat.
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
	// glamour's "dark" or "light" style, per Options.Theme. Selected for an
	// interactive TTY. glamour v2 dropped v1's WithAutoStyle and defaults to
	// dark itself, so light-terminal selection is the caller's
	// responsibility — see Theme and ResolveTheme (mitto-u7k3).
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

// Theme selects which glamour color palette ModeStyled uses. It is ignored
// by ModePlain (notty has no color to tune) and ModeDegraded. The zero value
// is ThemeDark, so every existing Options{Mode: ...} literal that predates
// this field keeps rendering exactly as before (mitto-u7k3 plan decision).
type Theme int

const (
	// ThemeDark selects glamour's "dark" style. Zero value.
	ThemeDark Theme = iota
	// ThemeLight selects glamour's "light" style.
	ThemeLight
)

// Options configures a single Render call.
type Options struct {
	Mode Mode
	// Theme selects the ModeStyled color palette (dark/light). Callers
	// resolve it themselves (see ResolveTheme) rather than Render sniffing
	// the environment, for the same determinism reason as Width below.
	Theme Theme
	// Width is the wrap width in columns. Always supplied by the caller
	// (see TerminalWidth) — Render never queries the terminal itself, so
	// its output stays deterministic for golden-file testing. Non-positive
	// values fall back to glamour's own default (80).
	Width int
}

// rendererKey identifies a cached *glamour.TermRenderer by the (mode, theme,
// width) triple that determines its output. ModeDegraded never reaches this
// cache. theme is meaningless for ModePlain, but including it unconditionally
// keeps the key simple; ResolveTheme's ModePlain callers always pass the
// zero Theme anyway, so no extra cache entries are created in practice.
type rendererKey struct {
	mode  Mode
	theme Theme
	width int
}

var (
	rendererMu    sync.Mutex
	rendererCache = map[rendererKey]*glamour.TermRenderer{}
)

// glamourStyle maps a (mode, theme) pair to glamour's builtin style name.
func glamourStyle(mode Mode, theme Theme) string {
	if mode == ModePlain {
		return glamourStyleNoTTY
	}
	if theme == ThemeLight {
		return glamourStyleLight
	}
	return glamourStyleDark
}

// Builtin glamour style names (charm.land/glamour/v2/styles), duplicated here
// as untyped constants so this package does not need to import the styles
// subpackage just for these string literals.
const (
	glamourStyleDark  = "dark"
	glamourStyleLight = "light"
	glamourStyleNoTTY = "notty"
)

// termRenderer returns a cached *glamour.TermRenderer for (mode, theme,
// width), creating one on first use. glamour's TermRenderer is stateful and
// not concurrency-safe (mitto-pscc.8 plan) — callers must render under the
// returned mutex, which is what Render below does.
func termRenderer(mode Mode, theme Theme, width int) (*glamour.TermRenderer, error) {
	key := rendererKey{mode: mode, theme: theme, width: width}

	rendererMu.Lock()
	defer rendererMu.Unlock()

	if r, ok := rendererCache[key]; ok {
		return r, nil
	}

	opts := []glamour.TermRendererOption{glamour.WithStylePath(glamourStyle(mode, theme))}
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

	r, err := termRenderer(opts.Mode, opts.Theme, opts.Width)
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
