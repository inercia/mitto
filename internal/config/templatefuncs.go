package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

// gitCmdTimeout bounds how long a git subprocess invocation is allowed to run
// before it is killed, so template/CEL evaluation never hangs on a stalled repo.
const gitCmdTimeout = 5 * time.Second

// bdCmdTimeout bounds how long a bd (beads) subprocess invocation is allowed to
// run before it is killed. Mirrors gitCmdTimeout for the git helpers.
const bdCmdTimeout = 5 * time.Second

// beadsCacheTTL bounds how long a BeadsCount/HasBeads result is memoised per
// (folder, labels, statuses) tuple, to avoid re-exec on rapid menu re-opens.
// Kept short so a beads mutation is reflected within one TTL window.
const beadsCacheTTL = 5 * time.Second

// beadsCountFailOpen is the sentinel returned by beadsCount on ANY error (bd
// missing, timeout, non-zero exit, unparseable JSON). It is a positive value
// so HasBeads(...) returns true and the prompt is NEVER wrongly hidden —
// consistent with the CEL fail-open policy. A legitimate empty result from bd
// (`[]`) is NOT an error and returns 0.
const beadsCountFailOpen = 1

// beadsCache memoises beadsCount results for beadsCacheTTL keyed by
// folder\x00labels\x00statuses. Simple sync.Mutex-guarded map with a
// timestamped entry per key.
var (
	beadsCacheMu sync.Mutex
	beadsCache   = map[string]beadsCacheEntry{}
)

type beadsCacheEntry struct {
	count int
	at    time.Time
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

// fileExists reports whether path exists and is a regular file.
// Relative paths are resolved against folder (workspace root).
func fileExists(folder, path string) bool {
	info, ok := statResolved(folder, path)
	return ok && !info.IsDir()
}

// dirExists reports whether path exists and is a directory.
// Relative paths are resolved against folder (workspace root).
func dirExists(folder, path string) bool {
	info, ok := statResolved(folder, path)
	return ok && info.IsDir()
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

// beadsCount counts beads matching ALL comma-separated labels AND ANY of the
// comma-separated statuses, running `bd list -l <labels> --status <statuses>
// --all --json` in folder and parsing the resulting JSON array length.
//
// Fail-open: on ANY error (bd missing, not a beads repo, timeout, non-zero
// exit, unparseable JSON) returns beadsCountFailOpen (a positive sentinel) so
// HasBeads(...) returns true and callers gating a prompt never wrongly hide
// it — consistent with the CEL fail-open policy. A legitimate empty result
// (`[]`, exit 0) is NOT an error and returns 0.
//
// Results are memoised for beadsCacheTTL per (folder, labels, statuses) tuple
// to bound exec frequency on rapid menu re-opens. Consumers relying on
// short-circuit ordering (e.g. `CommandExists("bd") && DirExists(".beads") &&
// HasBeads(...)`) still get zero exec cost when the cheap gates fail.
func beadsCount(folder, labels, statuses string) int {
	key := folder + "\x00" + labels + "\x00" + statuses
	beadsCacheMu.Lock()
	if e, ok := beadsCache[key]; ok && time.Since(e.at) < beadsCacheTTL {
		beadsCacheMu.Unlock()
		return e.count
	}
	beadsCacheMu.Unlock()

	out, ok := runBd(folder, "list", "-l", labels, "--status", statuses, "--all", "--json")
	if !ok {
		return beadsCountFailOpen
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		// Empty stdout is unexpected (bd emits at least `[]`); treat as error.
		return beadsCountFailOpen
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return beadsCountFailOpen
	}
	count := len(arr)

	beadsCacheMu.Lock()
	beadsCache[key] = beadsCacheEntry{count: count, at: time.Now()}
	beadsCacheMu.Unlock()
	return count
}

// hasBeads reports whether beadsCount(folder, labels, statuses) > 0. Convenience
// wrapper sharing the same fail-open + cache semantics as beadsCount.
func hasBeads(folder, labels, statuses string) bool {
	return beadsCount(folder, labels, statuses) > 0
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
// @mitto:children (and @mitto:mcp_children) substitution.
//
// Format: "id (name) [acp-server], id2 (name2) [acp-server2]"
// "(name)" is omitted when Name == "".
// "[acp-server]" is omitted when ACPServer == "".
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
//   - dirExists(path)  — true iff path is a directory.
//   - commandExists(name) — true iff name is in PATH.
//   - GitRepo(path?) — true iff the folder (default: workspace root) is inside a git work tree.
//   - GitFileModified(path) — true iff the tracked file has pending (staged/unstaged) changes.
//   - GitDirModified(path?) — true iff the directory (default: workspace root) has any pending
//     changes, including untracked files.
//   - GitFileTracked(path) — true iff path is tracked by git (present in the index).
//   - GitFileDeleted(path) — true iff the tracked file has been deleted (staged or unstaged).
//   - BeadsCount(labels, statuses) — count of beads matching ALL comma-separated labels
//     AND ANY of the comma-separated statuses. Fail-open (positive sentinel) on error.
//   - HasBeads(labels, statuses) — BeadsCount(...) > 0. Same fail-open semantics.
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
//   - trim, lower, upper, contains, hasPrefix, hasSuffix — thin strings wrappers.
//   - join(sep, elems) — strings.Join with sep first (template-natural argument order).
func BuildTemplateFuncMap(ctx *PromptEnabledContext) template.FuncMap {
	var (
		folder             string
		toolServers        map[string]ServerToolInfo
		args               map[string]string
		userData           map[string]string
		modelTags          []string
		promptTextResolver func(name string) (string, error)
	)
	if ctx != nil {
		folder = ctx.Workspace.Folder
		toolServers = ctx.Tools.Servers
		args = ctx.Args
		userData = ctx.UserData
		modelTags = ctx.Session.ModelTags
		promptTextResolver = ctx.PromptTextResolver
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
		"GitFileTracked": func(path string) bool { return gitFileTracked(folder, path) },
		"GitFileDeleted": func(path string) bool { return gitFileDeleted(folder, path) },
		// BeadsCount / HasBeads — see beadsCount/hasBeads (templatefuncs.go) for
		// fail-open + short-TTL cache semantics. Cheap gates (CommandExists("bd"),
		// DirExists(".beads")) must come BEFORE these via && short-circuit so bd
		// only runs when the workspace actually has a beads database.
		"BeadsCount": func(labels, statuses string) int { return beadsCount(folder, labels, statuses) },
		"HasBeads":   func(labels, statuses string) bool { return hasBeads(folder, labels, statuses) },
		"HasPattern": func(pattern string) bool { return hasPattern(toolServers, pattern) },
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
		"Cond":      condFn,
		"When":      condFn, // alias for Cond
		"Trim":      strings.TrimSpace,
		"Lower":     strings.ToLower,
		"Upper":     strings.ToUpper,
		"Contains":  strings.Contains,
		"HasPrefix": strings.HasPrefix,
		"HasSuffix": strings.HasSuffix,
		"Join":      func(sep string, elems []string) string { return strings.Join(elems, sep) },
	}
}
