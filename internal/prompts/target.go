package prompts

import (
	"fmt"
	"strings"
	"text/template"
)

// PromptTargetContext is the reduced data context passed to Go text/template
// rendering of PromptTarget.Title at dispatch time (mitto-5qbo).
//
// It intentionally exposes only fields already in scope at the two dispatch
// sites (MCP mitto_conversation_new and web POST /api/sessions) so no
// cross-package plumbing is required for the MVP. Extending this context
// (e.g. adding .Item.*) is tracked as a follow-up.
type PromptTargetContext struct {
	// Args carries the caller-supplied prompt arguments (input.Arguments in
	// MCP, req.Arguments in web). A nil map indexes safely to "".
	Args map[string]string
	// Session exposes the small subset of session-shaped fields available
	// at dispatch time. Currently only the linked bead ID.
	Session struct {
		// BeadsIssue is the top-level linked bead ID for the new
		// conversation (input.BeadsIssue / req.BeadsIssue). Empty when
		// the dispatch is not bead-scoped.
		BeadsIssue string
	}
	// Workspace exposes workspace-scoped context.
	Workspace struct {
		// Folder is the resolved working directory of the new conversation.
		Folder string
	}
}

// targetTitleFuncMap returns the stripped template.FuncMap allowed in
// target.title templates. Deliberately narrower than cel.BuildTemplateFuncMap:
// helpers such as Cond/When/Session.*/ACP.* require a *PromptEnabledContext
// that is not in scope here and would silently render "" on the reduced
// context. Only pure, side-effect-free helpers are exposed.
func targetTitleFuncMap(ctx PromptTargetContext) template.FuncMap {
	return template.FuncMap{
		"Arg": func(name string, def ...string) string {
			if v, ok := ctx.Args[name]; ok && v != "" {
				return v
			}
			if len(def) > 0 {
				return def[0]
			}
			return ""
		},
		"Default": func(fallback, val string) string {
			if val != "" {
				return val
			}
			return fallback
		},
		"Trim":      strings.TrimSpace,
		"Lower":     strings.ToLower,
		"Upper":     strings.ToUpper,
		"Contains":  strings.Contains,
		"HasPrefix": strings.HasPrefix,
		"HasSuffix": strings.HasSuffix,
	}
}

// RenderPromptTargetTitle renders a prompt's target.title as a Go text/template
// against ctx (mitto-5qbo).
//
// Fast path: when tpl has no template syntax it is returned unchanged (no
// parse, no allocation of a FuncMap) — every existing literal target.title
// is byte-for-byte unaffected.
//
// Fail-closed: parse and execution errors are wrapped and returned; the
// caller must abort the dispatch (no session created). An empty or
// whitespace-only render is also treated as an error, mirroring the
// literal-title guard in ValidatePromptTarget (an empty rendered title
// would silently collide across callers when combined with reuseTitle).
//
// promptName is used for error messages and should be the prompt's Name so
// operators can locate the offending frontmatter block.
func RenderPromptTargetTitle(promptName, tpl string, ctx PromptTargetContext) (string, error) {
	if !HasTemplateSyntax(tpl) {
		return tpl, nil
	}
	name := promptName
	if name == "" {
		name = "prompt"
	}
	name = name + ".target.title"
	// Use the fragments-less renderer: target.title strings never use
	// `{{ template "..." }}`, and attaching CurrentFragments here would
	// require them to parse against the stripped targetTitleFuncMap — which
	// omits helpers like FileExists/ReadFile used by fragments such as
	// support/shared/channel-fragment-read (mitto-eyf).
	rendered, err := RenderPromptTemplateWithoutFragments(name, tpl, ctx, targetTitleFuncMap(ctx))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rendered) == "" {
		return "", fmt.Errorf("prompt %q: target.title rendered to an empty/whitespace-only string (template: %q)", promptName, tpl)
	}
	return rendered, nil
}
