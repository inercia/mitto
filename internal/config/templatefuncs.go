package config

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// =============================================================================
// Pure-Go condition helpers — single source of truth shared by CEL bindings
// (cel_evaluator.go) and the template FuncMap (BuildTemplateFuncMap below).
// Changing logic here propagates identically to both callers.
// =============================================================================

// hasPattern reports whether any name in names matches the glob pattern.
// Fail-open: returns true when available is false (tool list not yet fetched).
func hasPattern(available bool, names []string, pattern string) bool {
	if !available {
		return true // fail-open during MCP-tools cache warm-up
	}
	for _, name := range names {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

// hasAllPatterns reports whether every pattern is matched by at least one name.
// Fail-open: returns true when available is false.
func hasAllPatterns(available bool, names []string, patterns []string) bool {
	if !available {
		return true
	}
	for _, pattern := range patterns {
		found := false
		for _, name := range names {
			if matched, err := filepath.Match(pattern, name); err == nil && matched {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// hasAnyPattern reports whether any pattern is matched by at least one name.
// Fail-open: returns true when available is false.
func hasAnyPattern(available bool, names []string, patterns []string) bool {
	if !available {
		return true
	}
	for _, pattern := range patterns {
		for _, name := range names {
			if matched, err := filepath.Match(pattern, name); err == nil && matched {
				return true
			}
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
//   - hasPattern(pattern) — true iff any MCP tool name matches pattern (fail-open).
//   - Model(tag) — true iff the current model carries the capability tag (case-insensitive).
//   - cond(expr) / when(expr) — compile+evaluate a CEL expression via GetCELEvaluator()
//     against the SAME ctx used for enabledWhen. Fail-closed: returns (false, error) on
//     compile or eval failure, which aborts template execution (and thus the send).
//     The args CEL variable is populated from ctx.Args so conditions can branch on arguments.
//   - trim, lower, upper, contains, hasPrefix, hasSuffix — thin strings wrappers.
//   - join(sep, elems) — strings.Join with sep first (template-natural argument order).
func BuildTemplateFuncMap(ctx *PromptEnabledContext) template.FuncMap {
	var (
		folder         string
		toolsAvailable bool
		toolNames      []string
		args           map[string]string
		userData       map[string]string
		modelTags      []string
	)
	if ctx != nil {
		folder = ctx.Workspace.Folder
		toolsAvailable = ctx.Tools.Available
		toolNames = ctx.Tools.Names
		args = ctx.Args
		userData = ctx.UserData
		modelTags = ctx.Session.ModelTags
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
		"HasPattern":    func(pattern string) bool { return hasPattern(toolsAvailable, toolNames, pattern) },
		// Model(tag) — true iff the session's current model carries the capability tag
		// (case-insensitive), resolved from the models: profiles. False for an unknown model.
		"Model": func(tag string) bool { return hasModelTag(modelTags, tag) },
		"Cond":          condFn,
		"When":          condFn, // alias for Cond
		"Trim":          strings.TrimSpace,
		"Lower":         strings.ToLower,
		"Upper":         strings.ToUpper,
		"Contains":      strings.Contains,
		"HasPrefix":     strings.HasPrefix,
		"HasSuffix":     strings.HasSuffix,
		"Join":          func(sep string, elems []string) string { return strings.Join(elems, sep) },
	}
}
