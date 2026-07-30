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

// workspaceDirsMaxResults caps the number of entries returned by
// HandleWorkspaceDirs so a pathological directory (thousands of sub-directories)
// cannot balloon the response or the frontend dropdown. Entries beyond the cap
// are silently omitted; the response payload does NOT indicate truncation
// because the endpoint feeds a UI dropdown, not a paginator.
const workspaceDirsMaxResults = 500

// HandleWorkspaceDirs handles GET /api/workspace-dirs?working_dir=&dir=&glob=.
//
// Lists immediate sub-directories under working_dir/dir (non-recursive)
// optionally filtered by glob (filepath.Match, applied to the sub-directory's
// base name). Returned paths are workspace-relative (relative to working_dir),
// suitable for a "dirname"-typed prompt parameter that feeds template helpers
// or is joined with a filename to build a ReadFile argument.
//
// Path safety: mirrors HandleWorkspaceFiles. dir is treated as
// workspace-relative. Absolute paths and paths that escape working_dir after
// Clean are rejected. Symlinks are resolved and re-checked for containment.
//
// Hidden directories (leading ".") are excluded by default so the picker does
// not surface .git, .mitto, node_modules-style dotfolders, etc. This matches
// the ergonomic default of the filename picker's typical use (curated author
// content, not repo internals).
//
// Failure modes: missing or non-directory dir yields an empty list (200 OK),
// matching the "fail-open, dialog degrades to text input" contract in
// PromptParameterDialog. Missing working_dir is a 400. Cap:
// workspaceDirsMaxResults entries; extras are dropped.
func (h *Handlers) HandleWorkspaceDirs(w http.ResponseWriter, r *http.Request) {
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
	// mitto-ebb: accept repeated ?glob=… query params (list form). A
	// sub-directory is a candidate when it matches ANY entry (union
	// semantics). Empty list = no filter.
	globs := r.URL.Query()["glob"]

	if dir != "" && filepath.IsAbs(dir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "dir must be workspace-relative, not absolute")
		return
	}
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

	// Containment check: joined must be inside working_dir after Clean.
	cleanRoot := filepath.Clean(workingDir)
	joined := filepath.Join(cleanRoot, dir)
	cleanJoined := filepath.Clean(joined)
	if cleanJoined != cleanRoot && !strings.HasPrefix(cleanJoined, cleanRoot+string(filepath.Separator)) {
		writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
		return
	}

	// Resolve symlinks and re-check containment. Fail-open to empty listing.
	resolved, err := filepath.EvalSymlinks(cleanJoined)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
		return
	}
	resolvedRoot, rerr := filepath.EvalSymlinks(cleanRoot)
	if rerr != nil {
		resolvedRoot = cleanRoot
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
		return
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
		return
	}

	if pathglob.PatternsContainDoublestar(globs) {
		// Anchor the walk only when every pattern shares the same non-empty
		// literal prefix; otherwise walk from the resolved root.
		prefix := pathglob.CommonLiteralPrefix(globs)
		walkRoot := resolved
		if prefix != "" {
			joined := filepath.Join(resolved, prefix)
			cleaned := filepath.Clean(joined)
			if cleaned != resolved && !strings.HasPrefix(cleaned, resolved+string(filepath.Separator)) {
				writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
				return
			}
			if pi, perr := os.Stat(cleaned); perr != nil || !pi.IsDir() {
				writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
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
			MaxResults: workspaceDirsMaxResults,
			MaxVisited: pathglob.WalkMatchMaxVisited,
			WantFiles:  false,
		})
		dirs := make([]string, 0, len(res.Matches))
		for _, m := range res.Matches {
			abs := filepath.Join(walkRoot, filepath.FromSlash(m))
			rel, rerr := filepath.Rel(resolvedRoot, abs)
			if rerr != nil {
				continue
			}
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		sort.Strings(dirs)
		if h.deps.Logger != nil {
			if res.Truncated {
				h.deps.Logger.Debug("workspace-dirs listed",
					"working_dir", workingDir, "dir", dir, "glob", globs,
					"count", len(dirs), "truncated", true, "reason", res.Reason)
			} else {
				h.deps.Logger.Debug("workspace-dirs listed",
					"working_dir", workingDir, "dir", dir, "glob", globs, "count", len(dirs))
			}
		}
		writeJSONOK(w, map[string]interface{}{"dirs": dirs})
		return
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		writeJSONOK(w, map[string]interface{}{"dirs": []string{}})
		return
	}

	// Filter to real sub-directories (symlink-to-dir is skipped — mirrors the
	// filename handler's regular-file-only stance so a symlink under dir cannot
	// leak paths outside the workspace via the dropdown). A sub-directory
	// wins when ANY listed glob matches its base name (union semantics).
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(dirs) >= workspaceDirsMaxResults {
			break
		}
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		// Skip hidden directories by default (leading ".").
		if strings.HasPrefix(name, ".") {
			if len(globs) == 0 {
				continue
			}
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
		rel, rerr := filepath.Rel(resolvedRoot, filepath.Join(resolved, name))
		if rerr != nil {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	sort.Strings(dirs)

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("workspace-dirs listed",
			"working_dir", workingDir, "dir", dir, "glob", globs, "count", len(dirs))
	}
	writeJSONOK(w, map[string]interface{}{"dirs": dirs})
}
