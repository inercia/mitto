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

| Flag           | Maps to                                                                                                                                                                                                                                                               |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--url`        | SDK `baseURL`                                                                                                                                                                                                                                                         |
| `--token`      | `api.WithBearerToken`                                                                                                                                                                                                                                                 |
| `--api-prefix` | `api.WithAPIPrefix` (new option, `mitto-rwxq.7`)                                                                                                                                                                                                                      |
| `--timeout`    | `api.WithTimeout`                                                                                                                                                                                                                                                     |
| `--output`     | `table` (default) \| `json` \| `yaml`                                                                                                                                                                                                                                 |
| `--no-color`   | forces glamour's notty style                                                                                                                                                                                                                                          |
| `--style`      | `auto` (default) \| `dark` \| `light` glamour palette for styled mode (mitto-u7k3); `auto` detects the terminal background — the chat TUI via `tea.RequestBackgroundColor`, any other termmd caller via `termmd.ResolveTheme`'s `lipgloss.HasDarkBackground` fallback |

Precedence, resolved once in `mitto-pscc.4`:

```
explicit flag > MITTO_URL / MITTO_TOKEN / MITTO_API_PREFIX env
              > instance.json (mitto-pscc.2) > error
```

`NO_COLOR` (env) is equivalent to `--no-color`. `--api-prefix` cannot be
honored until `mitto-rwxq.7` adds `WithAPIPrefix` (`api.New` hardcodes
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

| Code | Meaning            | Source                                                                                               |
| ---- | ------------------ | ---------------------------------------------------------------------------------------------------- |
| 0    | ok                 | —                                                                                                    |
| 1    | generic            | anything not classified below                                                                        |
| 2    | usage              | cobra flag/arg validation error                                                                      |
| 3    | server unreachable | connection-refused / no-such-host / timeout, stale instance file                                     |
| 4    | auth failure       | `errors.Is(err, api.ErrUnauthenticated \| api.ErrForbidden)`                                         |
| 5    | not found          | `errors.Is(err, api.ErrNotFound)`                                                                    |
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

- **Conversation ID is optional.** `mitto conversation chat <conversation-id>`
  keeps the direct attach path and does not list sessions. With no ID, the CLI
  calls `ListSessions`, excludes archived sessions and only children whose
  `child_origin` is `auto`, sorts the remainder newest-first by `updated_at`,
  and opens a Bubble Tea selector before starting the same chat bootstrap.
  Human- and MCP-created children remain visible. Rows include title/ID,
  status, workspace or directory, and update time; Esc/Ctrl-C/q cancels without
  attaching. An empty eligible list or list failure exits non-zero with a clear
  error.
- **TTY required.** Non-TTY stdout or stdin exits 2 (usage) with a message
  pointing at `conversation send --wait`.
- **`mitto-pscc.11` landed**: input history and slash-command completion,
  reimplemented as plain imperative structs (`internal/chatui/inputhistory.go`,
  `completion.go`, `commands.go`) rather than pulling in `reeflective/readline`
  (which owns its own terminal and cannot coexist with an alt-screen Bubble
  Tea program). Key routing precedence in `handleKey`, highest first:
  permission modal > completion menu > input history > textarea.
  - **History**: `up`/`down` recall previously submitted lines, gated to the
    textarea's first/last line (`Line() == 0` / `Line() == LineCount()-1`) so
    multi-line editing is unaffected. Persisted per conversation at
    `$MITTO_DIR/chat-history/<conversation-id>.json`, capped at 200 entries;
    loaded once at bootstrap, saved via a `tea.Cmd` after each submit (never
    inline in `Update`).
  - **Completion**: `tab` on a single-line `/`-prefixed input opens a menu
    (immediate completion on a single match); `up`/`down`/`tab` navigate,
    `enter` accepts, `esc` closes the menu only (does not cancel the turn).
    The chat command set is `/help` (`/h`, `/?`), `/quit` (`/exit`, `/q`),
    `/cancel`, and `/clear` (TUI-only — clears the transcript pane; no `mitto
cli` counterpart). An unrecognized `/word` is refused locally (error
    item) rather than forwarded to the agent, matching `mitto cli`.
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
  **`mitto-u7k3` landed**: glamour v2 dropped v1's `WithAutoStyle`, so styled
  mode's dark/light palette is now a `termmd.Theme` (`Options.Theme`, zero
  value `ThemeDark`) resolved by `ResolveTheme` — `--style dark|light` >
  `$GLAMOUR_STYLE` (only when exactly `dark`/`light`) > terminal background
  detection > dark. One-shot commands detect via
  `lipgloss.HasDarkBackground`; the chat TUI cannot use that raw-mode query
  (it would fight Bubble Tea's own input reader) and instead issues
  `tea.RequestBackgroundColor()` from `Init` and resolves on the
  `tea.BackgroundColorMsg` reply in `Update`.
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
- **Semantic palette and no-color behavior (`mitto-1bml.1`)**: chat roles,
  status, completion, permission modal, textarea, and the conversation picker
  share one dark/light semantic palette. An auto `tea.BackgroundColorMsg`
  updates both Glamour and every Lipgloss/Bubbles style. `--no-color` and
  `$NO_COLOR` select a plain palette that preserves text, selection markers,
  rounded borders, padding, and spacing while emitting no ANSI styling.
- **Status semantics (`mitto-1bml.5`)**: the footer keeps conversation/ACP
  identity separate from explicit symbol-plus-label connection cues
  (`connecting`, `connected`, or `disconnected`) and an independent `working`
  cue. The same symbols and labels remain in no-color mode. ANSI-aware
  truncation preserves a single row; very narrow terminals retain compact,
  distinct state symbols when the full labels cannot fit.
- **Interactive surface hierarchy (`mitto-1bml.4`)**: the focused composer has
  an accent boundary and a separate muted key hint; completion rows distinguish
  command names from descriptions and retain a `>` selection marker in plain
  mode; permission requests use a warning boundary with explicit `[y] Approve`
  and `[n] Deny` actions. All three surfaces use ANSI-aware wrapping or
  truncation and omit borders when a terminal is too narrow to contain them.
  Their rendered heights, rather than fixed row assumptions, determine the
  transcript viewport so opening completion or permission UI cannot overlap it.
- **Transcript item hierarchy (`mitto-1bml.3`)**: user, assistant, thought,
  tool, local system, and error entries carry persistent bracketed text labels.
  User/assistant/thought labels are decorated outside the cached Glamour body,
  preserving Markdown syntax colors and streaming-cache reuse. The same labels
  remain as structural cues in `--no-color`/`$NO_COLOR` mode.

### Visual palette regression matrix and manual checks

`internal/chatui/visual_regression_test.go` renders the picker, representative
chat frames, completion and permission surfaces, and every footer state directly.
It deliberately avoids a PTY and `teatest`, and leaves Markdown byte goldens in
`internal/termmd`. The matrix checks the following presentation contract:

| Presentation | Color behavior | Required non-color cues |
| ------------ | -------------- | ----------------------- |
| `--style dark` | Dark semantic palette for every chat and picker surface | `>` selection marker, bracketed transcript roles, state symbols and labels, explicit permission actions |
| `--style light` | Light semantic palette with the same text and geometry as dark | Same cues as dark; palette changes must not change content or layout |
| `--no-color` / `$NO_COLOR` | No ANSI styling from any surface | All selection, role, status, border, and action cues remain visible |

Before changing the palette or terminal layout, manually run
`mitto conversation chat` in a real terminal and check:

1. Pin `--style dark` and `--style light`; move the picker and completion
   selections and confirm exactly one highlighted row follows the `>` marker.
2. Show user, assistant, thought, tool, system, and error entries; confirm their
   bracketed labels remain visible and Markdown bodies retain readable syntax.
3. Open a permission request and confirm `[y] Approve` and `[n] Deny` remain
   distinct in color and text.
4. Observe connecting, connected, working, and disconnected footer transitions;
   confirm both symbols and labels change.
5. Repeat with `--no-color` (and separately `$NO_COLOR`), then resize to a narrow
   terminal; confirm ANSI is absent, cues remain distinguishable, rows do not
   overflow, and the transcript does not overlap the composer or modal.

- **Test strategy (`mitto-pscc.12`)**: four layers, each independently
  useful and owning disjoint files, closing gaps rather than building from
  scratch (Layers 1 and 2 pre-existed from `.7`/`.8`).
  1. **`Update()` table tests** (`internal/chatui/model_test.go`) — every
     `api.Event` kind and every internal `tea.Msg` (`sendDoneMsg`,
     `cancelDoneMsg`, `permAnsweredMsg`, `streamEndMsg`) drives `Update`
     directly against a `nil` `*api.Session`; assertions are on model
     state and on the returned `tea.Cmd`'s identity (its `()` result type),
     never by executing a `Cmd` that would dereference the nil session.
     Covers the `WindowSizeMsg` geometry contract too (transcript height =
     terminal height minus the rendered bottom surface, rendered status, and
     two separator rows, clamped to 1).
  2. **Renderer goldens** (`internal/termmd/testdata/`, shipped with `.8`) —
     `corpus.plain.golden`/`corpus.styled.golden`/`fallback.golden` plus a
     `go.mod`-read version-pin test (`TestGlamourVersion_Pinned`) asserting
     the exact `charm.land/glamour/v2` version the goldens were generated
     against, so a dependency bump shows up as a pin failure pointing here
     rather than an unexplained golden diff. Width-variation goldens are
     gated on `mitto-pscc.8.1`'s stable-prefix cache (still open as of this
     writing) and deliberately not added speculatively.
  3. **Scripted-WebSocket pump tests** (`internal/chatui/pump_test.go`) —
     drive `RunPump` against a real `*api.Session` connected to a stub
     `httptest`-backed WebSocket server, covering duplicate/out-of-order
     seq (dedup) and the close-frame path (exactly one `streamEndMsg`
     carrying the terminal error). The ~30-line `wsTestServer` harness is
     **copied** from `pkg/api/resilience_test.go` rather than exported as a
     shared testing package — no second consumer had appeared, and
     `RunPump` already takes the `programSender` interface, so a fake
     sender is sufficient. One test also pins a real, easy-to-miss
     contract: a `*api.Session` disconnect terminates the specific
     `EventsChan()` stream unconditionally, even with `WithReconnect`
     enabled — `RunPump` never sees the redialed connection's events
     through the same stream, since pump-level reconnect awareness is
     explicitly out of scope until `mitto-rwxq.5`.
  4. **Build-tagged e2e smoke** (`tests/integration/inprocess/`) — one thin
     path (`//go:build integration`) wiring `chatui.Model` + `chatui.RunPump`
     against `SetupTestServer`'s real web server and mock ACP agent: connect,
     send a prompt, see the reply rendered in `View()`, cancel cleanly. It
     drives the `Model` directly rather than through a PTY, since the TUI
     needs an alt-screen terminal CI does not have and Layers 1–3 already
     cover the logic — this only pins the real wiring.
  - **`teatest` (`charmbracelet/x/exp/teatest`): rejected**, confirmed absent
    from `go.sum`. It would mostly duplicate Layer 1 more slowly by
    asserting on rendered bytes instead of model state, and adds
    golden-output brittleness against `lipgloss`/`glamour` style drift —
    Layer 2 already owns markdown-rendering goldens; `teatest` would
    encourage re-litigating that concern at the wrong layer. **Revisit
    trigger**: only if a rendering/layout bug appears that Layers 1–3
    provably cannot catch, and if adopted, scope it to layout assertions
    only, never markdown content.

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
- **`list --workspace` filters by workspace UUID or name** (`mitto-pscc.5.1`).
  `GET /api/sessions` derives `workspace_uuid`/`workspace_name` per session
  (live session → `SessionManager.GetWorkspaceUUIDForSession`; else a
  `(WorkingDir, ACPServer)` registry lookup; left empty if neither resolves —
  workspace membership is never persisted on `session.Metadata`, only
  derived at list time). `api.SessionInfo` mirrors both fields. The CLI
  matches `--workspace` against either field client-side (UUID exact,
  name case-insensitive); a value matching no configured workspace yields
  an empty result rather than an error.
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
  _stale_ entry (one no longer matching a real call site) also fails, so the
  list cannot rot into permission-by-accident. The only entries are
  `mcp.go`'s client and request: `mitto mcp --proxy-to` speaks MCP
  Streamable-HTTP JSON-RPC to an MCP endpoint, not the Mitto REST API.

## 11. `mitto auth` decisions (`mitto-pscc.9`)

**The token `auth status` reports is the one `instance.json` holds.** Before
`mitto-pscc.9` the server resolved its shared token only from
`MITTO_SHARED_TOKEN`, `settings.json`, or the keychain, so the token
`instance.json` already carried was never an accepted credential. `mitto web`
and the macOS app now resolve that token (`instancefile.ResolveToken`) _before_
constructing the server and adopt it into `web.auth.shared_token` **only when no
operator-configured token exists** — explicit config always wins, and the
adopted value is never written back to `settings.json` or the keychain. A token
alone still does not enable authentication (`simple`/`cloudflare` must be
configured first), so adoption is a no-op on a fully unauthenticated server.

**Rotation is server-side, not a CLI file rewrite.** `mitto auth rotate` calls
`POST {prefix}/api/auth/rotate-token`; the server generates the new token,
rewrites `instance.json` **first**, and installs it on the live `AuthManager`
only after that write succeeds — a failed write leaves the old token valid
everywhere instead of desyncing memory from disk. A CLI-only rewrite of
`instance.json` would leave the running server validating the old token, which
is worse than having no command at all.

- **Localhost-only**, like `/api/save-file-to-path`: the request is rejected
  outright when it arrives through the external listener. The loopback bypass in
  `AuthMiddleware` runs before the bearer check, so rotation works even with
  auth disabled and there is no chicken-and-egg — but it must therefore never be
  reachable remotely.
- **Refused (409) for an operator-configured token**, naming the source.
  Rotating a secret that lives in the environment, `settings.json`, or the
  keychain is out of scope; the operator updates it at its source.
- The response carries **only the new fingerprint**. The value goes to
  `instance.json` and nowhere else — not a response body, log line, or argv.
- Every client holding the previous token is rejected immediately; there is no
  grace window, so `rotate` prints that warning on stderr.

**A token is only ever shown as a fingerprint** —
`instancefile.Fingerprint` (first 8 hex chars of SHA-256). `auth status`
prints `token_source` (`flag`|`env`|`instance.json`|`none`) and
`token_fingerprint`, never the value, no prefix and no length. The fingerprint
exists so an operator can confirm two token values match (e.g. before/after
`rotate`) without exposing the secret.

**`auth status` proves reachability _and_ the credential.** `/api/health` and
`/api/auth-info` are both public, so they would report "reachable" even with a
wrong token; when `auth-info` says auth is enabled, `status` additionally issues
one authenticated `ListSessions` call so a bad credential surfaces as exit 4.
All three endpoints went into `pkg/api` (`auth_admin.go`) rather than being
allowlisted as raw `net/http` calls — §10's gate applies to `auth` too.

## 12. Out of scope

Workspace management, prompts, processors, settings, agents, files,
dashboard, any shell-completion work, and any change to `mitto cli`.

## Related issues

`mitto-pscc` (epic) · `mitto-pscc.2`–`.11` (implementation) · `mitto-rwxq.3`
(typed errors) · `mitto-rwxq.4` (auth) · `mitto-rwxq.7` (`WithAPIPrefix`,
`ListSessions` filters) · [Go Client Library](go-client-library.md) ·
[API Stability Tiers](api-stability.md).
