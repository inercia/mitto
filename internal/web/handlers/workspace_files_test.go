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
