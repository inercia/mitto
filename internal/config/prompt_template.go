package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
)

// migratableMittoVars maps deprecated @mitto:<token> names (without the prefix)
// to their Go-template replacement. This is the single authoritative source of truth
// for which @mitto: tokens have a template equivalent and should be warned about.
var migratableMittoVars = map[string]string{
	"session_id":            "{{ .Session.ID }}",
	"parent_session_id":     "{{ .Session.ParentID }}",
	"parent":                "{{ if .Parent.Exists }}{{ .Session.ParentID }} ({{ .Parent.Name }}){{ end }}",
	"session_name":          "{{ .Session.Name }}",
	"working_dir":           "{{ .Workspace.Folder }}",
	"acp_server":            "{{ .ACP.Name }}",
	"workspace_uuid":        "{{ .Workspace.UUID }}",
	"beads_issue":           "{{ .Session.BeadsIssue }}",
	"mcp_children_count":    "{{ .Children.MCPCount }}",
	"periodic":              "{{ .Session.IsPeriodic }}",
	"periodic_forced":       "{{ .Session.IsPeriodicForced }}",
	"available_acp_servers": "{{ .ACP.AvailableText }}",
	"children":              "{{ .Children.AllText }}",
	"mcp_children":          "{{ .Children.MCPText }}",
	"user_data":             "{{ .Session.UserDataJSON }}",
	"user_data_schema":      "{{ .Workspace.UserDataSchemaJSON }}",
}

// keepListMittoVars lists @mitto: token names that have no template equivalent.
// All five original keep-list tokens have been graduated to migratableMittoVars.
// This variable is kept (empty) because DeprecatedMittoVars still references it.
var keepListMittoVars = map[string]struct{}{}

// mittoVarRe matches @mitto:<token> occurrences (preceded by any char so we can
// detect backslash-escapes). We capture the preceding char + the token name.
var mittoVarRe = regexp.MustCompile(`@mitto:([a-z_]+)`)

// deprecationWarnLogged provides per-process deduplication so each (prompt, vars)
// combination only logs once regardless of how many times the prompt is reloaded.
var deprecationWarnLogged sync.Map

// DeprecatedMittoVars returns a sorted, unique list of MIGRATABLE @mitto: token
// names (without the "@mitto:" prefix) found in body. Keep-list tokens and
// backslash-escaped occurrences (\@mitto:...) are excluded. Returns nil when body
// contains no deprecated token.
func DeprecatedMittoVars(body string) []string {
	if !strings.Contains(body, "@mitto:") {
		return nil // fast path
	}
	seen := make(map[string]struct{})
	matches := mittoVarRe.FindAllStringIndex(body, -1)
	for _, loc := range matches {
		start := loc[0]
		token := body[start+len("@mitto:") : loc[1]]
		// Skip escaped occurrences: backslash immediately before @mitto:
		if start > 0 && body[start-1] == '\\' {
			continue
		}
		if _, keep := keepListMittoVars[token]; keep {
			continue
		}
		if _, migratable := migratableMittoVars[token]; migratable {
			seen[token] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// DeprecatedMittoVarReplacement returns the Go-template replacement string for a
// migratable @mitto: token name (without the "@mitto:" prefix), or "" if unknown.
func DeprecatedMittoVarReplacement(token string) string {
	return migratableMittoVars[token]
}

// WarnDeprecatedMittoVars emits a single slog.Warn when body contains migratable
// @mitto: tokens. Deduplication prevents repeated warnings for the same
// (promptName, vars) combination within the same process lifetime.
func WarnDeprecatedMittoVars(promptName, body string) {
	vars := DeprecatedMittoVars(body)
	if len(vars) == 0 {
		return
	}
	key := promptName + "|" + strings.Join(vars, ",")
	if _, loaded := deprecationWarnLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	slog.Warn("prompt body uses deprecated @mitto: variables; migrate to Go templates",
		"prompt", promptName,
		"vars", vars,
		"hint", "see docs/devel/prompt-templates.md §9")
}

// templateOpenDelim is the text/template action open delimiter.
const templateOpenDelim = "{{"

// HasTemplateSyntax reports whether body contains any text/template action,
// i.e. whether RenderPromptTemplate would do real work (vs. the fast path).
func HasTemplateSyntax(body string) bool {
	return strings.Contains(body, templateOpenDelim)
}

// PrecompileTemplateConds statically validates that all cond/when string-literal
// arguments in body are valid CEL expressions. It is a best-effort helper: dynamic
// (non-literal) cond arguments are compiled against whatever value they evaluate to
// at dry-run time, which is acceptable.
//
// Returns nil for bodies without template syntax (fast path). Returns a non-nil
// error on the first CEL compile failure, wrapped as:
//
//	prompt template %q: cond precompile: <compile error>
//
// Wired at load time (ParsePromptFile) and save time (MCP mitto_prompt_update,
// REST POST /api/workspace-prompts) as of mitto-m7sb.6.
func PrecompileTemplateConds(name, body string) error {
	if !HasTemplateSyntax(body) {
		return nil
	}
	// condStub compiles the expression string only (no evaluation).
	// Returns (false, err) on compile failure so template execution stops immediately.
	condStub := func(expr string) (bool, error) {
		ev := GetCELEvaluator()
		if ev == nil {
			return false, nil // evaluator unavailable; skip validation
		}
		if _, err := ev.Compile(expr); err != nil {
			return false, err
		}
		return false, nil
	}
	// Start with the full FuncMap so parse succeeds for templates that use other funcs.
	fm := BuildTemplateFuncMap(&PromptEnabledContext{})
	fm["cond"] = condStub
	fm["when"] = condStub

	t, err := template.New(name).Option("missingkey=zero").Funcs(fm).Parse(body)
	if err != nil {
		return fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, &PromptEnabledContext{}); err != nil {
		return fmt.Errorf("prompt template %q: cond precompile: %w", name, err)
	}
	return nil
}

// RenderPromptTemplate renders a prompt body with Go text/template.
//
// Fast path: if body has no template syntax it is returned unchanged (no parse).
// Otherwise the body is parsed and executed against data with the given funcs.
// missingkey=zero: a missing MAP key renders as "" (like ${MISSING}); struct
// field typos still produce an error. No HTML escaping (text/template).
//
// name is used only in error messages (use the prompt name when available).
// data is the render context (later: *PromptEnabledContext). funcs may be nil.
// Returns the rendered string, or a non-nil error on parse/exec failure
// (fail-closed: the caller must abort the send on error).
func RenderPromptTemplate(name, body string, data any, funcs template.FuncMap) (string, error) {
	if !HasTemplateSyntax(body) {
		return body, nil
	}
	t, err := template.New(name).Option("missingkey=zero").Funcs(funcs).Parse(body)
	if err != nil {
		return "", fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt template %q: render error: %w", name, err)
	}
	return buf.String(), nil
}
