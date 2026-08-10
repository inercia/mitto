# CLI Conversation Commands — Design Decision Record

Status: decided (`mitto-pscc.1`). Scope: design only — no code ships in this
record; implementation is tracked across the sibling issues of the
`mitto-pscc` epic (`mitto-pscc.2`–`.11`), referenced by ID below. This
record decides the command surface, output contract and exit-code mapping
for `mitto conversation` / `mitto auth` before any of those commands land,
so the surface does not drift between sibling issues.

## Context

`mitto-pscc` adds a `mitto conversation` command suite (plus `mitto auth`)
built entirely on the Go SDK ([`pkg/api`](go-client-library.md)), so common
Mitto operations are scriptable from the terminal. This is new command
surface, not a migration: an audit of `internal/cmd` (`mitto-pscc` epic
notes) found no existing commands that talk to a running Mitto server over
HTTP — `mitto cli` spawns an ACP agent directly and `mitto web` **is** the
server. Neither is touched by this record.

## 1. Command tree

```
mitto conversation new     — create a conversation
mitto conversation list    — list conversations
mitto conversation get     — show conversation details
mitto conversation delete  — delete a conversation
mitto conversation send    — enqueue a prompt (REST queue), --wait
mitto conversation chat    — interactive terminal chat over WebSocket
mitto auth status          — inspect the resolved server/token
mitto auth rotate          — rotate the shared token
```

`conversation` and `auth` are plain cobra parents registered in
`internal/cmd` (`mitto-pscc.4`). No `mitto conv` alias in the first cut.
`mitto cli` is untouched and **not** deprecated.

## 2. Global flags and precedence

Registered as **persistent flags on the `conversation`/`auth` parents**,
not on `rootCmd`: rootCmd's existing persistent flags (`--acp`, `--dir`,
`--auto-approve`) are ACP-spawn concepts that do not apply to
server-touching commands, and vice versa.

| Flag           | Maps to                                          |
| -------------- | ------------------------------------------------ |
| `--url`        | SDK `baseURL`                                    |
| `--token`      | `api.WithBearerToken`                            |
| `--api-prefix` | `api.WithAPIPrefix` (new option, `mitto-rwxq.7`) |
| `--timeout`    | `api.WithTimeout`                                |
| `--output`     | `table` (default) \| `json` \| `yaml`            |
| `--no-color`   | forces glamour's notty style                     |

Precedence, resolved once in `mitto-pscc.4`:

```
explicit flag > MITTO_URL / MITTO_TOKEN / MITTO_API_PREFIX env
              > instance.json (mitto-pscc.2) > error
```

`NO_COLOR` (env) is equivalent to `--no-color`. `--api-prefix` cannot be
honored until `mitto-rwxq.7` adds `WithAPIPrefix` (`client.New` hardcodes
`/mitto` today) — until then the flag is accepted but only the default
prefix works, and `conversation list` filters client-side pending that
same issue's `ListSessions` filter arguments.

## 3. `PersistentPreRunE` must be skipped

`rootCmd.PersistentPreRunE` loads local `settings.json` and touches the
macOS Keychain. The `conversation` and `auth` subtrees must skip it: a
server-touching command must not require local config and must never
block on a Keychain prompt when run non-interactively (e.g. from a script
or CI).

Skip on the **parent** name, not the command name. `internal/cmd/root.go`
has two skip mechanisms and only one of them works here: `mcp` and
`prompt` are leaf commands skipped by `cmd.Name()`, whereas `prompts`,
`processors` and `agents` are parents skipped by `cmd.Parent().Name()`.
`conversation` and `auth` are parents, so `mitto-pscc.4` must extend the
`cmd.Parent().Name()` branch — a `cmd.Name() == "conversation"` check
would never fire for `conversation get`.

## 4. Output contract

- Default: human-readable table on stdout via `text/tabwriter` (no new
  dependency), one fixed column set per command.
- `--output json` / `--output yaml`: marshal the **SDK response types
  directly** (`api.SessionInfo`, `api.QueuedMessage`,
  `api.QueueListResponse`, `api.LoopConfig`, ...) — no bespoke CLI DTOs, so
  the emitted shape is exactly the documented REST shape and cannot drift.
  YAML round-trips through the same JSON tags (marshal-to-JSON-then-YAML),
  so JSON tags remain the single source of truth for field names.
- Machine output (the table, or `json`/`yaml`) goes to stdout and nothing
  else does. All human chatter, progress and errors go to stderr.
- A list command with zero results prints `[]`, never `null`, and exits 0.
- Streamed agent markdown (`mitto-pscc.8`, glamour) never passes through
  this table/json/yaml path — it is a separate rendering concern for
  `conversation chat` and any command that prints an agent message body.

## 5. Exit codes

| Code | Meaning            | Source                                                           |
| ---- | ------------------ | ---------------------------------------------------------------- |
| 0    | ok                 | —                                                                |
| 1    | generic            | anything not classified below                                    |
| 2    | usage              | cobra flag/arg validation error                                  |
| 3    | server unreachable | connection-refused / no-such-host / timeout, stale instance file |
| 4    | auth failure       | `errors.Is(err, api.ErrUnauthenticated \| api.ErrForbidden)`     |
| 5    | not found          | `errors.Is(err, api.ErrNotFound)`                                |
| 6    | wait timed out     | `conversation send --wait` only: `--wait-timeout` expired before the agent finished (`mitto-pscc.6`) |

Mapped **mechanically in one function** in `mitto-pscc.4`, not per
command. `(*api.APIError).Is` matches by HTTP status
([`pkg/api/errors.go`](../../pkg/api/errors.go)), so an app-specific `Code`
(e.g. `queue_full` on a 409) still classifies correctly through the status.

Mechanism: `cmd/mitto/main.go` currently does a blanket `os.Exit(1)` on any
`Execute` error. An `exitCodeError` type carries the resolved code;
`Execute` returns it unchanged and `main` unwraps it with `errors.As`,
defaulting to 1 when the error isn't one. Existing commands (`cli`, `web`,
...) are unaffected — they never produce an `exitCodeError`.

## 6. Stability

Exit codes, `--output json` field names (inherited from the SDK types),
and flag names are a public contract from first release, covered by
[API Stability Tiers](api-stability.md) (`stable`, deprecation window
applies). **Table output is explicitly unstable** and must not be parsed
by scripts — `--output json`/`yaml` is the contract.

## 7. `conversation chat` decisions (rescoped `mitto-pscc.7`/`.8`)

- **TTY required.** Non-TTY stdout or stdin exits 2 (usage) with a message
  pointing at `conversation send --wait`.
- **Input history / slash-command completion are deferred**, not required
  for the first cut (`bubbles/textarea` provides neither; `mitto cli`
  keeps its `reeflective/readline` affordances unchanged). Tracked as
  `mitto-pscc.11`, blocked on `mitto-pscc.7`.
- `--no-color`/`NO_COLOR` select glamour's notty style; they never disable
  rendering and never change `--output`, which is always colourless.
- Reference architecture: `charmbracelet/crush` — one `tea.Model`,
  sub-components as imperative structs, an event-pump goroutine calling
  `program.Send`, no I/O in `Update`, `x/ansi` for ANSI-safe string work.

## 8. `conversation send` decisions (`mitto-pscc.6`)

- **With `--wait`, the WebSocket is connected (and its event stream
  registered via `Session.EventsChan`) before the REST enqueue call**, not
  after. `handleAddToQueue` fires `go bs.TryProcessQueuedMessage()`
  (`internal/web/handlers/queue.go`), so an idle session can finish the
  whole turn before a post-enqueue dial completes, hanging the command to
  `--wait-timeout` on a turn that already succeeded.
- **Correlation is two-phase, keyed on the queue message ID.**
  `prompt_complete` carries no message ID, so on a busy session it could
  otherwise be mistaken for the wrong (already in-flight) turn's
  completion. The command first waits for `OnQueueMessageSending` to report
  the exact ID returned by the enqueue call, then treats the next
  `prompt_complete` as this message's completion.
- **A wait timeout never cancels the agent.** On expiry the command closes
  its WebSocket and exits with code 6; the queued/running turn continues
  server-side.
- **`--image` cannot be combined with `--prompt-name`** (exit 2): the SDK
  has no combined "named prompt + images" enqueue call.
- **`--output json`/`yaml` with `--wait`** emits a single object
  (`{queued, message, event_count}`) once the turn completes, instead of
  streaming the reply to stdout.

## 9. Out of scope

Workspace management, prompts, processors, settings, agents, files,
dashboard, any shell-completion work, and any change to `mitto cli`.

## Related issues

`mitto-pscc` (epic) · `mitto-pscc.2`–`.11` (implementation) · `mitto-rwxq.3`
(typed errors) · `mitto-rwxq.4` (auth) · `mitto-rwxq.7` (`WithAPIPrefix`,
`ListSessions` filters) · [Go Client Library](go-client-library.md) ·
[API Stability Tiers](api-stability.md).
