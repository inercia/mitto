---
description: Command processors for pre/post processing, external command execution, message transformation
globs:
  - "internal/processors/**/*"
  - "config/processors/**/*"
---

# Processors Package

The `internal/processors` package provides three processor modes for message pre/post-processing. Processors are loaded from YAML in `MITTO_DIR/processors/` and the embedded `config/processors/builtin/` directory.

**Multi-document files:** A single `.yaml`/`.yml` file may contain multiple `---`-separated processor documents. Each document is validated and loaded independently; invalid or empty documents are skipped with a warning. For workspace processors in multi-document files, the per-workspace enable/disable toggle is recorded in `.mittorc` (processors override list) rather than edited in place — `UpdateProcessorFileEnabled` refuses multi-document files; use `SaveWorkspaceRCProcessorEnabled` instead. `IsMultiDocFile(path)` detects this case.

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
  on: userPrompt       # required: userPrompt | agentResponded | agentIdle
  match: first         # required: first | all | allExceptFirst (NOT all-except-first)
  rerun:               # optional; only valid with on:userPrompt + match:first
    afterSentMsgs: 15
    afterTokens: 50000
    afterTime: 1h
  # agentResponded/agentIdle-only (forbidden on userPrompt):
  stopReasons: [end_turn]   # default ["end_turn"]; valid: end_turn max_tokens max_turn_requests refusal cancelled
  excludeOrigins: []        # origins to skip: user queue periodic-runner mcp-send-prompt
  cadence:             # optional throttle; only valid with on:agentResponded|agentIdle + match:all/allExceptFirst
    everyNTurns: 3     # fire every N agent responses (pre-increment; everyNTurns:3 → turns 3,6,9,…)
    everyNTokens: 15000 # AND: after N cumulative tokens since last firing
    afterInterval: 5m  # AND: after this wall-clock duration since last firing
priority: 100          # lower = earlier
enabled: true          # false = never loads (build-time gate)
enabledWhen: 'acp.matchesServerType("augment") && !session.isPeriodic'  # CEL runtime gate
on_error: skip         # skip | fail

# Text-mode only (forbidden for agentResponded/agentIdle):
text: "static text"
mutate: prepend        # prepend | append — REQUIRED when text: is set

# Command-mode only:
command: ./script.sh
input: message         # message | conversation | none
output: prepend        # transform | prepend | append | discard
                       # transform/prepend/append FORBIDDEN for agentResponded/agentIdle
outputFormat: json     # json | raw — raw uses stdout verbatim (trimmed); command-mode only

# Prompt-mode only:
prompt: |
  Analyze these messages: @mitto:messages   # legacy; see note below
timeout: 300s
```

## Phase/Field Rules

`agentResponded` and `agentIdle` share **identical** field/output rules (column below). They differ only in *when* they fire: `agentResponded` fires after every turn; `agentIdle` fires only on the turn where the agent drains its queue and goes idle.

| Field / output              | `on: userPrompt`      | `on: agentResponded` / `on: agentIdle` |
| --------------------------- | --------------------- | -------------------------------------- |
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

`Manager.ApplyAfter()` runs both `agentResponded` and `agentIdle` processors; results (`ApplyAfterResult`) are consumed by `BackgroundSession.applyAfterProcessors()` → notifications via `OnNotification`, action buttons via the existing store, user-data via atomic write.

**`agentIdle` gating**: `BackgroundSession.processNextQueuedMessage()` returns `dispatched bool`; the prompt loop passes `sessionIdle = !dispatched` into `applyAfterProcessors` → `AfterProcessorInput.SessionIdle`. In `ApplyAfter`, an `agentIdle` processor is skipped when `!SessionIdle`, but its cadence counters are still pre-incremented (NOT reset), so a queued burst accumulates toward cadence and the processor fires once at the idle breakpoint. Use `agentIdle` for memory/insight processors that need the full exchange; use `agentResponded` for per-turn side effects.

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

| Processor             | Mode    | Purpose                            |
| --------------------- | ------- | ---------------------------------- |
| `session-context`     | text    | Prepend session metadata           |
| `check-mcp-tools`     | text    | Suggest MCP install if missing     |
| `delegate-to-coder`   | text    | Delegate work to coder session     |
| `beads-track-tasks`   | text    | Remind agent to track tasks in `bd` |
| `beads-prime`         | command | Inject `bd prime --memories-only`  |
| `auggie-manage-rules` | prompt  | Generate/update `.augment/rules/`  |
| `claude-manage-memory`| prompt  | Generate/update Claude memory      |
| `memorize-preferences`| prompt  | Save user prefs to `AGENTS.md`     |
| `identify-user-data`  | prompt  | Auto-fill workspace user data      |

## CEL Context for `enabledWhen`

Key CEL variables/functions (full reference in `docs/config/processors.md`):

| Context                 | Examples                                                                    |
| ----------------------- | --------------------------------------------------------------------------- |
| `acp.*`                 | `acp.matchesServerType("augment")`, `acp.name`, `acp.type`, `acp.tags`     |
| `session.*`             | `session.isPeriodic`, `session.isChild`, `session.id`                       |
| `workspace.*`           | `workspace.hasUserDataSchema`, `workspace.hasMittoRC`, `workspace.hasMetadataDescription`, `workspace.folder` |
| `children.*`            | `children.exists`, `children.count`, `children.mcp_count`, `children.promptingCount`, `children.idleCount` |
| `tools.*`               | `tools.hasPattern("mitto_*")`, `tools.hasAllPatterns(["a_*", "b_*"])`       |
| `commandExists(cmd)`    | `commandExists("git")`, `commandExists("docker")` — checks system PATH |
| `fileExists(path)`    | `fileExists("Makefile")`, `fileExists("go.mod")` — checks if file exists (not directory); workspace-relative |
| `dirExists(path)`     | `dirExists(".github")`, `dirExists("src")` — checks if directory exists; workspace-relative |

## Common Mistakes

- **Missing `on:` or `match:`** — both required
- **`all-except-first`** — use camelCase: `allExceptFirst`
- **Text-mode without `mutate:`** — required field
- **`rerun:` with `match: all`** — only valid with `match: first`
- **`cadence:` with `on: userPrompt`** — only valid with `agentResponded`/`agentIdle`
- **`cadence:` with `match: first`** — not needed; firing once requires no cadence
- **`cadence:` with no thresholds** — at least one field required
- `cadence` and `rerun` are mutually exclusive (different `on:` values)

## Defaults

`command` paths: `./`/`../` → processor dir; absolute; otherwise PATH. Defaults: `enabled=true`, `timeout=5s` (300s prompt-mode), `priority=100`, `input=message`, `output=transform`, `outputFormat=json`, `working_dir=session`, `on_error=skip`.
