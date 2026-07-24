package prompts

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"github.com/inercia/mitto/internal/cel"
)

// checkTemplateRefs walks the parse tree of every template attached to t and
// returns the first `{{ template "name" ... }}` invocation whose name has no
// matching sub-template attached. text/template's own Parse never rejects
// unknown template refs — only Execute does — so callers that only want to
// parse-validate (ValidatePromptTemplateSyntax) must walk the tree explicitly.
func checkTemplateRefs(t *template.Template) error {
	var walk func(node parse.Node) error
	walk = func(node parse.Node) error {
		if node == nil {
			return nil
		}
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return nil
			}
			for _, sub := range n.Nodes {
				if err := walk(sub); err != nil {
					return err
				}
			}
		case *parse.IfNode:
			if err := walk(n.List); err != nil {
				return err
			}
			return walk(n.ElseList)
		case *parse.RangeNode:
			if err := walk(n.List); err != nil {
				return err
			}
			return walk(n.ElseList)
		case *parse.WithNode:
			if err := walk(n.List); err != nil {
				return err
			}
			return walk(n.ElseList)
		case *parse.TemplateNode:
			if t.Lookup(n.Name) == nil {
				return fmt.Errorf("template %q not defined", n.Name)
			}
		}
		return nil
	}
	for _, tmpl := range t.Templates() {
		if tmpl.Tree == nil || tmpl.Root == nil {
			continue
		}
		if err := walk(tmpl.Root); err != nil {
			return err
		}
	}
	return nil
}

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
	"loop":                  "{{ .Session.IsLoop }}",
	"loop_forced":           "{{ .Session.IsLoopForced }}",
	"loop_run_on_start":     "{{ .Session.IsLoopRunOnStart }}",
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

// PrecompileTemplateConds statically validates that all Cond/When string-literal
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
		ev := cel.GetCELEvaluator()
		if ev == nil {
			return false, nil // evaluator unavailable; skip validation
		}
		if _, err := ev.Compile(expr); err != nil {
			return false, err
		}
		return false, nil
	}
	// Start with the full FuncMap so parse succeeds for templates that use other funcs.
	fm := cel.BuildTemplateFuncMap(&cel.PromptEnabledContext{})
	fm["Cond"] = condStub
	fm["When"] = condStub

	t := template.New(name).Option("missingkey=zero").Funcs(fm)
	// Attach every installed fragment as an associated sub-template BEFORE parsing
	// the caller body so text/template's own parser rejects `{{ template "unknown" }}`
	// references at load time (same class of failure as an unbalanced `{{ ... }}`).
	// Nil registry (default until bootstrap installs one) skips attachment and preserves
	// pre-fragment behavior bytewise. Mirrors RenderPromptTemplate's attach loop.
	if frags := CurrentFragments(); frags != nil {
		for fragName, fragBody := range frags.All() {
			if _, err := t.New(fragName).Parse(fragBody); err != nil {
				return fmt.Errorf("prompt template %q: fragment %q parse: %w", name, fragName, err)
			}
		}
	}
	if _, err := t.Parse(body); err != nil {
		return fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, &cel.PromptEnabledContext{}); err != nil {
		return fmt.Errorf("prompt template %q: cond precompile: %w", name, err)
	}
	return nil
}

// ValidatePromptTemplateSyntax parse-checks a prompt body for valid Go
// text/template syntax WITHOUT executing it. It catches structural errors such as
// an unbalanced action ("unexpected EOF") before a body is enqueued for dispatch,
// so a broken free-text prompt is rejected at dispatch time with a clear error
// instead of being silently delivered raw to a child (mitto-e7u).
//
// Fast path: bodies without template syntax return nil. The full FuncMap is
// registered so legitimate function calls (Cond, When, Session.*, etc.) parse
// successfully; no template execution is performed, so this never false-positives
// on funcs that would need real render context.
func ValidatePromptTemplateSyntax(name, body string) error {
	if !HasTemplateSyntax(body) {
		return nil
	}
	if name == "" {
		name = "prompt"
	}
	fm := cel.BuildTemplateFuncMap(&cel.PromptEnabledContext{})
	t := template.New(name).Option("missingkey=zero").Funcs(fm)
	// Attach every installed fragment so `{{ template "unknown" }}` fails at parse
	// (see PrecompileTemplateConds for the same rationale and pattern).
	if frags := CurrentFragments(); frags != nil {
		for fragName, fragBody := range frags.All() {
			if _, err := t.New(fragName).Parse(fragBody); err != nil {
				return fmt.Errorf("prompt template %q: fragment %q parse: %w", name, fragName, err)
			}
		}
	}
	if _, err := t.Parse(body); err != nil {
		return fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	// text/template.Parse does not reject `{{ template "unknown" }}` — only
	// Execute does. Walk the parse tree so an unknown fragment ref fails at
	// load time on this parse-only path too (mitto-g61.4).
	if err := checkTemplateRefs(t); err != nil {
		return fmt.Errorf("prompt template %q: %w", name, err)
	}
	return nil
}

// RenderPromptTemplate renders a prompt body with Go text/template.
//
// Fast path: if body has no template syntax it is returned unchanged (no parse).
// Otherwise the body is parsed and executed against data with the given funcs.
// missingkey=zero: a missing MAP key renders as "" (like an absent .Args key); struct
// field typos still produce an error. No HTML escaping (text/template).
//
// If a fragment registry is installed via SetCurrentFragments, each fragment is
// attached to the template set as an associated sub-template before the caller
// body is parsed, so `{{ template "name" . }}` in the body resolves. A nil
// registry (default until bootstrap installs one) skips attachment entirely and
// preserves pre-fragment behavior bytewise. Fragment bodies are validated at
// load time (LoadFragmentsFromDir) but re-parsed here per render since Go's
// text/template does not expose a "clone parsed set" primitive that composes
// cleanly with a per-call FuncMap.
//
// name is used only in error messages (use the prompt name when available).
// data is the render context (later: *PromptEnabledContext). funcs may be nil.
// Returns the rendered string, or a non-nil error on parse/exec failure
// (fail-closed: the caller must abort the send on error).
func RenderPromptTemplate(name, body string, data any, funcs template.FuncMap) (string, error) {
	if !HasTemplateSyntax(body) {
		return body, nil
	}
	t := template.New(name).Option("missingkey=zero").Funcs(funcs)
	if frags := CurrentFragments(); frags != nil {
		for fragName, fragBody := range frags.All() {
			if _, err := t.New(fragName).Parse(fragBody); err != nil {
				return "", fmt.Errorf("prompt template %q: fragment %q parse: %w", name, fragName, err)
			}
		}
	}
	if _, err := t.Parse(body); err != nil {
		return "", fmt.Errorf("prompt template %q: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt template %q: render error: %w", name, err)
	}
	return buf.String(), nil
}
