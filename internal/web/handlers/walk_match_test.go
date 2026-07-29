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
