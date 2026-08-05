package cel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/inercia/mitto/internal/pathglob"
)

// gitCmdTimeout bounds how long a git subprocess invocation is allowed to run
// before it is killed, so template/CEL evaluation never hangs on a stalled repo.
const gitCmdTimeout = 5 * time.Second

// promptTextMaxDepth caps recursion for PromptTextWithArgs sub-renders so a
// self-referential body (e.g. {{ PromptTextWithArgs "self" .Args }}) cannot
// crash the process. Beyond this depth the sub-render errors out; the outer
// template execution aborts with the wrapped error, matching PromptText's
// fail-closed policy.
const promptTextMaxDepth = 3

// bdCmdTimeout bounds how long a bd (beads) subprocess invocation is allowed to
// run before it is killed. Mirrors gitCmdTimeout for the git helpers.
const bdCmdTimeout = 5 * time.Second

// beadsCacheTTL bounds how long a BeadsCount/HasBeads/BeadHasLabels/BeadIsOpen
// /BeadMetadata result is memoised, to avoid re-exec on rapid menu re-opens.
// Raised from the original 5s to 30s (mitto-z0t D4): safe because external
// mutations invalidate the affected folder's entries immediately via the
// .beads/ fsnotify watcher (see InvalidateBeadsCache, wired from
// internal/web Server.OnBeadsChanged), so this is now a bounded backstop for
// folders the watcher does not cover rather than the primary freshness
// mechanism.
const beadsCacheTTL = 30 * time.Second

// beadsFailCacheTTL bounds how long a FAILED bd invocation (missing bd,
// non-zero exit, unparseable JSON) is memoised (mitto-z0t D3). Before this,
// every fail-open path returned without caching anything, so a broken or
// slow bd was the hottest possible path — it re-forked on every single
// evaluation. Kept short (well under beadsCacheTTL) so a transient failure
// self-heals quickly once bd starts working again.
const beadsFailCacheTTL = 2 * time.Second

// beadsCountFailOpen is the sentinel returned by beadsCount on ANY error (bd
// missing, timeout, non-zero exit, unparseable JSON). It is a positive value
// so HasBeads(...) returns true and the prompt is NEVER wrongly hidden —
// consistent with the CEL fail-open policy. A legitimate empty result from bd
// (`[]`) is NOT an error and returns 0.
const beadsCountFailOpen = 1

// beadsCache memoises beadsCount results keyed by folder\x00labels\x00statuses.
// Simple sync.Mutex-guarded map with a timestamped entry per key. Failed
// lookups are cached too (entry.failed=true) under the shorter
// beadsFailCacheTTL — see beadsCountLookup/beadsCacheStore.
var (
	beadsCacheMu sync.Mutex
	beadsCache   = map[string]beadsCacheEntry{}

	// beadsListSF collapses concurrent `bd list` execs for the same
	// (folder, labels, statuses) key into one fork (mitto-z0t D2): when
	// beadsCacheTTL has just expired, several concurrent enabledWhen
	// evaluations (e.g. overlapping /api/workspace-prompts requests) would
	// otherwise all miss together and fork in parallel.
	beadsListSF singleflight.Group
)

type beadsCacheEntry struct {
	count  int
	at     time.Time
	failed bool // true when this entry memoises a fail-open result (beadsFailCacheTTL applies)
}

// beadsShowCacheEntry is a single cached `bd show <id> --json` snapshot,
// positive (ok=true) or negative (ok=false, on any error), keyed by
// folder\x00id. Introduced by mitto-z0t D1 to replace the earlier scheme
// where beadHasLabels/beadIsOpen/beadMetadata each cached only their own
// DERIVED answer while sharing an uncached showBead — so evaluating all
// three gates on the same bead forked `bd show` three times. Now all three
// derive from this ONE shared snapshot per (folder, id).
type beadsShowCacheEntry struct {
	bead bdBead
	ok   bool
	at   time.Time
}

var (
	beadsShowCacheMu sync.Mutex
	beadsShowCache   = map[string]beadsShowCacheEntry{}

	// beadsShowSF collapses concurrent `bd show` execs for the same
	// (folder, id) key into one fork (mitto-z0t D2). Mirrors beadsListSF.
	beadsShowSF singleflight.Group
)

// globCacheTTL bounds how long a fileExists/dirExists glob-mode result is
// memoised per (folder, pattern, wantFiles) tuple, to avoid repeat walks on
// rapid menu re-opens. Raised from the original 5s to 30s (mitto-ayl): at the
// observed /mitto/api/workspace-prompts request rate (~1 per 6.4s) a 5s TTL
// expires between essentially every request, so the memo never hit and every
// evaluation re-walked the workspace. Staleness is now PARTIALLY
// watcher-bounded (mitto-ayl.1): InvalidateGlobCache / InvalidateAllGlobCaches
// are driven from the .beads/ and prompts fsnotify watchers, but no watcher
// covers arbitrary glob patterns over the whole workspace, so this TTL remains
// the backstop for changes neither watcher observes; 30s mirrors beadsCacheTTL
// and is short enough that a newly-added/removed file affects prompt
// visibility within one window.
const globCacheTTL = 30 * time.Second

// globWalkTimeout bounds a single fileExists/dirExists glob-mode walk. On
// deadline the walker returns Truncated=true reason="deadline" and the caller
// fails open (returns true) so a prompt is never wrongly hidden by a slow
// filesystem — consistent with the CEL fail-open policy.
const globWalkTimeout = 2 * time.Second

// globCache memoises fileExists/dirExists glob-mode results for globCacheTTL,
// keyed by folder\x00pattern\x00wantFiles. Simple sync.Mutex-guarded map with
// a timestamped bool per key. Non-glob (literal) calls bypass this cache and
// keep the current O(1) os.Stat path.
var (
	globCacheMu sync.Mutex
	globCache   = map[string]globCacheEntry{}

	// globSF collapses concurrent existsByGlob walks for the same
	// (folder, pattern, wantFiles) key into one walk (mitto-ayl): several
	// prompts can share an identical FileExists/DirExists glob gate (e.g. the
	// skills prompts all gate on FileExists("**/SKILL.md")), so when the
	// cache entry for that key is cold, an overlapping batch of enabledWhen
	// evaluations (one /mitto/api/workspace-prompts request touches many
	// prompts; concurrent requests multiply it) would otherwise start one
	// full workspace walk per evaluation instead of sharing one. Mirrors
	// beadsListSF/beadsShowSF.
	globSF singleflight.Group
)

type globCacheEntry struct {
	value bool
	at    time.Time
}

// globCacheLookup returns the memoised value for key if present and not
// expired (globCacheTTL). Mirrors beadsCacheLookup.
func globCacheLookup(key string) (bool, bool) {
	globCacheMu.Lock()
	defer globCacheMu.Unlock()
	e, ok := globCache[key]
	if !ok || time.Since(e.at) >= globCacheTTL {
		return false, false
	}
	return e.value, true
}

// globCacheStore memoises value for key. Mirrors beadsCacheStore.
func globCacheStore(key string, value bool) {
	globCacheMu.Lock()
	globCache[key] = globCacheEntry{value: value, at: time.Now()}
	globCacheMu.Unlock()
}

// containsGlobMeta reports whether pattern uses any glob metacharacter that
// switches fileExists/dirExists to walker mode: '*', '?', '[', '{'. Matches
// pathglob.LiteralPrefix's segment-splitter so the two agree on what counts
// as "has a wildcard".
func containsGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[{")
}

// resolveGlobRoot returns the (walkRoot, patternRel, ok) triple for a glob
// pattern rooted at folder. It mirrors the handler-side literal-prefix
// anchoring: when pattern has a non-empty literal prefix, walk from
// folder/prefix and strip the prefix from the pattern; otherwise walk from
// folder verbatim.
//
// ok=false rejects the call (fileExists/dirExists then return false without
// walking): empty folder or pattern, absolute pattern, ".." escape after
// Clean, or the anchored root is not an existing directory.
func resolveGlobRoot(folder, pattern string) (walkRoot, patternRel string, ok bool) {
	if folder == "" || pattern == "" {
		return "", "", false
	}
	// Reject absolute paths (mirrors readFile / statResolved policy — CEL
	// glob helpers are workspace-scoped by construction).
	if filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "/") {
		return "", "", false
	}
	cleanFolder := filepath.Clean(folder)
	prefix := pathglob.LiteralPrefix(pattern)
	walkRoot = cleanFolder
	patternRel = pattern
	if prefix != "" {
		joined := filepath.Join(cleanFolder, prefix)
		cleaned := filepath.Clean(joined)
		// Containment check: after Clean, the anchored root must sit inside
		// folder — a "../" escape in prefix must be rejected.
		if cleaned != cleanFolder && !strings.HasPrefix(cleaned, cleanFolder+string(filepath.Separator)) {
			return "", "", false
		}
		pi, perr := os.Stat(cleaned)
		if perr != nil || !pi.IsDir() {
			return "", "", false
		}
		walkRoot = cleaned
		patternRel = strings.TrimPrefix(pattern, prefix+"/")
	}
	return walkRoot, patternRel, true
}

// walkMatchFn indirects pathglob.WalkMatch so tests can wrap it (count calls,
// inject a delay) to pin existsByGlob's globSF singleflight collapse without
// needing a real large/slow filesystem. Always pathglob.WalkMatch in
// production; mirrors the existing test-seam convention of
// pathglob.WalkMatchMaxVisited being a var for the same reason.
var walkMatchFn = pathglob.WalkMatch

// existsByGlob reports whether ANY entry under folder matches pattern using
// pathglob.WalkMatch with maxResults=1. wantFiles gates the entry-type filter
// (regular files vs. directories), matching the fileExists/dirExists split.
//
// Fail-open on any walker truncation (deadline, visited_cap, results_cap):
// results_cap with matches>0 is a hit; results_cap with matches==0 is
// unreachable (the walker only reports results_cap after appending a match);
// deadline/visited_cap with matches==0 fails open (returns true) so a prompt
// is never wrongly hidden by a slow or huge filesystem.
//
// Results are memoised for globCacheTTL per (folder, pattern, wantFiles)
// tuple. Concurrent misses on the same key collapse to a single walk via
// globSF (mitto-ayl), mirroring beadsCount/showBead.
func existsByGlob(folder, pattern string, wantFiles bool) bool {
	cacheKey := folder + "\x00" + pattern + "\x00"
	if wantFiles {
		cacheKey += "f"
	} else {
		cacheKey += "d"
	}
	if v, ok := globCacheLookup(cacheKey); ok {
		return v
	}

	v, _, _ := globSF.Do(cacheKey, func() (interface{}, error) {
		// Re-probe inside the flight: a sibling call may have just populated
		// the entry while this goroutine waited to be scheduled/acquire the
		// singleflight slot, in which case skip the walk entirely.
		if v, ok := globCacheLookup(cacheKey); ok {
			return v, nil
		}

		walkRoot, patternRel, ok := resolveGlobRoot(folder, pattern)
		if !ok {
			globCacheStore(cacheKey, false)
			return false, nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), globWalkTimeout)
		defer cancel()
		res := walkMatchFn(pathglob.WalkMatchOpts{
			Ctx:        ctx,
			Root:       walkRoot,
			Patterns:   []string{patternRel},
			MaxResults: 1,
			MaxVisited: pathglob.WalkMatchMaxVisited,
			WantFiles:  wantFiles,
		})
		var result bool
		if len(res.Matches) > 0 {
			result = true
		} else if res.Truncated && (res.Reason == "deadline" || res.Reason == "visited_cap") {
			// Fail-open: a slow/huge filesystem must not wrongly hide a prompt.
			result = true
			slog.Debug("cel glob exists fail-open",
				"pattern", pattern, "folder", folder, "wantFiles", wantFiles,
				"reason", res.Reason)
		} else {
			result = false
		}

		globCacheStore(cacheKey, result)
		return result, nil
	})
	return v.(bool)
}

// =============================================================================
// Pure-Go condition helpers — single source of truth shared by CEL bindings
// (cel_evaluator.go) and the template FuncMap (BuildTemplateFuncMap below).
// Changing logic here propagates identically to both callers.
// =============================================================================

// resolveServerName extracts the owning server name from a tool pattern or
// concrete tool name: the token before the first underscore (e.g. "jira_*"
// or "jira_create_issue" -> "jira"). A pattern/name with no underscore
// resolves to itself.
func resolveServerName(pattern string) string {
	if idx := strings.IndexByte(pattern, '_'); idx > 0 {
		return pattern[:idx]
	}
	return pattern
}

// resolveServerEntry returns pattern's owning server entry from servers,
// falling back to AllServersToolKey when the specific server isn't present
// (used by callers without real per-server identity, e.g. processors — see
// NewProcessorToolsContext). ok is false when neither is found.
func resolveServerEntry(servers map[string]ServerToolInfo, pattern string) (ServerToolInfo, bool) {
	if info, ok := servers[resolveServerName(pattern)]; ok {
		return info, true
	}
	info, ok := servers[AllServersToolKey]
	return info, ok
}

// hasPattern reports whether pattern is satisfied under the per-server MCP
// tool availability rule (docs/devel/mcp-tool-discovery.md, Q3.2/Q4.1):
// fail-open (true) unless pattern's owning server is known AND Reachable, in
// which case matching is name-based against that server's own tool names.
// Single source of truth shared with the CEL path (cel_evaluator.go).
func hasPattern(servers map[string]ServerToolInfo, pattern string) bool {
	info, ok := resolveServerEntry(servers, pattern)
	if !ok || info.State != ServerToolStateReachable {
		return true
	}
	for _, name := range info.Names {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

// hasAllPatterns reports whether every pattern is satisfied (see hasPattern).
func hasAllPatterns(servers map[string]ServerToolInfo, patterns []string) bool {
	for _, pattern := range patterns {
		if !hasPattern(servers, pattern) {
			return false
		}
	}
	return true
}

// hasAnyPattern reports whether any pattern is satisfied (see hasPattern).
func hasAnyPattern(servers map[string]ServerToolInfo, patterns []string) bool {
	for _, pattern := range patterns {
		if hasPattern(servers, pattern) {
			return true
		}
	}
	return false
}

// hasModelTag reports whether tag is present in tags (case-insensitive membership).
// Single source of truth shared by the Model(tag) template func and the Session.HasModelTag
// CEL macro. Returns false for an empty tag set, so Model("x") is false when the current
// model is unknown or carries no matching profile tag (never errors the render).
func hasModelTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// matchesServerType reports whether acpType case-insensitively matches any of serverTypes.
// Fail-open: returns true when acpName is "" (no ACP server active).
func matchesServerType(acpName, acpType string, serverTypes []string) bool {
	if acpName == "" {
		return true
	}
	for _, st := range serverTypes {
		if strings.EqualFold(st, acpType) {
			return true
		}
	}
	return false
}

// commandExists reports whether name is found in the system PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// fileExists reports whether path exists and is a regular file. Relative
// paths are resolved against folder (workspace root).
//
// When path contains any glob metacharacter ('*', '?', '[', '{'), fileExists
// switches to a bounded workspace walk (pathglob.WalkMatch with maxResults=1)
// and reports whether ANY regular file matches. The walk is capped by
// globWalkTimeout, visited_cap and results_cap; fail-open on cap/deadline
// (returns true) so a slow or huge filesystem never wrongly hides a prompt.
// Absolute globs and "../" escapes are rejected (return false).
//
// The literal (no-metachar) path is unchanged: O(1) os.Stat via statResolved.
// Glob-mode results are memoised for globCacheTTL per (folder, pattern, wantFiles).
func fileExists(folder, path string) bool {
	if containsGlobMeta(path) {
		return existsByGlob(folder, path, true)
	}
	info, ok := statResolved(folder, path)
	return ok && !info.IsDir()
}

// dirExists reports whether path exists and is a directory. Relative paths
// are resolved against folder (workspace root).
//
// When path contains any glob metacharacter ('*', '?', '[', '{'), dirExists
// switches to a bounded workspace walk (pathglob.WalkMatch with maxResults=1)
// and reports whether ANY directory matches. Same caps, fail-open policy and
// caching as fileExists.
func dirExists(folder, path string) bool {
	if containsGlobMeta(path) {
		return existsByGlob(folder, path, false)
	}
	info, ok := statResolved(folder, path)
	return ok && info.IsDir()
}

// readFileMaxBytes caps the byte size of a single ReadFile template invocation
// so a runaway or malicious fragment cannot balloon the rendered prompt. 256 KB
// is well above the largest support-channel fragment observed to date (~6 KB)
// while still bounded enough to keep the render deterministic. Content beyond
// this cap is silently truncated at a byte boundary; a UTF-8 rune split would
// be visible in the rendered prompt but not corrupt subsequent template parsing.
const readFileMaxBytes = 256 * 1024

// readFile reads a workspace-relative regular file and returns its contents
// as a string. It is fail-open by design: empty string on any error (missing
// path, symlink to elsewhere, directory, over-size, permission denied) so
// callers pair it with FileExists to distinguish "missing" from "empty" —
// mirroring the FileExists / DirExists idiom used elsewhere in the FuncMap.
//
// Path safety: absolute paths are rejected (return ""); relative paths are
// resolved against folder and must remain inside it after Clean — a "../"
// escape returns "". Symlinks that resolve outside folder are also rejected.
//
// Size cap: readFileMaxBytes (256 KB). Files larger than the cap have their
// content truncated to the cap; callers that need to detect truncation
// should stat the file separately.
func readFile(folder, path string) string {
	if path == "" || folder == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return ""
	}
	joined := filepath.Join(folder, path)
	// Clean folder for the containment check so a trailing slash or "./" in
	// either operand does not spuriously fail the HasPrefix comparison.
	cleanFolder := filepath.Clean(folder)
	cleanJoined := filepath.Clean(joined)
	if cleanJoined != cleanFolder && !strings.HasPrefix(cleanJoined, cleanFolder+string(filepath.Separator)) {
		return ""
	}
	// Resolve symlinks and re-check containment so a symlink inside folder
	// pointing at /etc/passwd (or similar) cannot leak out.
	resolved, err := filepath.EvalSymlinks(cleanJoined)
	if err != nil {
		return ""
	}
	resolvedFolder, ferr := filepath.EvalSymlinks(cleanFolder)
	if ferr != nil {
		resolvedFolder = cleanFolder
	}
	if resolved != resolvedFolder && !strings.HasPrefix(resolved, resolvedFolder+string(filepath.Separator)) {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(resolved)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, readFileMaxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return ""
	}
	return string(buf[:n])
}

// readTemplate reads a workspace-relative file (same path-safety and size-cap
// semantics as readFile — fail-open, returns "" on missing / directory /
// oversize / path-escape / symlink-escape) and then renders its contents as a
// Go text/template against ctx with funcs attached. This is the variable-
// expanding counterpart to readFile: authors can include a file whose body
// references {{ .Args.X }} / {{ .Session.* }} / any helper in the FuncMap,
// while ReadFile stays the verbatim path.
//
// Error policy differs by phase (matches PromptTextWithArgs / mitto-47y.1):
//   - Read step is **fail-open**: any read-side failure (empty path, absolute
//     path, ".." escape, symlink-out, directory, oversize, missing) returns
//     ("", nil). Callers pair with FileExists to distinguish absent from empty.
//   - Render step is **fail-closed**: parse error, execution error, unknown
//     function, and depth-exceeded all return a non-nil error which propagates
//     through the outer template's fail-closed policy (docs/devel/prompt-
//     templates.md §7) and aborts the send.
//
// Fast path: if the file body contains no `{{`, it is returned verbatim (no
// parse cost, no missingkey substitution). Files that DO contain `{{` are
// always parsed — including files whose only `{{` occurrences are inside a
// GitHub-Actions `${{ ... }}` expression, since Go text/template treats `$`
// and `{{ }}` independently. Use ReadFile for verbatim inclusion of arbitrary
// Markdown that may contain `{{`.
//
// Depth guard: reuses promptTextMaxDepth (3), incrementing the closure's
// PromptTextDepth on each nested render so a self-referential file
// (a.tmpl -> a.tmpl) or a two-file cycle (a -> b -> a) errors out with a
// "recursion depth exceeded" message rather than blowing the stack.
//
// Fragments: renderNestedPromptBody attaches the workspace fragment registry
// (via the fragmentProvider hook, preserving the internal/cel ->
// internal/prompts decoupling in mitto-b8k.3) so a file containing
// {{ template "_shared/foo" . }} resolves the same way it would in a
// top-level prompt render (mitto-twa).
func readTemplate(name, folder string, ctx *PromptEnabledContext, depth int) (string, error) {
	contents := readFile(folder, name)
	if contents == "" {
		return "", nil
	}
	if depth >= promptTextMaxDepth {
		return "", fmt.Errorf("ReadTemplate(%q): recursion depth exceeded (max %d)", name, promptTextMaxDepth)
	}
	// Shallow-copy ctx so incrementing PromptTextDepth for the nested render
	// does not mutate the parent scope (mirrors PromptTextWithArgs' inner =
	// *ctx idiom). ctx may be nil when the FuncMap was built with a nil ctx
	// (e.g. tests, menu-time enabledWhen); a nil ctx short-circuits the read
	// step above (readFile("", ...) returns ""), so we would not reach here.
	// Guard anyway for defensiveness.
	var inner PromptEnabledContext
	if ctx != nil {
		inner = *ctx
	}
	inner.PromptTextDepth = depth + 1
	innerFuncs := BuildTemplateFuncMap(&inner)
	rendered, err := renderNestedPromptBody(name, contents, &inner, innerFuncs)
	if err != nil {
		return "", fmt.Errorf("ReadTemplate(%q): %w", name, err)
	}
	return rendered, nil
}

// runGit runs `git <args...>` with the working directory set to folder (when
// non-empty), bounded by gitCmdTimeout. It returns the trimmed stdout and true
// when git exits 0. Returns ("", false) when git is unavailable, the folder is
// not a git work tree, or the command fails / exits non-zero.
func runGit(folder string, args ...string) (string, bool) {
	if !commandExists("git") {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if folder != "" {
		cmd.Dir = folder
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.Trim(string(out), "\n"), true
}

// gitStatusPorcelain returns the `git status --porcelain` lines for pathspec
// (relative to folder; whole work tree when pathspec is ""). ok is false when
// git is unavailable or folder is not a repo. An empty (non-nil) slice means a
// clean status. Each element is a raw porcelain v1 line ("XY path").
func gitStatusPorcelain(folder, pathspec string) ([]string, bool) {
	args := []string{"status", "--porcelain"}
	if pathspec != "" {
		args = append(args, "--", pathspec)
	}
	out, ok := runGit(folder, args...)
	if !ok {
		return nil, false
	}
	if out == "" {
		return []string{}, true
	}
	return strings.Split(out, "\n"), true
}

// gitRepo reports whether folder (or the given path relative to it) is inside a
// git work tree — the general gatekeeper for "is this folder using git at all".
// An empty path checks the workspace folder itself. Returns false when git is
// unavailable, the location does not exist, or it is not a git work tree.
func gitRepo(folder, path string) bool {
	dir := folder
	if path != "" {
		if filepath.IsAbs(path) {
			dir = path
		} else {
			dir = filepath.Join(folder, path)
		}
	}
	out, ok := runGit(dir, "rev-parse", "--is-inside-work-tree")
	return ok && out == "true"
}

// gitFileModified reports whether a specific tracked file has pending changes
// (staged or unstaged) relative to HEAD/index. Untracked files ("??") are NOT
// considered modified. Returns false for an empty path, outside a repo, or git
// unavailable. Relative paths are resolved against folder (workspace root).
func gitFileModified(folder, path string) bool {
	if path == "" {
		return false
	}
	lines, ok := gitStatusPorcelain(folder, path)
	if !ok {
		return false
	}
	for _, ln := range lines {
		if len(ln) < 2 {
			continue
		}
		xy := ln[:2]
		if xy == "??" {
			continue // untracked is not "modified"
		}
		if strings.TrimSpace(xy) != "" {
			return true
		}
	}
	return false
}

// gitDirModified reports whether the given directory has any pending changes,
// including untracked files (i.e. the working tree is dirty under path). An
// empty path defaults to "." (the whole workspace/work tree). Returns false
// outside a repo or when git is unavailable.
func gitDirModified(folder, path string) bool {
	if path == "" {
		path = "."
	}
	lines, ok := gitStatusPorcelain(folder, path)
	if !ok {
		return false
	}
	return len(lines) > 0
}

// gitFileTracked reports whether path is tracked by git (present in the index).
// A file whose deletion is not yet committed is still tracked. Returns false
// for an empty path, an untracked path, outside a repo, or git unavailable.
func gitFileTracked(folder, path string) bool {
	if path == "" {
		return false
	}
	_, ok := runGit(folder, "ls-files", "--error-unmatch", "--", path)
	return ok
}

// gitFileDeleted reports whether a specific file has been deleted in git — i.e. a
// tracked file removed from the working tree, whether the deletion is staged
// ("D " in the index column) or unstaged (" D" in the work-tree column).
// Returns false for an empty path, outside a repo, or git unavailable.
func gitFileDeleted(folder, path string) bool {
	if path == "" {
		return false
	}
	lines, ok := gitStatusPorcelain(folder, path)
	if !ok {
		return false
	}
	for _, ln := range lines {
		if len(ln) < 2 {
			continue
		}
		if ln[0] == 'D' || ln[1] == 'D' {
			return true
		}
	}
	return false
}

// runBd runs `bd <args...>` with the working directory set to folder (when
// non-empty), bounded by bdCmdTimeout. Returns the raw stdout bytes and true
// when bd exits 0. Returns (nil, false) when bd is unavailable, exits non-zero,
// or the command times out. Mirrors runGit.
func runBd(folder string, args ...string) ([]byte, bool) {
	if !commandExists("bd") {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), bdCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bd", args...)
	if folder != "" {
		cmd.Dir = folder
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

// beadsCacheLookup returns the memoised value for key if present and not
// expired. A failed (fail-open) entry expires after the shorter
// beadsFailCacheTTL; a successful entry after beadsCacheTTL (mitto-z0t D3).
func beadsCacheLookup(key string) (int, bool) {
	beadsCacheMu.Lock()
	defer beadsCacheMu.Unlock()
	e, ok := beadsCache[key]
	if !ok {
		return 0, false
	}
	ttl := beadsCacheTTL
	if e.failed {
		ttl = beadsFailCacheTTL
	}
	if time.Since(e.at) >= ttl {
		return 0, false
	}
	return e.count, true
}

// beadsCacheStore memoises value for key, tagged failed when it represents a
// fail-open result (see beadsCacheLookup for the TTL this implies).
func beadsCacheStore(key string, value int, failed bool) {
	beadsCacheMu.Lock()
	beadsCache[key] = beadsCacheEntry{count: value, at: time.Now(), failed: failed}
	beadsCacheMu.Unlock()
}

// beadsCount counts beads matching ALL comma-separated labels AND ANY of the
// comma-separated statuses, running `bd list -l <labels> --status <statuses>
// --all --json` in folder and parsing the resulting JSON array length.
//
// Fail-open: on ANY error (bd missing, not a beads repo, timeout, non-zero
// exit, unparseable JSON) returns beadsCountFailOpen (a positive sentinel) so
// HasBeads(...) returns true and callers gating a prompt never wrongly hide
// it — consistent with the CEL fail-open policy. A legitimate empty result
// (`[]`, exit 0) is NOT an error and returns 0. Failures are memoised too
// (beadsFailCacheTTL, mitto-z0t D3) so a broken/slow bd is not re-exec'd on
// every single evaluation.
//
// Results are memoised for beadsCacheTTL per (folder, labels, statuses) tuple
// to bound exec frequency on rapid menu re-opens. Concurrent misses on the
// same key collapse to a single exec via beadsListSF (mitto-z0t D2). Consumers
// relying on short-circuit ordering (e.g. `CommandExists("bd") &&
// DirExists(".beads") && HasBeads(...)`) still get zero exec cost when the
// cheap gates fail.
func beadsCount(folder, labels, statuses string) int {
	key := folder + "\x00" + labels + "\x00" + statuses
	if v, ok := beadsCacheLookup(key); ok {
		return v
	}

	v, _, _ := beadsListSF.Do(key, func() (interface{}, error) {
		if v, ok := beadsCacheLookup(key); ok {
			return v, nil
		}

		out, ok := runBd(folder, "list", "-l", labels, "--status", statuses, "--all", "--json")
		if !ok {
			beadsCacheStore(key, beadsCountFailOpen, true)
			return beadsCountFailOpen, nil
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			// Empty stdout is unexpected (bd emits at least `[]`); treat as error.
			beadsCacheStore(key, beadsCountFailOpen, true)
			return beadsCountFailOpen, nil
		}
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			beadsCacheStore(key, beadsCountFailOpen, true)
			return beadsCountFailOpen, nil
		}
		count := len(arr)
		beadsCacheStore(key, count, false)
		return count, nil
	})
	return v.(int)
}

// hasBeads reports whether beadsCount(folder, labels, statuses) > 0. Convenience
// wrapper sharing the same fail-open + cache semantics as beadsCount.
func hasBeads(folder, labels, statuses string) bool {
	return beadsCount(folder, labels, statuses) > 0
}

// bdBead is the minimal subset of a `bd show <id> --json` record that the
// per-issue gate helpers inspect. bd emits the full issue with many more fields;
// only labels, status and metadata matter here. Metadata is a bd-side free-form
// map (nil / absent / null all decode as nil, which is safe to index) — used
// today by BeadMetadata(id, key) to resolve per-bead render-time values such as
// the Slack channel ID on a support question.
type bdBead struct {
	Labels   []string          `json:"labels"`
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`
}

// parseBdShow parses the stdout of `bd show <id> --json`, tolerating BOTH shapes
// bd has emitted across versions: a single-element JSON ARRAY (`[{...}]`, current
// bd) and a bare JSON OBJECT (`{...}`, older bd). Returns the first record and
// true on success; (zero, false) when stdout is empty or unparseable as either
// shape.
func parseBdShow(out []byte) (bdBead, bool) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return bdBead{}, false
	}
	// Array shape first (current bd): [{...}, ...].
	if trimmed[0] == '[' {
		var arr []bdBead
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return bdBead{}, false
		}
		if len(arr) == 0 {
			return bdBead{}, false
		}
		return arr[0], true
	}
	// Bare object shape (older bd): {...}.
	var obj bdBead
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return bdBead{}, false
	}
	return obj, true
}

// showBead returns the parsed `bd show <id> --json` record for (folder, id),
// backed by a shared snapshot cache (mitto-z0t D1): beadHasLabels, beadIsOpen
// and beadMetadata all call this instead of each forking their own `bd show`,
// so evaluating all three gates on the same bead performs AT MOST ONE exec
// per TTL window instead of three. Concurrent misses on the same (folder, id)
// collapse to a single exec via beadsShowSF (mitto-z0t D2). A failed lookup
// (bd missing, non-zero exit, unparseable JSON) is cached too, under the
// shorter beadsFailCacheTTL (mitto-z0t D3), and (bdBead{}, false) is returned
// so callers apply their own fail-open policy.
func showBead(folder, id string) (bdBead, bool) {
	key := folder + "\x00" + id
	if e, ok := beadsShowCacheLookup(key); ok {
		return e.bead, e.ok
	}

	v, _, _ := beadsShowSF.Do(key, func() (interface{}, error) {
		if e, ok := beadsShowCacheLookup(key); ok {
			return e, nil
		}

		out, ok := runBd(folder, "show", id, "--json")
		if !ok {
			e := beadsShowCacheEntry{ok: false, at: time.Now()}
			beadsShowCacheStore(key, e)
			return e, nil
		}
		bead, ok := parseBdShow(out)
		e := beadsShowCacheEntry{bead: bead, ok: ok, at: time.Now()}
		beadsShowCacheStore(key, e)
		return e, nil
	})
	e := v.(beadsShowCacheEntry)
	return e.bead, e.ok
}

// beadsShowCacheLookup returns the memoised showBead snapshot for key if
// present and not expired. A failed (ok=false) entry expires after the
// shorter beadsFailCacheTTL; a successful entry after beadsCacheTTL.
func beadsShowCacheLookup(key string) (beadsShowCacheEntry, bool) {
	beadsShowCacheMu.Lock()
	defer beadsShowCacheMu.Unlock()
	e, found := beadsShowCache[key]
	if !found {
		return beadsShowCacheEntry{}, false
	}
	ttl := beadsCacheTTL
	if !e.ok {
		ttl = beadsFailCacheTTL
	}
	if time.Since(e.at) >= ttl {
		return beadsShowCacheEntry{}, false
	}
	return e, true
}

// beadsShowCacheStore memoises entry for key.
func beadsShowCacheStore(key string, entry beadsShowCacheEntry) {
	beadsShowCacheMu.Lock()
	beadsShowCache[key] = entry
	beadsShowCacheMu.Unlock()
}

// beadHasLabels reports whether the single bead identified by id carries ALL of
// the comma-separated labels, running `bd show <id> --json` in folder and
// inspecting the returned `labels` array. Unlike hasBeads (which aggregates
// across the whole workspace), this scopes to ONE specific issue — used to gate
// a prompt on the CURRENT conversation's linked bead (Session.BeadsIssue) in
// the conversation/prompts menus, where Item.* labels are unavailable.
//
// Fail-open: on ANY error (bd missing, empty id, not a beads repo, timeout,
// non-zero exit, unparseable JSON) returns true so a gate using it never
// wrongly hides a prompt — consistent with the CEL fail-open policy. An empty
// labels list is treated as "no requirement" and returns true.
//
// Derives from the shared showBead(folder, id) snapshot (mitto-z0t D1), which
// is itself cached/singleflighted, so evaluating this alongside beadIsOpen
// and/or beadMetadata on the same bead performs at most one `bd show` exec.
func beadHasLabels(folder, id, labels string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true // fail-open: no id to check
	}
	want := splitCSV(labels)
	if len(want) == 0 {
		return true // no requirement
	}

	bead, ok := showBead(folder, id)
	if !ok {
		return true // fail-open
	}

	have := make(map[string]struct{}, len(bead.Labels))
	for _, l := range bead.Labels {
		have[l] = struct{}{}
	}
	for _, w := range want {
		if _, ok := have[w]; !ok {
			return false
		}
	}
	return true
}

// beadIsOpen reports whether the single bead identified by id is NOT closed
// (status != "closed"), running `bd show <id> --json` in folder. Scopes to ONE
// specific issue — used alongside beadHasLabels to gate prompts on the CURRENT
// conversation's linked bead in the conversation/prompts menus, mirroring the
// beadsIssues menu's `Item.Status != "closed"` guard.
//
// Fail-open: on ANY error (bd missing, empty id, timeout, non-zero exit,
// unparseable JSON) returns true so a gate using it never wrongly hides a
// prompt — consistent with the CEL fail-open policy.
//
// Derives from the shared showBead(folder, id) snapshot (mitto-z0t D1); see
// beadHasLabels for the shared-exec rationale.
func beadIsOpen(folder, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true // fail-open: no id to check
	}

	bead, ok := showBead(folder, id)
	if !ok {
		return true // fail-open
	}
	return bead.Status != "closed"
}

// beadMetadata returns the string value of bead <id>'s metadata[key], via
// `bd show <id> --json`. Companion to beadHasLabels/beadIsOpen — scopes to ONE
// specific issue and is used at RENDER time (not enabledWhen) to inline a
// per-bead value such as the Slack channel ID from a support question's
// metadata.
//
// Fail-open (returns ""): empty id, missing bd, timeout, non-zero exit,
// unparseable JSON, absent bead, absent/null metadata field, or absent key. A
// nil bead.Metadata indexes safely to "".
//
// Derives from the shared showBead(folder, id) snapshot (mitto-z0t D1); see
// beadHasLabels for the shared-exec rationale. No separate string-valued
// cache is needed since the snapshot already holds the parsed Metadata map.
func beadMetadata(folder, id, key string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "" // fail-open: no id to check
	}

	bead, ok := showBead(folder, id)
	if !ok {
		return "" // fail-open
	}
	return bead.Metadata[key] // nil map indexes to ""
}

// InvalidateBeadsCache drops every memoised beadsCount/showBead cache entry
// for folder (mitto-z0t D5), so the next BeadsCount/HasBeads/BeadHasLabels/
// BeadIsOpen/BeadMetadata call for that folder re-execs bd instead of
// returning a stale in-memory value. Intended to be called from
// (*web.Server).OnBeadsChanged for each event.WorkingDirs entry when the
// .beads/ fsnotify watcher reports an external mutation, which is what lets
// beadsCacheTTL be raised safely (staleness is now watcher-bounded, not
// TTL-bounded). A linear scan over the (typically tiny) cache maps is
// acceptable here: entries are few, per-folder invalidation is infrequent
// relative to lookups, and this keeps the key scheme simple (folder is a
// substring of each composite key, not a separate index).
func InvalidateBeadsCache(folder string) {
	if folder == "" {
		return
	}
	prefix := folder + "\x00"

	beadsCacheMu.Lock()
	for k := range beadsCache {
		if k == folder || strings.HasPrefix(k, prefix) {
			delete(beadsCache, k)
		}
	}
	beadsCacheMu.Unlock()

	beadsShowCacheMu.Lock()
	for k := range beadsShowCache {
		if strings.HasPrefix(k, prefix) {
			delete(beadsShowCache, k)
		}
	}
	beadsShowCacheMu.Unlock()
}

// InvalidateAllBeadsCaches drops every memoised beadsCount/showBead cache
// entry across all folders (mitto-z0t D5). Companion to InvalidateBeadsCache
// for callers that don't have a specific folder scope (e.g. tests, or a
// global cache-clear).
func InvalidateAllBeadsCaches() {
	beadsCacheMu.Lock()
	beadsCache = map[string]beadsCacheEntry{}
	beadsCacheMu.Unlock()

	beadsShowCacheMu.Lock()
	beadsShowCache = map[string]beadsShowCacheEntry{}
	beadsShowCacheMu.Unlock()
}

// InvalidateGlobCache drops every memoised fileExists/dirExists glob-mode
// cache entry for folder (mitto-ayl.1), so the next glob-mode FileExists/
// DirExists call for that folder re-walks instead of returning a stale
// in-memory value. Intended to be called from (*web.Server).OnBeadsChanged
// for each event.WorkingDirs entry, riding the same .beads/ fsnotify signal
// that already drives InvalidateBeadsCache: BeadsWatcher suppresses this
// process's own bd activity, so a surviving event is an EXTERNAL mutation
// (bd from another process, direct .beads/ writes, or notably a git
// pull/checkout) — precisely the class of change that can add/remove a file
// a glob gate cares about (e.g. FileExists("**/SKILL.md")). Coverage is a
// partial proxy, not total, which is why globCacheTTL remains as a backstop
// for changes no watcher observes.
//
// Does NOT cancel an in-flight globSF walk for a key under folder: a walk
// already running may still store its result after this call returns. That
// is acceptable — the in-flight walk started after the fs event that
// triggered this invalidation, so its result is a FRESHER read than the
// entry being dropped, not a stale one.
//
// A linear scan over the (typically tiny) cache map is acceptable here for
// the same reasons documented on InvalidateBeadsCache.
func InvalidateGlobCache(folder string) {
	if folder == "" {
		return
	}
	prefix := folder + "\x00"

	globCacheMu.Lock()
	for k := range globCache {
		if k == folder || strings.HasPrefix(k, prefix) {
			delete(globCache, k)
		}
	}
	globCacheMu.Unlock()
}

// InvalidateAllGlobCaches drops every memoised fileExists/dirExists
// glob-mode cache entry across all folders (mitto-ayl.1). Companion to
// InvalidateGlobCache for callers that don't have a specific folder scope —
// e.g. (*web.Server).OnPromptsChanged, whose event carries prompt
// directories rather than workspace roots, so a folder-scoped invalidation
// key is not available.
func InvalidateAllGlobCaches() {
	globCacheMu.Lock()
	globCache = map[string]globCacheEntry{}
	globCacheMu.Unlock()
}

// splitCSV splits a comma-separated string into trimmed, non-empty tokens.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// =============================================================================
// Exported formatting helpers (single source of truth for legacy @mitto: output)
// =============================================================================

// FormatACPServers renders the available ACP server list as a human-readable
// comma-separated string, producing output byte-identical to the legacy
// @mitto:available_acp_servers substitution.
//
// Format: "name [tag1, tag2] (current), name2 [tag3]"
// Tags bracket is omitted when Tags is empty.
// " (current)" is appended only on entries where Current == true.
// Returns "" when servers is nil or empty.
func FormatACPServers(servers []ACPServerInfo) string {
	if len(servers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(servers))
	for _, srv := range servers {
		s := srv.Name
		if len(srv.Tags) > 0 {
			s += " [" + strings.Join(srv.Tags, ", ") + "]"
		}
		if srv.Current {
			s += " (current)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// FormatChildren renders a child-session list as a human-readable
// comma-separated string, producing output byte-identical to the legacy
// @mitto:children (and @mitto:mcp_children) substitution when no child has a
// linked beads issue.
//
// Format: "id (name) [acp-server] {bd-id}, id2 (name2) [acp-server2] {bd-id2}"
// "(name)" is omitted when Name == "".
// "[acp-server]" is omitted when ACPServer == "".
// "{bd-id}" is omitted when BeadsIssue == "".
// Returns "" when children is nil or empty.
func FormatChildren(children []ChildInfo) string {
	if len(children) == 0 {
		return ""
	}
	parts := make([]string, 0, len(children))
	for _, child := range children {
		s := child.ID
		if child.Name != "" {
			s += " (" + child.Name + ")"
		}
		if child.ACPServer != "" {
			s += " [" + child.ACPServer + "]"
		}
		if child.BeadsIssue != "" {
			s += " {" + child.BeadsIssue + "}"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// FormatPeers renders a workspace-peers list as a human-readable
// comma-separated string. Mirrors FormatChildren for the peers namespace.
//
// Format: "id (name) [acp-server] {bd-id}, id2 (name2) [acp-server2] {bd-id2}"
// "(name)" is omitted when Name == "".
// "[acp-server]" is omitted when ACPServer == "".
// "{bd-id}" is omitted when BeadsIssue == "".
// Returns "" when peers is nil or empty.
func FormatPeers(peers []PeerInfo) string {
	if len(peers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(peers))
	for _, peer := range peers {
		s := peer.ID
		if peer.Name != "" {
			s += " (" + peer.Name + ")"
		}
		if peer.ACPServer != "" {
			s += " [" + peer.ACPServer + "]"
		}
		if peer.BeadsIssue != "" {
			s += " {" + peer.BeadsIssue + "}"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// =============================================================================
// Template FuncMap builder
// =============================================================================

// BuildTemplateFuncMap returns a template.FuncMap populated from ctx for use
// with RenderPromptTemplate. Safe to call with a nil ctx (returns zero-value
// closures; arg always returns ""; cond/when return false on CEL evaluator error).
//
// Registered functions:
//   - arg(name, default?) — ctx.Args[name] if present and non-empty, else default or "".
//   - default(fallback, val) — val if non-empty, else fallback.
//   - fileExists(path) — true iff path is a regular file (relative to workspace folder).
//     When path contains any glob metacharacter ('*', '?', '[', '{'),
//     switches to a bounded workspace walk (pathglob.WalkMatch, maxResults=1,
//     2s timeout) and reports whether ANY regular file matches. Fail-open on
//     cap/deadline (returns true). Absolute globs and "../" escapes return
//     false. Glob results are memoised for 5s.
//   - dirExists(path)  — true iff path is a directory. Same glob-mode
//     semantics as fileExists but matches directories.
//   - ReadFile(path) — file contents as a string (workspace-relative; fail-open
//     on missing / oversize / path-escape; capped at readFileMaxBytes). Pair
//     with FileExists to distinguish absent from empty.
//   - ReadTemplate(path, .) — the variable-expanding counterpart to ReadFile:
//     reads a workspace-relative file (same fail-open read semantics) and then
//     sub-renders its contents as a Go text/template against the current
//     context, so the included file may reference {{ .Args.X }} and any
//     FuncMap helper. The second argument is the current dot (`.`) — its
//     value is IGNORED in v1; the sub-render always uses the closure's
//     captured ctx. Render step is fail-closed (parse/exec error, unknown
//     func, or depth-exceeded returns an error). Depth-capped at
//     promptTextMaxDepth. Fragments ARE attached in the sub-render, same as
//     PromptTextWithArgs (mitto-twa).
//   - commandExists(name) — true iff name is in PATH.
//   - GitRepo(path?) — true iff the folder (default: workspace root) is inside a git work tree.
//   - GitFileModified(path) — true iff the tracked file has pending (staged/unstaged) changes.
//   - GitDirModified(path?) — true iff the directory (default: workspace root) has any pending
//     changes, including untracked files.
//   - GitStatusFiles(path?) — []string of raw `git status --porcelain` lines
//     ("XY path", e.g. " M file", "?? new", "A  staged") for the workspace
//     (or the given pathspec). Empty slice on a clean tree; nil outside a repo
//     or when git is unavailable. Template-only (no CEL binding).
//   - GitFileTracked(path) — true iff path is tracked by git (present in the index).
//   - GitFileDeleted(path) — true iff the tracked file has been deleted (staged or unstaged).
//   - BeadsCount(labels, statuses) — count of beads matching ALL comma-separated labels
//     AND ANY of the comma-separated statuses. Fail-open (positive sentinel) on error.
//   - HasBeads(labels, statuses) — BeadsCount(...) > 0. Same fail-open semantics.
//   - BeadHasLabels(id, labels) — true iff the single bead <id> carries ALL
//     comma-separated labels (via `bd show <id> --json`). Fail-open on error.
//     Scopes to one issue (unlike HasBeads, which aggregates across the workspace).
//   - BeadIsOpen(id) — true iff the single bead <id> is not closed (via
//     `bd show <id> --json`). Fail-open on error. Companion to BeadHasLabels.
//   - BeadMetadata(id, key) — string value of the single bead <id>'s metadata[key]
//     (via `bd show <id> --json`). Fail-open (""): missing bd, unparseable JSON,
//     absent bead, absent/null metadata, or absent key. Render-time only — used
//     to inline per-bead values (e.g. Slack channel ID) into a prompt body when
//     the caller did not pass them explicitly.
//   - hasPattern(pattern) — true iff any MCP tool name matches pattern (fail-open).
//   - Model(tag) — true iff the current model carries the capability tag (case-insensitive).
//   - cond(expr) / when(expr) — compile+evaluate a CEL expression via GetCELEvaluator()
//     against the SAME ctx used for enabledWhen. Fail-closed: returns (false, error) on
//     compile or eval failure, which aborts template execution (and thus the send).
//     The args CEL variable is populated from ctx.Args so conditions can branch on arguments.
//   - PromptText(name) — inline another workspace prompt's full body by NAME
//     (mitto-85y.3). Wired at dispatch time via ctx.PromptTextResolver;
//     fail-closed on nil resolver, empty name, or unknown/failed resolution.
//     Trailing newlines are stripped from the returned body; interior whitespace
//     is preserved. Pairs with the `prompts` parameter type.
//   - PromptTextWithArgs(name, args) — like PromptText but ALSO sub-renders the
//     fetched body against args as a fresh text/template scope (mitto-47y.1).
//     The inner body's {{ .Args.X }} placeholders bind to the supplied inner
//     args map, independent of the outer scope. args must be map[string]string
//     (typically produced by ArgsMap). Fail-closed on nil resolver, empty name,
//     resolver error, sub-render parse/exec error, or recursion depth exceeded
//     (cap: promptTextMaxDepth). Nested bodies get the same funcmap (so they
//     may themselves call PromptTextWithArgs) and may use {{ template
//     "_shared/..." }} fragments — the workspace fragment registry is
//     attached to the sub-render (mitto-twa; lifts the mitto-47y.1 Phase-A
//     limitation). Trailing newlines are stripped.
//   - ArgsMap(name) — reads args[name] as a JSON-encoded map[string]string and
//     returns the decoded map (mitto-47y.1). Empty/absent field returns an
//     empty non-nil map (Phase B may omit the field when the picked prompt has
//     no parameters). Malformed JSON returns (nil, error) — fail-closed.
//     Intended to feed PromptTextWithArgs the inner arg scope decoded from a
//     picker-side JSON string (e.g. .Args.Prompt_Args).
//   - trim, lower, upper, contains, hasPrefix, hasSuffix — thin strings wrappers.
//   - join(sep, elems) — strings.Join with sep first (template-natural argument order).
//   - Dir(path) — path.Dir (forward-slash, not OS-native) for deriving a sibling
//     path from a workspace-relative argument.
func BuildTemplateFuncMap(ctx *PromptEnabledContext) template.FuncMap {
	var (
		folder             string
		toolServers        map[string]ServerToolInfo
		args               map[string]string
		userData           map[string]string
		modelTags          []string
		promptTextResolver func(name string) (string, error)
		promptTextDepth    int
	)
	if ctx != nil {
		folder = ctx.Workspace.Folder
		toolServers = ctx.Tools.Servers
		args = ctx.Args
		userData = ctx.UserData
		modelTags = ctx.Session.ModelTags
		promptTextResolver = ctx.PromptTextResolver
		promptTextDepth = ctx.PromptTextDepth
	}

	// cond/when: compile+evaluate a CEL expression against ctx using the singleton.
	// Fail-closed: any error aborts template execution (and thus the prompt send).
	condFn := func(expr string) (bool, error) {
		ev := GetCELEvaluator()
		if ev == nil {
			return false, fmt.Errorf("cond %q: CEL evaluator unavailable", expr)
		}
		compiled, err := ev.Compile(expr)
		if err != nil {
			return false, fmt.Errorf("cond %q: %w", expr, err)
		}
		return ev.Evaluate(compiled, ctx) // (true,nil) when ctx==nil; (true,err) on eval error
	}

	return template.FuncMap{
		"Arg": func(name string, def ...string) string {
			if v, ok := args[name]; ok && v != "" {
				return v
			}
			if len(def) > 0 {
				return def[0]
			}
			return ""
		},
		// UserData returns the conversation user-data value for name, or "" if absent.
		// A nil map (absent at menu time) indexes safely to "".
		"UserData": func(name string) string { return userData[name] },
		"Default": func(fallback, val string) string {
			if val != "" {
				return val
			}
			return fallback
		},
		"FileExists":    func(path string) bool { return fileExists(folder, path) },
		"DirExists":     func(path string) bool { return dirExists(folder, path) },
		"CommandExists": func(name string) bool { return commandExists(name) },
		// ReadFile inlines the contents of a workspace-relative regular file
		// verbatim into the rendered template. Fail-open (empty string on
		// missing/oversize/escape/error) so callers pair it with FileExists
		// to distinguish absent from empty. See readFile (this file) for the
		// path-safety and size-cap semantics. Intended for small, curated
		// fragment files (e.g. .mitto/support/<channel>/*.md); do NOT use for
		// arbitrary user-supplied paths without an out-of-band allow-list.
		"ReadFile": func(path string) string { return readFile(folder, path) },
		// ReadTemplate is the variable-expanding counterpart to ReadFile: it
		// reads a workspace-relative file (same fail-open path-safety and
		// size-cap semantics as ReadFile) and then renders its contents as a
		// Go text/template against the current context, so the included file
		// may reference {{ .Args.X }} / {{ .Session.* }} / any FuncMap helper.
		//
		// The second argument is the current dot (`.`) — a caller idiom of
		// `{{ ReadTemplate "path/to.md" . }}`. Its value is IGNORED in v1;
		// the sub-render always uses the closure's captured ctx (which is the
		// same *PromptEnabledContext as the outer dot at render time). The
		// parameter is retained at the API level so the call site is self-
		// documenting and so a future extension can widen the semantics
		// without a source-incompatible signature change.
		//
		// Read step: fail-open (same as ReadFile — missing/oversize/escape
		// return "" with nil error). Render step: fail-closed (parse or
		// execution error, unknown func, or depth-exceeded return a non-nil
		// error which propagates through the outer template's fail-closed
		// policy). Fragments (`{{ template "_shared/..." }}`) ARE attached in
		// the sub-render, same as PromptTextWithArgs (mitto-twa).
		"ReadTemplate": func(path string, _ any) (string, error) {
			return readTemplate(path, folder, ctx, promptTextDepth)
		},
		"GitRepo": func(path ...string) bool {
			p := ""
			if len(path) > 0 {
				p = path[0]
			}
			return gitRepo(folder, p)
		},
		"GitFileModified": func(path string) bool { return gitFileModified(folder, path) },
		"GitDirModified": func(path ...string) bool {
			p := ""
			if len(path) > 0 {
				p = path[0]
			}
			return gitDirModified(folder, p)
		},
		"GitStatusFiles": func(path ...string) []string {
			p := ""
			if len(path) > 0 {
				p = path[0]
			}
			lines, ok := gitStatusPorcelain(folder, p)
			if !ok {
				return nil
			}
			return lines
		},
		"GitFileTracked": func(path string) bool { return gitFileTracked(folder, path) },
		"GitFileDeleted": func(path string) bool { return gitFileDeleted(folder, path) },
		// BeadsCount / HasBeads — see beadsCount/hasBeads (templatefuncs.go) for
		// fail-open + short-TTL cache semantics. Cheap gates (CommandExists("bd"),
		// DirExists(".beads")) must come BEFORE these via && short-circuit so bd
		// only runs when the workspace actually has a beads database.
		"BeadsCount": func(labels, statuses string) int { return beadsCount(folder, labels, statuses) },
		"HasBeads":   func(labels, statuses string) bool { return hasBeads(folder, labels, statuses) },
		// BeadHasLabels(id, labels) — true iff the single bead <id> carries ALL
		// comma-separated labels (via `bd show <id> --json`). Fail-open. Scopes to
		// one issue, unlike HasBeads which aggregates across the workspace.
		"BeadHasLabels": func(id, labels string) bool { return beadHasLabels(folder, id, labels) },
		// BeadIsOpen(id) — true iff the single bead <id> is not closed. Fail-open.
		// Companion to BeadHasLabels for gating on the linked bead's status.
		"BeadIsOpen": func(id string) bool { return beadIsOpen(folder, id) },
		// BeadMetadata(id, key) — string value of bead <id>'s metadata[key], via
		// `bd show <id> --json`. Fail-open (""). Render-time helper for inlining
		// a per-bead value (e.g. Slack channel ID) when the caller did not pass
		// it as an explicit argument.
		"BeadMetadata": func(id, key string) string { return beadMetadata(folder, id, key) },
		"HasPattern":   func(pattern string) bool { return hasPattern(toolServers, pattern) },
		// Model(tag) — true iff the session's current model carries the capability tag
		// (case-insensitive), resolved from the models: profiles. False for an unknown model.
		"Model": func(tag string) bool { return hasModelTag(modelTags, tag) },
		// PromptText(name) inlines another workspace prompt's full body by NAME
		// (mitto-85y.3). The fetched body is inlined VERBATIM into the outer
		// template output; because Go text/template does not re-parse rendered
		// results, any Go-template actions inside the fetched body appear
		// literally in the final output. Two-pass rendering is intentionally
		// not implemented. Fail-closed: nil resolver / empty name / resolver
		// error all propagate as a template execution error, which aborts the
		// send for named/loop prompts (matching RenderPromptTemplate's policy).
		"PromptText": func(name string) (string, error) {
			if promptTextResolver == nil {
				return "", fmt.Errorf("PromptText: no resolver available")
			}
			if name == "" {
				return "", fmt.Errorf("PromptText: empty prompt name")
			}
			body, err := promptTextResolver(name)
			if err != nil {
				return "", fmt.Errorf("PromptText(%q): %w", name, err)
			}
			return strings.TrimRight(body, "\n"), nil
		},
		// PromptTextWithArgs(name, args) fetches the named prompt's body via
		// the resolver, then sub-renders it against a fresh scope whose .Args
		// is the supplied inner map (mitto-47y.1). This is the two-pass
		// counterpart to PromptText: the inner body's {{ .Args.X }} bind to
		// innerArgs, independent of the outer scope. Recursion is capped at
		// promptTextMaxDepth to keep self-referential bodies from crashing
		// the process. Fragments ({{ template "_shared/..." }}) ARE attached
		// in the sub-render (mitto-twa; lifts the mitto-47y.1 Phase-A
		// limitation).
		"PromptTextWithArgs": func(name string, innerArgs any) (string, error) {
			if promptTextResolver == nil {
				return "", fmt.Errorf("PromptTextWithArgs: no resolver available")
			}
			if name == "" {
				return "", fmt.Errorf("PromptTextWithArgs: empty prompt name")
			}
			if promptTextDepth >= promptTextMaxDepth {
				return "", fmt.Errorf("PromptTextWithArgs(%q): recursion depth exceeded (max %d)", name, promptTextMaxDepth)
			}
			innerMap, err := coerceArgsMap(innerArgs)
			if err != nil {
				return "", fmt.Errorf("PromptTextWithArgs(%q): %w", name, err)
			}
			body, err := promptTextResolver(name)
			if err != nil {
				return "", fmt.Errorf("PromptTextWithArgs(%q): %w", name, err)
			}
			// Build a shallow copy of ctx with Args replaced and depth
			// incremented, so the sub-render is isolated from the outer
			// scope. ctx may be nil at menu/enabledWhen time — but a nil
			// resolver would have short-circuited above, so ctx is non-nil
			// here in practice; guard anyway for defensiveness.
			var inner PromptEnabledContext
			if ctx != nil {
				inner = *ctx
			}
			inner.Args = innerMap
			inner.PromptTextDepth = promptTextDepth + 1
			innerFuncs := BuildTemplateFuncMap(&inner)
			rendered, err := renderNestedPromptBody(name, body, &inner, innerFuncs)
			if err != nil {
				return "", fmt.Errorf("PromptTextWithArgs(%q): %w", name, err)
			}
			return strings.TrimRight(rendered, "\n"), nil
		},
		// ArgsMap(name) reads args[name] as a JSON-encoded map[string]string
		// (mitto-47y.1). Empty/absent field returns an empty non-nil map so
		// Phase B may omit the field when the picked prompt has no
		// parameters. Malformed JSON returns (nil, error) — fail-closed.
		"ArgsMap": func(name string) (map[string]string, error) {
			raw := args[name]
			if raw == "" {
				return map[string]string{}, nil
			}
			var out map[string]string
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				return nil, fmt.Errorf("ArgsMap(%q): %w", name, err)
			}
			if out == nil {
				out = map[string]string{}
			}
			return out, nil
		},
		"Cond":      condFn,
		"When":      condFn, // alias for Cond
		"Trim":      strings.TrimSpace,
		"Lower":     strings.ToLower,
		"Upper":     strings.ToUpper,
		"Contains":  strings.Contains,
		"HasPrefix": strings.HasPrefix,
		"HasSuffix": strings.HasSuffix,
		"Join":      func(sep string, elems []string) string { return strings.Join(elems, sep) },
		// Dir returns the directory portion of a forward-slash path (path.Dir
		// semantics — NOT OS-native filepath.Dir). Intended for deriving a
		// sibling-file path from a workspace-relative argument such as
		// {{ .Args.Test }}: e.g. Dir("a/b/test_foo.md") == "a/b",
		// Dir("test_foo.md") == ".", Dir("") == ".". Pairs with FileExists /
		// ReadFile to inline files that sit next to a user-selected file.
		"Dir": path.Dir,

		// dict builds a map[string]any from alternating key/value pairs. It
		// mirrors the well-known Sprig helper of the same name so shared
		// fragments can be passed structured arguments — the template call
		// syntax `{{ template "name" X }}` accepts only a single value, so
		// callers that need to pass multiple named fields use a dict:
		//
		//     {{ template "_shared/foo" (dict "Ctx" . "Filter" "state:drafting") }}
		//
		// Keys must be strings; an odd number of arguments returns an error
		// so silent truncation cannot mask a caller bug.
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments (%d) — keys and values must be paired", len(pairs))
			}
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key at position %d is %T, want string", i, pairs[i])
				}
				out[key] = pairs[i+1]
			}
			return out, nil
		},
	}
}

// renderNestedPromptBody sub-renders body against data using funcs. It mirrors
// the outer prompt renderer's fragment-attach behavior (internal/prompts'
// RenderPromptTemplate): every fragment returned by fragmentsForNestedRender
// is attached as an associated sub-template BEFORE body is parsed, so
// {{ template "_shared/foo" . }} resolves the same way in ReadTemplate and
// PromptTextWithArgs sub-renders as it does in a top-level prompt render
// (mitto-twa; lifts the mitto-47y.1 Phase-A "no fragments in sub-render"
// limitation). A nil/empty fragment set (no provider installed, e.g. a
// standalone internal/cel test or a binary that never loads prompts) skips
// the attach loop entirely, leaving behavior bytewise-identical to before
// mitto-twa. Attach happens via the func-typed fragmentProvider hook rather
// than a direct import: internal/cel deliberately does not import
// internal/prompts (decoupled in mitto-b8k.3), and TestReadTemplate_
// NoPromptsImport pins that. A fragment that fails to parse is a fail-closed
// error, consistent with RenderPromptTemplate. name is used only for error
// messages.
func renderNestedPromptBody(name, body string, data any, funcs template.FuncMap) (string, error) {
	if !strings.Contains(body, "{{") {
		return body, nil
	}
	tmpl := template.New(name).Option("missingkey=zero").Funcs(funcs)
	for fragName, fragBody := range fragmentsForNestedRender() {
		if _, err := tmpl.New(fragName).Parse(fragBody); err != nil {
			return "", fmt.Errorf("fragment %q parse: %w", fragName, err)
		}
	}
	tmpl, err := tmpl.Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render error: %w", err)
	}
	return buf.String(), nil
}

// coerceArgsMap accepts the second argument to PromptTextWithArgs and returns
// a map[string]string suitable for the inner scope's .Args. It accepts:
//   - map[string]string (typical: from ArgsMap or the outer .Args)
//   - map[string]any (defensive: text/template may hand us this for dict-built
//     maps or JSON-derived data)
//   - nil (returns an empty non-nil map so the sub-render behaves like a
//     prompt dispatched with no arguments)
//
// Any other type is rejected so callers cannot silently pass a struct or
// stringly-typed slice and get "".
func coerceArgsMap(v any) (map[string]string, error) {
	if v == nil {
		return map[string]string{}, nil
	}
	switch m := v.(type) {
	case map[string]string:
		if m == nil {
			return map[string]string{}, nil
		}
		return m, nil
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			if val == nil {
				out[k] = ""
				continue
			}
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("args[%q] is %T, want string", k, val)
			}
			out[k] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("args must be map[string]string or map[string]any, got %T", v)
	}
}
