package handlers

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestContainsDoublestar(t *testing.T) {
	cases := map[string]bool{
		"":              false,
		"*.md":          false,
		"prod-*":        false,
		"**/*.md":       true,
		"docs/**/*.md":  true,
		"**":            true,
		"a/**b":         true, // literal contains "**"
		"**/foo/bar.md": true,
	}
	for pat, want := range cases {
		if got := containsDoublestar(pat); got != want {
			t.Errorf("containsDoublestar(%q) = %v, want %v", pat, got, want)
		}
	}
}

func TestLiteralPrefix(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"", ""},
		{"*", ""},
		{"**/*.md", ""},
		{"docs/**", "docs"},
		{"docs/**/*.md", "docs"},
		{"docs/*/x.md", "docs"},
		{"docs/sub/foo.md", "docs/sub"},
		{"a/b/c/*.go", "a/b/c"},
		{"[abc]/x", ""},
		{"docs/{a,b}/x", "docs"},
		{"docs/?/x", "docs"},
	}
	for _, tc := range cases {
		if got := literalPrefix(tc.pattern); got != tc.want {
			t.Errorf("literalPrefix(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func mkTree(t *testing.T, root string, files, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent %s: %v", f, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
}

func TestWalkMatch_MatchesNestedFiles(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md", "sub/b.md", "sub/deep/c.md", "sub/deep/d.txt"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	sort.Strings(res.matches)
	got := strings.Join(res.matches, ",")
	want := "a.md,sub/b.md,sub/deep/c.md"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
	if res.truncated {
		t.Fatalf("unexpected truncated=true reason=%s", res.reason)
	}
}

func TestWalkMatch_PrunesHeavyDirs(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"src/a.md", "node_modules/big.md", ".git/HEAD.md", "vendor/v.md", "dist/x.md"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	sort.Strings(res.matches)
	if got := strings.Join(res.matches, ","); got != "src/a.md" {
		t.Fatalf("matches = %q, want %q", got, "src/a.md")
	}
}

func TestWalkMatch_HonorsResultsCap(t *testing.T) {
	root := t.TempDir()
	files := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		files = append(files, "sub/f"+strconv.Itoa(i)+".md")
	}
	mkTree(t, root, files, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md"}, maxResults: 5, maxVisited: 50000, wantFiles: true})
	if len(res.matches) != 5 {
		t.Fatalf("len(matches) = %d, want 5", len(res.matches))
	}
	if !res.truncated || res.reason != "results_cap" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=results_cap", res.truncated, res.reason)
	}
}

func TestWalkMatch_HonorsVisitedCap(t *testing.T) {
	root := t.TempDir()
	files := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		files = append(files, "sub/f"+strconv.Itoa(i)+".md")
	}
	mkTree(t, root, files, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md"}, maxResults: 500, maxVisited: 3, wantFiles: true})
	if !res.truncated || res.reason != "visited_cap" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=visited_cap", res.truncated, res.reason)
	}
}

func TestWalkMatch_HonorsDeadline(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md", "sub/b.md"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	if !res.truncated || res.reason != "deadline" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=deadline", res.truncated, res.reason)
	}
}

// TestPatternsContainDoublestar pins the list-form recursive detector: any
// entry with "**" flips the caller into the recursive walker branch.
func TestPatternsContainDoublestar(t *testing.T) {
	cases := []struct {
		patterns []string
		want     bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"*.md"}, false},
		{[]string{"*.md", "*.rst"}, false},
		{[]string{"**/*.md"}, true},
		{[]string{"*.md", "**/*.rst"}, true}, // one recursive is enough
		{[]string{"docs/**/*.md", "spec/**/*.md"}, true},
	}
	for _, tc := range cases {
		if got := patternsContainDoublestar(tc.patterns); got != tc.want {
			t.Errorf("patternsContainDoublestar(%v) = %v, want %v", tc.patterns, got, tc.want)
		}
	}
}

// TestCommonLiteralPrefix pins the walk-root optimization gate: an anchored
// walk root is safe ONLY when every pattern shares the same non-empty literal
// prefix. Divergent prefixes must yield "" so the caller falls back to the
// workspace root — otherwise the walk would silently miss matches under other
// literal prefixes.
func TestCommonLiteralPrefix(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		want     string
	}{
		{"empty list", []string{}, ""},
		{"single with prefix", []string{"docs/**/*.md"}, "docs"},
		{"single without prefix", []string{"**/*.md"}, ""},
		{"two with same prefix", []string{"docs/**/*.md", "docs/**/*.rst"}, "docs"},
		{"three with same prefix", []string{"docs/**/*.md", "docs/*/x.md", "docs/*/y.md"}, "docs"},
		{"divergent prefixes → empty (must fall back to root)", []string{"docs/**/*.md", "spec/**/*.md"}, ""},
		{"one anchored + one unanchored → empty", []string{"docs/**/*.md", "**/*.md"}, ""},
		{"deeper vs shallow same first segment → different literalPrefix → empty", []string{"docs/**/*.md", "docs/sub/foo.md"}, ""},
	}
	for _, tc := range cases {
		if got := commonLiteralPrefix(tc.patterns); got != tc.want {
			t.Errorf("%s: commonLiteralPrefix(%v) = %q, want %q", tc.name, tc.patterns, got, tc.want)
		}
	}
}

// TestWalkMatch_MultiPattern_Union pins union semantics: a file matches when
// ANY pattern in the list matches. Files that match neither are dropped;
// results include entries reached via each pattern.
func TestWalkMatch_MultiPattern_Union(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md", "b.rst", "c.txt", "sub/d.md", "sub/e.rst"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md", "**/*.rst"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	sort.Strings(res.matches)
	got := strings.Join(res.matches, ",")
	want := "a.md,b.rst,sub/d.md,sub/e.rst"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
	if res.truncated {
		t.Fatalf("unexpected truncated=true reason=%s", res.reason)
	}
}

// TestWalkMatch_MultiPattern_Dedup pins that a file matching multiple patterns
// is reported EXACTLY ONCE. The walker sees each entry once and short-circuits
// on the first pattern hit, so this naturally holds — the regression guard
// pins the invariant against a future refactor that iterates patterns in the
// outer loop.
func TestWalkMatch_MultiPattern_Dedup(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Both patterns match a.md; result must not contain a duplicate.
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"**/*.md", "*.md"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	if len(res.matches) != 1 || res.matches[0] != "a.md" {
		t.Fatalf("matches = %v, want exactly [a.md] (dedup)", res.matches)
	}
}

// TestWalkMatch_MultiPattern_DivergentPrefixesFromRoot pins the fallback: when
// the caller has already selected the workspace root as the walk root
// (because commonLiteralPrefix returned ""), the walker must still find
// matches under EACH pattern's own literal prefix without any anchoring.
func TestWalkMatch_MultiPattern_DivergentPrefixesFromRoot(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"docs/a.md",
		"docs/sub/b.md",
		"spec/x.md",
		"spec/deep/y.md",
		"other/skip.md",
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Divergent prefixes — the caller would walk from root; the walker must
	// still recognize BOTH prefix branches via PathMatch.
	res := walkMatch(walkMatchOpts{ctx: ctx, root: root, patterns: []string{"docs/**/*.md", "spec/**/*.md"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	sort.Strings(res.matches)
	got := strings.Join(res.matches, ",")
	want := "docs/a.md,docs/sub/b.md,spec/deep/y.md,spec/x.md"
	if got != want {
		t.Fatalf("matches = %q, want %q (other/skip.md must be excluded)", got, want)
	}
}

// TestWalkMatch_MultiPattern_SharedPrefixAnchored pins that when every
// pattern shares the same non-empty literal prefix, the caller can anchor
// the walk at that prefix and pass the patterns stripped of the prefix; the
// walker still returns forward-slash paths RELATIVE TO the anchored root.
// This mirrors the anchored-optimization codepath in the handler.
func TestWalkMatch_MultiPattern_SharedPrefixAnchored(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"docs/a.md",
		"docs/b.rst",
		"docs/sub/c.md",
		"docs/sub/d.rst",
		"other/skip.md",
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Simulate the handler's anchored branch: walk from docs/ and strip the
	// shared "docs/" prefix from each pattern.
	docsRoot := filepath.Join(root, "docs")
	res := walkMatch(walkMatchOpts{ctx: ctx, root: docsRoot, patterns: []string{"**/*.md", "**/*.rst"}, maxResults: 500, maxVisited: 50000, wantFiles: true})
	sort.Strings(res.matches)
	got := strings.Join(res.matches, ",")
	// Paths returned are RELATIVE to docsRoot; other/ is unreachable from
	// docsRoot so skip.md never surfaces.
	want := "a.md,b.rst,sub/c.md,sub/d.rst"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}
