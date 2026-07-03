# Message Queue System

This document covers the message queue architecture, including queue management, automatic title generation, and WebSocket notifications.

## Overview

The queue system allows users to send messages while the agent is busy. Messages are queued and automatically delivered when the agent becomes idle. Each queued message can have an auto-generated title for easy identification.

```mermaid
flowchart TB
    subgraph "User Actions"
        USER[User] -->|POST /queue| API[Queue API]
        USER -->|View queue| LIST[GET /queue]
    end

    subgraph "Queue Storage"
        API -->|Add| QUEUE[session.Queue]
        QUEUE -->|Persist| FILE[(queue.json)]
    end

    subgraph "Title Generation"
        API -->|Enqueue| WORKER[QueueTitleWorker]
        WORKER -->|Generate| AUX[Auxiliary Session]
        AUX -->|Title| WORKER
        WORKER -->|UpdateTitle| QUEUE
        WORKER -->|Broadcast| WS[WebSocket]
    end

    subgraph "Queue Processing"
        BS[BackgroundSession] -->|Pop| QUEUE
        BS -->|Prompt| AGENT[ACP Agent]
    end
```

## Configuration

Queue behavior is configured globally or per-workspace (NOT per-session):

```yaml
conversations:
  queue:
    enabled: true # Auto-process queued messages (default: true)
    delay_seconds: 0 # Delay before sending next message (default: 0)
    max_size: 10 # Maximum messages in queue (default: 10)
    auto_generate_titles: true # Generate short titles (default: true)
```

### Configuration Scope

| Setting                | Scope            | Rationale                                  |
| ---------------------- | ---------------- | ------------------------------------------ |
| `enabled`              | Global/Workspace | Consistent behavior across sessions        |
| `delay_seconds`        | Global/Workspace | Rate limiting applies uniformly            |
| `max_size`             | Global/Workspace | Resource limits are workspace-wide         |
| `auto_generate_titles` | Global/Workspace | Feature toggle, not per-session preference |

## Queue Package (`internal/session/queue.go`)

### Types

```go
// QueuedMessage represents a message waiting to be sent to the agent.
type QueuedMessage struct {
    ID            string            `json:"id"`                       // Unique ID (q-{timestamp}-{random})
    Message       string            `json:"message"`                  // Text content (empty for named-prompt items)
    ImageIDs      []string          `json:"image_ids,omitempty"`      // Attached images
    FileIDs       []string          `json:"file_ids,omitempty"`       // Attached files
    QueuedAt      time.Time         `json:"queued_at"`                // When queued
    ClientID      string            `json:"client_id,omitempty"`      // Source client
    Title         string            `json:"title,omitempty"`          // Auto-generated title (skipped for named-prompt items)
    ScheduledTime *time.Time        `json:"scheduled_time,omitempty"` // Deliver after this time (nil = immediate)
    Arguments     map[string]string `json:"arguments,omitempty"`      // Go-template argument values applied at dispatch ({{ .Args.NAME }} / {{ Arg "NAME" "default" }})
    PromptName    string            `json:"prompt_name,omitempty"`    // Named-prompt: resolved to full text at dispatch (empty for ad-hoc messages)
}

// Queue manages the message queue for a single session.
// Thread-safe with atomic file persistence.
type Queue struct { ... }
```

### Methods

| Method                                                                            | Description                                                   |
| --------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `Add(message, imageIDs, fileIDs, clientID, scheduled, sz, arguments, promptName)` | Add message, returns `ErrQueueFull` if at capacity            |
| `List()`                                                                          | Get all messages in FIFO order                                |
| `Get(id)`                                                                         | Get specific message by ID                                    |
| `Remove(id)`                                                                      | Remove specific message                                       |
| `Pop()`                                                                           | Remove and return next ready message (skips future-scheduled) |
| `Clear()`                                                                         | Remove all messages                                           |
| `Len()`                                                                           | Get queue length                                              |
| `UpdateTitle(id, title)`                                                          | Update a message's title                                      |
| `HasScheduledMessages()`                                                          | Check if any scheduled messages exist                         |
| `NextScheduledTime()`                                                             | Get earliest scheduled time of pending messages               |

### Error Values

| Error                | Condition                                               |
| -------------------- | ------------------------------------------------------- |
| `ErrQueueEmpty`      | `Pop()` on empty queue or no ready messages             |
| `ErrMessageNotFound` | `Get()`, `Remove()`, or `UpdateTitle()` with invalid ID |
| `ErrQueueFull`       | `Add()` when queue has `maxSize` messages               |

## Scheduled Messages

Messages can optionally have a `ScheduledTime` that defers delivery until a future time.

### Behavior

- **Non-scheduled messages** (ScheduledTime = nil): Delivered immediately when the agent becomes idle. Backward compatible with all existing behavior.
- **Scheduled messages** (ScheduledTime set): Held in the queue until `time.Now() >= ScheduledTime`.

### Pop() Ordering

When `Pop()` is called, it selects the next ready message:

1. **First non-scheduled message** (FIFO among immediate messages)
2. If no immediate messages, the **earliest due scheduled message** (by ScheduledTime)
3. Returns `ErrQueueEmpty` if no messages are ready (even if future-scheduled messages exist)

### Periodic Check

The `PeriodicRunner` checks all active sessions for due scheduled messages on each poll cycle (default: 1 minute). When a scheduled message becomes due, it triggers `TryProcessQueuedMessage()` on the session.

### API

- **REST**: `POST /api/sessions/{id}/queue` accepts optional `scheduled_time` field (RFC 3339)
- **MCP**: `mitto_conversation_send_prompt` accepts optional `schedule_time` parameter (RFC 3339)
- **List**: Scheduled messages appear in the list response with the `scheduled_time` field

### Frontend

Scheduled messages display a ⏰ badge with a relative time string (e.g., "in 5 min", "in 2h") in the queue dropdown. The display updates every 30 seconds.

## Periodic Prompts: On-Completion Delivery

Periodic prompts normally fire on a fixed schedule (checked by the `PeriodicRunner` poll loop). A periodic prompt may instead set `trigger: onCompletion`, which fires the next run **after the agent stops responding**, rather than on a clock.

### Delivery model

When a turn completes and a session goes fully idle, `BackgroundSession` invokes the `onTurnIdle` hook, which routes to `PeriodicRunner.OnConversationIdle(sessionID)`. For an enabled `onCompletion` config this arms a one-shot timer for `delay` seconds (clamped up to the global floor `min_periodic_completion_delay_seconds`, default 5). When the timer fires, `fireOnCompletion` re-validates the config, checks the max-duration cap, and delivers via `TriggerNow`. The delivered run's own completion produces another idle transition, which arms the next run — a self-sustaining loop.

```mermaid
sequenceDiagram
    participant Agent
    participant BS as BackgroundSession
    participant PR as PeriodicRunner
    participant Store as PeriodicStore

    Agent->>BS: turn completes (stop_reason=end_turn)
    BS->>PR: onTurnIdle → OnConversationIdle(sessionID)
    alt enabled onCompletion config
        PR->>PR: armCompletionTimer(delay clamped to floor)
        Note over PR: after delay
        PR->>Store: Get() — re-validate (enabled? onCompletion? archived?)
        PR->>Store: ReachedMaxDuration(now)?
        alt maxDuration reached
            PR->>Store: Update(enabled=false)
            PR-->>BS: onPeriodicAutoStopped → broadcast periodic_updated
        else within cap
            PR->>BS: TriggerNow(resetTimer=true) → deliver run
            BS->>Agent: prompt
            Note over BS,Agent: completion re-arms via onTurnIdle
        end
    else not an onCompletion loop
        PR->>PR: cancelCompletionTimer(sessionID)
    end
```

### Loop safety

- **Delay floor** — `delay` is clamped up to `min_periodic_completion_delay_seconds` (default 5) so a misconfigured `delay: 0` cannot spin a hot loop.
- **Single pending timer** — arming replaces (stops) any existing timer for the session, so at most one firing is queued.
- **Max iterations** — the standard per-run counter still applies; reaching the effective cap disables the prompt.
- **Max duration** — `maxDuration` is a wall-clock cap from the first run; `fireOnCompletion` checks it before delivering and auto-stops (disables + broadcasts) once exceeded.
- **Busy / archived guards** — a busy session is skipped (the next idle re-arms); an archived or disabled config drops the timer.

### Interplay with the runner and suspension

The schedule-based poll loop and the on-completion timers are independent paths on the same `PeriodicRunner`. On-completion timers are armed by idle events, not the poll loop, so they are unaffected by the poll interval. A suspended periodic session (Tier-1 GC after `periodic_suspend_timeout`) has no live `BackgroundSession` to emit idle events; the on-completion loop resumes once the session is resumed. See [acp.md](acp.md) for suspension details.

## Periodic Prompts: On-Tasks Delivery

A periodic prompt may set `trigger: onTasks`, which fires whenever the **beads issues in the conversation's working directory change** on disk, optionally gated by a **CEL condition** so it only fires for meaningful changes (e.g. "the open bug count increased", "an issue labelled `PR opened` was created or updated"). Like `onCompletion`, this is event-driven, not clock-driven — `Frequency` is not required and is ignored.

### Trigger semantics

A workspace-wide `BeadsWatcher` (fsnotify on `.beads/`, debounced) calls `PeriodicRunner.OnBeadsChanged(event)` whenever a watched working directory changes. For every **enabled** `onTasks` conversation whose working directory is in `event.WorkingDirs`, the runner:

1. Fetches the latest beads snapshot once per working directory (`bd list --json --all -n 0`), shared across all conversations watching that directory.
2. Diffs it against that **conversation's own persisted baseline** (see below) using `config.DiffTasks`.
3. Evaluates the conversation's CEL `Condition` (empty = fire on any material change).
4. Fires via `TriggerNow` when all guards pass and the condition is true.

The very first `OnBeadsChanged` call for a conversation only **captures the baseline** — it never fires (no spurious first run when `onTasks` is newly enabled or the server restarts before a baseline exists). `BootstrapTasksBaseline` performs the same capture-without-firing on enable/startup.

### Condition language (CEL)

Conditions are CEL expressions evaluated by `config.TasksConditionEvaluator` (`internal/config/tasks_condition.go`) against a `TasksChangeContext` with three variables:

| Variable  | Shape                                                                                                                                                                   | Meaning                                      |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| `Tasks`   | `Open`, `Closed`, `InProgress`, `Ready`, `Blocked` (ints); `CountByType`, `CountByStatus`, `CountByLabel`, `OpenByType` (`map<string,int>`); `All` (list of issue maps) | Current snapshot (after the change)          |
| `Prev`    | same shape as `Tasks`                                                                                                                                                   | Snapshot at the conversation's last baseline |
| `Changes` | `Added`, `Updated`, `Removed`, `Closed`, `Reopened`, `LabelAdded`, `Touched` (= Added ∪ Updated) — all lists of issue maps                                              | The diff between `Prev` and `Tasks`          |

Each issue map exposes canonical keys: `id`, `type`, `status`, `priority`, `labels`, `title`, `assignee`, `updated_at`.

```
# Open "bug" count increased
Tasks.OpenByType["bug"] > Prev.OpenByType["bug"]

# An issue labelled "PR opened" was created or updated
Changes.Touched.exists(i, "PR opened" in i.labels)

# A new P0/P1 bug appeared
Changes.Added.exists(i, i.type == "bug" && i.priority <= 1)

# Empty condition = fire on ANY beads change
```

**Native-CEL map caveat:** `Tasks`/`Prev`/`Changes` are plain CEL maps, not proto messages — indexing a key that doesn't exist (e.g. `OpenByType["bug"]` when no bug has ever existed in that snapshot) is a **runtime error**, not a zero value. Conditions that index a type/status/label must ensure the key can already be present in the baseline, or guard with `"bug" in Tasks.OpenByType`.

**Fail-closed semantics:** unlike prompt `enabledWhen` (which fails open), a `Condition` that fails to compile or errors at evaluation time (including the missing-key case above) makes the trigger **not fire** — a misconfigured condition must never cause spurious unattended runs. Compile errors are also rejected synchronously on save (`session.ConditionValidator`, wired to `config.ValidateCondition`).

### The diff baseline (`internal/web/tasks_baseline.go`)

Each `onTasks` conversation keeps its **own** baseline file (`tasks_baseline.json`, alongside `periodic.json`) holding the raw `bd list` JSON at the time it was last considered "current" for that conversation. The baseline is **per-conversation, not per-working-directory** — several `onTasks` conversations watching the same directory each diff against their own baseline, which is what makes Layer 2 loop prevention (below) possible without any actor/attribution support from `bd`.

### Loop prevention (4 layers)

An `onTasks` conversation (or a child it delegates to) will usually _edit_ beads itself as part of doing its work — without safeguards this would re-trigger itself indefinitely, since its own edits show up as a fresh delta against the baseline.

```mermaid
sequenceDiagram
    participant Watcher as BeadsWatcher
    participant PR as PeriodicRunner
    participant Baseline as TasksBaselineStore
    participant CEL as TasksConditionEvaluator
    participant Agent

    Watcher->>PR: OnBeadsChanged(event)
    PR->>PR: Layer 1 — isTasksSubtreeBusy(sessionID)?
    alt conversation or a delegated child is busy
        PR->>PR: armTasksRebase (quiescence timer)
        Note over PR: event dropped for now
    else idle
        PR->>PR: Layer 0 — maxDuration reached? cooldown active?
        alt guard trips
            PR->>PR: skip (or auto-stop on maxDuration)
        else guards pass
            PR->>Baseline: diff(prev, curr) via DiffTasks
            alt no baseline yet
                PR->>Baseline: Set(curr) — capture only, no fire
            else material delta
                PR->>CEL: Evaluate(Condition, {Tasks, Prev, Changes})
                alt condition true
                    PR->>Agent: TriggerNow (fires the run)
                    PR->>Baseline: Set(curr) — baseline advances immediately
                    PR->>PR: Layer 3 — recordTasksFireOutcome(delta)
                else condition false/error
                    PR->>PR: skip (fail-closed on error)
                end
            end
        end
    end

    Note over PR,Agent: run (and any delegated children) finish and go idle
    PR->>PR: quiescence window elapses
    PR->>Baseline: rebase to latest snapshot (Layer 2)
    Note over Baseline: absorbs the run's own edits — they never<br/>reappear as a delta against the NEXT event
```

- **Layer 0 — hard backstops.** A per-conversation `CooldownSeconds` (clamped up to the global floor `SetMinPeriodicTasksCooldownSeconds`, default 30s) rate-limits fires regardless of the condition. `MaxIterations` and `MaxDurationSeconds` are the same caps used by every trigger; `MaxDurationSeconds` is checked (and auto-stops, mirroring `onCompletion`) before the cooldown check.
- **Layer 1 — busy guard (temporal).** While the conversation's turn is active — **or any delegated child conversation is still running or blocked on `mitto_children_tasks_wait`** (`isTasksSubtreeBusy`) — incoming events are deferred (`armTasksRebase`), not evaluated. This is the guard against the run's OWN in-flight edits.
- **Layer 2 — quiescence rebase (the real fix).** Once the conversation's entire delegated-child subtree goes idle, a short quiescence timer (`SetTasksQuiescenceWindow`, default 30s) fires and **rebases the baseline to the current beads snapshot**, absorbing the run's own edits into the new "current" state before the next real event is evaluated. Trade-off: an external change that lands _during_ the busy window is also absorbed and won't trigger a follow-up fire — the fired conversation can re-check state at its own startup if that matters.
- **Layer 3 — no-progress circuit breaker.** `recordTasksFireOutcome` tracks, per conversation, the set of issue IDs touched (`Changes.Touched`) by consecutive fires. When `tasksNoProgressLimit` (3) consecutive fires touch **no issue beyond** what the previous fire already touched, the trigger auto-pauses (`periodicStore.MarkStopped(session.StoppedReasonNoProgress)`) — this catches a condition that is steady-state-true (e.g. a threshold that baseline-rebase alone cannot silence) before it can hot-loop.

**Out of scope:** actor-based delta filtering (skipping only _other actors'_ edits) was investigated and explicitly deferred — `internal/beads/cli.go` does not stamp a per-change actor, and `bd list --json` exposes only `created_by`/`owner`, not a last-touched actor. The baseline-rebase approach (Layer 2) makes this unnecessary for correctness today.

### Configuration fields (`session.PeriodicPrompt`)

| Field             | JSON               | Meaning                                                                                                           |
| ----------------- | ------------------ | ----------------------------------------------------------------------------------------------------------------- |
| `Trigger`         | `trigger`          | `"onTasks"`                                                                                                       |
| `Condition`       | `condition`        | CEL expression; empty = fire on any material beads change                                                         |
| `ConditionPreset` | `condition_preset` | Optional UI preset id that was compiled into `Condition`                                                          |
| `CooldownSeconds` | `cooldown_seconds` | Per-conversation cooldown floor; `0` = use the global floor                                                       |
| `StoppedReason`   | `stopped_reason`   | `"noProgress"` when Layer 3 auto-paused the loop (also `maxIterations`/`maxDuration`, shared with other triggers) |

### Testing

`internal/config/tasks_condition_test.go` unit-tests snapshot parsing, diffing, and CEL evaluation (including the fail-closed cases). `internal/web/periodic_runner_test.go` unit-tests the guard/decision logic (`evaluateTasksChange`) and each loop-prevention layer in isolation. `tests/integration/inprocess/periodic_ontasks_e2e_test.go` drives the full stack end-to-end against the mock ACP server — CEL-gated firing, the busy-guard + quiescence-rebase interaction, the cooldown floor, the no-progress circuit breaker, and `MaxIterations`/`MaxDurationSeconds` auto-stop — by calling `PeriodicRunner.OnBeadsChanged` directly with a fake `beads.Client` standing in for `bd list` (the `BeadsWatcher` itself is out of scope for that test and is unit-tested separately).

## Title Generation

### Architecture

```mermaid
sequenceDiagram
    participant API as Queue API
    participant Worker as QueueTitleWorker
    participant Aux as Auxiliary Session
    participant Queue as session.Queue
    participant WS as WebSocket

    API->>Worker: Enqueue(sessionID, messageID, message)
    Note over Worker: Buffered channel (100 requests)

    loop Process sequentially
        Worker->>Aux: GenerateQueuedMessageTitle(message)
        Aux-->>Worker: "Fix Bug" (2-3 words)
        Worker->>Queue: UpdateTitle(messageID, title)
        Worker->>WS: Broadcast queue_message_titled
    end
```

### Components

| Component                    | File                           | Purpose                      |
| ---------------------------- | ------------------------------ | ---------------------------- |
| `QueueTitleWorker`           | `internal/web/queue_title.go`  | Sequential request processor |
| `GenerateQueuedMessageTitle` | `internal/auxiliary/global.go` | Prompt for title generation  |
| `Queue.UpdateTitle`          | `internal/session/queue.go`    | Persist title to queue.json  |

### QueueTitleWorker

The worker processes title requests sequentially to avoid overwhelming the auxiliary conversation:

```go
// Create worker (done in Server initialization)
worker := NewQueueTitleWorker(store, logger)
worker.OnTitleGenerated = func(sessionID, messageID, title string) {
    // Broadcast to WebSocket clients
}

// Enqueue request (non-blocking)
worker.Enqueue(QueueTitleRequest{
    SessionID: sessionID,
    MessageID: msg.ID,
    Message:   message,
})

// Shutdown (waits for pending requests)
worker.Close()
```

**Design decisions:**

- **Sequential processing**: Prevents concurrent auxiliary requests
- **Buffered channel (100)**: Drops requests if overwhelmed (logs warning)
- **30-second timeout**: Per-request timeout for title generation
- **Graceful shutdown**: Waits for in-flight request to complete

## Named-Prompt Queue Items

Queue items can carry a **prompt name** (+ optional substitution arguments) instead of a full message body. The backend resolves the name to full text at dispatch — not at enqueue time — using the target conversation's workspace context (`resolvePromptByName` in `internal/web/server.go`).

### Key properties

| Property         | Behavior                                                                                                   |
| ---------------- | ---------------------------------------------------------------------------------------------------------- |
| `prompt_name`    | Name of the workspace prompt to send; resolved at dispatch                                                 |
| `arguments`      | Go-template argument values applied at dispatch time via `{{ .Args.NAME }}` / `{{ Arg "NAME" "default" }}` |
| `message`        | Empty string for named-prompt items                                                                        |
| Title generation | **Skipped** — the prompt name itself serves as the label in the queue UI                                   |

### Why resolution happens at dispatch

Resolution is deferred to the target conversation's context so that workspace-specific prompts, ACP-server-filtered lists, and `enabledWhen` conditions are evaluated in the right environment, even when the request came from a different workspace or was created atomically with the session.

### Shared Frontend Seed Helper (`web/static/hooks/useConversationSeeding.js`)

All menu-driven prompt sends (prompts menu, Cmd+/ slash picker, beads-issue menus, beads-list menus) go through a **single shared helper** — never POST the full prompt body directly:

| Export                                                                                                | Purpose                                                                |
| ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `buildSeedQueueBody(prompt, {arguments})`                                                             | Builds `{prompt_name, arguments}` POST body (never includes `message`) |
| `seedConversationWithPrompt(sessionId, prompt, {arguments})`                                          | POST `{prompt_name}` to an existing session's queue                    |
| `startConversationWithPrompt({workingDir, acpServer, name, beadsIssue, prompt, arguments, periodic})` | Create a new conversation (one-time or periodic — see below)           |
| `configurePeriodicSchedule(sessionId, prompt, periodic, {fetchImpl})`                                 | PUT periodic config onto an already-created session                    |

#### One-time path (no `periodic`)

When `periodic` is absent, `startConversationWithPrompt` posts `initial_prompt_name` + `arguments` to `POST /api/sessions` — the backend seeds the queue atomically:

```javascript
const { seedConversationWithPrompt, startConversationWithPrompt } =
  useConversationSeeding({ newSession });

// Seed an existing conversation
await seedConversationWithPrompt(
  sessionId,
  { name: "Review Code" },
  { arguments: { ISSUE_ID: "mitto-42" } },
);

// Create a new conversation and seed it atomically (one-time)
await startConversationWithPrompt({
  workingDir,
  acpServer,
  prompt: { name: "Review Code" },
  arguments: { ISSUE_ID: "mitto-42" },
});
```

#### Periodic path (`periodic` present)

When `periodic: { value, unit, at? }` is provided, `startConversationWithPrompt`:

1. Creates the session via `POST /api/sessions` **without** `initial_prompt_name` (no one-time queue seed).
2. Calls `configurePeriodicSchedule` which PUTs `/api/sessions/{id}/periodic` with:
   ```json
   {
     "prompt_name": "...",
     "frequency": { "value": 1, "unit": "hours" },
     "enabled": true
   }
   ```
   The `at` field (HH:MM UTC) is included only when `unit === "days"`.
3. Returns `{ sessionId }` on success, or `{ error }` if the PUT fails (session already created — error is surfaced to the caller).

```javascript
// Create a new PERIODIC conversation driven by a named prompt
await startConversationWithPrompt({
  workingDir,
  acpServer,
  prompt: { name: "Daily Standup" },
  periodic: { value: 1, unit: "days", at: "09:00" }, // at is UTC HH:MM
});
```

The `at` value in the `periodic` object must already be in **UTC** when passed to `startConversationWithPrompt`. The `PeriodicScheduleDialog` component handles the local→UTC conversion before calling the helper.

#### Menu-branching rules

Menus branch on `prompt.periodic` (non-null = periodic prompt):

- **`handleSendPromptToConversation`** (per-conversation context menu): if `prompt.periodic` is set and the session is **not a child** (`parent_session_id` is empty), opens `PeriodicScheduleDialog` then creates a NEW periodic conversation — it does not seed the existing one. Child conversations are silently skipped (the backend also 400s on periodic-for-child).
- **`handleRunBeadsPrompt`** / **`handleRunBeadsListPrompt`** (beads menus): same branching via the `onOpenPeriodicDialog` callback passed from `app.js` into `useBeadsIntegration`.
- Non-periodic prompts are completely unaffected.

## REST API

### Endpoints

| Method   | Path                                | Description              |
| -------- | ----------------------------------- | ------------------------ |
| `GET`    | `/api/sessions/{id}/queue`          | List all queued messages |
| `POST`   | `/api/sessions/{id}/queue`          | Add message to queue     |
| `GET`    | `/api/sessions/{id}/queue/{msg_id}` | Get specific message     |
| `DELETE` | `/api/sessions/{id}/queue/{msg_id}` | Delete specific message  |
| `DELETE` | `/api/sessions/{id}/queue`          | Clear entire queue       |

### `POST /api/sessions` — Atomic Create + Seed

`SessionCreateRequest` supports `initial_prompt_name` (+ `arguments`) for atomically creating a conversation and seeding its queue in one request:

```json
// POST /api/sessions
{
  "working_dir": "/path/to/project",
  "acp_server": "auggie",
  "initial_prompt_name": "Review Code",
  "arguments": { "ISSUE_ID": "mitto-42" }
}
```

The backend calls `seedQueueWithNamedPrompt()` immediately after creating the session, using the same queue plumbing as `POST /api/sessions/{id}/queue`. Title generation is skipped for named-prompt items.

### Request/Response Examples

**POST /api/sessions/{id}/queue** — ad-hoc message

```json
// Request
{"message": "Fix the login bug", "image_ids": []}

// Response (201 Created)
{
  "id": "q-1738396800-abc12345",
  "message": "Fix the login bug",
  "queued_at": "2026-02-01T12:00:00Z",
  "title": ""  // Initially empty, updated asynchronously
}
```

**POST /api/sessions/{id}/queue** — named-prompt item

```json
// Request (no "message" field — prompt name is resolved at dispatch)
{
  "prompt_name": "Review Code",
  "arguments": { "ISSUE_ID": "mitto-42" }
}

// Response (201 Created)
{
  "id": "q-1738396800-def67890",
  "prompt_name": "Review Code",
  "queued_at": "2026-02-01T12:00:00Z"
  // No "title" — skipped for named-prompt items; prompt name is used as label
}
```

**GET /api/sessions/{id}/queue** (mixed queue)

```json
{
  "messages": [
    {
      "id": "q-1738396800-abc12345",
      "message": "Fix the login bug",
      "queued_at": "2026-02-01T12:00:00Z",
      "title": "Login Bug Fix"
    },
    {
      "id": "q-1738396800-def67890",
      "prompt_name": "Review Code",
      "arguments": { "ISSUE_ID": "mitto-42" },
      "queued_at": "2026-02-01T12:01:00Z"
    }
  ],
  "count": 2
}
```

**Error: Queue Full (409 Conflict)**

```json
{
  "error": "queue_full",
  "message": "Queue is full. Maximum 10 messages allowed."
}
```

## WebSocket Notifications

### Message Types

| Type                    | Direction       | Description                 |
| ----------------------- | --------------- | --------------------------- |
| `queue_updated`         | Server → Client | Queue state changed         |
| `queue_message_sending` | Server → Client | Message about to be sent    |
| `queue_message_sent`    | Server → Client | Message delivered to agent  |
| `queue_message_titled`  | Server → Client | Title generated for message |

### Payload Examples

**queue_updated**

```json
{
  "type": "queue_updated",
  "data": {
    "session_id": "20260201-120000-abc12345",
    "queue_length": 3,
    "action": "added", // "added", "removed", "cleared"
    "message_id": "q-1738396800-abc12345"
  }
}
```

**queue_message_titled**

```json
{
  "type": "queue_message_titled",
  "data": {
    "session_id": "20260201-120000-abc12345",
    "message_id": "q-1738396800-abc12345",
    "title": "Login Bug Fix"
  }
}
```

## Queue Processing Flow

```mermaid
sequenceDiagram
    participant User
    participant API as REST API
    participant Queue as session.Queue
    participant Worker as TitleWorker
    participant BS as BackgroundSession
    participant WS as WebSocket
    participant Agent

    User->>API: POST /queue (message)
    API->>Queue: Add(message, maxSize)
    Queue-->>API: QueuedMessage{id}
    API->>WS: queue_updated {added}
    API->>Worker: Enqueue(title request)
    API-->>User: 201 Created

    par Title Generation
        Worker->>Worker: GenerateTitle()
        Worker->>Queue: UpdateTitle(id, title)
        Worker->>WS: queue_message_titled
    end

    Note over Agent: Agent finishes current prompt

    BS->>BS: processNextQueuedMessage()
    BS->>Queue: Pop()
    Queue-->>BS: QueuedMessage
    BS->>WS: queue_message_sending
    opt delay_seconds > 0
        BS->>BS: Sleep(delay)
    end
    BS->>WS: queue_updated {removed}
    BS->>Agent: Prompt(message)
    BS->>WS: queue_message_sent
```

## File Storage

### Location

```
sessions/
└── {session_id}/
    ├── events.jsonl      # Event log (append-only)
    ├── metadata.json     # Session metadata
    └── queue.json        # Message queue (transient)
```

### queue.json Format

```json
{
  "messages": [
    {
      "id": "q-1738396800-abc12345",
      "message": "Fix the login bug",
      "image_ids": [],
      "queued_at": "2026-02-01T12:00:00Z",
      "client_id": "web-client-1",
      "title": "Login Bug Fix"
    },
    {
      "id": "q-1738396800-def67890",
      "message": "",
      "prompt_name": "Review Code",
      "arguments": { "ISSUE_ID": "mitto-42" },
      "queued_at": "2026-02-01T12:01:00Z",
      "client_id": "web-client-1"
    }
  ],
  "updated_at": "2026-02-01T12:01:00Z"
}
```

Named-prompt items persist `prompt_name` and `arguments`; `message` is empty. The name is resolved to full text at dispatch — the persisted file never contains the resolved prompt body.

### Design Decisions

1. **Separate file**: Queue is transient (messages removed when processed), unlike append-only events
2. **Atomic writes**: Uses `fileutil.WriteJSONAtomic()` to prevent corruption
3. **Title in queue**: Stored with message for persistence across server restarts

## Automatic Queue Dequeuing

The queue system supports automatic dequeuing for idle agent sessions:

### Behavior

1. **After prompt completion**: When the agent finishes responding, `processNextQueuedMessage()` is called automatically, which pops the next message from the queue and sends it (applying the configured delay first).

2. **On server startup**: `ProcessPendingQueues()` checks all persisted sessions for queued messages. For sessions with pending items, it:
   - Resumes the session (starts ACP process)
   - Sends the first queued message immediately (delay is skipped because `lastResponseComplete` is zero for freshly resumed sessions)

3. **Delay handling**: The `delay_seconds` configuration controls how long to wait:
   - **Normal flow**: After a prompt completes, sleep for `delay_seconds` before sending the next queued message
   - **Startup flow**: On startup, the delay is skipped for the first message because `lastResponseComplete` is zero (no previous response yet)

### Methods

| Method                       | Location            | Purpose                                                         |
| ---------------------------- | ------------------- | --------------------------------------------------------------- |
| `processNextQueuedMessage()` | `BackgroundSession` | Called after prompt completion, applies delay synchronously     |
| `TryProcessQueuedMessage()`  | `BackgroundSession` | Used for startup/periodic checking, respects delay elapsed time |
| `ProcessPendingQueues()`     | `SessionManager`    | Called on server startup, resumes sessions with queued items    |

## Frontend Integration

### State Management

The frontend tracks queue state via `useWebSocket` hook:

```javascript
const {
  queueLength, // Current queue size
  queueConfig, // { enabled, max_size, delay_seconds }
} = useWebSocket();
```

### Queue Full Prevention

`ChatInput` component prevents sending when queue is full:

```javascript
const isQueueFull = isStreaming && queueLength >= queueConfig.max_size;

// Show error if user tries to send
if (isQueueFull) {
  setSendError(
    `Queue is full (${queueConfig.max_size}/${queueConfig.max_size})`,
  );
  return;
}
```

### Title Update Handling

```javascript
case "queue_message_titled":
  console.log(`Queue message titled: ${msg.data?.message_id} -> "${msg.data?.title}"`);
  // Future: Update queue management UI
  break;
```

## Thread Safety

| Component           | Mechanism        | Notes                            |
| ------------------- | ---------------- | -------------------------------- |
| `Queue`             | `sync.Mutex`     | Protects read-modify-write cycle |
| `QueueTitleWorker`  | Buffered channel | Sequential processing            |
| `BackgroundSession` | Observer pattern | Thread-safe notifications        |

## Related Documentation

- [Session Management](session-management.md) — Session lifecycle and state ownership
- [WebSocket Documentation](websockets/) — WebSocket protocol details
- [Architecture](architecture.md) — Overall system architecture
