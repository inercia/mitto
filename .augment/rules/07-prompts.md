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

### Predefined types (canonical registry: `internal/config/prompt_param_types.go`)

Frontend mirror: `KNOWN_PARAM_TYPES` in `web/static/utils/prompts.js`. Both must stay in sync.

| Type | Description |
| ---- | ----------- |
| `beadsId` | Beads issue ID (e.g. `"mitto-42"`). Auto-filled by `beadsIssues` menu. |
| `beadsTitle` | Beads issue title. Auto-filled by `beadsIssues` menu. |
| `sessionId` | Mitto conversation/session UUID. |
| `childSessionId` | Child conversation/session UUID (relative to host). Auto-filled in `conversation` menu when the host has exactly one non-archived child; otherwise the picker is scoped to the host's children. Valid only in `prompts`/`conversation` menus. |
| `workspaceId` | Mitto workspace UUID. |
| `workspaceFolder` | Absolute path to a workspace root directory. |
| `text` | Generic free-form text (catch-all). Renders as a single-line input by default; add `multiLine: true` to render a resizable multi-line textarea. |
| `boolean` | Yes/no flag, rendered as a checkbox. Supplied as the string `"true"`/`"false"` (default unchecked → `"false"`). Never gates menu visibility; always collected via the dialog. |

### Type-based menu gating

Prompt shown in menu **M** only when M supplies **every required** declared type. Frontend: `menuSatisfies(prompt, menu)`. Menu types: `beadsIssues` → `{beadsId, beadsTitle}`; others supply none. See `MENU_PARAM_TYPES` in `web/static/utils/prompts.js`.

**Optional parameters** (`required: false`) never gate: the prompt appears in any menu regardless of whether the menu can supply the type. When the menu *can* supply it, the arg auto-fills via `collectPromptArguments`; when it cannot, the param is silently omitted and no dialog is shown (`getMissingPromptParameters` excludes optional params).

**Boolean parameters** (`type: boolean`) never gate either, regardless of `required`: a checkbox always has a definite answer. They are always collected via the dialog (`getMissingPromptParameters` always includes them) and never block **Save**; the value is emitted as the string `"true"`/`"false"` (default unchecked → `"false"`).

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

**Exemplars**: `beads-issue-investigate`, `beads-issue-discuss`,
`beads-issue-status`, `beads-issue-resolved`, `beads-issue-work`.

**Guard tests**: `*ThreeModeTargetResolution` tests + `TestBuiltinPrompts_NoDeprecatedMittoVars`
in `internal/config/prompt_template_test.go`.

Full recipe: [docs/config/prompts.md § Context-adaptive prompts (three modes)](../docs/config/prompts.md#context-adaptive-prompts-three-modes).

## Key Types

`WebPrompt`: Name, Prompt, Description, Group, BackgroundColor, Icon, Source ("builtin"|"file"|"settings"|"workspace"), Enabled (*bool: nil=enabled, false=disabled), EnabledWhen (CEL, server-side only), Periodic (non-nil = periodic conversation), Singleton (bool: `true` = no concurrent conversation instances for this prompt in the same working dir; see below).

### Singleton Prompts (find-or-route)

A prompt with `singleton: true` must not have more than one non-archived conversation per working dir. A session records the prompt that created it in `session.Metadata.OriginPromptName` at create time (set on `POST /api/sessions` from `initial_prompt_name`/`origin_prompt_name`). When a singleton prompt is launched, `HandleCreateSession` (`internal/web/handlers/session_create.go`) scans existing non-archived sessions by `(WorkingDir, OriginPromptName)` under a keyed lock (`lockSingleton`); on a match it reuses that conversation instead of creating a new one — re-seeding the queue if idle, focus-only if busy — and responds with `reused: true`. The frontend threads `reused` through `useWebSocket.js` → `useConversationSeeding.js` and shows a "Reusing existing ..." toast instead of "Started ..." (`useBeadsIntegration.js`, `app.js`). Applied to the builtin beadsList maintenance prompts (overview, reevaluate, cleanup-stale, group-epics, status-all-inprogress) — deliberately **not** to "Start working on ready", since concurrent work-starting conversations are legitimate.

`PromptPeriodic` (YAML `periodic:`): `value`/`unit`/`at` (schedule period), `maxIterations`, plus the on-completion fields `trigger` (`schedule` default | `onCompletion`), `delay` (int seconds for onCompletion; clamped to the global floor), and `maxDuration` (duration string e.g. `4h`; wall-clock cap from the first run). `MaxIterations` caps scheduled runs; effective cap = min(prompt maxIterations, config default 100, hardcoded 1000). Backend auto-disables (not archives) when either the iteration cap or `maxDuration` is hit.

## Merging & Caching

`MergePrompts()` filters disabled; `MergePromptsKeepDisabled()` keeps `enabled:false` for dialogs. PromptsCache auto-refreshes `MITTO_DIR/prompts/` on changes.

## API & Toggle

`GET /api/workspace-prompts?dir=...&session_id=...` (fully merged), `include_global=true` (disabled too), `PUT /api/workspace-prompts/toggle-enabled` (toggle state). Disable: set `enabled: false` in `.mitto/prompts/*.prompt.yaml` or `.mittorc`. Re-enable: remove the `enabled: false` entry.

## Menu-Driven Prompt Sends (Named-Prompt Mechanism)

All menus (prompts, beadsIssues, beadsList) send `prompt_name` only — never the full body. Frontend helpers in `useConversationSeeding.js`: `seedConversationWithPrompt()` (existing session), `startConversationWithPrompt()` (new ± periodic), `makePeriodicNow()` (convert to periodic). Backend resolves name at dispatch via `resolvePromptByName()` in target workspace context; the body is then **Go-template rendered** (if it contains `{{`) before `${VAR}` substitution. **Anti-pattern**: never POST resolved text to `/api/sessions/{id}/queue` — send `prompt_name` instead.

## MCP Prompt Tools

- `mitto_prompt_list` — List merged prompts (metadata)
- `mitto_prompt_get` — Get full prompt by name
- `mitto_prompt_update` — Create/update workspace-local overrides (`.mitto/prompts/<slug>.prompt.yaml`)

Updates replicate the 5-layer REST API merge. Name slugification via `config.SlugifyPromptName()`.

## Frontend & Builtin Conventions

**Frontend**: Never merge client-side — backend does all merging. Refetch on: file changes, visibility change, 30s interval (session-scoped CEL filters like `Session.IsChild` trigger refetch on activeSessionId change).

**Builtin content**: Prefer **Go template syntax** (`{{ .Session.ID }}`, `{{ if .Session.IsChild }}...{{ end }}`, `{{ if Cond "..." }}...{{ end }}`) for new and edited builtin prompt bodies. `@mitto:*` tokens are **deprecated in prompt bodies** (a non-fatal warning is logged at load/save) — EXCEPT for the keep-list tokens (`@mitto:available_acp_servers`, `@mitto:children`, `@mitto:mcp_children`, `@mitto:user_data`, `@mitto:user_data_schema`) which have no template equivalent yet and do not trigger a warning. `@mitto:` stays fully supported in **processors** (not deprecated there). See `docs/devel/prompt-templates.md` for the full engine spec and `docs/config/prompts.md#go-template-syntax-in-prompts` for the user-facing reference and migration table. Cross-session UI: propose best plan, confirm via `mitto_ui_options(allow_free_text: true)`.

## enabledWhen Filtering & Preferred Models

Server-side via `filterPromptsByEnabled()` / `buildPromptEnabledContext()`. Use `enabledWhen` (CEL) exclusively. Full CEL context: see `05-msghooks.md`. Useful functions: `FileExists(".git/config")`, `CommandExists("gh")`, `Tools.HasPattern("github_*")`.

**Per-conversation user data (`UserData`)**: exposed as a `map[string]string` in both the template context (`{{ UserData "NAME" }}` / `{{ index .UserData "NAME" }}`) and CEL (`UserData["NAME"]` / `"NAME" in UserData`), built from the same conversation attributes that back `Session.UserDataJSON`. Wired exactly like `Args` (struct field + `cel.Variable` + `buildActivation` normalization + template func), but populated at **both** menu time (`buildPromptEnabledContext`) and send time (`buildProcessorInput`) — the parity invariant — so menu gating and body rendering agree. Use it for set-if-unset, else-do-Y flows; the opaque `UserDataJSON` blob cannot drive a per-field conditional.

**Model capability tags (`Session.ModelTags`)**: the **current** model's tags, resolved from the `models:` profiles ([docs/config/models.md](../../docs/config/models.md)) via `config.ResolveModelTags(modelName)` — same `contains/exact/startsWith/regex/lookAlike` engine (`config.ConstraintMatchesName`) as ACP-server model constraints. Branch on capability, not brittle model-name strings: template `{{ if Model "smart" }}`, CEL `Session.HasModelTag("smart")` / `"smart" in Session.ModelTags`. Wired like `UserData` (parity at menu time via `BackgroundSession.CurrentModelName()` and send time via `pdGetAgentModels()`). Reflects the **baseline/active** model at render time, NOT a prompt's `preferredModels` (applied after render). Case-insensitive; degrades to empty (`Model("x") == false`, never errors) when the model is unknown or no profile matches.

### preferredModels Field

Prompts may declare preferred model(s) for auto-selection at prompt-dispatch time. Each entry is a **structured reference to a global model profile** (Settings → Models — see [docs/config/models.md](../../docs/config/models.md)) with **exactly one** of `modelName` / `modelTag`:

```yaml
preferredModels:
  - modelName: Claude Sonnet   # matches a profile by its `name` (case-insensitive)
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

### Pitfalls

- `EnabledWhen` has `json:"-"` → settings override of a builtin loses `enabledWhen`. Merge logic must carry forward from lower-priority source.
- Never round-trip merged prompts via `POST /api/config` — set `prompts: []` explicitly. Backend must filter `req.Prompts` to `Source == PromptSourceSettings` only.
- Context-adaptive prompts: avoid `CommandExists("bd") && DirExists(".beads")` in `enabledWhen` — it hides the prompt exactly when mode 3 (conversation menu, no linked bead) applies.

## Iteration.IsUninterrupted (mitto-5xjn)

`{{ .Iteration.IsUninterrupted }}` is `true` only on a **scheduled** (non-forced, non-FreshContext) periodic run that directly follows another such run with nothing in between — no user interjection, no forced "run now", no FreshContext, same process lifetime.

**Reset boundaries** (set marker to false):
- Archive/unarchive, GC suspend/resume, process restart — auto-reset because BackgroundSession is recreated.
- ACP process reinit/restart (`restartACPProcess`).
- Periodic loop config change (`PUT /api/sessions/{id}/periodic`).
- Periodic loop pause or re-enable (`PATCH /api/sessions/{id}/periodic`).

**Authoring rule**: the compact "continue" branch MUST carry a durable re-anchor — a one-line goal restatement plus a pointer to the on-disk state file or linked bead — because long loops compact history. Always render the verbose form whenever `IsFirst || !IsUninterrupted`:

```
{{ if .Iteration.IsFirst }}
  ...verbose full-context form...
{{ else if .Iteration.IsUninterrupted }}
  Continue: <one-line goal>. State: <file or bead ref>.
  ...compact delta-only instructions...
{{ else }}
  ...verbose full-context form (interrupted or restarted)...
{{ end }}
```
