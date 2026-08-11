package termmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func readStreamCorpusT(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "stream_corpus.md"))
	if err != nil {
		t.Fatalf("read stream corpus: %v", err)
	}
	return string(data)
}

// normalizeANSILines strips ANSI escapes and blank/whitespace-only lines, so
// two renders can be compared on content and order without being sensitive
// to the exact blank-line spacing StreamRenderer's margin-trim-and-glue
// composition produces at a stable-prefix boundary (mitto-pscc.8.1's
// documented byte-identity deviation: visually equivalent, not
// byte-identical, to a full render).
func normalizeANSILines(t *testing.T, s string) []string {
	t.Helper()
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

// TestStreamRenderer_MatchesFullRender_Structural is the deferred
// chatui-adjacent equivalence check named in the mitto-pscc.8.1
// Implementation comment: for many split points and both glamour-backed
// modes, the streamed render's content and order must match a fresh
// whole-body Render, modulo the blank-line spacing normalizeANSILines
// ignores.
func TestStreamRenderer_MatchesFullRender_Structural(t *testing.T) {
	body := readStreamCorpusT(t)
	for _, mode := range []Mode{ModeStyled, ModePlain} {
		for _, chunkSize := range []int{7, 40, 137, 5000} {
			opts := Options{Mode: mode, Width: testWidth}
			want := normalizeANSILines(t, Render(body, opts))

			sr := NewStreamRenderer(opts)
			var got []string
			for _, c := range streamChunks(body, chunkSize) {
				got = normalizeANSILines(t, sr.Render(c))
			}

			if len(got) != len(want) {
				t.Fatalf("mode=%v chunkSize=%d: got %d lines, want %d\ngot:\n%s\nwant:\n%s",
					mode, chunkSize, len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("mode=%v chunkSize=%d: line %d = %q, want %q", mode, chunkSize, i, got[i], want[i])
				}
			}
		}
	}
}

// TestStreamRenderer_SetOptions_WidthChangeInvalidatesCache verifies a
// width change drops the cached stable prefix, since it was rendered at the
// old width.
func TestStreamRenderer_SetOptions_WidthChangeInvalidatesCache(t *testing.T) {
	body := readStreamCorpusT(t)
	sr := NewStreamRenderer(Options{Mode: ModeStyled, Width: 80})
	_ = sr.Render(body)
	if sr.stablePrefix == "" {
		t.Fatal("expected a non-empty stable prefix to be cached from the corpus")
	}

	sr.SetOptions(Options{Mode: ModeStyled, Width: 40})
	if sr.stablePrefix != "" {
		t.Errorf("SetOptions with a different width did not reset the cache")
	}

	// The result must still be correct post-invalidation, at the new width.
	got := normalizeANSILines(t, sr.Render(body))
	want := normalizeANSILines(t, Render(body, Options{Mode: ModeStyled, Width: 40}))
	if len(got) != len(want) {
		t.Fatalf("post-width-change render: got %d lines, want %d", len(got), len(want))
	}
}

// TestStreamRenderer_SetOptions_ModeChangeInvalidatesCache mirrors the
// width case for a styled/plain mode change.
func TestStreamRenderer_SetOptions_ModeChangeInvalidatesCache(t *testing.T) {
	body := readStreamCorpusT(t)
	sr := NewStreamRenderer(Options{Mode: ModeStyled, Width: testWidth})
	_ = sr.Render(body)
	if sr.stablePrefix == "" {
		t.Fatal("expected a non-empty stable prefix to be cached from the corpus")
	}

	sr.SetOptions(Options{Mode: ModePlain, Width: testWidth})
	if sr.stablePrefix != "" {
		t.Errorf("SetOptions with a different mode did not reset the cache")
	}
}

// TestStreamRenderer_SetOptions_NoChangeKeepsCache verifies SetOptions with
// identical options is a no-op that preserves the cache — this is what lets
// items.go call SetOptions on every render without paying an invalidation
// cost when nothing changed.
func TestStreamRenderer_SetOptions_NoChangeKeepsCache(t *testing.T) {
	body := readStreamCorpusT(t)
	opts := Options{Mode: ModeStyled, Width: testWidth}
	sr := NewStreamRenderer(opts)
	_ = sr.Render(body)
	cachedPrefix := sr.stablePrefix
	if cachedPrefix == "" {
		t.Fatal("expected a non-empty stable prefix to be cached from the corpus")
	}

	sr.SetOptions(opts)
	if sr.stablePrefix != cachedPrefix {
		t.Errorf("SetOptions with unchanged options must not reset the cache")
	}
}

// TestStreamRenderer_NonPrefixBodyChangeResetsCache verifies that when the
// next body is not a literal byte-prefix extension of what is cached (an
// edit, retraction, or an unrelated new body), the cache is dropped and the
// result still matches a fresh full render — not a stale/mismatched splice.
func TestStreamRenderer_NonPrefixBodyChangeResetsCache(t *testing.T) {
	opts := Options{Mode: ModePlain, Width: testWidth}
	sr := NewStreamRenderer(opts)

	first := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"
	_ = sr.Render(first)
	if sr.stablePrefix == "" {
		t.Fatal("expected a non-empty stable prefix to be cached")
	}

	// Composition is margin-trimmed-and-glued, not byte-identical to a
	// direct Render call (the documented mitto-pscc.8.1 deviation), so
	// compare structurally rather than by exact string equality.
	unrelated := "Completely different body.\n\nWith its own paragraphs.\n"
	got := normalizeANSILines(t, sr.Render(unrelated))
	want := normalizeANSILines(t, Render(unrelated, opts))
	if len(got) != len(want) {
		t.Fatalf("Render after a non-prefix body change: got %d lines, want %d\ngot:\n%s\nwant:\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestStreamRenderer_ModeDegraded_NeverCaches verifies ModeDegraded bypasses
// the stable-prefix cache entirely (it renders HTML via RenderHTMLFallback,
// never glamour) on every call, matching a direct Render call each time.
func TestStreamRenderer_ModeDegraded_NeverCaches(t *testing.T) {
	opts := Options{Mode: ModeDegraded}
	sr := NewStreamRenderer(opts)

	for _, html := range []string{
		"<p>growing</p>",
		"<p>growing</p><p>more</p>",
		"<p>growing</p><p>more</p><ul><li>x</li></ul>",
	} {
		got := sr.Render(html)
		want := Render(html, opts)
		if got != want {
			t.Errorf("ModeDegraded Render(%q) = %q, want %q", html, got, want)
		}
		if sr.stablePrefix != "" || sr.stablePrefixRender != "" {
			t.Errorf("ModeDegraded must never populate the stable-prefix cache, got prefix=%q render=%q",
				sr.stablePrefix, sr.stablePrefixRender)
		}
	}
}

// TestStreamRenderer_Reset verifies Reset drops the cache unconditionally
// and a subsequent Render still produces output matching a fresh full
// render.
func TestStreamRenderer_Reset(t *testing.T) {
	body := readStreamCorpusT(t)
	opts := Options{Mode: ModePlain, Width: testWidth}
	sr := NewStreamRenderer(opts)
	_ = sr.Render(body)
	if sr.stablePrefix == "" {
		t.Fatal("expected a non-empty stable prefix to be cached")
	}

	sr.Reset()
	if sr.stablePrefix != "" || sr.stablePrefixRender != "" {
		t.Errorf("Reset must clear both cache fields, got prefix=%q render=%q", sr.stablePrefix, sr.stablePrefixRender)
	}

	got := normalizeANSILines(t, sr.Render(body))
	want := normalizeANSILines(t, Render(body, opts))
	if len(got) != len(want) {
		t.Fatalf("post-Reset render: got %d lines, want %d", len(got), len(want))
	}
}

// TestTrimGlamourMargins verifies leading/trailing blank lines and
// whitespace are stripped, so two fragments can be glued without stacking
// their own document margins.
func TestTrimGlamourMargins(t *testing.T) {
	in := "\n\n  content line  \n\n"
	want := "content line"
	if got := trimGlamourMargins(in); got != want {
		t.Errorf("trimGlamourMargins(%q) = %q, want %q", in, got, want)
	}
}

// TestGlueRenders covers all four prefix/trail emptiness combinations.
func TestGlueRenders(t *testing.T) {
	tests := []struct {
		name, prefix, trail, want string
	}{
		{"both empty", "", "", ""},
		{"prefix empty", "", "trail", "trail"},
		{"trail empty", "prefix", "", "prefix"},
		{"both non-empty", "prefix", "trail", "prefix\n\ntrail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := glueRenders(tt.prefix, tt.trail); got != tt.want {
				t.Errorf("glueRenders(%q, %q) = %q, want %q", tt.prefix, tt.trail, got, tt.want)
			}
		})
	}
}
