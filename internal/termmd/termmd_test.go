package termmd

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// update regenerates golden files when set: go test ./internal/termmd/... -update
var update = flag.Bool("update", false, "update golden files")

const testWidth = 80

func readCorpus(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "corpus.md"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	return string(b)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

// TestRender_Plain pins glamour's notty style output: ASCII only, no ANSI
// escapes, so it is byte-identical regardless of the CI environment's
// terminal capabilities.
func TestRender_Plain(t *testing.T) {
	out := Render(readCorpus(t), Options{Mode: ModePlain, Width: testWidth})
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("plain mode output contains an ESC byte, want none:\n%s", out)
	}
	checkGolden(t, "corpus.plain.golden", out)
}

// TestRender_Styled pins glamour's dark style output at a fixed width. This
// is safe to golden despite being ANSI-styled because Render never queries
// the terminal — WithStylePath("dark") + a caller-supplied width are the
// only inputs, so the output is deterministic in any environment.
func TestRender_Styled(t *testing.T) {
	out := Render(readCorpus(t), Options{Mode: ModeStyled, Width: testWidth})
	checkGolden(t, "corpus.styled.golden", out)
}

// TestRenderHTMLFallback_Golden pins the degraded HTML-to-text path against
// a small legacy-style HTML body (mitto-pscc.3 predates markdown
// persistence, so old events carry HTML only).
func TestRenderHTMLFallback_Golden(t *testing.T) {
	htmlBody := "<p>Hello &amp; welcome.</p><ul><li>one</li><li>two</li></ul><p>Bye.</p>"
	checkGolden(t, "fallback.golden", RenderHTMLFallback(htmlBody))
}

// TestRender_ModeDegradedIgnoresGlamour verifies ModeDegraded never touches
// the glamour renderer cache, per Render's doc comment.
func TestRender_ModeDegradedIgnoresGlamour(t *testing.T) {
	htmlBody := "<p>plain <b>text</b></p>"
	got := Render(htmlBody, Options{Mode: ModeDegraded})
	want := RenderHTMLFallback(htmlBody)
	if got != want {
		t.Errorf("Render(ModeDegraded) = %q, want %q", got, want)
	}
}

// TestTermRenderer_WidthChangeInvalidates verifies a width change produces a
// distinct cache entry (and therefore differently-wrapped output) rather
// than reusing a renderer built for another width.
func TestTermRenderer_WidthChangeInvalidates(t *testing.T) {
	long := strings.Repeat("word ", 40)
	narrow := Render(long, Options{Mode: ModePlain, Width: 20})
	wide := Render(long, Options{Mode: ModePlain, Width: 100})
	if narrow == wide {
		t.Fatalf("expected different output for different widths, got identical:\n%s", narrow)
	}
}

// TestRender_ZeroWidthUsesGlamourDefault verifies Options{Width: 0} (the
// zero value, e.g. a caller that forgot to resolve a width) does not crash
// termRenderer's "if width > 0" guard against calling
// glamour.WithWordWrap(0) — it renders using glamour's own default wrap
// width instead, for both modes that reach the glamour path.
func TestRender_ZeroWidthUsesGlamourDefault(t *testing.T) {
	for _, mode := range []Mode{ModeStyled, ModePlain} {
		out := Render("A short paragraph.\n", Options{Mode: mode})
		if out == "" {
			t.Errorf("Render with zero Width and mode %v returned empty output", mode)
		}
	}
}

// TestRender_ConcurrentSafe exercises Render from multiple goroutines under
// -race: glamour's TermRenderer is documented as not concurrency-safe, so
// this pins that Render's shared-renderer-cache locking prevents a race.
func TestRender_ConcurrentSafe(t *testing.T) {
	md := readCorpus(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mode := ModeStyled
			if i%2 == 0 {
				mode = ModePlain
			}
			_ = Render(md, Options{Mode: mode, Width: testWidth})
		}(i)
	}
	wg.Wait()
}
