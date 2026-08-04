---
description: Loop prompt design patterns, silent mode, spawn deduplication, gate testing
globs:
  - "internal/config/prompts*.go"
  - "internal/web/handlers/session_*.go"
keywords:
  - loop
  - silent-mode
  - IsLoop
  - IsLoopForced
  - spawn-deduplication
  - Children
  - MCPText
  - gate-testing
---

# Loop Prompt Design Patterns

## Silent Mode vs Interactive Mode

Loop prompts must detect runtime context and adapt behavior:

```go
{{ if and .Session.IsLoop (not .Session.IsLoopForced) }}
  // Silent mode: scheduled run, user not watching
  // Use mitto_ui_notify ONLY (non-blocking)
  // Do NOT use interactive tools: options, form, textbox
  // Act autonomously when safe; notify on failures
{{ else }}
  // Interactive mode: forced run or non-loop conversation
  // May use all UI tools freely for confirmations
{{ end }}
```

**Key fields**:
- `.Session.IsLoop` — true if conversation has loop config enabled
- `.Session.IsLoopForced` — true if user force-triggered the run (via `mitto_conversation_run_loop_now_mitto`)

**Pattern**: Silent mode never blocks the user; interactive mode can present dialogs, options, textboxes for user input.

## Spawn Deduplication

When a loop prompt spawns child conversations for multi-step repairs, always check for existing children **before** spawning:

```go
Existing child conversations:
{{ .Children.MCPText }}

Before spawning a new conversation, search the list above for a matching title.
If found and still idle, RE-PROMPT it instead of spawning a duplicate.
```

**Fields**:
- `.Children.MCPText` — list of non-archived child conversations (from `mitto_children_tasks_wait_mitto` context)
- Search child titles for a substring match (e.g., "PR #66" in "Fix CI for PR #66")

**Spawn cap**: Limit to **3 spawns per loop run**. Prioritize by severity:
1. Rebase conflicts (blocks merge)
2. CI failures (blocks merge)
3. Unresolved review comments (informational)

**Benefits**:
- Avoids duplicate work in progress
- Reduces queue congestion during long-running repairs
- Enables smart re-prompting of idle fixers with new instructions

## Gate Testing Before External Actions

When a loop prompt spawns a fixer for CI failures, instruct it to run the full local gate suite BEFORE pushing:
`make fmt-check` → `make lint` → `make test` → `make build-mock-acp && make test-integration`. Full local validation breaks the incremental fix-one-reveal-next cycle that wastes CI runs.

## Notification Pattern

In silent mode, communicate via `mitto_ui_notify_mitto` (non-blocking). NEVER use interactive tools (`mitto_ui_options_mitto`, `mitto_ui_form_mitto`, `mitto_ui_textbox_mitto`) — they block on user input the loop will never receive.

## State Persistence

For long-running loop prompts that track external state (CI status, branch status, etc.):
- Store state in a workspace file (`.mitto/state/` convention)
- Use `.Iteration.IsUninterrupted` to detect continuation vs restart
- Compact continuation messages by referencing the state file path

## Spawning Children From a Loop: `arguments` vs `loop_arguments`

`mitto_conversation_new` (and the equivalent MCP call in a loop-body prompt) takes two DIFFERENT argument maps:

- `arguments:` — fills `.Args` **only on the initial prompt** dispatched at spawn time.
- `loop_arguments:` — fills `.Args` on **every subsequent loop re-fire** of the child.

**Anti-pattern**: passing only `arguments:` when the spawned child is itself a loop. The initial turn sees the value, but every re-fire renders with `.Args.<Name>=""` and the argument is lost across the loop's lifetime (bug `mitto-rtdr`, fixed 25ed20d9). With a positive-match gate (`{{ if eq .Args.Commit "true" }}`) the empty value silently resolves false; with a default-on gate the re-fire silently falls back to the default instead of the operator's choice.

**Fix**: when the child is a loop, MIRROR the same resolved value into both:

```yaml
mitto_conversation_new(
  arguments:      { SubmitStrategy: "Pull Request", ... }
  loop_arguments: { SubmitStrategy: "Pull Request", ... }   # required for re-fires
)
```

`TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments` pins this for `beads-issues/loop-processing.prompt.yaml`: for every `SubmitStrategy` the picker can produce (`Commit`, `Pull Request`, `None`) plus the unset case, each of the §A/§B/§C spawn blocks must carry BOTH `arguments:` and `loop_arguments:` with the **identical** resolved literal — mirroring into `arguments:` only, or mirroring a stale/different value, fails.

Related: parameter defaults are NOT auto-merged into `.Args` at render time; either pass the value explicitly or write default-on gates (`{{ ne .Args.SubmitStrategy "None" }}`, `{{ if ne .Args.Commit "false" }}`) instead of positive-match ones (`{{ if eq .Args.Commit "true" }}`).

## `coalesceDuringBusy` Silent-Swallow During Quiescence Rebase

When an `onTasks` loop is busy (child driver still running), fs-watcher fires do NOT dispatch — they arm a **quiescence rebase** timer. When the subtree quiesces, the baseline is silently rebased to include all intervening changes with **no fire, no dispatch**. A user action (e.g. changing a bead's type via the web UI) can be absorbed into the new baseline and never trigger the loop until an external event re-fires the watcher (`onTasks: baseline rebased after idle+quiescence` in logs). This is intentional coalescing (feature `coalesceDuringBusy`), not a bug — but it means supervisor loops can silently miss user-driven state changes for minutes.

## `runOnStart` Boot Pulse — Once-per-Process Guard

`fireOnStartPulses` (`internal/conversation/loop_runner.go` ~L1000) marks `runOnStartFired[sessionID] = true` **before** calling `triggerNowFull → deliverPrompt → promptResolver`. If prompt resolution fails at that instant, the guard has already consumed the pulse and the boot fire will **not** be retried this process lifetime — the loop appears "deaf on restart".

- Anti-flap window default: `config.DefaultRunOnStartAntiFlapSeconds = 60` seconds (not minutes). Only suppresses if the loop actually ran within that window.
- Historical root cause of `prompt "X" not found` at boot: prompts-cache warmed before the fragment registry — fixed by `mitto-g61` / commit `2fd8e7b3` (`internal/web/server.go` now calls `prompts.SetCurrentFragments(reg)` BEFORE starting the prompts watcher). The guard-consume-on-failure hazard remains latent for any other transient resolve failure.
- Diagnosis path: `grep 'Firing loop boot pulse' mitto.log` for `session_id`, then `grep 'Boot pulse delivery failed'` for the resolver error.

## Sending Prompts Inside a Running Loop Iteration

`mitto_conversation_send_prompt` inside a loop iteration does **not** run inline — it queues on the child, and the current iteration's `onCompletion` fires on THIS turn's end. Any `send_prompt` (e.g. dispatching a shared "Commit changes" prompt from inside a driver's phase prompt) lands as a NEW turn AFTER the current iteration finishes — arriving after the driver has already updated bd labels, spawned the next phase, or been reaped.

**Consequence**: you cannot chain follow-up work into the currently-active loop turn via `send_prompt`. For in-turn work, either inline the logic in the current prompt body or factor it into a template partial (`{{ template "…" . }}`). This is why the L1 beads-loop drivers commit inline via `git` rather than by dispatching a shared commit prompt.

## Schema-Extension Pattern for New Loop Fields

Canonical reference for adding an optional field to `LoopPrompt`: `CoalesceDuringBusy` (beads `mitto-dmb` / `mitto-f9q`). The plumbing touches, in order:

1. `internal/session/loop.go` — add `*bool` (or typed field) to `LoopPrompt`.
2. `internal/config/prompts/prompts.go` — parse from prompt frontmatter.
3. MCP types (`internal/mcpserver/`) — mirror as `Loop<Field>` on `ConversationStartInput` / `ConversationUpdateInput`.
4. REST DTOs (`internal/web/handlers/session_loop_*.go`) — expose over HTTP.
5. `applyPromptLoopDefaults` — merge prompt-declared default when caller did not pass one.
6. Runtime consumer (e.g. `loop_runner_tasks.go`) — honor the field.

Follow this pattern verbatim; skipping any layer breaks either the prompt-frontmatter path, the MCP path, or the REST path.
