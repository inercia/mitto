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
- **Exception: a command whose output has no single REST response shape**
  composes a CLI-owned struct whose fields are themselves SDK types
  (`conversation send --wait`'s `waitResult`, `conversation get`'s
  `conversationDetails`, `conversation delete`'s `deleteResult` — DELETE has
  no response body at all). The composition is CLI-owned; every nested
  payload is still an unmodified SDK type, so the anti-drift guarantee holds
  for the parts that come from the server.
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
- **`mitto-pscc.8` landed**: `internal/termmd` wraps `charm.land/glamour/v2`
  behind a single `Render(body string, opts Options) string` entry point,
  with `ModeStyled`/`ModePlain`/`ModeDegraded` and `ResolveMode`/
  `TerminalWidth` helpers centralising style/width policy. Owned by the CLI;
  `internal/conversation` must not import it. `.7`'s transcript pane and any
  one-shot command printing an agent message body are its consumers. The
  streaming stable-prefix cache is deferred as `mitto-pscc.8.1`.
  **Styled mode is dark-only**: glamour v2 dropped v1's `WithAutoStyle`, so
  light-terminal selection needs explicit background detection — deferred to
  `.7` as `mitto-u7k3`.
- **`mitto-pscc.7` landed**: `internal/chatui` is the CLI-owned TUI package
  (one `tea.Model` in `model.go`, imperative sub-components in
  `transcript.go`/`statusline.go`/`permission.go`, event pump in `pump.go`),
  driven by `mitto conversation chat` (`internal/cmd/conversation_chat.go`).
  Like `internal/termmd`, `internal/conversation` must not import it. The
  bootstrap reuses §8's connect-before-use ordering: `LoadEvents(--history)`
  and its `OnEventsLoaded` seed the transcript before `tea.NewProgram`, so
  history replay never races the live pump. **Our own `user_prompt` echo is
  dropped by sender ID** — the server broadcasts it back to the sender
  (`session_ws.go` `OnUserPrompt`), which would otherwise double-render every
  message the input textarea already appended optimistically; prompts from
  other clients on the same conversation still render.

## 8. `conversation send` decisions (`mitto-pscc.6`)

- **With `--wait`, the WebSocket is connected (and its event stream
  registered via `Session.EventsChan`) before the REST enqueue call**, not
  after. `handleAddToQueue` fires `go bs.TryProcessQueuedMessage()`
  (`internal/web/handlers/queue.go`), so an idle session can finish the
  whole turn before a post-enqueue dial completes, hanging the command to
  `--wait-timeout` on a turn that already succeeded.
- **A bare `Connect()` is not enough to receive live notifications.** The
  server only calls `BackgroundSession.AddObserver` from its `load_events`
  handler (`internal/web/session_ws.go` `postLoadProcessing`) — connecting
  alone leaves this client un-registered, so `agent_message`/
  `prompt_complete`/`queue_message_sending` would never arrive (found
  during the Test phase: the command would silently hang to
  `--wait-timeout` on every turn, regardless of completion). The command
  therefore calls `Session.LoadEvents(1, 0, 0)` right after connecting and
  blocks on the resulting `OnEventsLoaded` callback before enqueuing —
  `limit=1` keeps the (unused) historical replay minimal; those events are
  not modelled by the `Event` stream and are ignored.
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

## 9. Lifecycle verb decisions (`mitto-pscc.5`)

- **`new --wait` defers queue seeding until after the WebSocket is
  connected.** Without `--wait`, `--prompt-name` is seeded atomically at
  creation via `CreateSessionRequest.InitialPromptName`; with `--wait` that
  atomic seed would dispatch immediately and could finish the turn before a
  post-creation `Connect` — so `--wait` always enqueues after
  `connectAndAwaitLoad`, preserving §8's connect-before-enqueue ordering.
  That block is shared by `send --wait` and `new --wait`, not duplicated.
- **`list` filters client-side** pending `mitto-rwxq.7`'s `ListSessions`
  filter arguments (§2). `--dir` and `--archived` filter the full list
  locally; `--running` intersects it with `ListRunningSessions`. Archived
  conversations are excluded by default, matching
  `session.Metadata.Archived`'s "hidden from main list by default".
- **`list --workspace` is accepted but a no-op with a stderr warning**:
  neither `GET /api/sessions` nor `api.SessionInfo` carries a workspace
  UUID. Tracked as `mitto-pscc.5.1`; use `--dir` meanwhile.
- **`get`'s missing loop is not an error.** The session's own 404 maps to
  exit 5, but `GetLoop`'s synthetic `ErrNotFound` (session confirmed to
  exist) is swallowed to a nil `Loop`, omitted from `json`/`yaml`.
- **`delete` refuses on a non-TTY stdin without `--force`** (exit 2) rather
  than hanging on a read that will never receive input. Detection is a raw
  `os.Stdin.Stat()` + `os.ModeCharDevice` check against the **real**
  `os.Stdin`, deliberately not `cmd.InOrStdin()` and without adding
  `golang.org/x/term`. Declining the prompt exits 0.

## 10. Enforcing "SDK only" (`mitto-pscc.10`)

§Context's claim that these commands are built entirely on the SDK is
machine-enforced by `internal/cmd/no_raw_http_test.go`, which parses every
non-test `.go` file in `internal/cmd` (`go/parser`, so comments and string
literals never match) and fails on any use of a `net/http` client-egress
symbol that is not on an explicit allowlist.

- **Symbols are classified, not the import.** Flagging "imports `net/http`"
  would flag `conversation_send.go` for `http.DetectContentType` — a pure
  MIME sniff with no network — forcing a meaningless allowlist entry and
  eroding the signal an entry is meant to carry. Only egress constructs are
  flagged (`Client`, `DefaultClient`, `Transport`, `RoundTripper`,
  `NewRequest*`, `Get`/`Head`/`Post`/`PostForm`, `ReadResponse`); status and
  method constants are not.
- **No URL-path matching.** `mcp.go`'s target is a variable threaded from a
  flag, so no path is statically visible; forbidding client construction
  outright is both stronger and statically decidable.
- **Each allowlist entry is `(file, symbol)` + a mandatory `Reason`**, and a
  *stale* entry (one no longer matching a real call site) also fails, so the
  list cannot rot into permission-by-accident. The only entries are
  `mcp.go`'s client and request: `mitto mcp --proxy-to` speaks MCP
  Streamable-HTTP JSON-RPC to an MCP endpoint, not the Mitto REST API.

## 11. Out of scope

Workspace management, prompts, processors, settings, agents, files,
dashboard, any shell-completion work, and any change to `mitto cli`.

## Related issues

`mitto-pscc` (epic) · `mitto-pscc.2`–`.11` (implementation) · `mitto-rwxq.3`
(typed errors) · `mitto-rwxq.4` (auth) · `mitto-rwxq.7` (`WithAPIPrefix`,
`ListSessions` filters) · [Go Client Library](go-client-library.md) ·
[API Stability Tiers](api-stability.md).
