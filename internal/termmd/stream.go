package termmd

import "strings"

// StreamRenderer renders a growing markdown body across repeated streaming
// flushes, caching the render of a "stable prefix" so each flush only pays
// for re-rendering the trailing partial instead of the whole accumulated
// body (mitto-pscc.8.1).
//
// Not concurrency-safe: a caller owns one StreamRenderer per in-flight
// message and drives it from a single goroutine (chatui's Bubble Tea
// update loop). It renders both the cached prefix and the trailing
// partial via the package-level Render, so the underlying glamour
// renderers stay serialised under termmd's own rendererMu — no second
// lock is introduced.
//
// Composition is deliberately NOT byte-identical to Render(fullBody,
// opts): glamour's per-element "is this the first block" state (e.g.
// HeadingElement.First) and its blank-line padding differ between
// rendering a fragment standalone and rendering it as a later block of one
// document, so two independently rendered fragments can never be spliced
// back into exactly what a single render would produce (verified
// empirically against glamour v2.0.1 for a plain two-paragraph document
// under styled mode while implementing this type — see the mitto-pscc.8.1
// Implementation comment). Instead each fragment is rendered as its own
// document, its own leading/trailing margin is trimmed, and the two are
// glued with one blank line — visually equivalent to a full render, the
// same approach crush's shipped streaming cache uses (its own doc comment
// makes the identical concession). Boundary selection (stablePrefixLen) is
// deliberately conservative so a cut is only ever made at a point where no
// markdown construct spans it.
type StreamRenderer struct {
	opts Options

	stablePrefix       string
	stablePrefixRender string
}

// NewStreamRenderer returns a StreamRenderer that renders at opts until
// SetOptions changes it.
func NewStreamRenderer(opts Options) *StreamRenderer {
	return &StreamRenderer{opts: opts}
}

// SetOptions updates the render options, dropping the cache if they differ
// from the current ones — a width or mode change invalidates any cached
// prefix render, since it was produced under the old settings.
func (s *StreamRenderer) SetOptions(opts Options) {
	if opts == s.opts {
		return
	}
	s.opts = opts
	s.Reset()
}

// Reset drops every cached field. The next Render call is a full render.
func (s *StreamRenderer) Reset() {
	s.stablePrefix = ""
	s.stablePrefixRender = ""
}

// Render returns body rendered at the receiver's current Options, reusing
// the cached stable-prefix render when body is a literal byte-prefix
// extension of what is cached and a safe boundary is available.
func (s *StreamRenderer) Render(body string) string {
	// ModeDegraded renders HTML, not markdown — it never reaches glamour,
	// so there is no glamour-render cache to maintain for it.
	if s.opts.Mode == ModeDegraded {
		return Render(body, s.opts)
	}

	if !strings.HasPrefix(body, s.stablePrefix) {
		// Not an extension of what is cached (edit, retraction, clear, or
		// the very first call): drop the cache and start over.
		s.Reset()
	}

	// Re-scanning the whole body on every flush is a cheap text scan
	// relative to a glamour render, so this deliberately favours
	// simplicity (a full scan from the start of body) over incremental
	// cumulative-state tracking of a per-call delta — the O(n) win over
	// the naive path comes from skipping re-render of the prefix, not
	// from skipping the boundary scan.
	if boundary := stablePrefixLen(body); boundary > len(s.stablePrefix) {
		newChunk := body[len(s.stablePrefix):boundary]
		s.stablePrefixRender = glueRenders(s.stablePrefixRender, s.renderFragment(newChunk))
		s.stablePrefix = body[:boundary]
	}

	if s.stablePrefix == "" {
		return Render(body, s.opts)
	}
	trail := body[len(s.stablePrefix):]
	if trail == "" {
		return s.stablePrefixRender
	}
	return glueRenders(s.stablePrefixRender, s.renderFragment(trail))
}

// renderFragment renders text as an independent document and trims its own
// leading/trailing margin so it can be glued to another fragment without a
// doubled blank line.
func (s *StreamRenderer) renderFragment(text string) string {
	return trimGlamourMargins(Render(text, s.opts))
}

// trimGlamourMargins strips the leading/trailing blank lines glamour wraps
// every document in, so two independently rendered fragments can be joined
// with a single explicit separator instead of stacking each fragment's own
// margin. Only whole blank lines are removed: the leading whitespace of the
// first surviving line is glamour's own left indent (two spaces under the
// "dark" style) and must survive, or every fragment after a stable-prefix
// boundary would render flush-left while a full render stays indented.
func trimGlamourMargins(s string) string {
	lines := strings.Split(s, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// glueRenders joins two already-margin-trimmed fragments with a single
// blank-line separator, matching glamour's own paragraph-to-paragraph
// spacing. Either side may be empty.
func glueRenders(prefix, trail string) string {
	switch {
	case prefix == "" && trail == "":
		return ""
	case prefix == "":
		return trail
	case trail == "":
		return prefix
	default:
		return prefix + "\n\n" + trail
	}
}
