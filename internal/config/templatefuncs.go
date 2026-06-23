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
	)
	if ctx != nil {
		folder = ctx.Workspace.Folder
		toolsAvailable = ctx.Tools.Available
		toolNames = ctx.Tools.Names
		args = ctx.Args
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
		"arg": func(name string, def ...string) string {
			if v, ok := args[name]; ok && v != "" {
				return v
			}
			if len(def) > 0 {
				return def[0]
			}
			return ""
		},
		"default": func(fallback, val string) string {
			if val != "" {
				return val
			}
			return fallback
		},
		"fileExists":    func(path string) bool { return fileExists(folder, path) },
		"dirExists":     func(path string) bool { return dirExists(folder, path) },
		"commandExists": func(name string) bool { return commandExists(name) },
		"hasPattern":    func(pattern string) bool { return hasPattern(toolsAvailable, toolNames, pattern) },
		"cond":          condFn,
		"when":          condFn, // alias for cond
		"trim":          strings.TrimSpace,
		"lower":         strings.ToLower,
		"upper":         strings.ToUpper,
		"contains":      strings.Contains,
		"hasPrefix":     strings.HasPrefix,
		"hasSuffix":     strings.HasSuffix,
		"join":          func(sep string, elems []string) string { return strings.Join(elems, sep) },
	}
}
