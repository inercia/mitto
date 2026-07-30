package handlers

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/inercia/mitto/internal/pathglob"
)

// workspaceFilesMaxResults caps the number of entries returned by
// HandleWorkspaceFiles so a pathological directory (thousands of files) cannot
// balloon the response or the frontend dropdown. Files beyond the cap are
// silently omitted; the response payload does NOT indicate truncation because
// the endpoint feeds a UI dropdown, not a paginator — a directory that hits
// this cap is already too large to be usefully picked from.
const workspaceFilesMaxResults = 500

// HandleWorkspaceFiles handles GET /api/workspace-files?working_dir=&dir=&glob=.
//
// Lists regular files under working_dir/dir (non-recursive) optionally filtered
// by glob (filepath.Match, applied to the file's base name). Returned paths are
// workspace-relative (relative to working_dir), so they can be passed directly
// to the ReadFile template helper by a filename-typed prompt parameter.
//
// Path safety: dir is treated as workspace-relative. Absolute paths and paths
// that escape working_dir after Clean are rejected. Symlinks are resolved and
// re-checked for containment — a symlink inside the workspace pointing
// elsewhere cannot leak file names out. This mirrors the containment idiom in
// internal/cel/templatefuncs.go readFile().
//
// Failure modes: missing or non-directory dir yields an empty list (200 OK),
// matching the "fail-open, dialog degrades to text input" contract in
// PromptParameterDialog. Missing working_dir is a 400 (caller programming
// error). Cap: workspaceFilesMaxResults entries; extras are dropped.
func (h *Handlers) HandleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	dir := r.URL.Query().Get("dir")
	// mitto-ebb: accept repeated ?glob=… query params (list form). A file is
	// a candidate when it matches ANY entry (union semantics). Empty list =
	// no filter.
	globs := r.URL.Query()["glob"]

	// Reject absolute dir up-front — the endpoint is scoped to files INSIDE
	// working_dir. The Clean+HasPrefix check below would also catch this but
	// the explicit reject yields a clearer error at the caller.
	if dir != "" && filepath.IsAbs(dir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "dir must be workspace-relative, not absolute")
		return
	}
	// Compile-check every glob entry so a malformed pattern surfaces as a
	// 400 instead of silently matching nothing. Mirrors the parse-time guard
	// in prompts.ValidatePromptParameters. Uses doublestar so recursive
	// patterns ("**") are accepted here and honored below.
	for _, g := range globs {
		if g == "" {
			writeErrorJSON(w, http.StatusBadRequest, "", "glob entries must not be empty")
			return
		}
		if !doublestar.ValidatePattern(g) {
			writeErrorJSON(w, http.StatusBadRequest, "", "invalid glob")
			return
		}
	}

	// Containment check: joined must be inside working_dir after Clean, and
	// the symlink-resolved path must also be inside the symlink-resolved
	// working_dir (mirrors internal/cel/templatefuncs.go readFile).
	cleanRoot := filepath.Clean(workingDir)
	joined := filepath.Join(cleanRoot, dir)
	cleanJoined := filepath.Clean(joined)
	if cleanJoined != cleanRoot && !strings.HasPrefix(cleanJoined, cleanRoot+string(filepath.Separator)) {
		writeJSONOK(w, map[string]interface{}{"files": []string{}})
		return
	}

	// Resolve symlinks and re-check containment. When the target does not
	// exist (or the resolve fails), degrade to an empty listing — the dialog
	// falls back to a text input in that case.
	resolved, err := filepath.EvalSymlinks(cleanJoined)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"files": []string{}})
		return
	}
	resolvedRoot, rerr := filepath.EvalSymlinks(cleanRoot)
	if rerr != nil {
		resolvedRoot = cleanRoot
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		writeJSONOK(w, map[string]interface{}{"files": []string{}})
		return
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		writeJSONOK(w, map[string]interface{}{"files": []string{}})
		return
	}

	if pathglob.PatternsContainDoublestar(globs) {
		// Anchor the walk only when every pattern shares the same non-empty
		// literal prefix; otherwise walk from the resolved root and let
		// PathMatch handle the divergent patterns from there.
		prefix := pathglob.CommonLiteralPrefix(globs)
		walkRoot := resolved
		if prefix != "" {
			joined := filepath.Join(resolved, prefix)
			cleaned := filepath.Clean(joined)
			if cleaned != resolved && !strings.HasPrefix(cleaned, resolved+string(filepath.Separator)) {
				writeJSONOK(w, map[string]interface{}{"files": []string{}})
				return
			}
			if pi, perr := os.Stat(cleaned); perr != nil || !pi.IsDir() {
				writeJSONOK(w, map[string]interface{}{"files": []string{}})
				return
			}
			walkRoot = cleaned
		}
		patterns := make([]string, len(globs))
		for i, g := range globs {
			if prefix != "" {
				patterns[i] = strings.TrimPrefix(g, prefix+"/")
			} else {
				patterns[i] = g
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		res := pathglob.WalkMatch(pathglob.WalkMatchOpts{
			Ctx:        ctx,
			Root:       walkRoot,
			Patterns:   patterns,
			MaxResults: workspaceFilesMaxResults,
			MaxVisited: pathglob.WalkMatchMaxVisited,
			WantFiles:  true,
		})
		files := make([]string, 0, len(res.Matches))
		for _, m := range res.Matches {
			abs := filepath.Join(walkRoot, filepath.FromSlash(m))
			rel, rerr := filepath.Rel(resolvedRoot, abs)
			if rerr != nil {
				continue
			}
			files = append(files, filepath.ToSlash(rel))
		}
		sort.Strings(files)
		if h.deps.Logger != nil {
			if res.Truncated {
				h.deps.Logger.Debug("workspace-files listed",
					"working_dir", workingDir, "dir", dir, "glob", globs,
					"count", len(files), "truncated", true, "reason", res.Reason)
			} else {
				h.deps.Logger.Debug("workspace-files listed",
					"working_dir", workingDir, "dir", dir, "glob", globs, "count", len(files))
			}
		}
		writeJSONOK(w, map[string]interface{}{"files": files})
		return
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"files": []string{}})
		return
	}

	// Filter to regular files (by mode; a symlink pointing at a regular file
	// is not included — dir is meant to be a curated flat set of source files,
	// and symlinks are not covered by the containment guarantees applied to
	// dir itself). Globs are applied to the base name; a candidate wins if
	// ANY listed pattern matches (union semantics). Dedup is trivially free
	// here because os.ReadDir yields unique base names in a single pass.
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(files) >= workspaceFilesMaxResults {
			break
		}
		name := e.Name()
		if e.IsDir() {
			continue
		}
		fi, ferr := e.Info()
		if ferr != nil || !fi.Mode().IsRegular() {
			continue
		}
		if len(globs) > 0 {
			matched := false
			for _, g := range globs {
				ok, _ := doublestar.PathMatch(g, name)
				if ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// Return paths relative to working_dir so ReadFile can consume them
		// directly. Uses resolvedRoot as the base — entries come from resolved
		// (which may be a symlink-expanded path on macOS: /var → /private/var),
		// so anchoring off resolvedRoot yields a clean relative path.
		rel, rerr := filepath.Rel(resolvedRoot, filepath.Join(resolved, name))
		if rerr != nil {
			continue
		}
		files = append(files, filepath.ToSlash(rel))
	}
	sort.Strings(files)

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("workspace-files listed",
			"working_dir", workingDir, "dir", dir, "glob", globs, "count", len(files))
	}
	writeJSONOK(w, map[string]interface{}{"files": files})
}
