---
description: Command processors for pre/post processing, external command execution, message transformation
globs:
  - "internal/processors/**/*"
  - "config/processors/**/*"
---

# Processors Package

The `internal/processors` package provides three processor modes for message pre/post-processing. Processors are loaded from YAML in `MITTO_DIR/processors/` and the embedded `config/processors/builtin/` directory.

> Full schema and CEL reference: `docs/config/processors.md`

## Three Processor Modes

Use **exactly one** of:

| Field     | Mode        | How it works                                                           |
| --------- | ----------- | ---------------------------------------------------------------------- |
| `text`    | Text        | Static string injected into message (no process)                       |
| `command` | Command     | External script executed; stdout transforms/prepends/appends message   |
| `prompt`  | Prompt-mode | Prompt sent to auxiliary AI agent — **fire-and-forget**, non-blocking  |

Prompt-mode processors are collected in a `pendingPrompts` slice during the pipeline run, then dispatched via `dispatchPromptBatch`:
- **Single processor**: dispatched directly with its own name (fire-and-forget goroutine)
- **Multiple processors**: combined into one prompt with a "We would like to fulfill the following requirements:" header, dispatched as a single batched request — only ONE auxiliary session is created

Prompt-mode processor auxiliary sessions have access to Mitto's MCP tools (e.g., `mitto_ui_notify`) via a stdio MCP proxy, when `ACPProcessManager.MCPServerURL` is set. See `42-mcpserver-development.md` for the wiring pattern.

## Key YAML Fields

```yaml
name: my-processor
when:                  # required block — BOTH on: and match: are required
  on: userPrompt       # required: userPrompt | agentResponded
  match: first         # required: first | all | allExceptFirst (NOT all-except-first)
  rerun:               # optional; only valid with on:userPrompt + match:first
    afterSentMsgs: 15
    afterTokens: 50000
    afterTime: 1h
  # agentResponded-only (forbidden on userPrompt):
  stopReasons: [end_turn]   # default ["end_turn"]; valid: end_turn max_tokens max_turn_requests refusal cancelled
  excludeOrigins: []        # origins to skip: user queue periodic-runner mcp-send-prompt
  cadence:             # optional throttle; only valid with on:agentResponded + match:all/allExceptFirst
    everyNTurns: 3     # fire every N agent responses (pre-increment; everyNTurns:3 → turns 3,6,9,…)
    everyNTokens: 15000 # AND: after N cumulative tokens since last firing
    afterInterval: 5m  # AND: after this wall-clock duration since last firing
priority: 100          # lower = earlier
enabled: true          # false = never loads (build-time gate)
enabledWhen: 'acp.matchesServerType("augment") && !session.isPeriodic'  # CEL runtime gate
on_error: skip         # skip | fail

# Text-mode only (forbidden for agentResponded):
text: "static text"
mutate: prepend        # prepend | append — REQUIRED when text: is set

# Command-mode only:
command: ./script.sh
input: message         # message | conversation | none
output: prepend        # transform | prepend | append | discard
                       # transform/prepend/append FORBIDDEN for agentResponded

# Prompt-mode only:
prompt: |
  Analyze these messages: @mitto:messages   # legacy; see note below
timeout: 300s
```

## Phase/Field Rules

| Field / output              | `on: userPrompt`      | `on: agentResponded`         |
| --------------------------- | --------------------- | ---------------------------- |
| `text:`                     | ✅                    | ❌ forbidden                 |
| `mutate:` (req w/ text)     | ✅                    | ❌ forbidden                 |
| `command:` / `prompt:`      | ✅                    | ✅                           |
| `when.rerun:`               | ✅ (match:first only) | ❌ forbidden                 |
| `when.stopReasons:`         | ❌ forbidden          | ✅ default `[end_turn]`      |
| `when.excludeOrigins:`      | ❌ forbidden          | ✅                           |
| `output: transform/prepend/append` | ✅           | ❌ forbidden                 |
| `output: discard`           | ✅                    | ✅                           |
| `output: notify`            | ❌                    | ✅ JSON `{title,message,style}` or plain text |
| `output: actionButtons`     | ❌                    | ✅ JSON `[{label,prompt},…]` |
| `output: userData`          | ❌                    | ✅ JSON `{key:value}` patch  |

`Manager.ApplyAfter()` runs agentResponded processors; results (`ApplyAfterResult`) are consumed by `BackgroundSession.applyAfterProcessors()` → notifications via `OnNotification`, action buttons via the existing store, user-data via atomic write.

**Enable layers**: `enabled: false` → never loaded; `enabledWhen` (CEL) → loaded but skipped at runtime. Both together = never loaded.

**`@mitto:messages`**: legacy substitution; builtins use `mitto_conversation_history` MCP tool directly instead. Do NOT add new `messages:` blocks to builtin YAML files.

## Builtin Processors (`config/processors/builtin/`)

All builtins are **`enabled: true` by default**. Disable per workspace in the Workspaces dialog or `.mittorc`:

```yaml
# .mittorc
processors:
  - name: memorize-preferences
    enabled: false
```

| Processor             | Mode    | `enabledWhen` condition                                         | Purpose                            |
| --------------------- | ------- | --------------------------------------------------------------- | ---------------------------------- |
| `session-context`     | text    | _(none)_                                                        | Prepend session metadata           |
| `check-mcp-tools`     | text    | `!tools.hasPattern("mitto_*")`                                  | Suggest MCP install if missing     |
| `delegate-to-coder`   | text    | _(varies)_                                                      | Delegate work to coder session     |
| `delegate-playwright` | text    | _(varies)_                                                      | Delegate Playwright tests          |
| `cleanup-children`    | command | _(varies)_                                                      | Archive stale child sessions       |
| `use-ui-tools`        | text    | `tools.hasPattern("mitto_ui_*")`                                | Remind agent to use Mitto UI tools |
| `auggie-manage-rules` | prompt  | `acp.matchesServerType("augment") && !session.isPeriodic && !dirExists(".augment/rules")` | Generate initial `.augment/rules/` files |
| `auggie-update-rules` | prompt  | `acp.matchesServerType("augment") && !session.isPeriodic && dirExists(".augment/rules")`  | Update rules from conversation insights (agentResponded, cadence: every 10 turns/40k tokens/10m) |
| `claude-manage-memory`| prompt  | `acp.matchesServerType("claude-code") && !session.isPeriodic && !fileExists("CLAUDE.md") && !dirExists(".claude")` | Generate initial memory files |
| `claude-update-memory`| prompt  | `acp.matchesServerType("claude-code") && !session.isPeriodic && (fileExists("CLAUDE.md") \|\| dirExists(".claude"))` | Update memory from conversation insights (agentResponded, cadence: every 15 turns/60k tokens/10m) |
| `memorize-preferences`| prompt  | `!session.isPeriodic`                                           | Save user prefs to `AGENTS.md` (agentResponded, cadence: every 5 turns/30k tokens/5m) |
| `identify-user-data`  | prompt  | `workspace.hasUserDataSchema && !session.isPeriodic`            | Auto-fill workspace user data fields (agentResponded, cadence: every 3 turns/15k tokens) |
| `identify-workspace-metadata` | prompt | `workspace.hasMittoRC && !workspace.hasMetadataDescription && !session.isPeriodic` | Auto-fill `metadata.description` and `metadata.url` in `.mittorc` |

## CEL Context for `enabledWhen`

Key CEL variables/functions (full reference in `docs/config/processors.md`):

| Context                 | Examples                                                                    |
| ----------------------- | --------------------------------------------------------------------------- |
| `acp.*`                 | `acp.matchesServerType("augment")`, `acp.name`, `acp.type`, `acp.tags`     |
| `session.*`             | `session.isPeriodic`, `session.isChild`, `session.id`                       |
| `workspace.*`           | `workspace.hasUserDataSchema`, `workspace.hasMittoRC`, `workspace.hasMetadataDescription`, `workspace.folder` |
| `tools.*`               | `tools.hasPattern("mitto_*")`, `tools.hasAllPatterns(["a_*", "b_*"])`       |
| `commandExists(cmd)`    | `commandExists("git")`, `commandExists("docker")` — checks system PATH |
| `fileExists(path)`    | `fileExists("Makefile")`, `fileExists("go.mod")` — checks if file exists (not directory); workspace-relative |
| `dirExists(path)`     | `dirExists(".github")`, `dirExists("src")` — checks if directory exists; workspace-relative |

## Skip Reasons & Common Pitfalls

| Log `reason=`       | Cause                                               |
| ------------------- | --------------------------------------------------- |
| `enabled_false`     | `enabled: false` in YAML, no workspace override     |
| `enabledWhen_false` | CEL expression evaluated to false                   |
| `when_mismatch`     | `match: first` processor on a non-first message     |

Common mistakes rejected by the loader:
- **Missing `on:` or `match:`** — both required
- **`sent:` key** — old syntax; use `on: userPrompt` + `match: <value>`
- **`all-except-first`** (kebab) — use `allExceptFirst` (camelCase)
- **Text-mode without `mutate:`** — required; `prepend` or `append`
- **`rerun:` with `match: all`** — only valid with `match: first`
- **`cadence:` with `on: userPrompt`** — only valid with `agentResponded` (rule 12)
- **`cadence:` with `match: first`** — not valid; firing once needs no cadence (rule 13)
- **`cadence:` with no threshold fields** — at least one must be set (rule 14)
- **Negative `everyNTurns`/`everyNTokens`** — must be non-negative (rule 15)
- **Unparseable `afterInterval`** — must be a valid Go duration string like `"5m"` (rule 16)

`cadence` and `rerun` are mutually exclusive by construction: `rerun` only applies to `on:
userPrompt` and `cadence` only applies to `on: agentResponded`.

## Defaults

`command` paths: `./`/`../` → processor dir; absolute; otherwise PATH. Defaults: `enabled=true`, `timeout=5s` (300s prompt-mode), `priority=100`, `input=message`, `output=transform`, `working_dir=session`, `on_error=skip`.
