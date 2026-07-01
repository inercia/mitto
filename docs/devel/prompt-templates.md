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
| `${VAR}` / `${VAR:-default}` (bash-like) | `processors.SubstituteArguments` | **Removed** — use `{{ .Args.NAME }}` / `{{ Arg "NAME" "default" }}` |
| `@mitto:variable` | `processors.SubstituteVariables` | **Deprecated** in prompt bodies; kept for processor configs |
| `enabledWhen` CEL expressions | `config.CELEvaluator` | **Extended** — reused as `cond` / `when` template function |

---

## 2. Background: the three legacy mechanisms

### 2.1  `${VAR}` / `${VAR:-default}` — bash-like argument substitution

File: `internal/processors/arguments.go` — `SubstituteArguments(text string, args map[string]string) string`

Applied in `resolveAndSubstitute` (step 3 below) when `meta.Arguments` is non-empty.
Regex: `` `\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}` `` (captured in `argPlaceholderRe`).
`${VAR}` → `Args["VAR"]` or `""`; `${VAR:-default}` → value when present AND non-empty, else default.

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
  2. argCount = len(meta.Arguments)                     [retained for audit trail]
  3. (removed) processors.SubstituteArguments was the bash-like ${VAR} pass; removed in mitto-4so
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

This guarantees that `{{ .Session.ID }}` in a template and `Session.ID` in an `enabledWhen`
CEL expression always read the same field from the same struct.

**Template accessor ↔ CEL variable ↔ Go field (guaranteed same value):**

| Template accessor | CEL variable | Go field (`PromptEnabledContext`) |
|---|---|---|
| `{{ .Session.ID }}` | `Session.ID` | `Session.ID` |
| `{{ .Session.Name }}` | `Session.Name` | `Session.Name` |
| `{{ .Session.IsChild }}` | `Session.IsChild` | `Session.IsChild` |
| `{{ .Session.IsPeriodic }}` | `Session.IsPeriodic` | `Session.IsPeriodic` |
| `{{ .Session.HasMessages }}` | `Session.HasMessages` | `Session.HasMessages` |
| `{{ .Session.BeadsIssue }}` | `Session.BeadsIssue` | `Session.BeadsIssue` |
| `{{ .Session.UserDataJSON }}` | — | `Session.UserDataJSON` — JSON of session user-data attributes |
| `{{ Model "tag" }}` | `Session.HasModelTag("tag")` / `"tag" in Session.ModelTags` | `Session.ModelTags` — capability tags of the **current** model (from `models:` profiles); `[]` when unknown |
| `{{ .Session.ModelName }}` | — | `Session.ModelName` — display name of the current model; `""` when unknown |
| `{{ UserData "NAME" }}` / `{{ index .UserData "NAME" }}` | `UserData["NAME"]` (new) | `UserData["NAME"]` (new) — per-conversation user-data field; `""` when unset |
| `{{ .ACP.Name }}` | `ACP.Name` | `ACP.Name` |
| `{{ .ACP.Type }}` | `ACP.Type` | `ACP.Type` |
| `{{ .Workspace.Folder }}` | `Workspace.Folder` | `Workspace.Folder` |
| `{{ .Workspace.UUID }}` | `Workspace.UUID` | `Workspace.UUID` |
| `{{ .Workspace.UserDataSchemaJSON }}` | — | `Workspace.UserDataSchemaJSON` — JSON of workspace user-data schema fields |
| `{{ .Parent.Name }}` | `Parent.Name` | `Parent.Name` |
| `{{ .Parent.Exists }}` | `Parent.Exists` | `Parent.Exists` |
| `{{ .Children.Count }}` | `Children.Count` | `Children.Count` |
| `{{ .Children.MCPCount }}` | `Children.MCPCount` | `Children.MCPCount` |
| `{{ .Children.All }}` | — | `Children.All` — `[]config.ChildInfo` for all children |
| `{{ .Children.MCP }}` | — | `Children.MCP` — `[]config.ChildInfo` for MCP-origin children only |
| `{{ .ACP.Available }}` | — | `ACP.Available` — `[]config.ACPServerInfo` for workspace ACP servers |
| `{{ .Args.NAME }}` | `Args["NAME"]` (new) | `Args["NAME"]` (new) |
| `{{ .Iteration.Number }}` | — | `Iteration.Number` — 0-based index of the current periodic run; 0 for non-periodic |
| `{{ .Iteration.Max }}` | — | `Iteration.Max` — configured max runs (0 = unlimited); 0 for non-periodic |
| `{{ .Iteration.IsPeriodic }}` | — | `Iteration.IsPeriodic` — `true` when triggered by the periodic runner |
| `{{ .Iteration.IsFirst }}` | — | `Iteration.IsFirst` — `true` when `Number == 0` |
| `{{ .Iteration.IsLast }}` | — | `Iteration.IsLast` — `true` when `Max > 0 && Number == Max-1` |
| `{{ .Iteration.IsUninterrupted }}` | — | `Iteration.IsUninterrupted` — `true` only on a scheduled, non-forced periodic run directly following another such run (no user interjection / forced run / FreshContext; same process lifetime) |

`Args` is populated from `meta.Arguments` at send time. At menu time (`enabledWhen`
evaluation), `Args` is `nil`. Template rendering runs at **send time only**, so `Args` is
always the real argument map (possibly empty).

**Extending the CEL env (mitto-m7sb.5):** Add `cel.Variable("args", cel.MapType(cel.StringType, cel.StringType))` to `NewCELEvaluator` and map it in `buildActivation` as `"args": ctx.Args`. This allows `enabledWhen: "Args['BRANCH'] != \"\""` for conditional visibility that depends on arguments.

**User data (mitto-5y9x):** `UserData` is declared and wired the same way as `Args` — a `cel.Variable("UserData", cel.MapType(cel.StringType, cel.DynType))` in `NewCELEvaluator`, normalized to an empty map in `buildActivation` so `"X" in UserData` never panics, plus the `UserData "NAME"` template func and the `.UserData` map. Unlike `Args`, `UserData` is populated at **both** menu time (`buildPromptEnabledContext`) and send time (`buildProcessorInput`) — from the same per-conversation attributes that back `Session.UserDataJSON` — so `enabledWhen` can gate on `UserData["X"]`.

**Model tags (mitto-i5sr):** `Session.ModelTags` exposes the **current** model's capability tags, resolved from the `models:` profiles (see [models.md](../config/models.md)) via `config.ResolveModelTags(modelName)` — the same `contains/exact/startsWith/regex/lookAlike` engine (`config.ConstraintMatchesName`) used by ACP-server model constraints. It is wired like `UserData`: a `cel.Variable("Session.ModelTags", cel.ListType(cel.StringType))`, the `Session.HasModelTag(tag)` receiver macro (mirroring `Tools.HasPattern`), the `Model(tag)` template func, and the `"tag" in Session.ModelTags` operator. Populated at **both** menu time (`buildPromptEnabledContext`, from `BackgroundSession.CurrentModelName()`) and send time (`buildProcessorInput`, from `pdGetAgentModels()`), so menu and send agree. Tags reflect the session's **baseline/active** model at render time, **not** a prompt's `preferredModels` (which apply after render). Membership is case-insensitive and degrades to an empty set (`Model("x") == false`, never an error) when the model is unknown (cold start / suspended session) or no profile matches.

**Prompt `preferredModels` field:** A prompt may declare a `preferredModels:` list of **structured references** to global model profiles — each entry is exactly one of `modelName: <profile name>` or `modelTag: <tag>`. Entries are ordered first-match-wins; a `modelTag` resolves deterministically by profile order in the `models:` list. Resolution keeps the current model if it already satisfies the preference (no needless switch). This replaces the previous glob-pattern list (`- "*sonnet*"`). Full spec: [models.md § Referenced by prompts (`preferredModels`)](../config/models.md#referenced-by-prompts-preferredmodels).

---

## 5. Expression language: `Cond` / `When` template functions

The `Cond` (alias `When`) template function evaluates a CEL expression string at send time:

```go
// Example use in a prompt body:
{{ if Cond "Session.IsChild && FileExists(\".git/config\")" }}
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
path, pre-compile all string-literal arguments to `Cond`/`When` calls using the static AST walk
(the same `Compile` call, discarding the result). This catches syntax errors at save time.

---

## 6. Template FuncMap

All helper functions listed below share a single Go implementation (extracted from or alongside
`internal/config/cel_evaluator.go`) to prevent drift between CEL bindings and template funcs.
The shared pure-Go helpers are: `statResolved`, the glob-match logic, `matchesServerType`.

| Function | Signature | Semantics |
|---|---|---|
| `Arg` | `Arg(name, defaultVal string) string` | `Args[name]` if present AND non-empty, else `defaultVal`. Replaces the removed `${NAME:-default}` bash syntax. |
| `UserData` | `UserData(name string) string` | `UserData[name]` (per-conversation user-data field), or `""` when unset. Handles names with spaces, e.g. `UserData "JIRA Ticket"`. The `.UserData` map is also directly accessible: `{{ index .UserData "JIRA Ticket" }}`. |
| `Default` | `Default(fallback, val string) string` | Returns `val` if non-empty, else `fallback`. Same as sprig `default`. |
| `Cond` | `Cond(celExpr string) (bool, error)` | Evaluate CEL expression against send-time context. |
| `When` | alias for `Cond` | |
| `FileExists` | `FileExists(path string) bool` | File exists at `path` (relative to `Workspace.Folder`). Calls `statResolved`. |
| `DirExists` | `DirExists(path string) bool` | Directory exists. Calls `statResolved`. |
| `CommandExists` | `CommandExists(name string) bool` | Command is in PATH (`exec.LookPath`). |
| `GitRepo` | `GitRepo(path ...string) bool` | Folder (default: whole workspace) is inside a git work tree (`git rev-parse --is-inside-work-tree`). Gatekeeper for the other `Git*` checks. |
| `GitFileModified` | `GitFileModified(path string) bool` | Tracked file at `path` has pending (staged/unstaged) changes vs HEAD/index; untracked files are `false`. Relative to `Workspace.Folder`. Runs `git` as a subprocess (bounded 5s). |
| `GitDirModified` | `GitDirModified(path ...string) bool` | Directory (default: whole workspace) has any pending changes, including untracked files. |
| `GitFileTracked` | `GitFileTracked(path string) bool` | `path` is tracked by git (present in the index). |
| `GitFileDeleted` | `GitFileDeleted(path string) bool` | Tracked file at `path` has been deleted (staged or unstaged deletion). |
| `Model` | `Model(tag string) bool` | Current model carries capability `tag` (case-insensitive), resolved from `models:` profiles. `false` when the model is unknown or no profile matches. |

**No `html` escaping.** Use `text/template` (not `html/template`). Prompt bodies are
plain text / Markdown sent to an AI agent, not rendered in a browser.

---

## 7. Error and validation policy

| Location | Policy | Mechanism |
|---|---|---|
| Send time (`renderTemplateBody`) | **Fail-closed** | Return error from `resolveAndSubstitute` → `PromptWithMeta` returns error → error broadcast to UI observers, send aborted |
| Load time (`ParsePromptFile`) | **Fail-fast** | `text/template.New(...).Parse(body)` on every prompt load; return parse error |
| Save / update time (MCP `mitto_prompt_update`) | **Fail-fast** | Same parse call before persisting |
| `Cond`/`When` literal args (load time) | **Fail-fast** | `CELEvaluator.Compile(litArg)` during AST walk; discard program |

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
| `@mitto:parent` | `{{ .Parent.Ref }}` | `ParentContext.Ref()` mirrors `formatParentSession`: `"id (name)"`, or just `"id"` when the name is empty, or `""` when there is no parent |
| `@mitto:session_name` | `{{ .Session.Name }}` | |
| `@mitto:working_dir` | `{{ .Workspace.Folder }}` | |
| `@mitto:acp_server` | `{{ .ACP.Name }}` | |
| `@mitto:workspace_uuid` | `{{ .Workspace.UUID }}` | |
| `@mitto:beads_issue` | `{{ .Session.BeadsIssue }}` | |
| `@mitto:mcp_children_count` | `{{ .Children.MCPCount }}` | int, not string |
| `@mitto:periodic` | `{{ .Session.IsPeriodic }}` | bool, not `"true"`/`"false"` string |
| `@mitto:periodic_forced` | `{{ .Session.IsPeriodicForced }}` | bool, not `"true"`/`"false"` string. Field added to `SessionContext` (mitto-m7sb.3); fully wired into the CEL env (`Session.IsPeriodicForced`). |
| `@mitto:available_acp_servers` | `{{ .ACP.AvailableText }}` | `config.FormatACPServers(ctx.ACP.Available)`; format: `"name [tags] (current), name2"` |
| `@mitto:children` | `{{ .Children.AllText }}` | `config.FormatChildren(ctx.Children.All)`; format: `"id (name) [acp], id2"` |
| `@mitto:mcp_children` | `{{ .Children.MCPText }}` | `config.FormatChildren(ctx.Children.MCP)`; MCP-origin only |
| `@mitto:user_data` | `{{ .Session.UserDataJSON }}` | JSON of session user-data attributes; `""` when none |
| `@mitto:user_data_schema` | `{{ .Workspace.UserDataSchemaJSON }}` | JSON of workspace user-data schema fields; `""` when none |

All `@mitto:` tokens now have template equivalents. The `@mitto:` forms remain supported for
backward compatibility in processors and prompt bodies, but usage in prompt bodies logs a
deprecation warning (see `WarnDeprecatedMittoVars`). Prefer the template forms in new prompts.

`@mitto:user_data` / `{{ .Session.UserDataJSON }}` still render the full JSON blob (kept for
backward compat). For a single field, prefer the structured `{{ UserData "NAME" }}` func or
`{{ index .UserData "NAME" }}` map access — they enable the set-if-unset, else-do-Y pattern
that the opaque blob cannot drive.

---

## 10. Corner cases

### 10.1  Timing asymmetry: `Args` is empty at menu time

`enabledWhen` runs at menu time; `Args` is `nil` (no prompt has been dispatched yet). Do NOT
write `enabledWhen` expressions that branch on `Args["BRANCH"]` for menu visibility — those
will always evaluate the empty-map path. Template `{{ .Args.NAME }}` is send-time only.

### 10.2  CEL single-quote nesting inside template double-quotes

Go template strings use backtick literals or escaped double quotes. CEL string literals use
double quotes. When embedding a CEL expression inside `{{ if Cond "..." }}`:

```
# Wrong — inner double-quotes break the template string:
{{ if Cond "FileExists(".git/config")" }}

# Right — escape inner double-quotes:
{{ if Cond "FileExists(\".git/config\")" }}
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
  {{- if Cond "Session.IsChild" }}
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

### 10.9  `Tools.HasPattern` fail-open is menu-time only

At menu time, `ToolsContext.Available == false` causes `Tools.HasPattern` to return `true`
(fail-open) so tool-gated prompts aren't hidden during MCP tool cache warm-up. At send time
(template `cond` evaluation), the real tool list is always available (warm cache). No asymmetry
issue for the `cond` function.

### 10.10  Periodic runner IS covered

`internal/web/periodic_runner.go` dispatches prompts via `bs.PromptWithMeta(promptText, meta)`
with `meta.SenderID = "periodic-runner"` (line ~1149). Because it goes through `PromptWithMeta`,
it passes through `resolveAndSubstitute` and therefore through template rendering. No special
periodic-runner handling is needed.

---

## 11. Migration summary

| Phase | Action |
|---|---|
| mitto-m7sb.2 | Add template rendering to `resolveAndSubstitute`. `{{ ... }}` is the primary mechanism; `${VAR}` remained as a legacy fallback during this phase. |
| mitto-m7sb.10 | Add migration guide to `docs/config/prompts.md`; annotate built-in prompts with migration comments. |
| mitto-m7sb.12 | Migrate built-in prompts in `config/prompts/` from `${VAR}` / `@mitto:` to `{{ ... }}`. |
| mitto-4so | **Removed** `${VAR}` / `${VAR:-default}` bash-like argument substitution (`SubstituteArguments`) from `resolveAndSubstitute`. `{{ .Args.NAME }}` / `{{ Arg "NAME" "default" }}` are the only mechanisms. `@mitto:` stays in processor configs indefinitely; `SubstituteVariables` in `applyProcessorsAndBuildBlocks` is retained for processor backward-compat. |

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
