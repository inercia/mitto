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
  - Trigger.Kind
  - Trigger.OnChild
  - LoopDispatchOptions
  - trigger-aware-branching
---

# Loop Prompt Design Patterns

## Multi-Trigger Schema (mitto-r6j)

`loop.trigger:` is a **list**, and each trigger's own attributes are grouped
under a nested block of the same name. Every listed trigger arms
**independently** — a `[onTasks, onCompletion]` loop reacts to beads changes
AND re-arms after every turn, simultaneously, for the whole lifetime of the
loop:

```yaml
loop:
  trigger: [onTasks, onCompletion]
  onTasks:
    condition: 'Changes.Touched.exists(i, "ready-for-review" in i.labels)'
  onCompletion:
    delay: 30
  maxIterations: 0
  maxDuration: "0"
```

**Placement rule**: a field nests under a trigger key iff it only affects that
trigger (`schedule.value`/`unit`/`at`, `onCompletion.delay`,
`onTasks.condition`/`coalesceDuringBusy`/`settleWindow`/`cooldown`); everything
else (`maxIterations`, `maxDuration`, `freshContext`, `runOnStart`, `mode`,
`default`) stays a loop-wide sibling of `trigger:` since it applies regardless
of which trigger fires. See
[docs/config/prompts.md § Loop Prompts](../../docs/config/prompts.md#loop-prompts)
for the full field reference and
[docs/devel/message-queue.md § Multi-Trigger Architecture](../../docs/devel/message-queue.md#loop-prompts-multi-trigger-architecture)
for the dispatch-claim/coalescing mechanics.

**Coalescing**: when two armed triggers want to fire in the same narrow
window, exactly ONE run is delivered — the other is **dropped, not queued**
(`ErrLoopDispatchCoalesced`, first-come-first-served per conversation).
Precedence within a single poll tick is `onTasks` > `onCompletion` >
`schedule` (event-driven legs are armed before the schedule due-check), but
across ticks it is whichever trigger's event lands first. Do not design a
multi-trigger prompt assuming a coalesced fire will be redelivered later — if
a run's information matters, put it in the delivered `PromptMeta`/CEL
`condition` state (e.g. `.Trigger.OnTasks.Changes.*`), not in "the next tick
will catch it."

**Trigger-aware branching (`.Trigger.Kind`, mitto-qzqm)**: any prompt that
arms two or more triggers MUST branch on `{{ .Trigger.Kind }}` so schedule /
onCompletion / onTasks / onChild / onSlack runs render distinct framings.
`.Trigger` is **non-nil for every loop dispatch** (invariant flipped by
mitto-qzqm — including schedule / onCompletion runs that carry no structured
payload) and nil only for non-loop / ad-hoc human prompts. Guard the outer
pointer with `{{ with .Trigger }}` and branch on Kind inside:

```
{{ with .Trigger }}
{{ if eq .Kind "onChild" }}A child conversation changed state.
{{ else if eq .Kind "onTasks" }}{{ with .OnTasks }}Beads changed: {{ range .Changes.Touched }}{{ .id }} {{ end }}{{ end }}
{{ else if .IsRunOnStart }}Boot pulse recovery.
{{ else }}Routine {{ .Kind }} run.
{{ end }}
{{ end }}
```

Companion flags: `.Trigger.IsManual` mirrors `.Session.IsLoopForced` (manual
"Run Now" click); `.Trigger.IsRunOnStart` mirrors `.Session.IsLoopRunOnStart`
(once-per-boot pulse — `31-loop-prompts.md § runOnStart` covers the
process-wide guard). Both populated on every loop dispatch. A single-branch
body that ignores `.Trigger.Kind` is an anti-pattern once a second trigger is
armed — the losing trigger's run is dropped (see Coalescing above), so if a
run's framing matters, encode it via `.Trigger.Kind`, not "the next tick will
catch it."

**onChild provenance (`.Trigger.OnChild.*`, mitto-qvlh)**: populated only when
`.Trigger.Kind == "onChild"`. Fields: `ChildID` (bounded session identifier —
intentionally NOT the child's title/name, since deleted-child metadata may
already be gone by the time `anyDeleted` fires), `Event` (`"anyEndResponse"`
| `"anyDeleted"` | `"anyLoopStopped"`), `StoppedReason` (non-empty ONLY for
`anyLoopStopped`; carries the child's own loop-stop reason, e.g.
`"maxDuration"`). Guard both pointer levels — the outer `.Trigger` remains
nil for non-loop prompts, and `.OnChild` is nil for non-onChild fires:

```
{{ with .Trigger }}{{ with .OnChild }}
Child {{ .ChildID }} fired via {{ .Event }}{{ if .StoppedReason }} (stopped: {{ .StoppedReason }}){{ end }}.
{{ end }}{{ end }}
```

The same detail is persisted credential-free on
`session.PromptProvenance.OnChild` for future replay.

**Anti-pattern — inert blocks**: a `schedule`/`onCompletion`/`onTasks` block
present for a trigger NOT listed in `trigger:` parses fine but is **inert**
(load-time WARN, not an error). This is easy to introduce by copy-pasting a
loop block from another prompt without pruning unused trigger sub-blocks. The
builtin set carries zero inert blocks (the 21 `onTasks` carryovers from the
mitto-r6j migration were deleted by mitto-7hh0) and
`TestBuiltinPrompts_NoInertLoopTriggerBlocks` keeps it that way; avoid adding
*new* inert blocks in prompts you author from scratch.

**Anti-pattern — assuming a shared cap resets per trigger**: `maxIterations`
and `maxDuration` are loop-wide, decremented/checked once per **delivered**
run regardless of which trigger fired it. A `[schedule, onCompletion]` loop
with `maxIterations: 10` does not get 10 schedule runs *plus* 10
onCompletion runs — it gets 10 runs total, however the mix landed.

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

`fireOnStartPulses` (`internal/conversation/loop_runner.go`) reserves `runOnStartFired[sessionID] = true` **before** calling `triggerNowFull` to prevent concurrent ticks from double-dispatching. Failed deliveries must release that reservation while the loop remains enabled: contention errors roll it back directly; prompt-resolution failures use their dedicated bounded strike counter; other synchronous and asynchronous failures use the trigger-agnostic `deliveryFailures` ceiling. Once a classifier auto-pauses the loop, the guard remains terminally set and no further boot retries occur.

- Anti-flap window default: `config.DefaultRunOnStartAntiFlapSeconds = 60` seconds (not minutes). Only suppresses if the loop actually ran within that window.
- Historical root cause of `prompt "X" not found` at boot: prompts-cache warmed before the fragment registry — fixed by `mitto-g61` / commit `2fd8e7b3` (`internal/web/server.go` now calls `prompts.SetCurrentFragments(reg)` BEFORE starting the prompts watcher).
- Diagnosis path: grep `Firing loop boot pulse`, `Boot pulse delivery failed`, `Re-armed runOnStart boot pulse after delivery failure`, and the eventual auto-stop record by `session_id`.

## Sending Prompts Inside a Running Loop Iteration

`mitto_conversation_send_prompt` inside a loop iteration does **not** run inline — it queues on the child, and the current iteration's `onCompletion` fires on THIS turn's end. Any `send_prompt` (e.g. dispatching a shared "Commit changes" prompt from inside a driver's phase prompt) lands as a NEW turn AFTER the current iteration finishes — arriving after the driver has already updated bd labels, spawned the next phase, or been reaped.

**Consequence**: you cannot chain follow-up work into the currently-active loop turn via `send_prompt`. For in-turn work, either inline the logic in the current prompt body or factor it into a template partial (`{{ template "…" . }}`). This is why the L1 beads-loop drivers commit inline via `git` rather than by dispatching a shared commit prompt.

## Trigger Metadata in Prompt Templates

Loop prompts can inspect **which trigger fired** the current run via the
`.Trigger` context (beads `mitto-qzqm` + `mitto-qvlh`):

- `.Trigger.Kind` — one of `"onTasks"`, `"onCompletion"`, `"onSlack"`,
  `"onChild"`, `"schedule"`, `"manual"`.
- `.Trigger.IsManual` — `true` when the user force-triggered the run via
  `mitto_conversation_run_loop_now_mitto` (equivalent to
  `.Session.IsLoopForced`, just grouped under the trigger root).
- `.Trigger.IsRunOnStart` — `true` for the once-per-process boot pulse
  (equivalent to `.Session.IsLoopRunOnStart`).
- `.Trigger.OnChild.{ChildID, Event, StoppedReason}` — for `onChild` fires
  only: the specific child conversation id, the lifecycle event
  (`"anyEndResponse"` / `"anyLoopStopped"` / `"anyDeleted"`), and — for
  `anyLoopStopped` only — the `StoppedReason`. `ChildID` intentionally omits
  child name/title because that metadata is already gone by delete time.

Persistence mirror: `session.PromptProvenance.OnChild` (JSON `omitempty` for
backward compat) captures the same fields on the winning dispatch for replay
and audit.

**Nil-safety rule (MANDATORY)**: prompts that are ALSO invoked outside loop
mode (e.g. `beads-issues/loop-processing.prompt.yaml` from the beads list
menu) MUST guard every `.Trigger` access — `.Trigger` is `nil` for non-loop
dispatches. Access it as `{{ if .Trigger }} … {{ end }}`, never bare.

**Coalescing invariant**: `.Trigger` and `PromptProvenance.OnChild` are only
built for the WINNING (successfully claimed) dispatch. `claimDispatch`
returns `ErrLoopDispatchCoalesced` **before** the trigger context is ever
allocated, so a coalesced-loser fire produces no misleading trigger log,
provenance record, or template context. `buildPromptTriggerContext`
(package-private, pure function) is extracted precisely so the fire-path can
be unit-tested without the full ACP stack.

**Log-ordering quirk**: `triggerNowFull` emits its `"Triggering immediate
loop delivery"` info log BEFORE `deliverPrompt` calls `claimDispatch` —
coalesced-loser tests must assert absence of a DOWNSTREAM debug log
(post-claim), not the upstream info log.

**Anti-pattern**: mechanical `if eq .Trigger.Kind "…"` branches in a
single-trigger prompt are semantic-neutral boilerplate. Only branch on
`.Trigger.Kind` where the trigger genuinely narrows the work (e.g. reconcile
just the fired child on `onChild` instead of the whole subtree) or preserves
information otherwise lost (e.g. the specific `.Trigger.OnChild.ChildID` /
`StoppedReason`). Across the 27 audited builtin loop prompts, only 3
carry trigger-aware branching; the other 24 correctly do not.

## LoopDispatchOptions Envelope

`internal/conversation/loop_runner.go` uses a typed
`LoopDispatchOptions` struct to carry fire-path metadata (trigger kind,
manual/runOnStart flags, `OnChild` payload) through
`triggerNowFull → deliverPrompt`. The public `TriggerNow*` API is unchanged
— the envelope is internal. When adding a new fire-path field, extend
`LoopDispatchOptions`; do NOT grow the positional signature (the previous
6+-parameter chain was the anti-pattern this refactor removed, bead
`mitto-qvlh`).

## `config → cel` Shim Aliases

`internal/processors/hook.go` references CEL context types
(`PromptTriggerContext`, `TriggerOnChildContext`, etc.) through the config
layer, but the canonical definitions live in `internal/cel`.
`internal/config/cel_shim.go` re-exports them via Go type aliases so
processors can consume them without importing `internal/cel` directly
(preserves the config→cel dependency direction).

**Rule**: any new type added to `internal/cel` that must be reachable from
`internal/processors` requires a mirrored `type X = cel.X` alias in
`internal/config/cel_shim.go`, or the build breaks with an `undefined` error
from `internal/processors`. Do NOT redefine the struct in the shim — that
creates two incompatible types.

## Schema-Extension Pattern for New Loop Fields

Canonical reference for adding an optional field to `LoopPrompt`: `CoalesceDuringBusy` (beads `mitto-dmb` / `mitto-f9q`). The plumbing touches, in order:

1. `internal/session/loop.go` — add `*bool` (or typed field) to `LoopPrompt`.
2. `internal/config/prompts/prompts.go` — parse from prompt frontmatter.
3. MCP types (`internal/mcpserver/`) — mirror as `Loop<Field>` on `ConversationStartInput` / `ConversationUpdateInput`.
4. REST DTOs (`internal/web/handlers/session_loop_*.go`) — expose over HTTP.
5. `applyPromptLoopDefaults` — merge prompt-declared default when caller did not pass one.
6. Runtime consumer (e.g. `loop_runner_tasks.go`) — honor the field.

Follow this pattern verbatim; skipping any layer breaks either the prompt-frontmatter path, the MCP path, or the REST path.
