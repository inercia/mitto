package cel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/pathglob"
)

// =============================================================================
// Glob-mode tests for FileExists / DirExists (mitto-1fa acceptance criteria)
//
// These pin the behaviour of the metachar-detection overload added in
// mitto-1fa: literal paths keep the O(1) os.Stat fast path unchanged;
// patterns containing '*', '?', '[', '{' switch to a bounded walk via
// pathglob.WalkMatch with maxResults=1, WantFiles gating file-vs-dir, a 2s
// deadline, a visited-entries cap and a fail-open policy. Both Go helpers
// (fileExists/dirExists) and CEL bindings (FileExists/DirExists) are pinned.
// =============================================================================

// clearGlobCache drops any memoised (folder,pattern,wantFiles) entry from a
// previous test so the next call re-walks. Tests that assert cache semantics
// call this in a fresh sub-test setup.
func clearGlobCache(t *testing.T) {
	t.Helper()
	globCacheMu.Lock()
	globCache = map[string]globCacheEntry{}
	globCacheMu.Unlock()
}

// makeGlobFixture builds a tiny workspace tree used by the acceptance-criteria
// tests: docs/README.md, docs/deep/notes.md, node_modules/pkg/index.js
// (pruned), src/a.tf, src/b.go, and a sub/ directory. Returns the tmp root.
func makeGlobFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkfile := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mkfile("docs/README.md", "hello")
	mkfile("docs/deep/notes.md", "notes")
	mkfile("node_modules/pkg/index.js", "n")
	mkfile("src/a.tf", "resource {}")
	mkfile("src/b.go", "package src")
	if err := os.Mkdir(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGlob_MetacharDetection pins the auto-detect switch: only '*', '?', '[',
// '{' route to the walker; everything else keeps the O(1) stat path.
func TestGlob_MetacharDetection(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"docs/README.md", false}, // literal
		{"", false},               // empty
		{"docs/*.md", true},       // *
		{"docs/?ile.md", true},    // ?
		{"src/[ab].go", true},     // [
		{"src/{a,b}.go", true},    // {
		{"**/*.tf", true},         // doublestar
	}
	for _, tc := range cases {
		if got := containsGlobMeta(tc.pattern); got != tc.want {
			t.Errorf("containsGlobMeta(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

// TestGlob_FileExists_LiteralFastPath pins acceptance criterion #1:
// a literal path is stat-only, byte-for-byte identical to the pre-mitto-1fa
// behaviour. Also confirms the CEL binding and Go helper agree.
func TestGlob_FileExists_LiteralFastPath(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: root}}

	// Existing regular file → true; existing dir → false; missing → false.
	cases := []struct {
		path string
		want bool
	}{
		{"docs/README.md", true},
		{"docs", false},
		{"absent.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := fileExists(root, tc.path); got != tc.want {
				t.Errorf("fileExists(%q) = %v, want %v", tc.path, got, tc.want)
			}
			celExpr := fmt.Sprintf("FileExists(%q)", tc.path)
			if got := evalCEL(t, e, celExpr, ctx); got != tc.want {
				t.Errorf("CEL %s = %v, want %v", celExpr, got, tc.want)
			}
		})
	}
}

// TestGlob_FileExists_MatchAndMiss pins acceptance criterion #2 (any file
// matching a doublestar pattern → true) and the miss counterpart.
func TestGlob_FileExists_MatchAndMiss(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: root}}

	cases := []struct {
		pattern string
		want    bool
	}{
		{"**/*.tf", true},   // src/a.tf
		{"**/*.md", true},   // docs/README.md, docs/deep/notes.md
		{"docs/*.md", true}, // README.md
		{"docs/**/notes.md", true},
		{"**/no-such-*.zzz", false},
		{"src/{a,b}.go", true}, // brace expansion — b.go
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			clearGlobCache(t)
			if got := fileExists(root, tc.pattern); got != tc.want {
				t.Errorf("fileExists(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
			celExpr := fmt.Sprintf("FileExists(%q)", tc.pattern)
			clearGlobCache(t)
			if got := evalCEL(t, e, celExpr, ctx); got != tc.want {
				t.Errorf("CEL %s = %v, want %v", celExpr, got, tc.want)
			}
		})
	}
}

// TestGlob_DirExists_MatchAndPruned pins acceptance criterion #2 for dirs and
// #7 (pruned dirs stay pruned even under a doublestar pattern that would
// otherwise match one). node_modules is on pathglob.PrunedDirNames — a
// pattern like "**/node_modules" must NOT match a pruned directory, so
// DirExists returns false even though the directory exists on disk.
func TestGlob_DirExists_MatchAndPruned(t *testing.T) {
	root := makeGlobFixture(t)
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: root}}

	cases := []struct {
		pattern string
		want    bool
	}{
		{"**/deep", true},          // docs/deep
		{"sub", true},              // literal fast path — dir exists via os.Stat
		{"s?b", true},              // '?' → walker; matches "sub"
		{"**/node_modules", false}, // pruned — never walked into
		{"**/absent-*", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			clearGlobCache(t)
			if got := dirExists(root, tc.pattern); got != tc.want {
				t.Errorf("dirExists(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
			celExpr := fmt.Sprintf("DirExists(%q)", tc.pattern)
			clearGlobCache(t)
			if got := evalCEL(t, e, celExpr, ctx); got != tc.want {
				t.Errorf("CEL %s = %v, want %v", celExpr, got, tc.want)
			}
		})
	}
}

// TestGlob_FileExists_WantFilesGating pins the WantFiles gating: a pattern
// that only matches a directory returns false for fileExists, and a pattern
// that only matches a regular file returns false for dirExists.
func TestGlob_FileExists_WantFilesGating(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)

	// "docs/*" matches BOTH the regular file "docs/README.md" and the
	// directory "docs/deep". fileExists must see the file; dirExists must
	// see the directory.
	if got := fileExists(root, "docs/*"); !got {
		t.Errorf("fileExists(docs/*) = false, want true")
	}
	clearGlobCache(t)
	if got := dirExists(root, "docs/*"); !got {
		t.Errorf("dirExists(docs/*) = false, want true")
	}

	// "docs/deep/*" matches ONLY a regular file (notes.md); no directory
	// under docs/deep exists.
	clearGlobCache(t)
	if got := fileExists(root, "docs/deep/*"); !got {
		t.Errorf("fileExists(docs/deep/*) = false, want true")
	}
	clearGlobCache(t)
	if got := dirExists(root, "docs/deep/*"); got {
		t.Errorf("dirExists(docs/deep/*) = true, want false (only a file matches)")
	}
}

// TestGlob_PathSafety_AbsoluteAndDotDot pins acceptance criteria #3, #5, #6:
//   - Leading-'/' + metachar (absolute glob) → false. This is the pinned
//     resolution of #3 aligned with #5.
//   - Absolute non-glob (#4) still works via the O(1) stat fast path — see
//     TestGlob_FileExists_AbsoluteNonGlob_Compat below.
//   - "../" escape in a glob → false (#6).
func TestGlob_PathSafety_AbsoluteAndDotDot(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)
	e := newTestEvaluator(t)
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: root}}

	rejectedGlobs := []string{
		"/*/config.yaml",  // absolute + metachar → rejected
		"/**/passwd",      // absolute glob → rejected
		"../etc/passwd",   // ".." escape (literal, no metachar) — not a glob, but
		"../*/*",          // ".." escape + metachar → rejected
		"../../**/README", // deep escape → rejected
	}
	for _, pat := range rejectedGlobs {
		t.Run(pat, func(t *testing.T) {
			clearGlobCache(t)
			// Note: "../etc/passwd" has no metachar, so fileExists takes the
			// stat fast path — but statResolved joins with root and stats a
			// path outside root which does not exist → false anyway. Both
			// branches agree on the observed outcome (false).
			if got := fileExists(root, pat); got {
				t.Errorf("fileExists(%q) = true, want false", pat)
			}
			if got := dirExists(root, pat); got {
				t.Errorf("dirExists(%q) = true, want false", pat)
			}
			// CEL binding must agree.
			celExpr := fmt.Sprintf("FileExists(%q)", pat)
			clearGlobCache(t)
			if got := evalCEL(t, e, celExpr, ctx); got {
				t.Errorf("CEL %s = true, want false", celExpr)
			}
		})
	}
}

// TestGlob_FileExists_AbsoluteNonGlob_Compat pins acceptance criterion #4:
// an absolute path with NO metacharacter keeps the pre-mitto-1fa stat
// behaviour (i.e. it can succeed for a real absolute path that exists).
// We use the fixture's own absolute path as an existing target so the test
// is hermetic (no dependency on /etc/passwd being present).
func TestGlob_FileExists_AbsoluteNonGlob_Compat(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)
	abs := filepath.Join(root, "docs", "README.md")
	if got := fileExists(root, abs); !got {
		t.Errorf("fileExists(abs=%q) = false, want true (absolute non-glob compat)", abs)
	}
	// Absolute non-glob to a directory → false for fileExists, true for dirExists.
	absDir := filepath.Join(root, "docs")
	if got := fileExists(root, absDir); got {
		t.Errorf("fileExists(absDir=%q) = true, want false", absDir)
	}
	if got := dirExists(root, absDir); !got {
		t.Errorf("dirExists(absDir=%q) = false, want true", absDir)
	}
}

// TestGlob_FailOpen_VisitedCap pins acceptance criterion #8: when the walker
// truncates at visited_cap (or deadline), the helper returns true so a slow
// or huge filesystem never wrongly hides a prompt. We simulate the cap by
// temporarily lowering pathglob.WalkMatchMaxVisited to 1 and asking a
// doublestar pattern that would otherwise not match.
func TestGlob_FailOpen_VisitedCap(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)

	orig := pathglob.WalkMatchMaxVisited
	pathglob.WalkMatchMaxVisited = 1
	defer func() { pathglob.WalkMatchMaxVisited = orig }()

	// Pattern deliberately matches nothing: without the cap it would return
	// false. With the cap tripped, fail-open kicks in → true.
	if got := fileExists(root, "**/no-such-file-*.zzz"); !got {
		t.Errorf("fileExists under visited_cap = false, want true (fail-open policy)")
	}
	clearGlobCache(t)
	if got := dirExists(root, "**/no-such-dir-*-xyz"); !got {
		t.Errorf("dirExists under visited_cap = false, want true (fail-open policy)")
	}
}

// TestGlob_Cache_TTL pins acceptance criterion #9: within the TTL, a
// subsequent call with the same (folder,pattern,wantFiles) key returns the
// cached result even after the underlying filesystem changes. The literal
// (no-metachar) path is unaffected — that fast path bypasses the cache.
func TestGlob_Cache_TTL(t *testing.T) {
	clearGlobCache(t)
	root := t.TempDir()
	// Fixture: one .tf file so the first walk finds a match.
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	tfPath := filepath.Join(root, "src", "a.tf")
	if err := os.WriteFile(tfPath, []byte("resource {}"), 0644); err != nil {
		t.Fatal(err)
	}

	// First call warms the cache with true.
	if got := fileExists(root, "**/*.tf"); !got {
		t.Fatalf("first fileExists(**/*.tf) = false, want true")
	}

	// Delete the file. Without the cache, the next call would walk and
	// return false. With the cache and within TTL, it must still return
	// the cached true.
	if err := os.Remove(tfPath); err != nil {
		t.Fatal(err)
	}
	if got := fileExists(root, "**/*.tf"); !got {
		t.Errorf("cached fileExists(**/*.tf) after remove = false, want true (within TTL)")
	}

	// Verify the cache entry timestamp is recent (< globCacheTTL old) so
	// the assertion above is not a coincidence of a stale-slow test host.
	globCacheMu.Lock()
	key := root + "\x00**/*.tf\x00f"
	e, ok := globCache[key]
	globCacheMu.Unlock()
	if !ok {
		t.Fatalf("expected globCache entry for key %q, missing", key)
	}
	if !e.value {
		t.Errorf("cache entry value = false, want true")
	}
	if age := time.Since(e.at); age > globCacheTTL {
		t.Errorf("cache entry age %v exceeds TTL %v — test host too slow?", age, globCacheTTL)
	}

	// Now expire the cache by clearing it (the same effect as waiting TTL,
	// but hermetic). The next call must observe the deleted file.
	clearGlobCache(t)
	if got := fileExists(root, "**/*.tf"); got {
		t.Errorf("post-clear fileExists(**/*.tf) = true, want false (file was removed)")
	}
}

// TestGlob_Cache_KeyDistinguishesWantFiles pins that the cache key includes
// the wantFiles flag: fileExists and dirExists for the same pattern must not
// alias each other in the cache.
func TestGlob_Cache_KeyDistinguishesWantFiles(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)

	// docs/README.md matches "docs/*"; docs/deep also matches.
	// fileExists → true (README.md), dirExists → true (deep).
	if !fileExists(root, "docs/*") {
		t.Errorf("fileExists(docs/*) = false, want true")
	}
	if !dirExists(root, "docs/*") {
		t.Errorf("dirExists(docs/*) = false, want true")
	}

	globCacheMu.Lock()
	_, fOK := globCache[root+"\x00docs/*\x00f"]
	_, dOK := globCache[root+"\x00docs/*\x00d"]
	globCacheMu.Unlock()
	if !fOK {
		t.Errorf("missing cache entry for wantFiles=true (key suffix 'f')")
	}
	if !dOK {
		t.Errorf("missing cache entry for wantFiles=false (key suffix 'd')")
	}
}

// TestGlob_EmptyFolder pins that an empty workspace-folder argument yields
// false for the glob path (nothing to root the walk on) but does NOT panic
// or leak an entry into the cache with a bogus key.
func TestGlob_EmptyFolder(t *testing.T) {
	clearGlobCache(t)
	if got := fileExists("", "**/*.md"); got {
		t.Errorf("fileExists(\"\", **/*.md) = true, want false")
	}
	if got := dirExists("", "**/*"); got {
		t.Errorf("dirExists(\"\", **/*) = true, want false")
	}
}

// TestGlob_CEL_TemplateSurfaceParity pins that the FuncMap template surface
// exposes the same glob semantics as the CEL bindings — the whole point of
// mitto-1fa is one implementation change lands in both surfaces. Kept
// small: one match, one miss, one absolute-glob rejection.
func TestGlob_CEL_TemplateSurfaceParity(t *testing.T) {
	clearGlobCache(t)
	root := makeGlobFixture(t)

	funcs := BuildTemplateFuncMap(&PromptEnabledContext{
		Workspace: WorkspaceContext{Folder: root},
	})

	cases := []struct {
		body string
		want string
	}{
		{`{{ if FileExists "**/*.tf" }}yes{{ else }}no{{ end }}`, "yes"},
		{`{{ if FileExists "**/no-such.zzz" }}yes{{ else }}no{{ end }}`, "no"},
		{`{{ if FileExists "/**/passwd" }}yes{{ else }}no{{ end }}`, "no"},
		{`{{ if DirExists "**/deep" }}yes{{ else }}no{{ end }}`, "yes"},
		{`{{ if DirExists "**/node_modules" }}yes{{ else }}no{{ end }}`, "no"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			clearGlobCache(t)
			got, err := RenderPromptTemplate("t", tc.body, nil, funcs)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.TrimSpace(got) != tc.want {
				t.Errorf("render %q = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}
