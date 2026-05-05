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
when: first            # first | all | all-except-first
priority: 100          # lower = earlier
enabled: true          # false = never loads (build-time gate)
enabledWhen: 'acp.matchesServerType("augment") && !session.isPeriodic'  # CEL runtime gate
on_error: skip         # skip | fail

# Command-mode only:
command: ./script.sh
input: message         # message | conversation | none
output: prepend        # transform | prepend | append | discard

# Prompt-mode only:
prompt: |
  Analyze these messages: @mitto:messages   # legacy; see note below
timeout: 300s

# Auto re-run for when:first processors (whichever threshold fires first):
rerun:
  afterSentMsgs: 15    # message count since last run
  afterTokens: 50000   # token usage since last run (falls back to char estimation)
  afterTime: 1h        # elapsed time since last run
```

## Two Enable Layers

| Layer         | Field          | Skip reason logged      | Effect                                 |
| ------------- | -------------- | ----------------------- | -------------------------------------- |
| Build-time    | `enabled: false` | not loaded at all      | Processor never appears in pipeline    |
| Runtime (CEL) | `enabledWhen`  | `enabledWhen_false`     | Processor is loaded but skipped        |

When a processor has both `enabled: false` and `enabledWhen`, it is never loaded regardless of the CEL result.

## `@mitto:messages` Substitution

The `@mitto:messages` placeholder (plus `messages:` YAML block) is **supported for backward compatibility** in user-defined processors. Builtin processors have migrated to using the `mitto_conversation_history` MCP tool instead — the agent calls the tool directly to fetch filtered history. Do NOT add new `messages:` blocks to builtin YAML files.

## Adding New `@mitto:` Variables (Checklist)

New substitution variables (e.g. `@mitto:periodic_forced`) require changes in four files:

| File | Change |
|------|--------|
| `internal/web/background_session.go` | Add field to `PromptMeta` struct; wire it in `PromptWithMeta` → `processorInput` |
| `internal/processors/input.go` | Add field to `ProcessorInput` struct (with json tag) |
| `internal/processors/variables.go` | Add substitution case in `SubstituteVariables`; update doc comment |
| Callers (e.g. `periodic_runner.go`) | Pass the value when constructing `PromptMeta` |

## Builtin Processors (`config/processors/builtin/`)

All builtins are **`enabled: false` by default**. Enable per workspace in the Workspaces dialog or `.mittorc`:

```yaml
# .mittorc
processors:
  - name: memorize-preferences
    enabled: true
```

| Processor             | Mode    | `enabledWhen` condition                                         | Purpose                            |
| --------------------- | ------- | --------------------------------------------------------------- | ---------------------------------- |
| `session-context`     | text    | _(none)_                                                        | Prepend session metadata           |
| `check-mcp-tools`     | text    | `!tools.hasPattern("mitto_*")`                                  | Suggest MCP install if missing     |
| `delegate-to-coder`   | text    | _(varies)_                                                      | Delegate work to coder session     |
| `delegate-playwright` | text    | _(varies)_                                                      | Delegate Playwright tests          |
| `cleanup-children`    | command | _(varies)_                                                      | Archive stale child sessions       |
| `auggie-manage-rules` | prompt  | `acp.matchesServerType("augment") && !session.isPeriodic`       | Maintain `.augment/rules/` files   |
| `claude-manage-memory`| prompt  | `acp.matchesServerType("claude-code") && !session.isPeriodic`   | Maintain `CLAUDE.md` / `.claude/`  |
| `memorize-preferences`| prompt  | `!session.isPeriodic`                                           | Save user prefs to `AGENTS.md`     |
| `identify-user-data`  | prompt  | `workspace.hasUserDataSchema && !session.isPeriodic`            | Auto-fill workspace user data fields|
| `identify-workspace-metadata` | prompt | `workspace.hasMittoRC && !workspace.hasMetadataDescription && !session.isPeriodic` | Auto-fill `metadata.description` and `metadata.url` in `.mittorc` |

**Common skip reasons:**
- `enabledWhen_false` — CEL expression evaluated to false (e.g., wrong server type, tools already present)
- `check-mcp-tools` skipped when mitto tools already available → `tools.hasPattern("mitto_*")` is true
- `auggie-manage-rules` skipped when using Claude Code → `acp.matchesServerType("augment")` is false
- `identify-user-data` skipped when no user data schema in `.mittorc` → `workspace.hasUserDataSchema` is false
- `identify-workspace-metadata` skipped when no `.mittorc` or description already set → `workspace.hasMittoRC && !workspace.hasMetadataDescription`

## CEL Context for `enabledWhen`

Key CEL variables/functions (full reference in `docs/config/processors.md`):

| Context            | Examples                                                                    |
| ------------------ | --------------------------------------------------------------------------- |
| `acp.*`            | `acp.matchesServerType("augment")`, `acp.name`, `acp.type`, `acp.tags`     |
| `session.*`        | `session.isPeriodic`, `session.isChild`, `session.id`                       |
| `workspace.*`      | `workspace.hasUserDataSchema`, `workspace.hasMittoRC`, `workspace.hasMetadataDescription`, `workspace.folder` |
| `tools.*`          | `tools.hasPattern("mitto_*")`, `tools.hasAllPatterns(["a_*", "b_*"])`       |

## Skip Reason Reference

| Log `reason=`       | Cause                                                   |
| ------------------- | ------------------------------------------------------- |
| `enabled_false`     | `enabled: false` in YAML and no workspace override      |
| `enabledWhen_false` | CEL expression evaluated to false                       |
| `when_mismatch`     | `when: first` processor on a non-first message          |

## Command Resolution

- `./` or `../` prefix → relative to processor file directory
- Absolute path → used as-is
- Otherwise → PATH lookup

## Defaults

| Field         | Default   |
| ------------- | --------- |
| `enabled`     | true      |
| `timeout`     | 5s (300s for prompt-mode) |
| `priority`    | 100       |
| `input`       | message   |
| `output`      | transform |
| `working_dir` | session   |
| `on_error`    | skip      |
