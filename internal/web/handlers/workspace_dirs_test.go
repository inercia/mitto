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
