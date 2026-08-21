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

// doWorkspaceDirs runs HandleWorkspaceDirs with the given query params and
// returns the recorder + decoded body. Mirrors doWorkspaceFiles.
func doWorkspaceDirs(t *testing.T, workingDir, dir, glob string) (*httptest.ResponseRecorder, map[string]interface{}) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-dirs"+q, nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceDirs(w, req)

	var body map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
	}
	return w, body
}

func dirsList(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	raw, ok := body["dirs"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("dirs entry is not a string: %v (%T)", v, v)
		}
		out = append(out, s)
	}
	return out
}

func TestWorkspaceDirs_MissingWorkingDir(t *testing.T) {
	w, _ := doWorkspaceDirs(t, "", "", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceDirs_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/workspace-dirs?working_dir=/tmp", nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceDirs(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWorkspaceDirs_AbsoluteDirRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceDirs(t, tmp, "/etc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceDirs_InvalidGlobRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceDirs(t, tmp, "", "%5Babc") // "[abc" URL-encoded
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestWorkspaceDirs_EmptyDirReturnsEmptyList(t *testing.T) {
	tmp := t.TempDir()
	w, body := doWorkspaceDirs(t, tmp, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty", got)
	}
}

func TestWorkspaceDirs_ListsOnlySubDirectoriesUnderDir(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Two real sub-directories.
	for _, name := range []string{"api", "plans"} {
		if err := os.MkdirAll(filepath.Join(sub, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	// A regular file — must be excluded from the listing.
	if err := os.WriteFile(filepath.Join(sub, "index.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	w, body := doWorkspaceDirs(t, tmp, "docs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	want := []string{"docs/api", "docs/plans"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

func TestWorkspaceDirs_HiddenDirsExcludedByDefault(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"visible", ".hidden", ".git"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	w, body := doWorkspaceDirs(t, tmp, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	if strings.Join(got, ",") != "visible" {
		t.Fatalf("dirs = %v, want [visible] (hidden dirs excluded)", got)
	}
}

func TestWorkspaceDirs_GlobFiltersOnBaseName(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"2024-jan", "2024-feb", "2025-jan", "notes"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	w, body := doWorkspaceDirs(t, tmp, "", "2024%2A") // "2024*" URL-encoded
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	want := []string{"2024-feb", "2024-jan"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

func TestWorkspaceDirs_ContainmentDotDotEscape(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(other, "secret"), 0o755); err != nil {
		t.Fatalf("mkdir other/secret: %v", err)
	}
	// dir "../other" escapes ws; must return empty list, not leak names.
	w, body := doWorkspaceDirs(t, ws, "../other", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty (escape rejected)", got)
	}
}

func TestWorkspaceDirs_SymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows CI")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(other, "secret"), 0o755); err != nil {
		t.Fatalf("mkdir other/secret: %v", err)
	}
	// Symlink inside ws pointing at other/ — after EvalSymlinks the target is
	// outside ws so the listing must be empty.
	if err := os.Symlink(other, filepath.Join(ws, "escape")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	w, body := doWorkspaceDirs(t, ws, "escape", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty (symlink escape rejected)", got)
	}
}

func TestWorkspaceDirs_ResultCap(t *testing.T) {
	tmp := t.TempDir()
	for i := 0; i < workspaceDirsMaxResults+50; i++ {
		name := "d" + strconv.Itoa(i)
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	w, body := doWorkspaceDirs(t, tmp, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != workspaceDirsMaxResults {
		t.Fatalf("len(dirs) = %d, want %d", len(got), workspaceDirsMaxResults)
	}
}

func TestWorkspaceDirs_NonExistentDirEmptyList(t *testing.T) {
	tmp := t.TempDir()
	w, body := doWorkspaceDirs(t, tmp, "does-not-exist", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty (missing dir)", got)
	}
}

func TestWorkspaceDirs_DirWithOnlyFilesReturnsEmptyList(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	w, body := doWorkspaceDirs(t, tmp, "docs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty (files-only dir)", got)
	}
}

func mkDirs(t *testing.T, root string, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

func TestWorkspaceDirs_Recursive_MatchesNested(t *testing.T) {
	tmp := t.TempDir()
	mkDirs(t, tmp, []string{"env-a", "sub/env-b", "sub/deep/env-c", "sub/other"})
	w, body := doWorkspaceDirs(t, tmp, "", "%2A%2A%2Fenv-%2A") // **/env-*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := dirsList(t, body)
	want := []string{"env-a", "sub/deep/env-c", "sub/env-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

func TestWorkspaceDirs_Recursive_AnchoredPrefix(t *testing.T) {
	tmp := t.TempDir()
	mkDirs(t, tmp, []string{"deploy/env-a", "deploy/sub/env-b", "other/env-c"})
	w, body := doWorkspaceDirs(t, tmp, "", "deploy%2F%2A%2A%2Fenv-%2A") // deploy/**/env-*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	want := []string{"deploy/env-a", "deploy/sub/env-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

func TestWorkspaceDirs_Recursive_PrunesHeavyDirs(t *testing.T) {
	tmp := t.TempDir()
	mkDirs(t, tmp, []string{
		"src/nested",
		"node_modules/big",
		".git/refs",
		"vendor/x",
		"dist/y",
	})
	w, body := doWorkspaceDirs(t, tmp, "", "%2A%2A") // **
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	want := []string{"src", "src/nested"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v (heavy/hidden dirs pruned)", got, want)
	}
}

func TestWorkspaceDirs_Recursive_HonorsResultsCap(t *testing.T) {
	tmp := t.TempDir()
	dirs := make([]string, 0, workspaceDirsMaxResults+100)
	for i := 0; i < workspaceDirsMaxResults+100; i++ {
		dirs = append(dirs, "sub/d"+strconv.Itoa(i))
	}
	mkDirs(t, tmp, dirs)
	w, body := doWorkspaceDirs(t, tmp, "", "%2A%2A%2Fd%2A") // **/d*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != workspaceDirsMaxResults {
		t.Fatalf("len(dirs) = %d, want %d", len(got), workspaceDirsMaxResults)
	}
}

func TestWorkspaceDirs_Recursive_DoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows CI")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(filepath.Join(other, "hidden"), 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.Symlink(other, filepath.Join(ws, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	w, body := doWorkspaceDirs(t, ws, "", "%2A%2A")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	got := dirsList(t, body)
	if strings.Join(got, ",") != "sub" {
		t.Fatalf("dirs = %v, want [sub] (symlink not descended)", got)
	}
}

func TestWorkspaceDirs_Recursive_ContainmentPrefixEscape(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(other, "secret"), 0o755); err != nil {
		t.Fatalf("mkdir other/secret: %v", err)
	}
	w, body := doWorkspaceDirs(t, ws, "", "..%2Fother%2F%2A%2A") // ../other/**
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", w.Code, http.StatusOK)
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty (prefix escape rejected)", got)
	}
}

// doWorkspaceDirsMulti mirrors doWorkspaceDirs but accepts a list of glob
// entries emitted as repeated ?glob=… query parameters (mitto-ebb).
func doWorkspaceDirsMulti(t *testing.T, workingDir, dir string, globs []string) (*httptest.ResponseRecorder, map[string]interface{}) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-dirs"+q, nil)
	w := httptest.NewRecorder()
	h := New(Deps{})
	h.HandleWorkspaceDirs(w, req)

	var body map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
	}
	return w, body
}

// TestWorkspaceDirs_MultiGlob_Union pins mitto-ebb for dir-typed params: a
// sub-directory wins when ANY listed glob matches its base name.
func TestWorkspaceDirs_MultiGlob_Union(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"prod-a", "prod-b", "stage-c", "dev-d", "notes"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	w, body := doWorkspaceDirsMulti(t, tmp, "", []string{"prod-%2A", "stage-%2A"}) // prod-* and stage-*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := dirsList(t, body)
	want := []string{"prod-a", "prod-b", "stage-c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v (dev-d and notes excluded)", got, want)
	}
}

// TestWorkspaceDirs_MultiGlob_Dedup pins that a directory matching multiple
// listed globs is reported EXACTLY ONCE at the API level.
func TestWorkspaceDirs_MultiGlob_Dedup(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "prod-a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Both patterns match prod-a; the API must not report it twice.
	w, body := doWorkspaceDirsMulti(t, tmp, "", []string{"prod-%2A", "%2A-a"}) // prod-* and *-a
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := dirsList(t, body)
	if len(got) != 1 || got[0] != "prod-a" {
		t.Fatalf("dirs = %v, want exactly [prod-a] (dedup)", got)
	}
}

// TestWorkspaceDirs_MultiGlob_EmptyEntryRejected pins that an empty glob
// entry surfaces as 400.
func TestWorkspaceDirs_MultiGlob_EmptyEntryRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceDirsMulti(t, tmp, "", []string{"prod-%2A", ""})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestWorkspaceDirs_MultiGlob_InvalidEntryRejected pins that a malformed
// entry mid-list is caught (not just the first).
func TestWorkspaceDirs_MultiGlob_InvalidEntryRejected(t *testing.T) {
	tmp := t.TempDir()
	w, _ := doWorkspaceDirsMulti(t, tmp, "", []string{"prod-%2A", "%5Babc"}) // "prod-*" then "[abc"
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// TestWorkspaceDirs_NonRecursive_HoistsLiteralPrefix pins that a
// non-recursive glob whose literal prefix nests multiple directories deep
// (e.g. "apps/foo/bar/*") is anchored at that prefix instead of being
// applied verbatim to top-level base names. Regression: without the
// hoist, this pattern silently returned an empty list even when the
// nested directory contained matching sub-directories.
func TestWorkspaceDirs_NonRecursive_HoistsLiteralPrefix(t *testing.T) {
	tmp := t.TempDir()
	mkDirs(t, tmp, []string{
		"apps/cgw-managed-tools/test/promtps/a",
		"apps/cgw-managed-tools/test/promtps/b",
		"apps/cgw-managed-tools/test/promtps/c",
		"apps/other/unrelated",
	})
	w, body := doWorkspaceDirs(t, tmp, "", "apps%2Fcgw-managed-tools%2Ftest%2Fpromtps%2F%2A") // apps/cgw-managed-tools/test/promtps/*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := dirsList(t, body)
	want := []string{
		"apps/cgw-managed-tools/test/promtps/a",
		"apps/cgw-managed-tools/test/promtps/b",
		"apps/cgw-managed-tools/test/promtps/c",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

// TestWorkspaceDirs_NonRecursive_HoistsLiteralPrefix_WithFilter pins that
// after hoisting a multi-segment literal prefix, the trailing wildcard
// still filters base names as expected.
func TestWorkspaceDirs_NonRecursive_HoistsLiteralPrefix_WithFilter(t *testing.T) {
	tmp := t.TempDir()
	mkDirs(t, tmp, []string{
		"apps/foo/env-a",
		"apps/foo/env-b",
		"apps/foo/notes",
	})
	w, body := doWorkspaceDirs(t, tmp, "", "apps%2Ffoo%2Fenv-%2A") // apps/foo/env-*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := dirsList(t, body)
	want := []string{"apps/foo/env-a", "apps/foo/env-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dirs = %v, want %v (notes excluded)", got, want)
	}
}

// TestWorkspaceDirs_NonRecursive_MissingLiteralPrefixEmpty pins that a
// non-recursive glob whose literal prefix does not exist on disk yields
// an empty list (fail-open contract), not a 500.
func TestWorkspaceDirs_NonRecursive_MissingLiteralPrefixEmpty(t *testing.T) {
	tmp := t.TempDir()
	w, body := doWorkspaceDirs(t, tmp, "", "apps%2Fnope%2F%2A") // apps/nope/*
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := dirsList(t, body); len(got) != 0 {
		t.Fatalf("dirs = %v, want empty", got)
	}
}
