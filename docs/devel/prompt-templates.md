# Go Template Rendering in Prompt Bodies

This document is the authoritative design spec for adding Go `text/template` rendering to
prompt body text (`prompt:` field in `.prompt.yaml` files). All decisions here are **locked**;
implementation children (mitto-m7sb.2–.12) must follow this spec without reopening them.

**Scope: prompt bodies only.** `@mitto:` substitution in processors stays as-is.

---

## 1. Goal & scope

Replace the three overlapping ad-hoc substitution mechanisms in prompt bodies with a single
unified templating layer:

| Mechanism | Current location | Status after this epic |
|-----------|-----------------|----------------------|
| `${VAR}` / `${VAR:-default}` (bash-like) | `processors.SubstituteArguments` | **Deprecated** — kept as fallback during deprecation window |
| `@mitto:variable` | `processors.SubstituteVariables` | **Deprecated** in prompt bodies; kept for processor configs |
| `enabledWhen` CEL expressions | `config.CELEvaluator` | **Extended** — reused as `cond` / `when` template function |

---

## 2. Background: the three legacy mechanisms

### 2.1  `${VAR}` / `${VAR:-default}` — bash-like argument substitution

File: `internal/processors/arguments.go` — `SubstituteArguments(text string, args map[string]string) string`

Applied in `resolveAndSubstitute` (step 3 below) when `meta.Arguments` is non-empty.
Regex: `` `\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}` `` (captured in `argPlaceholderRe`).
`${VAR}` → `args["VAR"]` or `""`; `${VAR:-default}` → value when present AND non-empty, else default.

### 2.2  `@mitto:variable` — session-context substitution

File: `internal/processors/variables.go` — `SubstituteVariables(message string, input *ProcessorInput) string`

Applied in `applyProcessorsAndBuildBlocks` (step 6 below). Replaces 16 named placeholders with
fields from `*processors.ProcessorInput`. Escape: `\@mitto:foo` emits `@mitto:foo` literally.
Full placeholder list: see §9.

### 2.3  `enabledWhen` CEL — conditional visibility

File: `internal/config/cel_evaluator.go` — `CELEvaluator.Compile` / `Evaluate` / `buildActivation`.

Evaluated against `config.PromptEnabledContext` at **menu time** (not send time), controlling
whether a prompt appears in the UI. The template `cond` function reuses the same evaluator
and variables at **send time** (see §5).

---

## 3. The send pipeline: today vs. with template rendering

### 3.1  Current order (source of truth: `prompt_dispatcher.go`)

```
PromptWithMeta (bgsession_prompt.go:162)
  └─ promptDispatcher.resolveAndSubstitute (prompt_dispatcher.go:158)
       1. Resolve prompt name → full text  (if meta.PromptName != "" && message == "")
       2. argCount = len(meta.Arguments)
       3. processors.SubstituteArguments(message, meta.Arguments)  ← ${VAR} substitution
       4. Build argument metadata → meta.Meta
  └─ promptDispatcher.buildProcessorInput (prompt_dispatcher.go:298)
       5. Collect session metadata, child sessions, MCP tools, user data, RC
  └─ promptDispatcher.applyProcessorsAndBuildBlocks (prompt_dispatcher.go:393)
       6. Run processor pipeline (pdApplyProcessors)
       7. processors.SubstituteVariables(promptMessage, input)  ← @mitto: substitution
       8. History injection (pdBuildPromptWithHistory)
       9. Assemble finalBlocks → ACP agent
```

### 3.2  New order after mitto-m7sb.2 (insertion point in `resolveAndSubstitute`)

```
resolveAndSubstitute:
  1. Resolve prompt name → full text                    [unchanged]
  ** NEW: renderTemplateBody(message, ctx, args)        [mitto-m7sb.2]
       Fast-path: skip when body does not contain "{{"
       Engine: text/template, Option("missingkey=zero")
       Context: PromptEnabledContext + Args (see §4)
       FuncMap: cond/when, arg, fileExists, dirExists, commandExists (see §6)
       Error: fail-closed → return error → PromptWithMeta returns error
  2. argCount = len(meta.Arguments)                     [legacy fallback]
  3. processors.SubstituteArguments(...)                [legacy fallback]
  4. Build argument metadata                            [unchanged]
```

Steps 6–9 (`applyProcessorsAndBuildBlocks`) are unchanged by this epic.

---

## 4. The unified context: `config.PromptEnabledContext` + `Args`

**Decision: do NOT create a new TemplateContext struct.** Reuse `config.PromptEnabledContext`
(file: `internal/config/cel_context.go`) — the same struct that `enabledWhen` CEL uses —
extended with one new field:

```go
// In config.PromptEnabledContext (cel_context.go):
Args map[string]string  // arguments passed to the prompt (meta.Arguments); nil at menu time
```

This guarantees that `{{ .Session.ID }}` in a template and `session.id` in an `enabledWhen`
CEL expression always read the same field from the same struct.

**Template accessor ↔ CEL variable ↔ Go field (guaranteed same value):**

| Template accessor | CEL variable | Go field (`PromptEnabledContext`) |
|---|---|---|
| `{{ .Session.ID }}` | `session.id` | `Session.ID` |
| `{{ .Session.Name }}` | `session.name` | `Session.Name` |
| `{{ .Session.IsChild }}` | `session.isChild` | `Session.IsChild` |
| `{{ .Session.IsPeriodic }}` | `session.isPeriodic` | `Session.IsPeriodic` |
| `{{ .Session.BeadsIssue }}` | `session.beadsIssue` | `Session.BeadsIssue` |
| `{{ .ACP.Name }}` | `acp.name` | `ACP.Name` |
| `{{ .ACP.Type }}` | `acp.type` | `ACP.Type` |
| `{{ .Workspace.Folder }}` | `workspace.folder` | `Workspace.Folder` |
| `{{ .Workspace.UUID }}` | `workspace.uuid` | `Workspace.UUID` |
| `{{ .Parent.Name }}` | `parent.name` | `Parent.Name` |
| `{{ .Parent.Exists }}` | `parent.exists` | `Parent.Exists` |
| `{{ .Children.Count }}` | `children.count` | `Children.Count` |
| `{{ .Children.MCPCount }}` | `children.mcpCount` | `Children.MCPCount` |
| `{{ .Args.NAME }}` | `args["NAME"]` (new) | `Args["NAME"]` (new) |

`Args` is populated from `meta.Arguments` at send time. At menu time (`enabledWhen`
evaluation), `Args` is `nil`. Template rendering runs at **send time only**, so `Args` is
always the real argument map (possibly empty).

**Extending the CEL env (mitto-m7sb.5):** Add `cel.Variable("args", cel.MapType(cel.StringType, cel.StringType))` to `NewCELEvaluator` and map it in `buildActivation` as `"args": ctx.Args`. This allows `enabledWhen: "args['BRANCH'] != \"\""` for conditional visibility that depends on arguments.

---

## 5. Expression language: `cond` / `when` template functions

The `cond` (alias `when`) template function evaluates a CEL expression string at send time:

```go
// Example use in a prompt body:
{{ if cond "session.isChild && fileExists(\".git/config\")" }}
  Parent: {{ .Session.ParentID }}
{{ end }}
```

Implementation:
1. Call `config.GetCELEvaluator().Compile(exprString)` — cached; compile once.
2. Call `evaluator.Evaluate(compiled, &ctx)` — evaluates against the send-time context.
3. Return the `bool` result; propagate any error as a template execution error (fail-closed).

**Same grammar, same variables, same functions, same caching** as `enabledWhen`.
The only difference is the context is populated with send-time values (including `Args`).

**Load-time validation (mitto-m7sb.4):** In `ParsePromptFile` and the MCP `mitto_prompt_update`
path, pre-compile all string-literal arguments to `cond`/`when` calls using the static AST walk
(the same `Compile` call, discarding the result). This catches syntax errors at save time.

---

## 6. Template FuncMap

All helper functions listed below share a single Go implementation (extracted from or alongside
`internal/config/cel_evaluator.go`) to prevent drift between CEL bindings and template funcs.
The shared pure-Go helpers are: `statResolved`, the glob-match logic, `matchesServerType`.

| Function | Signature | Semantics |
|---|---|---|
| `arg` | `arg(name, defaultVal string) string` | `Args[name]` if present AND non-empty, else `defaultVal`. Mirrors `${name:-default}` bash semantics exactly. |
| `default` | `default(fallback, val string) string` | Returns `val` if non-empty, else `fallback`. Same as sprig `default`. |
| `cond` | `cond(celExpr string) (bool, error)` | Evaluate CEL expression against send-time context. |
| `when` | alias for `cond` | |
| `fileExists` | `fileExists(path string) bool` | File exists at `path` (relative to `Workspace.Folder`). Calls `statResolved`. |
| `dirExists` | `dirExists(path string) bool` | Directory exists. Calls `statResolved`. |
| `commandExists` | `commandExists(name string) bool` | Command is in PATH (`exec.LookPath`). |

**No `html` escaping.** Use `text/template` (not `html/template`). Prompt bodies are
plain text / Markdown sent to an AI agent, not rendered in a browser.

---

## 7. Error and validation policy

| Location | Policy | Mechanism |
|---|---|---|
| Send time (`renderTemplateBody`) | **Fail-closed** | Return error from `resolveAndSubstitute` → `PromptWithMeta` returns error → error broadcast to UI observers, send aborted |
| Load time (`ParsePromptFile`) | **Fail-fast** | `text/template.New(...).Parse(body)` on every prompt load; return parse error |
| Save / update time (MCP `mitto_prompt_update`) | **Fail-fast** | Same parse call before persisting |
| `cond`/`when` literal args (load time) | **Fail-fast** | `CELEvaluator.Compile(litArg)` during AST walk; discard program |

Errors at send time use `bs.notifyObservers(func(o SessionObserver) { o.OnError(msg) })` with a
descriptive message (e.g., `"template error in prompt 'my-prompt': ..."`).

---

## 8. Fast path

**Only render when the prompt body contains `{{`.**

```go
if !strings.Contains(body, "{{") {
    return body, nil  // fast path: no template syntax
}
```

This avoids parsing and executing every prompt through `text/template`. Most prompts today have
no template syntax. This check is identical to the `@mitto:` fast-path in `SubstituteVariables`
(`if !strings.Contains(message, "@mitto:") { return message }`).

---

## 9. `@mitto:` → template mapping table

| `@mitto:` placeholder | Template equivalent | Notes |
|---|---|---|
| `@mitto:session_id` | `{{ .Session.ID }}` | |
| `@mitto:parent_session_id` | `{{ .Session.ParentID }}` | |
| `@mitto:parent` | `{{ if .Parent.Exists }}{{ .Session.ParentID }} ({{ .Parent.Name }}){{ end }}` | `formatParentSession` produces `"id (name)"` format |
| `@mitto:session_name` | `{{ .Session.Name }}` | |
| `@mitto:working_dir` | `{{ .Workspace.Folder }}` | |
| `@mitto:acp_server` | `{{ .ACP.Name }}` | |
| `@mitto:workspace_uuid` | `{{ .Workspace.UUID }}` | |
| `@mitto:beads_issue` | `{{ .Session.BeadsIssue }}` | |
| `@mitto:mcp_children_count` | `{{ .Children.MCPCount }}` | int, not string |
| `@mitto:periodic` | `{{ .Session.IsPeriodic }}` | bool, not `"true"`/`"false"` string |
| `@mitto:available_acp_servers` | *(no direct equivalent)* | Complex formatted string from `ProcessorInput.AvailableACPServers` — not in `PromptEnabledContext`; keep `@mitto:` or add ctx extension |
| `@mitto:children` | *(no direct equivalent)* | Complex formatted string — keep `@mitto:` or add ctx extension |
| `@mitto:mcp_children` | *(no direct equivalent)* | Complex formatted string — keep `@mitto:` or add ctx extension |
| `@mitto:periodic_forced` | `{{ .Session.IsPeriodicForced }}` | bool, not `"true"`/`"false"` string. Field added to `SessionContext` (mitto-m7sb.3); fully wired into the CEL env (`session.isPeriodicForced`). |
| `@mitto:user_data_schema` | *(no direct equivalent)* | JSON string from `ProcessorInput` — not in ctx; keep `@mitto:` |
| `@mitto:user_data` | *(no direct equivalent)* | JSON string from `ProcessorInput` — not in ctx; keep `@mitto:` |

Gaps marked "no direct equivalent" remain as `@mitto:` legacy form in prompt bodies during the
deprecation window. They may be added to `PromptEnabledContext` in a follow-up increment.

---

## 10. Corner cases

### 10.1  Timing asymmetry: `Args` is empty at menu time

`enabledWhen` runs at menu time; `Args` is `nil` (no prompt has been dispatched yet). Do NOT
write `enabledWhen` expressions that branch on `args["BRANCH"]` for menu visibility — those
will always evaluate the empty-map path. Template `{{ .Args.NAME }}` is send-time only.

### 10.2  CEL single-quote nesting inside template double-quotes

Go template strings use backtick literals or escaped double quotes. CEL string literals use
double quotes. When embedding a CEL expression inside `{{ if cond "..." }}`:

```
# Wrong — inner double-quotes break the template string:
{{ if cond "fileExists(".git/config")" }}

# Right — escape inner double-quotes:
{{ if cond "fileExists(\".git/config\")" }}
```

### 10.3  Literal double-brace escaping

To emit a literal `{{` in output, use `{{ "{{" }}` (the template `{{` delimiter cannot be
escaped with a backslash). Example: `{{ "{{" }} .Example {{ "}}" }}` renders `{{ .Example }}`.

### 10.4  Invalid `{{ fi }}` — Go uses `{{ end }}`

Go `text/template` uses `{{ end }}` to close blocks, not `fi`. An `{{ fi }}` produces a
**parse error at load time** (caught by `ParsePromptFile` validation).

### 10.5  YAML block-scalar indentation + template whitespace trimming

YAML `|` block scalars preserve leading indentation relative to the first content line. Template
`{{-` / `-}}` trim surrounding whitespace. Prefer `-}}` before newlines inside YAML blocks to
avoid emitting blank lines:

```yaml
prompt: |
  Header text.
  {{- if cond "session.isChild" }}
  Parent: {{ .Session.ParentID }}
  {{- end }}
  Footer text.
```

### 10.6  CLI mode: empty context fields are safe

All fields in `PromptEnabledContext` have zero values (`""`, `false`, `0`). When a template
accesses `.Session.ID` in a context where `ID` was not populated (e.g. CLI mode without a
stored session), it returns `""` rather than panicking. This is guaranteed by `missingkey=zero`.

### 10.7  Struct-field typos still error

`missingkey=zero` applies to **map keys** (i.e. `{{ .Args.MISSING }}` → `""`). Struct field
typos (e.g. `{{ .Session.IDd }}`) produce a compile-time error from `text/template.Parse` and
are caught at load time by `ParsePromptFile`.

### 10.8  Title-generation path must NOT render templates

`BackgroundSession.TriggerTitleGenerationFromPeriodic` (in `bgsession_title.go`) resolves
a prompt name and feeds the result to an auxiliary AI session for title generation. It does NOT
call `PromptWithMeta`, so it is **outside the template-rendering chokepoint**. The raw prompt
text (with un-rendered `{{ ... }}` tokens) is sent to the auxiliary title generator. This is
correct: title generation reads the prompt template for summarization purposes, not for execution.
No special handling is required.

### 10.9  `tools.hasPattern` fail-open is menu-time only

At menu time, `ToolsContext.Available == false` causes `tools.hasPattern` to return `true`
(fail-open) so tool-gated prompts aren't hidden during MCP tool cache warm-up. At send time
(template `cond` evaluation), the real tool list is always available (warm cache). No asymmetry
issue for the `cond` function.

### 10.10  Periodic runner IS covered

`internal/web/periodic_runner.go` dispatches prompts via `bs.PromptWithMeta(promptText, meta)`
with `meta.SenderID = "periodic-runner"` (line ~1149). Because it goes through `PromptWithMeta`,
it passes through `resolveAndSubstitute` and therefore through template rendering. No special
periodic-runner handling is needed.

---

## 11. Deprecation plan

| Phase | Action |
|---|---|
| mitto-m7sb.2 | Add template rendering to `resolveAndSubstitute`. New syntax `{{ ... }}` works. `${VAR}` and `@mitto:` still work (legacy fallback stages 3, 7). |
| mitto-m7sb.10 | Add migration guide to `docs/config/prompts.md`; annotate built-in prompts with `# @mitto:session_id → {{ .Session.ID }}` comments |
| mitto-m7sb.12 | Migrate built-in prompts in `config/prompts/` from `${VAR}` / `@mitto:` to `{{ ... }}` |
| Future epic | Remove `SubstituteArguments` and `SubstituteVariables` from `resolveAndSubstitute` / `applyProcessorsAndBuildBlocks` once all prompts are migrated. `@mitto:` stays in processor configs indefinitely. |

---

## 12. Impacted files / child-issue map

| Bead | Scope | Key files |
|---|---|---|
| **mitto-m7sb.2** | Core renderer: `renderTemplateBody`, insert in `resolveAndSubstitute`, `missingkey=zero`, fast-path, `text/template.FuncMap` skeleton | `internal/conversation/prompt_dispatcher.go`, new `internal/config/prompt_template.go` |
| **mitto-m7sb.3** | Context builder: populate `PromptEnabledContext` at send time; add `Args map[string]string` field; add `IsPeriodicForced` to `SessionContext` | `internal/config/cel_context.go`, `internal/conversation/prompt_dispatcher.go` |
| **mitto-m7sb.4** | Load-time validation: `ParsePromptFile` + MCP `mitto_prompt_update` parse-and-validate; `cond` literal pre-compile | `internal/config/prompts.go`, `internal/web/handlers/` (prompt update handler) |
| **mitto-m7sb.5** | CEL env extension: add `args` map variable to `NewCELEvaluator` and `buildActivation` | `internal/config/cel_evaluator.go` |
| **mitto-m7sb.6** | FuncMap full impl: `arg`, `default`, `fileExists`, `dirExists`, `commandExists`, `cond`/`when`; extract shared pure-Go helper package | `internal/config/cel_evaluator.go` (extract), new `internal/config/templatefuncs.go` |
| **mitto-m7sb.10** | Docs update: migration guide in `docs/config/prompts.md` | `docs/config/prompts.md` |
| **mitto-m7sb.12** | Prompt migration: convert built-in prompts | `config/prompts/builtin/*.prompt.yaml` |
