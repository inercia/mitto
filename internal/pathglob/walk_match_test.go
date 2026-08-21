package pathglob

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
		if got := ContainsDoublestar(pat); got != want {
			t.Errorf("ContainsDoublestar(%q) = %v, want %v", pat, got, want)
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
		if got := LiteralPrefix(tc.pattern); got != tc.want {
			t.Errorf("LiteralPrefix(%q) = %q, want %q", tc.pattern, got, tc.want)
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
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	sort.Strings(res.Matches)
	got := strings.Join(res.Matches, ",")
	want := "a.md,sub/b.md,sub/deep/c.md"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
	if res.Truncated {
		t.Fatalf("unexpected truncated=true reason=%s", res.Reason)
	}
}

func TestWalkMatch_PrunesHeavyDirs(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"src/a.md", "node_modules/big.md", ".git/HEAD.md", "vendor/v.md", "dist/x.md"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	sort.Strings(res.Matches)
	if got := strings.Join(res.Matches, ","); got != "src/a.md" {
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
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md"}, MaxResults: 5, MaxVisited: 50000, WantFiles: true})
	if len(res.Matches) != 5 {
		t.Fatalf("len(matches) = %d, want 5", len(res.Matches))
	}
	if !res.Truncated || res.Reason != "results_cap" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=results_cap", res.Truncated, res.Reason)
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
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md"}, MaxResults: 500, MaxVisited: 3, WantFiles: true})
	if !res.Truncated || res.Reason != "visited_cap" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=visited_cap", res.Truncated, res.Reason)
	}
}

func TestWalkMatch_HonorsDeadline(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md", "sub/b.md"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	if !res.Truncated || res.Reason != "deadline" {
		t.Fatalf("truncated=%v reason=%q, want truncated=true reason=deadline", res.Truncated, res.Reason)
	}
}

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
		{[]string{"*.md", "**/*.rst"}, true},
		{[]string{"docs/**/*.md", "spec/**/*.md"}, true},
	}
	for _, tc := range cases {
		if got := PatternsContainDoublestar(tc.patterns); got != tc.want {
			t.Errorf("PatternsContainDoublestar(%v) = %v, want %v", tc.patterns, got, tc.want)
		}
	}
}

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
		if got := CommonLiteralPrefix(tc.patterns); got != tc.want {
			t.Errorf("%s: CommonLiteralPrefix(%v) = %q, want %q", tc.name, tc.patterns, got, tc.want)
		}
	}
}

func TestWalkMatch_MultiPattern_Union(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md", "b.rst", "c.txt", "sub/d.md", "sub/e.rst"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md", "**/*.rst"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	sort.Strings(res.Matches)
	got := strings.Join(res.Matches, ",")
	want := "a.md,b.rst,sub/d.md,sub/e.rst"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
	if res.Truncated {
		t.Fatalf("unexpected truncated=true reason=%s", res.Reason)
	}
}

func TestWalkMatch_MultiPattern_Dedup(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"a.md"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"**/*.md", "*.md"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	if len(res.Matches) != 1 || res.Matches[0] != "a.md" {
		t.Fatalf("matches = %v, want exactly [a.md] (dedup)", res.Matches)
	}
}

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
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: root, Patterns: []string{"docs/**/*.md", "spec/**/*.md"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	sort.Strings(res.Matches)
	got := strings.Join(res.Matches, ",")
	want := "docs/a.md,docs/sub/b.md,spec/deep/y.md,spec/x.md"
	if got != want {
		t.Fatalf("matches = %q, want %q (other/skip.md must be excluded)", got, want)
	}
}

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
	docsRoot := filepath.Join(root, "docs")
	res := WalkMatch(WalkMatchOpts{Ctx: ctx, Root: docsRoot, Patterns: []string{"**/*.md", "**/*.rst"}, MaxResults: 500, MaxVisited: 50000, WantFiles: true})
	sort.Strings(res.Matches)
	got := strings.Join(res.Matches, ",")
	want := "a.md,b.rst,sub/c.md,sub/d.rst"
	if got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}
