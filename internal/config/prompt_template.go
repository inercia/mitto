package config

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

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
// Does NOT wire into the prompt-loading pipeline — that is mitto-m7sb.5.
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
