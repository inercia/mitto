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
| `prompt`  | Prompt-mode | Prompt sent to a tracked auxiliary AI agent in a background worker    |

Prompt-mode processors are collected in a `pendingPrompts` slice during the pipeline run, then dispatched via `dispatchPromptBatch`:
- **Single processor**: dispatched directly with its own name (background goroutine)
- **Multiple processors**: combined into one prompt with a "We would like to fulfill the following requirements:" header, dispatched as a single batched request — only ONE auxiliary session is created

The pipeline remains non-blocking, but the worker is completion-aware: it persists a stable dispatch ID before the first auxiliary RPC, waits for a matching `MITTO_PROCESSOR_COMPLETION` response with a durable save count, and removes the spool entry only after terminal success. Failure releases the claim for retry; a process restart can reclaim an unacknowledged entry. For `conversationClosed` prompt processors, `SessionManager` synchronously captures the last 50 user/agent events before a delete can remove the session, and the processor layer appends that bounded immutable snapshot to the durable prompt. Pending-dispatch spool files are mode `0600`; acknowledgement or terminal eviction removes the snapshot with its entry. If capture fails, prompt-mode close work is not dispatched or acknowledged as a successful zero-save run.

**Dispatch admission control (mitto-hjx)**: `dispatchWithRetry` serializes its actual RPC-issuing retry window (`runDispatchRetryLoopTracked`) behind a workspace-keyed `sync.Mutex` — package-global `dispatchAdmissionGates` map, entered via `admitDispatch(workspaceUUID)`. Without this gate, a post-turn cluster of near-simultaneous `dispatchPromptBatch` fan-outs (e.g. multiple loop-processing children closing together, each firing several close-phase prompt-mode processors) all race the shared ACP process's threshold-1 proactive concurrent-RPC gate (`auxSessionCreateBusyRPCThreshold`, `internal/acpproc/acp_process_manager.go`) at once. Only one wins the slot; the rest return `acperrors.ErrProcessBusy`, which is deliberately EXCLUDED from `isSaturationDispatchErr`'s long-wait policy (`ErrProcessBusy` is a transient concurrency-load bail, not GC-recycle-shaped — mitto-xhsj), so losers only get the short ordinary retry budget (`dispatchPromptMaxRetries`) and their attempts collide again on every retry, amplifying the storm. The gate MUST be process-global (not a `*Manager` field): `SessionManager.ApplyOnCloseProcessors` clones a fresh `*Manager` per close, so a per-instance mutex would fail to serialize sibling dispatches for the same workspace. The gate wraps ONLY the RPC-issuing window, NOT the pre-flight `AppendClaimed` durable persistence, so a crash before RPC still leaves a recoverable spool entry. `FlushPendingDispatches` is already sequential per workspace and is unaffected. Any new fan-out of prompt-mode dispatches MUST either route through `dispatchWithRetry` or add its own admission control — do NOT try to fix a recurrence by widening `isSaturationDispatchErr` to include `ErrProcessBusy`, and do NOT retune `dispatchPromptMaxRetries` / `dispatchSaturationRetryInterval` / `dispatchSaturationMaxWait` (they are tuned for other failure classes: mitto-xhsj, mitto-rcro, mitto-e3ut.1, mitto-nnte, and are pinned by adjacent tests). Regression: `TestDispatchPromptBatch_PostTurnCluster_NoAdmissionControl_StampedesSharedProcess` in `internal/processors/apply_admission_control_test.go`.

**ErrProcessBusy busy-wait retry symmetry (mitto-hjx Facet B, commit d4b9abcc)**: `dispatchWithRetry` and `FlushPendingDispatches` must BOTH wrap their `runDispatchRetryLoopTracked` call in an outer busy-wait loop keyed on `errors.Is(lastErr, acperrors.ErrProcessBusy)` — first-encounter deadline init from the dispatch's own `timeout`, polling on `pendingDispatchBusyRetryInterval` (100ms), break on non-Busy terminal error or deadline expiry. Without the outer loop, a sustained-but-clearing busy window exhausts only the short ordinary retry budget (~6s) and persists the batch to the durable spool at ERROR instead of riding it out. `FlushPendingDispatches` already had the pattern; the fix mirrored it into `dispatchWithRetry`. Do NOT touch `runDispatchRetryLoopTracked`'s internal classification (`isSaturationDispatchErr`, `isNonRetryableDispatchErr`) — the busy-wait is an OUTER wrapper. Any future dispatch path added to `internal/processors` that calls `runDispatchRetryLoopTracked` MUST replicate the same outer busy-wait pattern for symmetry. Regression: `TestDispatchWithRetry_SustainedProcessBusy_PersistsInsteadOfRidingOutBusyWindow` in `internal/processors/apply_busy_budget_test.go`.

Prompt-mode processor auxiliary sessions have access to Mitto's MCP tools (e.g., `mitto_ui_notify`) when `ACPProcessManager.MCPServerURL` is set. Transport is capability-gated (mitto-8ip): if the agent advertised `mcp_capabilities.http` at init, a native `McpServerHttpInline` pointing at the same `MCPServerURL` user sessions use is emitted (no subprocess); otherwise the stdio `mitto mcp --proxy-to <url>` bridge is emitted as the ACP-spec mandatory-transport fallback. See `42-mcpserver-development.md` for the wiring pattern.

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
  stopReasons: [end_turn]   # default ["end_turn"]; origins to skip: excludeOrigins: [user, queue, ...]
  cadence:             # optional throttle; only valid with on:agentResponded|agentIdle + match:all/allExceptFirst
    everyNTurns: 3     # fire every N responses; everyNTokens: 15000; afterInterval: 5m (all AND-logic)
priority: 100          # lower = earlier
enabled: true          # false = never loads (build-time gate)
enabledWhen: 'acp.matchesServerType("augment") && !session.isLoop'  # CEL runtime gate
onError: skip          # skip | fail

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

# Prompt-mode parameters (declare typed ${VAR} inputs; mandatory non-empty default):
parameters:
  - name: HistoryLimit
    type: text   # beadsId|beadsTitle|sessionId|childSessionId|workspaceId|workspaceFolder|acpServer|text|boolean
    default: "10"  # REQUIRED — missing default is a load error (red badge in UI)
```

**Prompt-mode `${VAR}` substitution**: `${NAME}` → value or `""`; `${NAME:-fallback}` → value if set AND non-empty, else fallback; `\${NAME}` → literal. Resolution: declared `default` first, then per-workspace `.mittorc` `arguments:` overlay. Override values are saved in Workspaces → Processors (Save button) → `.mittorc` `processors: [{name, arguments: {k: v}}]`.

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
| `beads-prime`         | command | Inject memory-key index for on-demand recall |
| `auggie-manage-rules` | prompt  | Generate/update `.augment/rules/`  |
| `claude-manage-memory`| prompt  | Generate/update Claude memory      |
| `memorize-preferences`| prompt  | Save user prefs to `AGENTS.md`     |
| `identify-user-data`  | prompt  | Auto-fill workspace user data      |

## CEL Context for `enabledWhen`

Key CEL variables/functions (full reference in `docs/config/processors.md`):

| Context                 | Examples                                                                    |
| ----------------------- | --------------------------------------------------------------------------- |
| `acp.*`                 | `acp.matchesServerType("augment")`, `acp.name`, `acp.type`, `acp.tags`     |
| `session.*`             | `session.isLoop`, `session.isChild`, `session.id`                       |
| `Session.ModelTags`     | `Session.HasModelTag("smart")`, `"smart" in Session.ModelTags` — current model's tags from `models:` profiles (template: `{{ if Model "smart" }}`); empty when model unknown |
| `workspace.*`           | `workspace.hasUserDataSchema`, `workspace.hasMittoRC`, `workspace.hasMetadataDescription`, `workspace.folder` |
| `children.*`            | `children.exists`, `children.count`, `children.mcp_count`, `children.promptingCount`, `children.idleCount` |
| `tools.*`               | `tools.hasPattern("mitto_*")`, `tools.hasAllPatterns(["a_*", "b_*"])`       |
| `commandExists(cmd)`    | `commandExists("git")`, `commandExists("docker")` — checks system PATH |
| `fileExists(path)`      | `fileExists("Makefile")`, `fileExists("go.mod")` — checks if file exists (not directory); workspace-relative |
| `dirExists(path)`       | `dirExists(".github")`, `dirExists("src")` — checks if directory exists; workspace-relative |

**`tools.*` fail-open behavior:** return `true` when tool list is unknown (warm-up); evaluate against real list once fetched. **Processors always see known tools** (fail-open disabled internally).

## Common Mistakes
- Missing `on:` or `match:` — both required; use camelCase `allExceptFirst` (not `all-except-first`)
- Text-mode: `mutate:` required; `rerun:` only valid with `match: first`
- `cadence:` only valid with `agentResponded`/`agentIdle` + `match: all/allExceptFirst`; at least one threshold required; mutually exclusive with `rerun:`
- Prompt-mode parameters: missing `default` → load error (red badge in UI); use `${VAR:-fallback}` not bare `${VAR}` for resilience

## Defaults

`command` paths: `./`/`../` → processor dir; absolute; otherwise PATH. Defaults: `enabled=true`, `timeout=5s` (300s prompt-mode), `priority=100`, `input=message`, `output=transform`, `outputFormat=json`, `working_dir=session`, `onError=skip`.
