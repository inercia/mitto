package termmd

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

// wantGlamourVersion pins the charm.land/glamour/v2 version the goldens in
// this package were generated against (go.mod today), so a dependency bump
// that silently changes glamour's rendering shows up as a version-pin test
// failure pointing here, rather than an unexplained golden diff (mitto-pscc.12
// plan decision: "pin the glamour version so style drift is a visible diff
// rather than silent churn").
const wantGlamourVersion = "v2.0.1"

var glamourRequireRe = regexp.MustCompile(`(?m)^\s*charm\.land/glamour/v2\s+(\S+)`)

// TestGlamourVersion_Pinned fails loudly if the go.mod-resolved
// charm.land/glamour/v2 version drifts from wantGlamourVersion, since every
// golden file in testdata/ was generated against that exact version and
// glamour does not promise stable rendering output across releases.
//
// This reads go.mod directly rather than runtime/debug.ReadBuildInfo:
// BuildInfo.Deps is reliably populated for a `go build`-produced binary but
// is empty for a `go test`-produced binary in this toolchain (verified: a
// `package main` importing glamour built with `go build`/`go run` reports
// 27 deps including glamour, while the equivalent `go test -c` binary
// reports zero) — so asserting against go.mod is both more portable across
// toolchains and catches a version bump before anything is even built.
func TestGlamourVersion_Pinned(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	modPath := filepath.Join(root, "..", "..", "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatalf("read %s: %v", modPath, err)
	}
	m := glamourRequireRe.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("charm.land/glamour/v2 requirement not found in %s", modPath)
	}
	if got := m[1]; got != wantGlamourVersion {
		t.Errorf("go.mod requires charm.land/glamour/v2 %s, want %s (update wantGlamourVersion AND regenerate goldens with -update if this bump is intentional)", got, wantGlamourVersion)
	}
}

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

// TestRender_StyledLight pins glamour's light style output at a fixed width
// (mitto-u7k3), analogous to TestRender_Styled — deterministic for the same
// reason: WithStylePath("light") + a caller-supplied width are the only
// inputs, so Options.Theme: ThemeLight never touches the terminal.
func TestRender_StyledLight(t *testing.T) {
	out := Render(readCorpus(t), Options{Mode: ModeStyled, Theme: ThemeLight, Width: testWidth})
	checkGolden(t, "corpus.light.golden", out)
}

// TestRender_StyledDarkVsLightDiffer guards against ResolveTheme's
// precedence table silently regressing to a single style — ModeStyled with
// ThemeDark vs ThemeLight must produce different output for the same body,
// since golden-file byte equality between the two would mean Theme is not
// actually being threaded into glamourStyle/termRenderer.
func TestRender_StyledDarkVsLightDiffer(t *testing.T) {
	dark := Render(readCorpus(t), Options{Mode: ModeStyled, Theme: ThemeDark, Width: testWidth})
	light := Render(readCorpus(t), Options{Mode: ModeStyled, Theme: ThemeLight, Width: testWidth})
	if dark == light {
		t.Fatal("ModeStyled with ThemeDark and ThemeLight produced identical output, want the theme to change the palette")
	}
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

// TestNormalization_InvalidUTF8DoesNotHang reproduces mitto-3jph through the
// x/text normalization dependency reached by glamour's terminal renderer. The
// vulnerable iterator runs in a subprocess so the parent can fail boundedly.
func TestNormalization_InvalidUTF8DoesNotHang(t *testing.T) {
	const helperEnv = "MITTO_TEST_INVALID_UTF8_NORM_ITER"
	if os.Getenv(helperEnv) == "1" {
		var iter norm.Iter
		iter.InitString(norm.NFC, "\xf3\xcc\x80")
		for !iter.Done() {
			_ = iter.Next()
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNormalization_InvalidUTF8DoesNotHang$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("normalization hung on invalid UTF-8 (GO-2026-5970): %s", out)
	}
	if err != nil {
		t.Fatalf("normalization helper failed: %v: %s", err, out)
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
