# Web Interface

The web interface provides a browser-based UI for ACP communication, accessible via `mitto web`.

## Architecture Overview

```mermaid
graph TB
    subgraph "Browser"
        UI[Preact UI]
        EVENTS_WS[Events WebSocket<br/>/api/events]
        SESSION_WS[Session WebSocket<br/>/api/sessions/{id}/ws]
    end

    subgraph "Mitto Web Server"
        HTTP[HTTP Server]
        EVENTS_MGR[GlobalEventsManager]
        SESSION_WSC[SessionWSClient]
        BG_SESSION[BackgroundSession]
        MD_BUF[Markdown Buffer]
        WEB_CLIENT[Web ACP Client]
    end

    subgraph "ACP Server"
        AGENT[AI Agent]
    end

    UI <--> EVENTS_WS
    UI <--> SESSION_WS
    EVENTS_WS <-->|lifecycle events| EVENTS_MGR
    SESSION_WS <-->|session events| SESSION_WSC
    HTTP -->|Static Files| UI
    SESSION_WSC -.->|observes| BG_SESSION
    BG_SESSION --> WEB_CLIENT
    WEB_CLIENT <-->|stdin/stdout| AGENT
    WEB_CLIENT -->|chunks| MD_BUF
    MD_BUF -->|HTML| BG_SESSION
```

## REST API Endpoints

All routes are declared in `internal/web/routes.go`. Routes use Go 1.22 method-qualified patterns (`METHOD /path`); method mismatches return 405. Most endpoints require a valid session cookie. Exceptions are listed under **Public / Auth** below.

**Error envelope** (standard): `{"error": {"code": "...", "message": "...", "details":{...}?}}`. Common codes: `unauthenticated` (401), `method_not_allowed` (405), `server_error` (500). A small set of `external-stable` endpoints (native app / viewer / load-balancer integration) may retain a legacy flat `{"error": "..."}` shape and must not be renamed.

**Mid-migration note**: Some workspace-scoped endpoints are still at flat `/api/workspace-*` paths (notably `/api/workspace-prompts*`) and use `?working_dir=` query params. These are being nested under `/api/workspaces/{uuid}/…` as part of epic mitto-ank; see [`docs/devel/rest-api-conventions.md`](rest-api-conventions.md) for the full current→target mapping.

---

### Public / Auth

These endpoints do **not** require a session cookie.

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/login` | POST | Authenticate (only when auth is configured) |
| `/api/logout` | POST | End authenticated session |
| `/api/csrf-token` | GET | Return a CSRF token for subsequent mutations |
| `/api/auth-info` | GET | Login-page bootstrap (auth mode, OIDC config) |
| `/api/health` | GET | Load-balancer liveness probe — always 200 OK (external-stable) |
| `/api/callback/` | POST | Capability-URL webhook — token in path, no cookie needed (external-stable) |
| `/api/supported-runners` | GET | List runner types supported by the server |

---

### Sessions

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/sessions` | GET, POST | List all sessions (GET); create a new session (POST) |
| `GET /api/sessions/running` | GET | List currently running (non-idle) sessions |
| `GET /api/sessions/{id}` | GET | Get session detail |
| `PATCH /api/sessions/{id}` | PATCH | Update session metadata (name, archived, beads_issue, …) |
| `DELETE /api/sessions/{id}` | DELETE | Delete (archive) a session |
| `GET /api/sessions/{id}/events` | GET | Load persisted event log for a session (REST fallback; the primary live channel is the WebSocket below) |
| `/api/sessions/{id}/ws` | WebSocket | Per-session streaming WebSocket — see [`docs/devel/websockets/`](websockets/) |
| `/api/sessions/{id}/user-data` | GET, PUT | Per-session structured user-data attributes |
| `/api/sessions/{id}/callback` | GET, POST, DELETE | Get status / generate-rotate / revoke capability-URL token |
| `/api/sessions/{id}/settings` | GET, PUT | Per-session advanced feature flags |
| `/api/sessions/{id}/prune` | POST | Prune old events from session log |
| `/api/sessions/{id}/changes` | GET | Get uncommitted file changes for the session's working dir |
| `/api/sessions/{id}/images` | GET, POST | List uploaded images (GET); upload a new image (POST, multipart) |
| `/api/sessions/{id}/images/{imageId}` | GET, DELETE | Get or delete a specific uploaded image |
| `/api/sessions/{id}/images/from-path` | POST | Upload image by local file-system path (native macOS app — external-stable) |
| `/api/sessions/{id}/files` | GET, POST | List or upload attached files |
| `/api/sessions/{id}/files/{fileId}` | GET, DELETE | Get or delete a specific attached file |
| `/api/sessions/{id}/files/from-path` | POST | Attach file by local path (native macOS app — external-stable) |
| `/api/sessions/{id}/queue` | GET, POST | List pending prompts in queue (GET); enqueue a prompt (POST) |
| `/api/sessions/{id}/queue/{msgId}` | GET, DELETE | Get or cancel a specific queued prompt |
| `/api/sessions/{id}/loop` | GET, PUT, DELETE | Get or set loop execution configuration |
| `/api/sessions/{id}/loop/{subPath}` | varies | Loop sub-resource actions (e.g. trigger-now) |

---

### Workspaces

Workspace resource endpoints are identified by `{uuid}`. The older flat `/api/workspace-*` paths are being migrated; see the mid-migration note above.

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/workspaces` | GET, POST, DELETE | List all workspaces (GET); add (POST) or remove (DELETE, `?dir=`) a workspace |
| `GET /api/workspaces/{uuid}/effective-runner-config` | GET | Resolve the effective runner config for a workspace |
| `POST /api/workspaces/{uuid}/restart-acp` | POST | Restart the ACP process for a workspace |
| `GET /api/workspaces/{uuid}/metadata` | GET | Read workspace `.mittorc` metadata (description, URL, group) |
| `PUT /api/workspaces/{uuid}/metadata` | PUT | Save workspace `.mittorc` metadata |
| `GET /api/workspaces/{uuid}/user-data-schema` | GET | Read per-conversation user-data field schema |
| `PUT /api/workspaces/{uuid}/user-data-schema` | PUT | Save per-conversation user-data field schema |
| `GET /api/workspaces/{uuid}/processors` | GET | List message processors for a workspace |
| `PATCH /api/workspaces/{uuid}/processors/{name}` | PATCH | Enable or disable a specific processor (`{"enabled": bool}`) |
| `GET /api/workspaces/{uuid}/mcp-tools` | GET | List MCP servers for a workspace's ACP agent (`?acp_server=` required) |
| `POST /api/workspaces/{uuid}/mcp-tools/install` | POST | Install MCP servers via agent's `mcp-install.sh` |
| `POST /api/workspaces/{uuid}/mcp-tools/remove` | POST | Remove an MCP server via agent's `mcp-remove.sh` |
| `PUT /api/workspaces/{uuid}/folder-group` | PUT | Set the organizational group label for a workspace folder |
| `GET /api/folders/pin` | GET | Read a folder's `pinned` flag (query: `working_dir`) |
| `PUT /api/folders/pin` | PUT | Set a folder's `pinned` flag (query: `working_dir`; body: `{"pinned": bool}`) |
| `/api/workspace-prompts` | GET, POST, DELETE | List (`?working_dir=`), create (POST body `working_dir`), or delete (`?working_dir=&name=`) workspace prompts |
| `PATCH /api/workspace-prompts/{name}` | PATCH | Enable or disable a prompt (`?working_dir=`, body `{"enabled": bool}`) |

---

### Configuration & Flags

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/config` | GET, POST | Get full server configuration (GET); save updated configuration (POST) |
| `/api/agents/types` | GET | List configured ACP agent types |
| `/api/agents/scan` | GET | Scan for installed agent definitions |
| `/api/agents/confirm` | POST | Confirm/register scanned agents |
| `/api/supported-runners` | GET | List supported runner types (public, no auth) |
| `/api/runner-defaults` | GET | Get default runner settings |
| `/api/advanced-flags` | GET, POST | Get or update per-server advanced feature flags |
| `/api/external-status` | GET | Get status of external integrations (GitHub, etc.) |

---

### Issues (Beads)

Issues follow the RESTful `/api/issues` convention. `working_dir` is always a
query parameter (`?working_dir=...`); the issue id is a path segment:

| Path | Method | Description |
| ---- | ------ | ----------- |
| `/api/issues` | GET | List issues |
| `/api/issues/stats` | GET | Issue statistics |
| `/api/issues/{id}` | GET | Show a single issue |
| `/api/issues` | POST | Create an issue |
| `/api/issues/{id}` | PATCH | Update issue fields |
| `/api/issues/{id}` | DELETE | Delete an issue |
| `/api/issues/{id}/status` | POST | Change issue status (close/reopen/defer/undefer) |
| `/api/issues/{id}/comments` | POST | Add a comment |
| `/api/issues/{id}/dependencies` | POST | Manage issue dependencies |
| `/api/issues/cleanup` | POST | Prune closed issues |
| `/api/issues/config` | GET, PUT, DELETE | Get, set, or unset beads configuration |
| `/api/issues/upstream` | GET, PUT | Get or set the upstream task system |
| `/api/issues/sync` | POST | Sync with upstream (pull/push/sync) |

---

### Auxiliary

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/aux/improve-prompt` | POST | Rewrite a user prompt using the active agent |
| `/api/badge-click` | POST | Handle native macOS dock-badge click action (external-stable) |

---

### UI & Files

| Path | Method(s) | Description |
| ---- | --------- | ----------- |
| `/api/ui-preferences` | GET, POST | Read or save UI display preferences |
| `/api/files` | GET | Serve workspace files to the viewer (credentialed; external-stable) |
| `/api/save-file-to-path` | POST | Save content to a local file path (native macOS app — external-stable) |
| `/api/check-file-exists` | GET | Check whether a local file path exists (native macOS app — external-stable) |

---

### Events & WebSocket

| Path | Protocol | Description |
| ---- | -------- | ----------- |
| `/api/events` | WebSocket | Global session-lifecycle event stream (session created/archived/updated) — see [`docs/devel/websockets/`](websockets/) |
| `/api/sessions/{id}/ws` | WebSocket | Per-session streaming channel (agent chunks, tool calls, status) — see [`docs/devel/websockets/`](websockets/) |

### Session Metadata Fields

The `/api/sessions` endpoint returns an array of session objects with the following key fields:

| Field               | Type      | Description                                                          |
| ------------------- | --------- | -------------------------------------------------------------------- |
| `session_id`        | string    | Unique session identifier                                            |
| `name`              | string    | User-friendly session name (auto-generated or user-set)              |
| `acp_server`        | string    | ACP server name used for this session                                |
| `working_dir`       | string    | Workspace directory path                                             |
| `created_at`        | timestamp | Session creation time                                                |
| `updated_at`        | timestamp | Last activity time                                                   |
| `status`            | string    | Session status (active, idle, error)                                 |
| `archived`          | boolean   | Whether session is archived                                          |
| `parent_session_id` | string    | Parent session ID (if created via `mitto_conversation_new` MCP tool) |
| `loop_enabled`  | boolean   | Whether loop execution is configured                             |

#### Parent-Child Relationships

Sessions can have parent-child relationships when created via the `mitto_conversation_new` MCP tool:

- **Parent Session**: A session that spawns child sessions (no `parent_session_id`)
- **Child Session**: A session created by another session (has `parent_session_id` set)

**Use Cases**:

- Delegating subtasks to parallel agents
- Running background analysis while main conversation continues
- Preventing infinite recursion (children cannot spawn more children)

### Session Creation Flow

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant REST as REST API
    participant WS as WebSocket
    participant Backend as Mitto Backend
    participant ACP as ACP Agent

    Note over UI: User clicks "New Conversation"
    UI->>UI: Show WorkspaceDialog

    Note over UI: User selects workspace
    UI->>REST: POST /api/sessions {working_dir, acp_server}
    REST->>Backend: Create session
    Backend->>Backend: Generate session_id
    Backend->>ACP: Start ACP process
    ACP-->>Backend: Process started
    Backend-->>REST: {session_id, working_dir, acp_server}
    REST-->>UI: Session created

    Note over UI: Connect to new session
    UI->>WS: Connect to /api/sessions/{id}/ws
    Backend-->>WS: connected {session_id, client_id, is_running=true}
    WS-->>UI: Ready for conversation
```

### Image Upload Flow

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant REST as REST API
    participant Backend as Mitto Backend
    participant Store as Session Store

    Note over UI: User pastes/drops image
    UI->>UI: Create preview URL (blob)
    UI->>UI: Show image in pending state

    UI->>REST: POST /api/sessions/{id}/images (multipart/form-data)
    REST->>Backend: Process upload
    Backend->>Backend: Validate image (type, size)
    Backend->>Store: Save image to session directory
    Store-->>Backend: Image saved
    Backend-->>REST: {id: "image_abc123.png", url: "/api/sessions/{id}/images/image_abc123.png"}
    REST-->>UI: Upload complete

    UI->>UI: Update pending image with server URL
    UI->>UI: Image ready to send with prompt
```

## WebSocket Communication

> **📖 Complete reference:** See [WebSocket Documentation](websockets/) for the authoritative
> protocol specification, message types, sequence numbers, synchronization, and communication flows.

The web interface uses two WebSocket endpoints:

| Endpoint                | Handler              | Purpose                                               |
| ----------------------- | -------------------- | ----------------------------------------------------- |
| `/api/events`           | `GlobalEventsClient` | Session lifecycle events (created, deleted, renamed)  |
| `/api/sessions/{id}/ws` | `SessionWSClient`    | Per-session communication (prompts, responses, tools) |

This separation allows global events to be broadcast to all connected clients while per-session
events are scoped to interested clients only.

## Streaming Response Handling

The ACP agent sends responses as text chunks via `SessionUpdate` callbacks. The web interface
maintains real-time streaming while converting Markdown to HTML:

```mermaid
flowchart LR
    ACP["ACP Agent"] -->|"text chunks"| WC["WebClient<br/>Assigns seq"]
    WC -->|"seq + chunk"| MD["MarkdownBuffer<br/>Smart buffering"]
    MD -->|"HTML + seq"| BS["BackgroundSession<br/>Persistence + broadcast"]
    BS -->|"events with seq"| WS["WebSocket Clients"]
    BS -->|"immediate persist"| STORE["events.jsonl"]
```

1. **Chunk Reception**: `WebClient.SessionUpdate()` receives `AgentMessageChunk` events
2. **Sequence Assignment**: `WebClient` obtains `seq` from `SeqProvider` immediately at receive time
3. **Smart Buffering**: `MarkdownBuffer` accumulates chunks with their `seq` until semantic boundaries
4. **HTML Conversion**: Goldmark converts buffered Markdown to HTML
5. **WebSocket Delivery**: HTML chunks sent with preserved `seq` to browser
6. **Frontend Rendering**: Preact renders HTML via `dangerouslySetInnerHTML`, sorted by `seq`

For details on sequence number assignment and ordering guarantees, see
[Sequence Numbers](websockets/sequence-numbers.md).

### Markdown Buffer Strategy

The `MarkdownBuffer` balances real-time streaming with correct Markdown rendering:

| Flush Trigger   | Condition       | Rationale                       |
| --------------- | --------------- | ------------------------------- |
| Line complete   | `\n` received   | Most content is line-based      |
| Code block end  | Closing ```     | Don't break syntax highlighting |
| Paragraph break | `\n\n`          | Natural semantic boundary       |
| Timeout         | 200ms idle      | Ensure eventual delivery        |
| Buffer limit    | 4KB accumulated | Prevent memory issues           |

The buffer also tracks `pendingSeq` — the sequence number from the first chunk of buffered
content. When the buffer flushes, this seq is passed to the callback, ensuring correct ordering
even after buffering delays.

## Frontend Technology

The frontend uses a CDN-first approach for zero build complexity:

| Library           | Purpose                       | Size    |
| ----------------- | ----------------------------- | ------- |
| Preact            | UI framework                  | ~3KB    |
| HTM               | JSX-like syntax without build | ~1KB    |
| Tailwind Play CDN | Styling                       | Runtime |

All assets are embedded in the Go binary via `go:embed`, enabling single-binary distribution.

## Component Structure

```
App
├── SessionList (sidebar — unified daisyUI `menu` tree)
│   ├── Folder groups (by working_dir; nested children; per-folder Archived subgroup + Tasks node)
│   ├── Static nodes (Dashboard, per-folder Tasks)
│   └── SessionItem (per-row three-dot menu → ContextMenu)
├── Header (connection status, streaming indicator)
├── MessageList
│   ├── Message (user - plain text, blue bubble)
│   ├── Message (agent - HTML/Markdown, gray bubble)
│   ├── Message (thought - italic, purple accent)
│   ├── Message (tool - centered status badge)
│   ├── Message (error - red accent)
│   └── Message (system - centered, subtle)
├── ChatInput (textarea + send/cancel button)
├── WorkspaceDialog (workspace selection for new sessions)
├── WorkspaceConfigDialog (view/add/remove workspaces)
└── SessionPropertiesDialog (rename session, view workspace info)
```

## Sidebar: Unified Conversation Tree

The sidebar renders all conversations as a single hierarchical daisyUI `menu`
tree (`SessionList` → `SessionItem`), replacing the former three tabs
(Conversations / Loop / Archived) and the group-by toggle.

- **Folders** group conversations by working directory (resolved to the root
  parent for nested children). Child conversations nest under their parent.
- Each folder has an **Archived** subgroup (collapsed by default) and a static
  **Tasks** node (opens the beads view for that folder).
- A static **Dashboard** node clears the active conversation.
- **Category filter** (sidebar header dropdown): show/hide Regular, Loop,
  Archived, and Tasks. Persisted per-device in `sessionStorage`.
- Each row exposes an always-visible **three-dot (ellipsis) menu** that opens the
  shared `ContextMenu` (rename, pin, archive, delete, prompt groups…).
- **Expansion state** is persisted in `localStorage`
  (`mitto_conversation_expanded_groups`) and synced to the server via UI
  preferences. Keys are unscoped: a folder's `working_dir`,
  `archived:<folderKey>`, and `parent:<id>`.
- **Keyboard (⌘-[ / ⌘-]) and swipe** navigation traverse the flattened tree in
  visual order, skipping static nodes and respecting the category filter; the
  target's folder/archived/parent auto-expands and scrolls into view.
- **Pinned folders** stay in the sidebar even when they contain no active or
  stored conversations. The sidebar tree is seeded from both conversations and
  workspaces whose `folders.json` entry has `pinned: true`. An empty pinned
  folder shows a subtle "No conversations yet. Start one" hint when expanded
  and its expand action falls through to the folder's Tasks view. Use the
  sidebar toolbar's `+` (Add folder) button to pin an existing configured
  workspace to the sidebar, or the folder context menu's **Remove from sidebar**
  entry to unpin one. Both actions call `PUT /api/folders/pin` (see the
  endpoints table above).

> Conversations are categorized by `getFilterTabForSession` (regular / loop /
> archived). Loop prompts are configured per-conversation (ChatInput /
> SessionPanel), not via a creation tab. Startup restores the single last-active
> conversation regardless of category (falling back to the most recent overall).

## Responsive Design

- **Desktop (≥768px)**: Sidebar always visible, main chat area
- **Mobile (<768px)**: Sidebar hidden, hamburger menu to open overlay
- **Touch support**: Tap to open/close sidebar on mobile

## Mobile Wake Resync

Mobile browsers (iOS Safari, Android Chrome) suspend WebSocket connections when the device
sleeps. The frontend detects this and automatically recovers.

> **📖 Full details:** See [Synchronization — Mobile Wake Resync](websockets/synchronization.md)
> for the complete sync flow, keepalive mechanism, and zombie connection detection.

### Problem Scenario

1. User opens Mitto on phone, views a conversation
2. Phone goes to sleep (screen off)
3. WebSocket connection is terminated by the browser (or becomes a zombie)
4. Agent continues processing in the background (server-side)
5. User wakes phone — UI shows stale messages

### Solution Summary

```mermaid
flowchart LR
    WAKE["Phone wakes<br/>visibilitychange"] --> REFRESH["Refresh session list<br/>(REST)"]
    REFRESH --> RECONNECT["Force reconnect<br/>Close zombie WS"]
    RECONNECT --> SYNC["Sync via load_events<br/>after_seq from lastKnownSeqRef"]
    SYNC --> MERGE["Merge with dedup<br/>mergeMessagesWithSync()"]
```

- **Sequence tracking via `lastKnownSeqRef`** (not localStorage or React state alone)
- **Three-tier deduplication**: Server-side `lastSentSeq` + client-side seq tracker + content merge
- **Server authority**: When client and server disagree, the server always wins

---

## webview.log Staleness While App Hidden (macOS — Expected)

When auditing logs for the macOS app (`cmd/mitto-app`, a WKWebView host), `webview.log` frequently shows long stretches with no new output while `mitto.log` and `access.log` continue to advance. This is **expected behavior**, not a logging defect.

### Symptom

`webview.log` (WKWebView JS console output bridged to a native file) stops advancing for minutes or hours, creating apparent gaps in frontend observability. Backend logs keep flowing normally during the same window.

### Root Cause (Confirmed)

When the macOS app is hidden or backgrounded, WKWebView throttles and then fully suspends JS execution — including timers and `console.*` emission. The native console→file bridge receives nothing to write, so `webview.log` stops advancing. The suspension follows a two-phase pattern:

- **Throttle phase** (~2–3 min): output trickles after the `"App hidden, tracking time"` log marker
- **Suspend phase**: output stops entirely; console output produced while suspended is **dropped, not buffered**

### Recovery

Resumption is marked by the line:

```
[macOS] App became active, triggering staggered reconnect and sync
```

Logging restarts on activation. Sync recovers via seq-aligned `load_events`, with no data loss and no zombie sessions. Overnight or multi-hour gaps are simply long hidden/sleep periods.

### Guidance for Log Audits

Treat `webview.log` staleness during hidden periods as expected. To distinguish expected gaps from genuine defects:

- **Expected**: staleness is preceded by `"App hidden, tracking time"` and followed by `"App became active, triggering staggered reconnect and sync"`
- **Genuine defect**: staleness occurs **without** a preceding `"App hidden"` marker, or while the app is demonstrably active in the foreground

### Cross-References

- `.augment/rules/09-macos-app.md` — native WKWebView bridge and console capture
- `.augment/rules/23-web-frontend-mobile.md` — visibility change handling, wake resync
- `websockets/synchronization.md` — seq-aligned `load_events` and reconnection flow

---

## Profiling (opt-in, mitto-aek)

Mitto ships with no profiling endpoint by default — the release binary is also
built with `-s -w` (Makefile `LDFLAGS`), which strips Go symbols, so external
sampling tools (`sample`, `atos`) cannot attribute CPU to Go functions either.
To get real attribution, enable the `net/http/pprof` debug endpoints.

### Enabling

Precedence: CLI flag > `MITTO_PPROF` env var > `web.pprof` in `settings.json`.
Off unless one of these is set.

```bash
mitto web --pprof              # CLI
MITTO_PPROF=1 open Mitto.app   # macOS app (env var only, no CLI flag)
```

Or in `settings.json`:

```json
{ "web": { "pprof": true } }
```

### Security

The routes (`/debug/pprof/*`) are **loopback-bound and auth-gated**, matching
the pattern used by other localhost-only endpoints (`internal/web/handlers/save_file.go`):
requests from the external listener or from a non-loopback client IP get a
plain `404` (not `403`), so an external probe cannot even tell profiling is
enabled. They are not added to the auth middleware's public-path allowlist, so
normal session auth still applies on top of the loopback check.

### Capturing a profile

```bash
# 30s CPU profile
go tool pprof http://127.0.0.1:8080/debug/pprof/profile?seconds=30

# Heap snapshot
go tool pprof http://127.0.0.1:8080/debug/pprof/heap

# Full goroutine dump (stacks for every goroutine)
curl -s http://127.0.0.1:8080/debug/pprof/goroutine?debug=2 > goroutines.txt
```

Adjust the port to match the running instance. For symbol resolution in
external tools (`sample`, `atos`) rather than `go tool pprof`, build with
`make build-debug` (unstripped) instead of `make build`.

### Triaging goroutine counts (mitto-am0)

A steady-state count in the hundreds (600–1000 range observed across normal
usage with several active sessions) is **not** by itself evidence of a leak —
Mitto's goroutine population tracks workload rather than climbing
monotonically. Three lightweight instruments answer "is this growing?"
without needing pprof or a restart:

- **Periodic gauge, per-category attribution (mitto-x3x — reach for this
  first).** `internal/coldstart/gauge.go`'s `StartGauge` samples
  `coldstart.Contention()` every 60s (independent of cold-start frequency)
  and keeps the last 64 samples in a ring, each already broken into
  `live_acp_processes`, `connected_ws_clients`, and `open_mcp_sse_streams`
  alongside the raw `num_goroutine` total. Read it via the MCP
  `mitto_goroutine_gauge_recent` tool, or grep `goroutine_gauge_sample` in
  `mitto.log` (DEBUG per tick, promoted to INFO when the total moves by at
  least 10 since the last INFO line).
- **Live count, no restart.** `runtime.NumGoroutine()` is already exposed via
  the MCP `mitto_get_runtime_info` tool and the internal runtime-info API
  (`internal/mcpserver/types.go`), which also now reports the same
  per-category attribution as the periodic gauge for the current instant.
- **Historical series, already recorded.** `internal/coldstart/contention.go`
  logs `num_goroutine=N` (alongside `concurrent_prompting` and
  `live_acp_processes`) on every cold start, in `mitto.log`. Grep
  `num_goroutine=` across log rotations to reconstruct a time series correlated
  with concurrent load — this is normally enough to distinguish "rises and
  falls with load" from "ratchets upward regardless of load".

Only reach for `/debug/pprof/goroutine?debug=2` (stack attribution) once the
series above suggests an actual ratchet. Because enabling pprof requires a
restart, a pprof dump always describes a freshly-resumed instance — treat it
as a **ratio/baseline snapshot**, not a reconciliation of a specific historical
peak.

**Empirical baseline** (measured against an isolated instance with a mock ACP
server, zero workspaces/sessions after cold start): **~18 goroutines** fixed
cost — HTTP server accept/timeout loops, the four middleware `cleanupLoop`s,
`LoopRunner.Start`, `QueueTitleWorker`, `ACPProcessManager.StartGC`,
`BeadsWatcher.Start`, `PromptsWatcher.Start` (fsnotify, 2 goroutines),
`stats.NewAggregator`/`RetentionWorker`/`backfiller`/`BeadsSource`/
`UptimeRecorder`, `ShutdownManager.Start`, and `database/sql.OpenDB`. This is
restart-durable and does not scale with sessions.

**Per-session marginal cost**, measured by creating one session and then
attaching/detaching one WebSocket client:

| Event                                             | Δ goroutines | Source                                                                                                                                                                                                                                            |
| ------------------------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Session created (ACP process spawn, no WS client) | +9           | `acp-go-sdk.NewConnection` (4), `os/exec.(*Cmd).Start`, `acpproc/procstart.StartStderrMonitor`, `BackgroundSession.hsInitACPProcessDone`, `ACPProcessManager.{EnsurePrewarmed,GetOrCreateProcess}` (2, transient — settle once prewarm completes) |
| WebSocket client connects                         | +2           | `web.(*Server).handleSessionWS` — the `readPump`/`writePump` pair (`internal/web/session_ws.go`)                                                                                                                                                  |
| WebSocket client disconnects                      | −2           | same pump pair torn down cleanly                                                                                                                                                                                                                  |

So each **connected browser tab** costs ~2 goroutines (pumps) on top of ~7
durable per-ACP-process goroutines (the prewarm transients retire). A
workspace with several concurrently prompting sessions plus long-lived MCP SSE
keepalive streams (each pins a goroutine while the GET stream is open) easily
accounts for a population in the hundreds without anything being wrong.
**Conclusion: track the ratio (total − ~18 fixed baseline) ÷ live ACP
processes across restarts, not the raw total** — the raw total is a poor leak
signal on its own because it conflates fixed cost, per-session cost, and
transport-detail cost (idle SSE) that all vary independently of any actual
defect.

The `mitto_goroutine_gauge_recent` gauge above computes the per-category
pieces of that ratio directly, on a fixed schedule, instead of requiring a
cold start or a manual log-grep to get a fresh sample.
