---
description: Periodic prompt design patterns, silent mode, spawn deduplication, gate testing
globs:
  - "internal/config/prompts*.go"
  - "internal/web/handlers/session_*.go"
keywords:
  - periodic
  - silent-mode
  - IsPeriodic
  - IsPeriodicForced
  - spawn-deduplication
  - Children
  - MCPText
  - gate-testing
---

# Periodic Prompt Design Patterns

## Silent Mode vs Interactive Mode

Periodic prompts must detect runtime context and adapt behavior:

```go
{{ if and .Session.IsPeriodic (not .Session.IsPeriodicForced) }}
  // Silent mode: scheduled run, user not watching
  // Use mitto_ui_notify ONLY (non-blocking)
  // Do NOT use interactive tools: options, form, textbox
  // Act autonomously when safe; notify on failures
{{ else }}
  // Interactive mode: forced run or non-periodic conversation
  // May use all UI tools freely for confirmations
{{ end }}
```

**Key fields**:
- `.Session.IsPeriodic` — true if conversation has periodic config enabled
- `.Session.IsPeriodicForced` — true if user force-triggered the run (via `mitto_conversation_run_periodic_now_mitto`)

**Pattern**: Silent mode never blocks the user; interactive mode can present dialogs, options, textboxes for user input.

## Spawn Deduplication

When a periodic prompt spawns child conversations for multi-step repairs, always check for existing children **before** spawning:

```go
Existing child conversations:
{{ .Children.MCPText }}

Before spawning a new conversation, search the list above for a matching title.
If found and still idle, RE-PROMPT it instead of spawning a duplicate.
```

**Fields**:
- `.Children.MCPText` — list of non-archived child conversations (from `mitto_children_tasks_wait_mitto` context)
- Search child titles for a substring match (e.g., "PR #66" in "Fix CI for PR #66")

**Spawn cap**: Limit to **3 spawns per periodic run**. Prioritize by severity:
1. Rebase conflicts (blocks merge)
2. CI failures (blocks merge)
3. Unresolved review comments (informational)

**Benefits**:
- Avoids duplicate work in progress
- Reduces queue congestion during long-running repairs
- Enables smart re-prompting of idle fixers with new instructions

## Gate Testing Before External Actions

When a periodic prompt identifies CI failures and spawns a fixer conversation, instruct the fixer to **run the full local gate suite BEFORE pushing**:

```yaml
Before pushing, run ALL gates in order:
1. make fmt-check
2. make lint
3. make test (unit tests)
4. make build-mock-acp && make test-integration

Push only after ALL gates pass locally.
```

**Why**: Periodic automation that reveals CI failures incrementally (fix one, reveal the next) creates unnecessary re-runs. Full local validation before push breaks this cycle.

**Common gates in mitto**:
- `make fmt-check` — Go format check (gofmt)
- `make lint` — Linting (golangci-lint)
- `make test` — Unit tests
- `make build-mock-acp` — Build mock ACP test server
- `make test-integration` — Integration tests (requires mock-acp)

## Notification Pattern

In silent mode, communicate via `mitto_ui_notify_mitto`:

```go
mitto_ui_notify_mitto(
  self_id: "session-id",
  title: "⚠️ PR #66 — Lint Failed",
  message: "gofmt needed in prompt_dispatcher.go. Re-prompted fixer.",
  style: "warning"
)
```

**Never use** interactive tools in silent mode:
- ❌ `mitto_ui_options_mitto`
- ❌ `mitto_ui_form_mitto`
- ❌ `mitto_ui_textbox_mitto`

## State Persistence

For long-running periodic prompts that track external state (CI status, branch status, etc.):
- Store state in a **file in the workspace** (`.mitto/state/` convention)
- Reference state file path in compact continuation messages
- Use `.Iteration.IsUninterrupted` to detect continuation vs restart

Example: "Continue: PR #66 lint fix. State: `.mitto/state/pr66-ci.json`."

This enables compacting history while preserving state across long loops.
