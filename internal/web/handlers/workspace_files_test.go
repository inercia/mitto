package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// doWorkspaceFiles runs the HandleWorkspaceFiles handler with the given query
// params and returns the recorder + decoded body. workingDir, dir, glob may be
// omitted (empty string).
func doWorkspaceFiles(t *testing.T, workingDir, dir, glob string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	q := ""
	sep := "?"
	if workingDir != "" {
		q += sep + "working_dir=" + workingDir
		sep = "&"
	}
	if dir != "" {
		q += sep + "dir=" + dir
		sep = "&"
	}
	if glob != "" {
		q += sep + "glob=" + glob
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-files"+q, nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceFiles(w, req)

	var body map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
	}
	return w, body
}

// filesList extracts the "files" array from the decoded response, coerced to
// a []string. Missing/nil returns nil.
func filesList(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	raw, ok := body["files"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("files entry is not a string: %v (%T)", v, v)
		}
		out = append(out, s)
	}
	return out
}

func TestWorkspaceFiles_MissingWorkingDir(t *testing.T) {
	w, _ := doWorkspaceFiles(t, "", "", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceFiles_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/workspace-files?working_dir=/tmp", nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceFiles(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWorkspaceFiles_AbsoluteDirRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceFiles(t, tmp, "/etc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceFiles_InvalidGlobRejected(t *testing.T) {
	tmp := t.TempDir()
	// Unterminated character class -> filepath.Match returns ErrBadPattern.
	w, _ := doWorkspaceFiles(t, tmp, "", "%5Babc") // [abc URL-encoded
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceFiles_EmptyDirReturnsEmptyList(t *testing.T) {
	tmp := t.TempDir()
	w, body := doWorkspaceFiles(t, tmp, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	files := filesList(t, body)
	if len(files) != 0 {
		t.Fatalf("files = %v, want empty", files)
	}
}

func TestWorkspaceFiles_ListsRegularFilesUnderDir(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"a.md", "b.md", "c.txt"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Also create a subdirectory to confirm it is excluded from the listing.
	if err := os.MkdirAll(filepath.Join(sub, "child"), 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	w, body := doWorkspaceFiles(t, tmp, "docs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	files := filesList(t, body)
	want := []string{"docs/a.md", "docs/b.md", "docs/c.txt"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestWorkspaceFiles_GlobFilters(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.txt", "d.md"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	w, body := doWorkspaceFiles(t, tmp, "", "%2A.md") // *.md URL-encoded
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	files := filesList(t, body)
	want := []string{"a.md", "b.md", "d.md"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestWorkspaceFiles_ContainmentDotDotEscape(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "secret.md"), []byte("s"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// dir "../other" escapes ws; must return empty list, not leak names.
	w, body := doWorkspaceFiles(t, ws, "../other", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := filesList(t, body); len(got) != 0 {
		t.Fatalf("files = %v, want empty (escape rejected)", got)
	}
}

func TestWorkspaceFiles_SymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows CI")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "secret.md"), []byte("s"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// Symlink inside ws pointing at other/ — after EvalSymlinks the target is
	// outside ws so the listing must be empty.
	if err := os.Symlink(other, filepath.Join(ws, "escape")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	w, body := doWorkspaceFiles(t, ws, "escape", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := filesList(t, body); len(got) != 0 {
		t.Fatalf("files = %v, want empty (symlink escape rejected)", got)
	}
}

func TestWorkspaceFiles_ResultCap(t *testing.T) {
	tmp := t.TempDir()
	// Create > workspaceFilesMaxResults entries.
	for i := 0; i < workspaceFilesMaxResults+50; i++ {
		name := "f" + strconv.Itoa(i) + ".md"
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	w, body := doWorkspaceFiles(t, tmp, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	files := filesList(t, body)
	if len(files) != workspaceFilesMaxResults {
		t.Fatalf("len(files) = %d, want %d", len(files), workspaceFilesMaxResults)
	}
}

func TestWorkspaceFiles_NonExistentDirEmptyList(t *testing.T) {
	tmp := t.TempDir()
	w, body := doWorkspaceFiles(t, tmp, "does-not-exist", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := filesList(t, body); len(got) != 0 {
		t.Fatalf("files = %v, want empty (missing dir)", got)
	}
}

func writeFiles(t *testing.T, root string, paths []string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func TestWorkspaceFiles_Recursive_MatchesNested(t *testing.T) {
	tmp := t.TempDir()
	writeFiles(t, tmp, []string{"a.md", "sub/b.md", "sub/deep/c.md"})
	w, body := doWorkspaceFiles(t, tmp, "", "%2A%2A%2F%2A.md") // **/*.md
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := filesList(t, body)
	want := []string{"a.md", "sub/b.md", "sub/deep/c.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestWorkspaceFiles_Recursive_AnchoredPrefix(t *testing.T) {
	tmp := t.TempDir()
	writeFiles(t, tmp, []string{"docs/a.md", "docs/sub/b.md", "src/skip.md"})
	w, body := doWorkspaceFiles(t, tmp, "", "docs%2F%2A%2A%2F%2A.md") // docs/**/*.md
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := filesList(t, body)
	want := []string{"docs/a.md", "docs/sub/b.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestWorkspaceFiles_Recursive_PrunesHeavyDirs(t *testing.T) {
	tmp := t.TempDir()
	writeFiles(t, tmp, []string{
		"src/a.md",
		"node_modules/big.md",
		".git/HEAD.md",
		"vendor/v.md",
		"dist/x.md",
	})
	w, body := doWorkspaceFiles(t, tmp, "", "%2A%2A%2F%2A.md")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := filesList(t, body)
	if strings.Join(got, ",") != "src/a.md" {
		t.Fatalf("files = %v, want [src/a.md]", got)
	}
}

func TestWorkspaceFiles_Recursive_HonorsResultsCap(t *testing.T) {
	tmp := t.TempDir()
	files := make([]string, 0, workspaceFilesMaxResults+100)
	for i := 0; i < workspaceFilesMaxResults+100; i++ {
		files = append(files, "sub/f"+strconv.Itoa(i)+".md")
	}
	writeFiles(t, tmp, files)
	w, body := doWorkspaceFiles(t, tmp, "", "%2A%2A%2F%2A.md")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := filesList(t, body); len(got) != workspaceFilesMaxResults {
		t.Fatalf("len(files) = %d, want %d", len(got), workspaceFilesMaxResults)
	}
}

func TestWorkspaceFiles_Recursive_DoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows CI")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(filepath.Join(other, "hidden"), 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "hidden", "secret.md"), []byte("s"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "top.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write top: %v", err)
	}
	if err := os.Symlink(other, filepath.Join(ws, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	w, body := doWorkspaceFiles(t, ws, "", "%2A%2A%2F%2A.md")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := filesList(t, body)
	if strings.Join(got, ",") != "top.md" {
		t.Fatalf("files = %v, want [top.md] (symlink not descended)", got)
	}
}

func TestWorkspaceFiles_Recursive_ContainmentPrefixEscape(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "secret.md"), []byte("s"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// glob "../other/**/*.md" — literal prefix "../other" resolves outside ws.
	w, body := doWorkspaceFiles(t, ws, "", "..%2Fother%2F%2A%2A%2F%2A.md")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := filesList(t, body); len(got) != 0 {
		t.Fatalf("files = %v, want empty (prefix escape rejected)", got)
	}
}

// doWorkspaceFilesMulti mirrors doWorkspaceFiles but accepts a list of glob
// entries emitted as repeated ?glob=… query parameters (mitto-ebb).
func doWorkspaceFilesMulti(t *testing.T, workingDir, dir string, globs []string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	q := ""
	sep := "?"
	if workingDir != "" {
		q += sep + "working_dir=" + workingDir
		sep = "&"
	}
	if dir != "" {
		q += sep + "dir=" + dir
		sep = "&"
	}
	for _, g := range globs {
		q += sep + "glob=" + g
		sep = "&"
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-files"+q, nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceFiles(w, req)

	var body map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
	}
	return w, body
}

// TestWorkspaceFiles_MultiGlob_Union pins mitto-ebb: repeated ?glob=… params
// produce a UNION of matches (a file wins when ANY listed glob matches).
func TestWorkspaceFiles_MultiGlob_Union(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"a.md", "b.rst", "c.txt", "d.md"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	w, body := doWorkspaceFilesMulti(t, tmp, "", []string{"%2A.md", "%2A.rst"}) // *.md and *.rst
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := filesList(t, body)
	want := []string{"a.md", "b.rst", "d.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v (union of *.md and *.rst; c.txt excluded)", got, want)
	}
}

// TestWorkspaceFiles_MultiGlob_Dedup pins that a file matching multiple listed
// globs is reported EXACTLY ONCE at the API level.
func TestWorkspaceFiles_MultiGlob_Dedup(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Both patterns match a.md; the API must not report it twice.
	w, body := doWorkspaceFilesMulti(t, tmp, "", []string{"%2A.md", "a%2A"}) // *.md and a*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := filesList(t, body)
	if len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("files = %v, want exactly [a.md] (dedup)", got)
	}
}

// TestWorkspaceFiles_MultiGlob_EmptyEntryRejected pins that an empty glob
// entry (e.g. from ?glob=*.md&glob=) surfaces as 400 instead of silently
// matching nothing.
func TestWorkspaceFiles_MultiGlob_EmptyEntryRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceFilesMulti(t, tmp, "", []string{"%2A.md", ""})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestWorkspaceFiles_MultiGlob_InvalidEntryRejected pins that a malformed
// entry hidden mid-list (not just the first entry) is caught.
func TestWorkspaceFiles_MultiGlob_InvalidEntryRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceFilesMulti(t, tmp, "", []string{"%2A.md", "%5Babc"}) // "*.md" then "[abc"
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestWorkspaceFiles_MultiGlob_RecursiveDivergentPrefixes pins the walker
// fallback path through the HTTP layer: patterns with divergent literal
// prefixes force a walk from the workspace root and still union-match.
func TestWorkspaceFiles_MultiGlob_RecursiveDivergentPrefixes(t *testing.T) {
	tmp := t.TempDir()
	for _, rel := range []string{"docs/a.md", "docs/sub/b.md", "spec/x.md", "spec/deep/y.md", "other/skip.md"} {
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// docs/**/*.md and spec/**/*.md — divergent prefixes → walk from root.
	w, body := doWorkspaceFilesMulti(t, tmp, "", []string{
		"docs%2F%2A%2A%2F%2A.md",
		"spec%2F%2A%2A%2F%2A.md",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := filesList(t, body)
	// API returns forward-slash paths already sorted alphabetically.
	want := []string{"docs/a.md", "docs/sub/b.md", "spec/deep/y.md", "spec/x.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v (other/skip.md must be excluded)", got, want)
	}
}
