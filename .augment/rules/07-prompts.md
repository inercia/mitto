---
description: Prompt system architecture, workspace prompts, PromptsCache, merging priority, enable/disable mechanism, API endpoints
globs:
  - "internal/config/prompts*.go"
  - "internal/config/workspace_rc*.go"
  - "internal/web/session_api.go"
  - "web/static/app.js"
keywords:
  - prompts
  - WebPrompt
  - PromptsCache
  - MergePrompts
  - MergePromptsKeepDisabled
  - workspace-prompts
  - enabledWhen
  - predefinedPrompts
  - toggle-enabled
  - prompts menu
  - .mitto/prompts
---

# Prompt System

## Architecture Overview

Prompts are predefined text snippets shown in the ChatInput "Insert predefined prompt" menu. They come from multiple sources and are merged server-side into a single list per workspace.

```
┌──────────────────────────────────────────────────────────────────────┐
│              GET /api/workspace-prompts?dir=...&session_id=...       │
│                          (Single Source of Truth)                     │
│                                                                      │
│  Priority (lowest → highest):                                        │
│  1. Global file prompts    (MITTO_DIR/prompts/*.prompt.yaml)         │
│  2. Settings prompts       (settings.json .prompts)                  │
│  3. ACP server-specific    (prompts with acps: field + inline)       │
│  4. Workspace dir prompts  (.mitto/prompts/*.prompt.yaml)            │
│  5. Workspace inline       (.mittorc prompts section)                │
│                                                                      │
│  Filters: enabled:false removed, enabledWhen evaluated               │
└──────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              Frontend: predefinedPrompts = workspacePrompts
              (No client-side merge — backend does everything)
```

## Prompt File Format (`.prompt.yaml`)

```yaml
name: "Review Code"
description: "Review code for quality"
group: "Code Quality"
backgroundColor: "#4a90d9"
enabled: true
enabledWhen: "ACP.MatchesServerType('augment') && Tools.HasPattern('filesystem_*')"
prompt: |
  Please review the following code for quality, readability, and potential bugs.
```

**Removed fields**: `enabledWhenACP` and `enabledWhenMCP` have been fully removed from the codebase. If encountered in old code or docs, replace with equivalent `enabledWhen` CEL expressions. The old `requires:` string field and its frontend counterparts (string-capability gating) are also gone — replaced by the typed `parameters:` system below.

## Typed Parameters & Type-Based Menu Gating

### parameters: field

Prompts may declare typed inputs via a `parameters:` list. Each entry:

```yaml
parameters:
  - name: IssueID       # variable used as ${IssueID} in the prompt body
    type: beadsId       # one of the predefined types
    description: "..."  # optional
    required: true      # optional bool — controls menu gating:
                        #   absent/true → gates menu visibility (default)
                        #   false       → optional: auto-fills when menu supplies it,
                        #                 but never hides the prompt; no blocking form
    multiLine: true     # optional bool — only valid for type: text. Renders a
                        #   resizable multi-line textarea instead of a single-line
                        #   input. Rejected at load on any other type.
```

### Predefined types (canonical registry: `internal/prompts/param_types.go`)

Frontend mirror: `KNOWN_PARAM_TYPES` in `web/static/utils/prompts.js`. Both must stay in sync.

| Type | Description |
| ---- | ----------- |
| `beadsId` | Beads issue ID (e.g. `"mitto-42"`). Auto-filled by `beadsIssues` menu. |
| `beadsTitle` | Beads issue title. Auto-filled by `beadsIssues` menu. |
| `sessionId` | Mitto conversation/session UUID. |
| `childSessionId` | Child conversation/session UUID (relative to host). Auto-filled in `conversation` menu when the host has exactly one non-archived child; otherwise the picker is scoped to the host's children. Valid only in `prompts`/`conversation` menus. |
| `workspaceId` | Mitto workspace UUID. |
| `workspaceFolder` | Absolute path to a workspace root directory. |
| `text` | Generic free-form text (catch-all). Renders as a single-line input by default; add `multiLine: true` to render a resizable multi-line textarea, or `options: [...]` to constrain the value to a fixed enumeration rendered as a dropdown (mutually exclusive with `multiLine`). With a non-empty `options` list the param counts as an interactive picker (see below). |
| `boolean` | Yes/no flag, rendered as a checkbox. Supplied as the string `"true"`/`"false"` (default unchecked → `"false"`). Never gates menu visibility; always collected via the dialog. |
| `filename` | Workspace-relative file path, rendered as a dropdown of files under an optional `dir` (workspace-relative), optionally filtered by a `glob` **list** (`["*.md"]`, `["**/*.md", "**/*.rst"]` — union semantics; any listed pattern matches). Never gates menu visibility; always collected via the dialog (like `boolean`/`prompts`). Feeds `{{ ReadFile .Args.NAME }}` directly. `dir`/`glob` are UI hints only — path safety is enforced at read time by `ReadFile` (absolute-path/`..`/symlink-escape rejection, 256 KB cap). **Breaking (mitto-ebb):** scalar `glob: "*.md"` is rejected; use list form. |
| `dirname` | Workspace-relative directory path, rendered as a dropdown of sub-directories under an optional `dir` (workspace-relative), optionally filtered by a `glob` **list** applied to the base name (or full workspace-relative path with `**`) with union semantics. Never gates menu visibility; always collected via the dialog (like `filename`). Hidden directories (leading `.`) are excluded by default. `dir`/`glob` are UI hints only — the endpoint (`GET /api/workspace-dirs`) re-enforces path safety (absolute-path/`..`/symlink-escape rejection). |

### Type-based menu gating

Prompt shown in menu **M** only when M supplies **every required** declared type. Frontend: `menuSatisfies(prompt, menu)`. Menu types: `beadsIssues` → `{beadsId, beadsTitle}`; others supply none. See `MENU_PARAM_TYPES` in `web/static/utils/prompts.js`.

**Optional parameters** (`required: false`) never gate: the prompt appears in any menu regardless of whether the menu can supply the type. When the menu *can* supply it, the arg auto-fills via `collectPromptArguments`; when it cannot, the param is silently omitted and no dialog is shown (`getMissingPromptParameters` excludes optional params).

**Boolean parameters** (`type: boolean`) never gate either, regardless of `required`: a checkbox always has a definite answer. They are always collected via the dialog (`getMissingPromptParameters` always includes them) and never block **Save**; the value is emitted as the string `"true"`/`"false"` (default unchecked → `"false"`).

**Interactive picker parameters** (`isInteractivePickerParam` in `web/static/utils/prompts.js`: `boolean`, `prompts`, `filename`, `dirname`, and `text` **with a non-empty `options` list**) are all dialog-collected and never gate menu visibility — no menu context can auto-supply them. The dialog offers each picker unconditionally; a `filename`/`dirname` param whose backing folder is empty falls back to a text input so the user can still type a path.

**Options pickers** (`isOptionsPickerParam`, mitto-cwz.1): a `type: text` param with a non-empty `options` array renders as a dropdown, so it behaves like the other pickers — always collected, never gating. Because a dropdown has no empty state, `PromptParameterDialog` seeds the declared `default` into the initial values on open (declared defaults only; plain free-text defaults are still *not* auto-seeded). Any `initialValues` entry (e.g. a remembered value) overrides the declared default.

## Context-Adaptive Prompts (Three Modes)

**When to use**: a prompt that should work both from a specific bead *and* from
a plain conversation with no pre-selected issue.

**Four-point recipe**:

1. `menus: beadsIssues, conversation` — appears in both surfaces.
2. Typed param with `required: false` — never hides the prompt from any menu.
3. `$target` ladder at the top of the body (`.Session.BeadsIssue` →
   `.Args.IssueID` → mode 3: current problem, zero `bd` calls).
4. Gate **every** `bd` command and id-specific `git grep` behind
   `{{ if $target }} … {{ end }}` — mode 3 must emit **zero** `bd` calls.

**Exemplars**: `beads-issue-investigate`, `beads-issue-assess`,
`beads-issue-status`, `beads-issue-resolved`, `beads-issue-work`.

**Guard tests**: `*ThreeModeTargetResolution` tests + `TestBuiltinPrompts_NoDeprecatedMittoVars`
in `internal/config/prompt_template_test.go`.

Full recipe: [docs/config/prompts.md § Context-adaptive prompts (three modes)](../docs/config/prompts.md#context-adaptive-prompts-three-modes).

## Key Types

`WebPrompt`: Name, Prompt, Description, Group, BackgroundColor, Icon, Source ("builtin"|"file"|"settings"|"workspace"), Enabled (*bool: nil=enabled, false=disabled), EnabledWhen (CEL, server-side only), Loop (non-nil = loop conversation), Singleton (bool: `true` = no concurrent conversation instances for this prompt in the same working dir; see below).

### Singleton Prompts (find-or-route)

A prompt with `singleton: true` must not have more than one non-archived conversation per working dir. A session records the prompt that created it in `session.Metadata.OriginPromptName` at create time (set on `POST /api/sessions` from `initial_prompt_name`/`origin_prompt_name`). When a singleton prompt is launched, `HandleCreateSession` (`internal/web/handlers/session_create.go`) scans existing non-archived sessions by `(WorkingDir, OriginPromptName)` under a keyed lock (`lockSingleton`); on a match it reuses that conversation instead of creating a new one — re-seeding the queue if idle, focus-only if busy — and responds with `reused: true`. The frontend threads `reused` through `useWebSocket.js` → `useConversationSeeding.js` and shows a "Reusing existing ..." toast instead of "Started ..." (`useBeadsIntegration.js`, `app.js`). Applied to the builtin beadsList maintenance prompts (overview, reevaluate, cleanup-stale, group-epics, status-all-inprogress) — deliberately **not** to "Start working on ready", since concurrent work-starting conversations are legitimate.

`PromptLoop` (YAML `loop:`): `value`/`unit`/`at` (schedule period), `maxIterations`, plus the on-completion fields `trigger` (`schedule` default | `onCompletion`), `delay` (int seconds for onCompletion; clamped to the global floor), and `maxDuration` (duration string e.g. `4h`; wall-clock cap from the first run). `MaxIterations` caps scheduled runs; effective cap = min(prompt maxIterations, config default 100, hardcoded 1000). Backend auto-disables (not archives) when either the iteration cap or `maxDuration` is hit. Also `mode` (`always` default | `optional`) and `default` (initial enabled state for `mode: optional`; nil/absent = **on**).

When a named prompt is spawned via `mitto_conversation_new` / `mitto_conversation_update`, `applyPromptLoopDefaultsToStartInput`/`ToUpdateInput` (`internal/mcpserver/prompt_loop_defaults.go`) fill every `loop_*` field the caller left unset from the `loop:` block; explicit caller values always win and `loop_apply_prompt_defaults: false` disables the merge. `promptLoopDefaultEnabled` mirrors the frontend's `promptLoopDefaultOn` (`web/static/utils/prompts.js`): `mode != optional` → on; `mode: optional` → nil/`true` on, explicit `false` off. The `LoopEnabled` fill is **strictly subtractive** — it only ever writes `false`, never forces `true` (mitto-ydj). The REST `PUT /api/sessions/{id}/loop` helper deliberately excludes this fill: `session.LoopPrompt.Enabled` is a plain `bool` there, so unset is indistinguishable from `false`, and the frontend already applies the same logic client-side.

### `target.title` templating (mitto-5qbo)

Both prompt bodies AND `target.title` are Go text/templates rendered at dispatch time. `target.title` uses a **reduced context** (`prompts.PromptTargetContext`: only `.Args`, `.Session.BeadsIssue`, `.Workspace.Folder`) and a **stripped FuncMap** (`Arg`, `Default`, `Trim`, `Lower`, `Upper`, `Contains`, `HasPrefix`, `HasSuffix` — no `Cond`/`When`/`Model`/`HasBeads`, which require a full `PromptEnabledContext` not in scope at either dispatch entry point). Literal titles (no `{{`) hit a byte-for-byte fast path — every existing literal `target.title` is unaffected. Empty (or whitespace-only) renders are rejected pre-create so that templated titles like `"{{ .Args.IssueID }}: work"` can be combined with `reuse.title: true` (nested under `target.reuse`; mitto-6b3) to funnel per-argument buckets (same `IssueID` → same conversation; different `IssueID` → distinct). The three reuse-mode flags — `reuse.issue`, `reuse.title`, `reuse.coalesce` — all live under `target.reuse`; the legacy flat form (`target.reuseIssue` / `reuseTitle` / `reuseCoalesce`) is rejected by `ParsePromptFile` with a migration error. See `docs/devel/prompt-templates.md §11a`.

## Prompt Fragments (co-located `.tmpl` partials)

Shared body text can be factored into **fragments** invoked from any `.prompt.yaml` body via Go text/template's native `{{ template "name" . }}`. Two-extension convention, same directory tree as prompts: `*.prompt.yaml` → visible prompt (menus, actions, shortcuts); `*.tmpl` → hidden fragment (never in UI, only attached to the render-time template set). Isolation is structural — `LoadPromptsFromDir` filters strictly on `.prompt.yaml` and `LoadFragmentsFromDir` filters strictly on `.tmpl`; no code path converts one to the other. Names are slash-namespaced from the relative path with `.tmpl` stripped (`builtin/github/shared/pr-comments.tmpl` → `github/shared/pr-comments`). FuncMap and render context are inherited from the caller (`{{ template "x" . }}` for full context, `{{ template "x" .Args }}` to narrow). Unknown fragment names and cycles fail at `template.Parse` — free load-time validation with no custom code. fs-watcher tracks both extensions in the same tree. Layout convention: family-scoped shared fragments live under `<family>/shared/<name>.tmpl` (e.g. `github/shared/pr-comments.tmpl`, `support/shared/slack-tools.tmpl`, `beads-issues/shared/target-bead-header.tmpl`, `jira/shared/managed-body.tmpl`); genuinely cross-family fragments with no clear owner live under a top-level `_shared/` prefix (e.g. `_shared/session-context.tmpl`). Shipped fragments and per-candidate rejection rationale: `docs/devel/prompt-templates.md §11b`. Full spec: same section.

## Merging & Caching

`MergePrompts()` filters disabled; `MergePromptsKeepDisabled()` keeps `enabled:false` for dialogs. PromptsCache auto-refreshes `MITTO_DIR/prompts/` on changes.

## API & Toggle

`GET /api/workspace-prompts?dir=...&session_id=...` (fully merged), `include_global=true` (disabled too), `PUT /api/workspace-prompts/toggle-enabled` (toggle state). Disable: set `enabled: false` in `.mitto/prompts/*.prompt.yaml` or `.mittorc`. Re-enable: remove the `enabled: false` entry.

## Menu-Driven Prompt Sends (Named-Prompt Mechanism)

All menus (prompts, beadsIssues, beadsList) send `prompt_name` only — never the full body. Frontend helpers in `useConversationSeeding.js`: `seedConversationWithPrompt()` (existing session), `startConversationWithPrompt()` (new ± loop), `makeLoopNow()` (convert to loop). Backend resolves name at dispatch via `resolvePromptByName()` in target workspace context; the body is then **Go-template rendered** (if it contains `{{`) before `${VAR}` substitution. **Anti-pattern**: never POST resolved text to `/api/sessions/{id}/queue` — send `prompt_name` instead.

## MCP Prompt Tools

- `mitto_prompt_list` — List merged prompts (metadata)
- `mitto_prompt_get` — Get full prompt by name
- `mitto_prompt_update` — Create/update workspace-local overrides (`.mitto/prompts/<slug>.prompt.yaml`)

Updates replicate the 5-layer REST API merge. Name slugification via `config.SlugifyPromptName()`.

## Frontend & Builtin Conventions

**Frontend**: Never merge client-side — backend does all merging. Refetch on: file changes, visibility change, 30s interval (session-scoped CEL filters like `Session.IsChild` trigger refetch on activeSessionId change).

**Never call `/api/workspace-prompts` directly from a component or hook** — always go through `fetchWorkspacePromptsCached` (`web/static/utils/promptsCache.js`, mitto-8x9). Each request re-evaluates every prompt's `enabledWhen`, so bursts (menu opens, re-renders, `beads_changed` fan-out, one call per beads row) must be coalesced. Three layers, keyed on canonicalized params (`working_dir`, `session_id`, `enabled_context`, `item_*`): 3s TTL → in-flight promise dedup → `If-Modified-Since`/304 revalidation. Pass `{ force: true }` only where staleness would be wrong (workspace change, session switch, `mitto:prompts_changed`); that event handler is trailing-edge debounced 250ms in `useWorkspacePrompts.js` since one on-disk change fans out over `prompts_changed` + `mcp_tools_available` + the server's post-re-verify re-broadcast. After a mutation, call `invalidateWorkspacePromptsCache(workingDir?)`. Keep `Last-Modified` bookkeeping inside the cache — holding it as hook state changes the fetcher's identity on every response and re-arms the 30s/visibility effects.

**Builtin content**: Prefer **Go template syntax** (`{{ .Session.ID }}`, `{{ if .Session.IsChild }}...{{ end }}`, `{{ if Cond "..." }}...{{ end }}`) for new and edited builtin prompt bodies. `@mitto:*` tokens are **deprecated in prompt bodies** (a non-fatal warning is logged at load/save) — EXCEPT for the keep-list tokens (`@mitto:available_acp_servers`, `@mitto:children`, `@mitto:mcp_children`, `@mitto:user_data`, `@mitto:user_data_schema`) which have no template equivalent yet and do not trigger a warning. `@mitto:` stays fully supported in **processors** (not deprecated there). See `docs/devel/prompt-templates.md` for the full engine spec and `docs/config/prompts.md#go-template-syntax-in-prompts` for the user-facing reference and migration table. Cross-session UI: propose best plan, confirm via `mitto_ui_options(allow_free_text: true)`.

## enabledWhen Filtering & Preferred Models

Server-side via `filterPromptsByEnabled()` / `buildPromptEnabledContext()`. Use `enabledWhen` (CEL) exclusively. Full CEL context: see `05-msghooks.md`. Available helpers in both `enabledWhen` (CEL) and Go-template bodies — full catalogue in `internal/cel/templatefuncs.go` (`BuildTemplateFuncMap`); user-facing reference in [docs/config/prompts.md](../../docs/config/prompts.md#go-template-syntax-in-prompts):

- **Filesystem / path**: `FileExists(path)`, `DirExists(path)`, `ReadFile(path)` (workspace-relative, size-capped, path-safe; fail-open empty string, contents inlined **verbatim**), `ReadTemplate(path, .)` (same read semantics as `ReadFile`, but the file body is then **sub-rendered** as a Go text/template against the current context — the included file may reference `{{ .Args.X }}` / `{{ .Session.* }}` / any FuncMap helper; render step is **fail-closed** on parse/exec errors; recursion-capped at depth 3; fragments ARE attached in the sub-render, same as `PromptTextWithArgs`, mitto-twa), `Dir(path)` (forward-slash `path.Dir` — derive a sibling path from a workspace-relative argument, e.g. `{{ Dir .Args.Test }}/cleanup.md`). `FileExists`/`DirExists` **auto-detect** glob metacharacters (`* ? [ {`): with any of those in `path` they switch to a bounded workspace walk (via `internal/pathglob`, `maxResults=1`, 2 s timeout, hidden/`node_modules`/`vendor`/`dist`/`build`/`out`/`target` pruned) and report whether ANY match exists (e.g. `DirExists ".beads"` stays O(1) stat; `FileExists "**/*.go"` walks). Fail-open on cap/deadline (returns true). Absolute globs and `..`-escapes return `false`. Glob results are memoised for 30 s per `(folder, pattern, wantFiles)`, and concurrent misses on the same key collapse into a single walk via `singleflight` (mitto-ayl — at the observed `workspace-prompts` request rate the previous 5 s TTL expired between requests, so the memo never hit and every shared gate re-walked the workspace once per evaluation).
- **Command**: `CommandExists(name)` (looks up an executable on `PATH`).
- **Git**: `GitRepo([path])`, `GitFileModified(path)`, `GitDirModified([path])`, `GitStatusFiles([path])` (porcelain lines), `GitFileTracked(path)`, `GitFileDeleted(path)`.
- **Beads** — aggregate `BeadsCount` / `HasBeads` covered in the paragraph below; single-bead helpers here: `BeadHasLabels(id, labels)` (ALL match), `BeadIsOpen(id)`, `BeadMetadata(id, key)`. All fail-open.
- **Tools / session**: `Tools.HasPattern("github_*")` (CEL) / `HasPattern("...")` (template), `Model("smart")` — see the `Session.ModelTags` paragraph below.
- **Prompt composition**: `PromptText(name)` inlines another workspace prompt's body verbatim (mitto-85y.3; fail-closed on unknown name); `PromptTextWithArgs(name, args)` fetches the body and **sub-renders** it against a fresh `{{ .Args.X }}` scope (mitto-47y.1) — pair with `ArgsMap "FieldName"` which decodes a JSON-encoded `map[string]string` from `.Args.FieldName` (empty/absent → empty non-nil map, malformed → fail-closed). Sub-renders **do** attach `{{ template "_shared/..." }}` fragments (mitto-twa; lifts the earlier Phase-A limitation) and are recursion-capped at depth 3. `dict "k" v ...` builds a `map[string]any` for `{{ template "x" (dict ...) }}` fragment calls.
  - **Nested `_Args` mirroring** (mitto-47y.4): the JSON-encoded companion key `<PickerName>_Args` inherits the well-known "mirror between `arguments` and `loop_arguments`" rule (memory `mitto-rtdr`) — an MCP caller that spawns a looped child MUST pass the `_Args` value in BOTH maps, otherwise loop re-fires render inner values empty. The dispatcher logs a targeted WARN (fail-open) when an MCP/agent-origin dispatch supplies the bare picker key (e.g. `Prompt`) but omits the sibling `Prompt_Args` — the render still proceeds and just yields empty inner values, but the log line names the missing key so the mirroring bug surfaces quickly.
- **Conditionals**: `Cond "<CEL>"` / `When "<CEL>"` (alias) — evaluate a CEL expression against the full enabled-context inside a Go template.
- **Argument access**: `.Args.NAME` (missing → `""`), `Arg "NAME" "default"` (default-on-empty), `Default v fallback`.
- **String utilities**: `Trim`, `Lower`, `Upper`, `Contains`, `HasPrefix`, `HasSuffix`, `Join(sep, elems)`.

### Nested prompt parameters (`type: prompts`)

Pattern origin: epic `mitto-47y` — Phase A `mitto-47y.1` shipped the backend two-pass sub-render helpers, Phase B `mitto-47y.2` shipped the frontend PromptParameterDialog collection path, Phase C `mitto-47y.3` shipped `remember:folder` persistence for inner args, Phase D `mitto-47y.4` shipped the missing-`_Args` dispatch WARN.

**Decision guide — `PromptText` vs `PromptTextWithArgs`**:

- Use **`PromptText "<name>"`** when the referenced prompt is **argument-free** (or when you deliberately want unsubstituted `{{ .Args.X }}` placeholders to surface as literal text in the composed output). Verbatim inline; no sub-render; no fresh `.Args` scope. Cheapest option — prefer it whenever the composed prompt has no `parameters:` of its own.
- Use **`PromptTextWithArgs "<name>" <argsMap>`** when the referenced prompt **has parameters** (declares `parameters:` and reads them via `{{ .Args.X }}`) and you want those placeholders resolved. The helper fetches the body and **sub-renders** it against a fresh `.Args` scope built from `<argsMap>` (a `map[string]string`) — so the inner `{{ .Args.X }}` reads from the caller-supplied map, not the outer render's `.Args`.

**Wire format for `type: prompts` fields**: a picker parameter named `Prompt` emits two entries into the outer `.Args` at dispatch:

- `.Args.Prompt` — the picked prompt's **name** (string).
- `.Args.Prompt_Args` — a **JSON-encoded** `map[string]string` of the inner prompt's parameter values, decoded on the render side via `ArgsMap "Prompt_Args"` (empty/absent → empty non-nil map; malformed JSON → fail-closed).

Typical composition (inside the outer prompt's body):

```gotemplate
{{ PromptTextWithArgs .Args.Prompt (ArgsMap "Prompt_Args") }}
```

**Backend recursion cap**: sub-renders are capped at **depth 3** and **fail-closed** on overflow — cycles or accidental deep nesting are rejected at render time, not silently truncated. Sub-renders DO attach `{{ template "_shared/..." }}` fragments (mitto-twa; previously a Phase-A limitation, now lifted via the `internal/cel.SetFragmentProvider` hook so `internal/cel` still never imports `internal/prompts`).

**Inner `default:` values are NOT merged into the sub-render `.Args` scope** (audit `mitto-bz1`): a picked prompt's `.Args` contains only the keys the operator actually supplied via `<Picker>_Args`, never its own declared parameter defaults. So a picked-able prompt must gate on **default-on** comparisons (`{{ if ne .Args.X "false" }}`) rather than positive-match ones (`{{ if eq .Args.X "true" }}`), which silently resolve false when the key is absent — the same class of bug as `mitto-rtdr`.

**Audit of the builtin consumers** (`mitto-bz1`): exactly three builtin prompts declare a `type: prompts` parameter, and the three legitimate consumer shapes are pinned by `internal/prompts/type_prompts_picker_bz1_test.go` — `loop/prompt-until` (inline render via `PromptTextWithArgs` + `ArgsMap`), `beads-issues/loop-processing` Step 5H (spawn/delegate: the decoded `ArgsMap` forwarded as `arguments:` on `mitto_conversation_new`), and `misc/specialize-prompts` (name-only: the picked prompt is an *edit subject* for `mitto_prompt_get`/`mitto_prompt_update` and must never be rendered). A new picker consumer should match one of those three shapes; a bare `PromptText .Args.<Picker>` is always a bug (it silently drops the inner args).

**Frontend depth cap** (`mitto-47y.6.1`, v2 — supersedes the v1 depth-1 guard from `mitto-47y.2`): the PromptParameterDialog **recursively opens inner `type: prompts` pickers up to 3 nested picker levels**, matching backend `promptTextMaxDepth = 3` in `internal/cel/templatefuncs.go`. Level 0 is the outermost prompt; level-N pickers (N = 0, 1, 2) render their picker + inner block normally, and their `<Picker>_Args` companion is consumed by the backend via a `PromptTextWithArgs` sub-render at depth N+1. At level 3 — where the sub-render would exceed `promptTextMaxDepth` — the picker renders as the pre-existing disabled "nested prompt pickers are not supported here" note (defense-in-depth: the recursive submit serializer `buildInnerArgs` also skips level-3 pickers regardless of collected state, so the UI never sends args the backend would reject). The `MAX_NESTED_LEVEL = 3` constant at the top of `web/static/components/PromptParameterDialog.js` documents the pairing and MUST be kept in sync with `promptTextMaxDepth`.

**MCP wire shapes for picker values** (`mitto-47y.6.3`): the MCP boundary accepts **two** encodings for a `type: prompts` picker slot on `mitto_conversation_send_prompt` / `mitto_conversation_new` (both `arguments` and `loop_arguments`), and rewrites v2 → v1 before the value reaches the dispatcher or template engine (`normalizeMCPArguments` in `internal/mcpserver/prompt_normalize.go`).

- **v1 (canonical, still fully supported)**: two sibling entries in the outer args map — `Prompt: "<inner-name>"` + `Prompt_Args: "<json-string>"` (the JSON-encoded inner arguments). Pass-through, no rewrite.
- **v2 (new)**: a single JSON-encoded object in the picker slot — `Prompt: "{\"name\":\"<inner-name>\",\"arguments\":{\"X\":\"1\"}}"`. Detected by `type: prompts` + object-shape + non-empty `name`; anything else (bare strings, malformed JSON, JSON arrays, empty-name objects) falls through as v1. On success the outer key is rewritten to the inner name and the sibling `Prompt_Args` is synthesised from the inner arguments map — so the mirroring rule (memory `mitto-rtdr`) is satisfied structurally by the wire format itself (one payload → both mirrored slots produced by the normalizer). An existing sibling `Prompt_Args` is never overwritten (v1 wins on mixed input).

Round-trip equivalence is pinned by `TestNormalizeMCPArguments_RoundTrip_*` in `internal/mcpserver/prompt_normalize_test.go` (v2 → normalize → render must be byte-identical to v1 → render, using the real `cel.BuildTemplateFuncMap`). The v1-callers-missing-`_Args` WARN from Phase D still fires — v2 payloads never trip it because they arrive as a complete v1 shape at the dispatcher.

**MCP schema advertising** (same bead): `mitto_prompt_list` / `mitto_prompt_get` output carries a new `nested_prompt_schemas` field (`omitempty`) — a `map[picker-param-name] → map[inner-prompt-name] → []PromptParameter` catalog listing every workspace prompt (with parameters) that can be picked into each `type: prompts` slot, and their own parameter schemas. Absent on prompts without picker params or workspaces with no parametered inner prompts.

**Deprecation posture**: v1 remains fully supported and is the internal canonical shape (dispatcher, `PromptTextWithArgs` + `ArgsMap`, `session.LoopPrompt.Arguments`, `queue.AddWithOrigin` — all `map[string]string`-typed and unchanged). v2 is a **wire-format convenience** on the MCP surface for callers/agents that prefer a typed nested payload over a stringified JSON sibling. **No v1 removal is planned in this epic** — a separate follow-up bead will schedule the deprecation once agent adoption of v2 is measurable; until then both shapes are first-class.

**Beads gating (`BeadsCount` / `HasBeads`)**: query the workspace's `bd` (beads) DB from CEL AND Go templates. Both accept two comma-separated string args — `labels` (ALL match) and `statuses` (ANY match) — and run `bd list -l <labels> --status <statuses> --all --json` in `Workspace.Folder` (5s timeout, 5s in-memory cache). **Fail-open**: missing `bd`, non-zero exit, unparseable JSON, or timeout returns a positive sentinel (count=1 / true) so gated prompts are never wrongly hidden; a legitimate `[]` returns 0/false. Always short-circuit with cheap gates first so `bd` isn't exec'd when there's no DB: `CommandExists("bd") && DirExists(".beads") && HasBeads("support-question", "open,in_progress")`. Shared pure-Go helper (`beadsCount` in `internal/config/templatefuncs.go`) is the single source of truth for both surfaces — the CEL macros (`beadsCountMacro`, `hasBeadsMacro`) auto-inject `Workspace.Folder`.

**Per-conversation user data (`UserData`)**: exposed as a `map[string]string` in both the template context (`{{ UserData "NAME" }}` / `{{ index .UserData "NAME" }}`) and CEL (`UserData["NAME"]` / `"NAME" in UserData`), built from the same conversation attributes that back `Session.UserDataJSON`. Wired exactly like `Args` (struct field + `cel.Variable` + `buildActivation` normalization + template func), but populated at **both** menu time (`buildPromptEnabledContext`) and send time (`buildProcessorInput`) — the parity invariant — so menu gating and body rendering agree. Use it for set-if-unset, else-do-Y flows; the opaque `UserDataJSON` blob cannot drive a per-field conditional.

**Model capability tags (`Session.ModelTags`)**: the **current** model's tags, resolved from the `models:` profiles ([docs/config/models.md](../../docs/config/models.md)) via `config.ResolveModelTags(modelName)` — same `contains/exact/startsWith/regex/lookAlike` engine (`config.ConstraintMatchesName`) as ACP-server model constraints. Branch on capability, not brittle model-name strings: template `{{ if Model "smart" }}`, CEL `Session.HasModelTag("smart")` / `"smart" in Session.ModelTags`. Wired like `UserData` (parity at menu time via `BackgroundSession.CurrentModelName()` and send time via `pdGetAgentModels()`). Reflects the **baseline/active** model at render time, NOT a prompt's `preferredModels` (applied after render). Case-insensitive; degrades to empty (`Model("x") == false`, never errors) when the model is unknown or no profile matches.

### preferredModels Field

Prompts may declare preferred model(s) for auto-selection at prompt-dispatch time. Each entry is a **structured reference to a global model profile** (Settings → Models — see [docs/config/models.md](../../docs/config/models.md)) with **exactly one** of `modelName` / `modelTag`:

```yaml
preferredModels:
  - modelName: Claude Sonnet 4 # matches a profile by its `name` (case-insensitive)
  - modelTag: Coding           # selects any profile carrying this tag (case-insensitive)
```

- **`modelName`** — matches a global model profile by its `name` (case-insensitive equality).
- **`modelTag`** — selects any profile carrying that tag. Multiple profiles may share a tag; resolution is **deterministic by profile order** in the global `models:` list (first profile with the tag wins).
- Entries are **ordered, first-match-wins**: the backend tries each entry in order and stops at the first one that resolves to a profile whose criteria match an available model on the session's ACP server.

Backend calls `selectPreferredModel()` to pick the best matching active model. If the active model **already satisfies** the preference (i.e. its name matches the resolved profile's criteria), it is kept; otherwise the preference is applied. This enables smart routing of multi-model sessions without forcing model switches when not needed.

Old glob-string form (`- "*sonnet*"`) is **removed** — hard cutover, no fallback.

## Parameter Value Caching (`cache` block)

An optional `cache` sub-block on any `PromptParameter` enables per-conversation caching:

```yaml
parameters:
  - name: SlackChannel
    type: text
    cache:
      destination: memory   # only "memory" is valid in v1
      ttl: 1h               # optional Go duration; absent = conversation lifetime
```

- `destination` must be one of `KnownPromptCacheDestinations` (`"memory"` only in v1).
- `ttl` must be a positive Go duration if provided (`"0s"` / negative → validation error).
- Scoping is **per-conversation, per-parameter** — not global. Composite key `promptName\x00paramName` prevents prefix collisions.
- `Cache *PromptParameterCache` lives on `PromptParameter`; it flows through `ToWebPrompt` automatically (no change to `WebPrompt`).
- `ParsedTTL()` method on `*PromptParameterCache`: `"" → (0, nil)` (conversation lifetime), `"1h" → (time.Hour, nil)`, invalid → error.
- **Runtime dispatch** (mitto-pchx.3): inside `resolveAndSubstitute` in `prompt_dispatcher.go`, for each cacheable param BEFORE `SubstituteArguments`: (read/merge) if param is absent from `meta.Arguments` and a fresh cached value exists, it is injected; (write-back) every cacheable param present in `meta.Arguments` (including just-injected ones) is persisted with its TTL — this **refreshes** the TTL on each re-dispatch.
- **Status endpoint**: `GET /api/sessions/{id}/prompt-arg-cache?prompt=<name>` returns `{ "cached": ["A","B"] }` — **names only**, never values. Empty array when nothing cached (never null). Handler: `internal/web/handlers/session_prompt_arg_cache.go`.
- **Frontend dialog-skip** (mitto-pchx.5): before opening `PromptParameterDialog`, the frontend calls the status endpoint and subtracts cacheable+fresh params from the `missing` list (`fetchCachedParamNames` / `effectiveMissingParams` in `web/static/utils/prompts.js`). If nothing remains, it dispatches directly without showing the dialog.

## Parameter Value Remembering (`remember:` field, mitto-x8v)

Per-argument opt-in persistence of the last submitted value across conversations (contrast with `cache:`, which is per-conversation, in-memory, TTL-bound):

```yaml
parameters:
  - name: SlackChannel
    type: text
    remember: folder    # never (default) | folder | global (reserved, no storage yet)
```

- **Scope**: `folder` persists per `(workspace UUID, prompt name, arg name)`. `global` is enum-only in v1 (no storage path).
- **Storage**: one JSON per workspace UUID under `$MITTO_DIR/remembered-args/` (path via `appdir.RememberedArgsDir()`); atomic writes; RWMutex-guarded cache. `Store.Set()` **merges** into the existing per-workspace file so unrelated remembered args from other prompts are preserved.
- **Package**: `internal/rememberedargs/`. Wired into handlers via `Deps` closures (mirrors `ImprovePrompt` / `GenerateAuxTitle` in `internal/web/handlers/handlers.go`) — never a direct package import.
- **Write hook**: `handleAddToQueue` (`internal/web/handlers/queue.go`) saves best-effort (WARN + continue on failure) on every dispatch of a `remember: folder` arg.
- **Read endpoint**: `GET /api/workspace-prompts/remembered-args?working_dir=…&prompt=…` (`workspace_prompts_remembered.go`). MUST be registered before the `/{name}` sub-route to avoid path collision.
- **Frontend**: centralized in `web/static/components/PromptParameterDialog.js` (fetch-on-open effect merges over `initialValues`) — not replicated at the 12+ call sites. Remembered values **override** declared defaults.
- **Schema**: `Remember` field + `RememberNever|Folder|Global` constants + `IsValidRemember` validator in `internal/prompts/prompts.go` and `param_types.go`.

### Pitfalls

- `EnabledWhen` has `json:"-"` → settings override of a builtin loses `enabledWhen`. Merge logic must carry forward from lower-priority source.
- Never round-trip merged prompts via `POST /api/config` — set `prompts: []` explicitly. Backend must filter `req.Prompts` to `Source == PromptSourceSettings` only.
- Context-adaptive prompts: avoid `CommandExists("bd") && DirExists(".beads")` in `enabledWhen` — it hides the prompt exactly when mode 3 (conversation menu, no linked bead) applies.

## Iteration.IsUninterrupted (mitto-5xjn)

`{{ .Iteration.IsUninterrupted }}` is `true` only on a **scheduled** (non-forced, non-FreshContext) loop run that directly follows another such run with nothing in between — no user interjection, no forced "run now", no FreshContext, same process lifetime.

**Reset boundaries** (set marker to false):
- Archive/unarchive, GC suspend/resume, process restart
- ACP process reinit/restart
- Loop loop config change / pause / re-enable

**Authoring rule**: compact "continue" branch must carry durable re-anchor (one-line goal + file/bead ref). Always render verbose form when `IsFirst || !IsUninterrupted` to reset context after interruptions.

## CI Validation (mitto-11m)

Every PR runs `make check-prompts` in the `lint` job of `.github/workflows/tests.yml`. The umbrella target chains two validators:

- **`make check-model-tags`** — Go tests in `internal/config/` and `internal/processors/` verifying that every builtin prompt's `modelTag:` and every processor's `Session.HasModelTag("…")` resolves to a canonical tag in `config.CanonicalModelTags()`, and that `config/config.default.yaml`'s `models:` matches `config.DefaultModelProfiles()`.
- **`./mitto prompts verify`** — statically loads every fragment (`*.tmpl`) and prompt (`*.prompt.yaml`), validates YAML schema, precompiles Go templates and CEL expressions, and confirms every `{{ template "…" }}` reference resolves to a known fragment. Because `verify` reads from `MITTO_DIR/prompts/`, the target first runs `make build` and `./mitto prompts update-builtin --force` to deploy the embedded builtin tree.

Reproduce locally before pushing:

```bash
make check-prompts
```

A broken fragment reference, typo'd `modelTag`, or invalid prompt YAML fails CI before unit tests even run.
