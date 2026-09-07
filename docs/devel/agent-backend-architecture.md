# Agent/Backend Identities and Ownership Boundaries — Design Decision Record

Status: proposed (mitto-lrt.1). Scope: **naming and boundaries only** — this
record does not mandate a giant `Agent` interface, a global ACP→Agent rename,
or any code change. It exists to validate identities and ownership rules
_before_ Mitto adds a second upstream protocol (a prospective Agent Host
Protocol / AHP **client**, not an AHP server) alongside ACP. Concrete
contracts and the actual adapter implementation are tracked as follow-up
issues under the `mitto-lrt` epic once the open questions below are
resolved.

## Context

Mitto currently talks to exactly one upstream protocol, ACP
(`github.com/coder/acp-go-sdk`), and the domain layer leaks its SDK types
directly:

- `internal/conversation/interfaces.go`'s `SharedProcess` interface takes and
  returns `acp.SessionId`, `acp.ContentBlock`, `acp.McpServer`, and
  `*acp.AgentCapabilities` in its method signatures.
- `internal/conversation/session_handle.go`'s `SessionHandle` embeds
  `acp.AgentCapabilities` and `acp.SessionConfigId` fields directly.
- `internal/conversation/session_callbacks.go`'s `SessionCallbacks` uses
  `acp.*` request/response types for all nine callback signatures.

At the same time, identity is **already partly separated** in adjacent
packages, and this record must preserve that work rather than replace it:

- `session.Metadata` already distinguishes `SessionID` (Mitto-assigned,
  stable) from `ACPSessionID` (upstream-assigned, used for resume) and
  `ACPServer` (the configured server name).
- `workspaces.WorkspaceSettings.UUID` is a `uuid.New()` value independent of
  `ACPServer` — the workspace identity survives renaming or swapping the
  configured server.
- `internal/agents.AgentDefinition` is already identity/metadata/scripts
  only (`Metadata`, `DirName`, `Source`, `Path`, `AvailableCommands`) with no
  protocol-specific fields.
- Mitto's own per-session sequence numbers (`session.Event.Seq`) and the
  `events.jsonl` / `SessionObserver` / REST-WS seq contract are native to
  Mitto and are **not** derived from any upstream cursor.

The problem this record addresses: a second protocol adapter must not
require the domain layer (`internal/conversation`) to import that protocol's
SDK, must not conflate "which agent" with "which protocol transports it" or
"which OS process/session owns it," and must not let externally-initiated
actions silently double-fire Mitto's own automation (loops/processors).

## 1. Identities (proposed)

Four distinct identities, none of which embeds a protocol SDK type:

- **`AgentDefinition`** (retain `internal/agents` shape) — identity and
  metadata only: display name, install/status/MCP scripts, defaults. Says
  nothing about how the agent is _reached_ at runtime.
- **`BackendConnection`** (new, proposed) — protocol + connection
  configuration for reaching an agent: which protocol (ACP today, AHP
  later), command/URL, env, working directory. One `AgentDefinition` may be
  reachable via more than one `BackendConnection` (e.g. the same agent over
  ACP locally and AHP remotely).
- **`AgentRef`** (new, proposed) — the resolved pairing of a backend +
  selected provider/model actually in effect for a conversation, distinct
  from the static `BackendConnection` config it was resolved from.
- **Per-conversation backend-session reference** — the Mitto conversation ID
  and the upstream session identifier/cursor are kept as two separate
  fields (mirroring `session.Metadata.SessionID` vs `.ACPSessionID` today),
  never collapsed into one value.

A single host may advertise multiple agents; the same agent may be reachable
over more than one protocol; protocol choice is independent of which OS
process or session owns the connection.

## 2. Ownership & capability matrix (proposed)

Rows are per-concern, columns are "local ACP subprocess" (today's model,
`SharedProcess.Restart`/`.Generation`/`.RecommendedLoadTimeout`) vs. "remote
attach" (connect/reconnect/detach without process lifecycle control):

| Concern                                     | Local (ACP subprocess)                | Remote (attach)                                                |
| ------------------------------------------- | ------------------------------------- | -------------------------------------------------------------- |
| Launch / restart / kill                     | Mitto-owned                           | not available — host-owned                                     |
| History replay                              | `LoadSession`/`ResumeSession`         | protocol-defined resume/attach                                 |
| Model / mode / title changes                | Mitto-initiated, mirrored to upstream | may originate on either side (§3)                              |
| Permissions, terminals, files               | ACP request/response today            | protocol-defined equivalent, if any                            |
| Auxiliary work (title, MCP checks)          | scheduled on the same local process   | needs its own remote session, not "free"                       |
| Mitto loops (schedule/onCompletion/onTasks) | always Mitto-local                    | unaffected — loops are a Mitto concept, not projected upstream |

Destructive host operations (killing/restarting a remote agent process) are
explicitly **out of scope** for a remote `BackendConnection` — only locally
launched processes are Mitto's to manage.

## 3. External actions without echo or double-automation (proposed)

An upstream-initiated change (e.g. a remote host switches its own model or
appends history) must be **projected** into Mitto's session state
(`session.Metadata`, `SessionChangeData` events) exactly once, and must
**not** re-trigger Mitto-side automation that reacts to the same class of
event when Mitto itself made the change — e.g. a loop's `onTasks` trigger or
a processor gated on `session_change` must not double-fire because the
projection path and the Mitto-initiated path both emit the same event type.
The adapter boundary is responsible for tagging projected events distinctly
enough that this de-duplication is possible (exact mechanism deferred to
implementation).

## 4. IDs and cursors (proposed)

Mitto conversation IDs and Mitto's own per-conversation `Event.Seq` remain
authoritative and are never replaced by an upstream ID or cursor. Upstream
session identifiers/cursors (potentially non-monotonic, non-integer, or
host-defined) are stored alongside (as `ACPSessionID` is today) and
reconciled through a separate replay/projection step — never substituted
into `Event.Seq` or the WS/REST seq contract, which must keep working
unchanged for existing ACP-only clients.

## 5. Connection/session state and capability discovery (proposed)

A connection/session state model and error taxonomy are needed that
distinguish, per capability, three states rather than two:
**supported**, **explicitly unsupported** (the backend declared it does not
implement this), and **unknown** (the backend hasn't been asked / doesn't
declare capabilities at all). Collapsing "unknown" into "unsupported" would
incorrectly hide capabilities from backends that simply don't advertise
them; collapsing it into "supported" would incorrectly attempt calls the
backend will reject.

## 6. Incremental package-dependency diagram (partially realized by mitto-lrt.4)

```
internal/conversation  (domain: SharedProcess, SessionHandle, SessionCallbacks)
        |  depends on neutral contracts only — no acp-go-sdk, no protocol SDK
        v
   [adapter boundary]  <-- SharedProcess/SessionHandle/SessionCallbacks ARE this seam today
        |
        +--> internal/acp + internal/acpproc  (ACP protocol + subprocess tuning, unchanged)
        +--> (future) internal/ahp-ish adapter  (second protocol, same neutral contracts)
```

The existing `SharedProcess`/`SessionHandle`/`SessionCallbacks` interfaces
already occupy the adapter-boundary position architecturally — the proposed
work is to stop leaking `acp.*` types through that boundary, not to
introduce a new boundary.

**Package name (decided, mitto-lrt.4):** the neutral-contract package realizing
the seam above is `internal/agentbackend`. It defines the typed identifiers,
neutral prompt content/outcome/capability/state/event contracts, and small
separated interfaces (`Connection`, `ProviderDiscovery`, `SessionOps`,
`EventDelivery`, optional `ClientServices`) described in §1–§5, plus a
non-process in-memory fake proving the contracts don't collapse into an ACP
alias layer. It is purely additive: `internal/conversation` and
`internal/acpproc` are untouched. `internal/agentbackend` must never import
`acp-go-sdk`, an AHP client, `internal/acp`, `internal/acpproc`,
`internal/web`, `internal/conversation`, or `os/exec` — enforced by an
import-guard test.

**ACP adapter realized (mitto-lrt.6):** the ACP-to-neutral bridge now lives in
`internal/acpbackend`, an additive, standalone package that implements all five
neutral contracts (`Connection`, `ProviderDiscovery`, `SessionOps`,
`EventDelivery`, optional `ClientServices`) by wrapping the existing
`conversation.SharedProcess`. Dependency direction: `internal/acpbackend`
imports `internal/agentbackend` + `acp-go-sdk` + `internal/conversation` (the
last for the `SharedProcess` handle it wraps) — it is the protocol-specific home
for the SDK, so the `internal/agentbackend` import guard above stays intact; it
is **not** imported *by* `internal/conversation` in this increment.
Translators are pure functions with unit coverage: content blocks,
stop-reason/outcome, three-state capabilities, model/mode/config state, and
error mapping to the neutral sentinels. Inbound ACP notifications translate to
neutral `Event`s tagged `Origin=OriginLocal`, now carrying full tool-call/plan
payloads (`agentbackend.ToolCallPayload`/`PlanPayload`, realized in mitto-lrt.8
below — the bare-marker shim is gone). **Still deferred:** wiring the adapter
into `BackgroundSession` (lifecycle track, mitto-lrt.7 onward — the .7
increment introduced the ownership seam below but did not route the acpbackend
adapter through it yet) and live-wiring the event-projection engine
(mitto-lrt.8) into that same production data path (mitto-lrt.12+).
Documented shims/gaps to remove alongside that later work: the synthesized
`ConversationID` in `NewSession` (real Mitto IDs arrive when the lifecycle seam
is wired into production call sites); dropped Audio/embedded-Resource content
blocks and deferred non-message `SessionUpdate` kinds (still deferred — no
`EventKind` slot yet; unaffected by .8's tool-call/plan work).

**Ownership seam realized (mitto-lrt.7):** conversation lifecycle now has a
protocol-neutral acquisition + ownership seam, `BackendProvider` /
`BackendLease` (`internal/conversation/backend_provider.go`), mirroring the
existing `ProcessManager` dependency-inversion pattern (`SetACPProcessManager`).
`BackendProvider.AcquireSession` acquires a lease for a `New`/`Load`/`Resume`
intent; `BackendLease` models *ownership* of one acquired session —
`Ref`/`State`/`Capabilities`/`Detach`/`Reconnect`/`Terminate` — with ACP-only
`LocalProcess()`/`SessionHandle()` escape hatches so the existing
prompt/streaming data path keeps flowing through `SharedProcess`/`SessionHandle`
unchanged. Ownership rules from the ADR are encoded here: `Detach` releases a
share without killing a shared process or a host-owned session ("detach, not
kill"); `Reconnect` is single-flight (concurrent callers coalesce onto one
upstream resume, never replaying a possibly-accepted prompt); `Terminate` is
capability-gated and returns a typed `*agentbackend.UnsupportedError` for
backends that cannot honor it. `ClassifyAcquireError` (`backend_state.go`) maps
known acquisition/reconnect errors to neutral `agentbackend.LifecycleState`
(connection-unavailable/session-missing → `Disconnected`, busy/saturated and
concurrent GC-recycle → `Reconnecting`, permanent ACP classification →
`Stopped`) while returning the original error unchanged for `errors.Is`/`As`.
The ACP implementation delegates byte-identically to `ProcessManager`; a
non-process fake (`backend_provider_fake_test.go`, built on
`agentbackend.FakeHost`) proves create/attach/detach/resume, sibling isolation
under concurrent detach, and single-flight reconnect with **no**
process/PID/runner/restart.

**Deliberately deferred (mitto-lrt.7):** to keep the hardened ACP lifecycle
zero-regression, the seam ships tested but **not yet wired into the production
`SessionManager`/`BackgroundSession` acquisition call sites** — those still
acquire via `ProcessManager.GetOrCreateProcess` directly, so a `nil`
`BackendProvider` is a valid, common state and callers fall back to the
pre-existing path. Routing the per-prompt data path through `agentbackend`'s
neutral `Event`s now has a projection engine to route through
(mitto-lrt.8, below), but live-wiring either seam into the production data
path remains deferred to mitto-lrt.12+.

**Event projection & durable replay realized (mitto-lrt.8):** a new,
additive, protocol-neutral leaf package `internal/eventprojection` consumes
`agentbackend.Event` and emits sequence-numbered `ProjectedEvent`s to a
caller-supplied `ProjectionSink`. Design mirrors the .4/.6/.7/.9 formula
(pure package + fake + import guard; **not** wired into
`BackgroundSession`/`SessionManager` this increment). Key pieces:
- **`Projector`** (`projection.go`): coalesces consecutive same-kind/
  same-origin `EventAgentMessage`/`EventAgentThought` chunks to a logical
  boundary (content-agnostic — no markdown/HTML awareness, unlike
  `MarkdownBuffer`) before allocating a Mitto seq via the inverted
  `SeqAllocator` seam (mirrors `conversation.SeqProvider`). Any other event
  kind is itself a boundary.
- **Replay/dedup** (`checkpoint.go`): a durable `Checkpoint` per `SourceID`
  (`{Backend, Provider, ProviderSession}`, since `SessionRef` alone doesn't
  carry backend identity) tracks `LastCursor` plus a bounded dedup ring +
  identity→seq map. Only `agentbackend.Event.UpstreamCursor`-bearing events
  participate in dedup/replay — a cursor-less event (the common case for
  today's ACP adapter, which never sets it) is always treated as new/live, a
  safe default that never silently drops or falsely dedups. A replayed
  identity is **re-emitted** with its **original** Mitto seq (`PhaseReplay`),
  not a fresh one, so a `ProjectionSink` writer can idempotently no-op;
  crash-consistency ordering is `sink.Emit()` (the durable event write)
  **then** `CheckpointStore.Save()` — proven against `agentbackend.FakeHost`'s
  existing `ResumeSession` sequence-gap signal.
  **Explicit non-guarantee:** this gives at-most-once *projection* into a
  sink, never exactly-once *side-effect execution* by whatever consumes the
  sink's output (see package doc).
- **Prompt correlation** (`correlation.go`): `PromptCorrelation` links an
  optimistic local prompt ID to the upstream identity that later echoes it;
  a confirmed echo (`Origin=OriginRemote` + resolved link) is **not**
  re-projected as a new external action, while a genuinely external,
  unresolvable `OriginRemote` update is projected exactly once with
  `SuppressLocalAutomation=true` so processors/loops don't double-fire.
- **Seams stay inverted**: `SeqAllocator`, `CheckpointStore`, and
  `ProjectionSink` are interfaces defined in this leaf package; the durable,
  session-sidecar-backed `CheckpointStore` implementation
  (`session.Store.Read/WriteSessionSidecarJSON`, the same pattern as
  `mcpserver/child_report_store.go`) lives in the **sibling** package
  `internal/eventprojection/eventprojectionsession` — kept outside the core
  so `internal/eventprojection`'s own `imports_test.go` guard (mirroring
  `internal/agentbackend`'s) can forbid `internal/session` (plus
  `acp-go-sdk`, `internal/acp`, `internal/acpproc`, `internal/web`,
  `internal/conversation`, `os/exec`) transitively, exactly like .6 put its
  ACP-backed seam implementation in a higher package than the neutral
  contracts it implements.
- **Contract extension**: `agentbackend.Event` gained additive
  `ToolCall *ToolCallPayload` / `Plan *PlanPayload` fields (id/title/status/
  kind; plan entries) — the "bare marker, deferred to mitto-lrt.8" shim
  `acpbackend.translateSessionUpdate` carried since .6 is now gone; ACP tool
  call/plan `SessionUpdate`s translate to fully-populated neutral payloads.

**Agent identity/availability realized (mitto-lrt.9):** `internal/agents`
gains a display-name-independent `AgentDefinition.StableID()` (precedence:
explicit `Metadata.AgentID` override > `ACPId` > `Name` > `DirName`), and a
new additive `internal/agents/availability.go` models runtime reachability
without conflating it with static definitions: `ProviderReach` (`Local` —
backed by an on-disk `AgentDefinition` with scripts — vs. `Remote` — only
host-advertised, no scripts) and a four-state `AvailabilityState`
(`Installed`/`Configured`/`Connected`/`Available`) replacing the previous
single collapsed boolean. `ComposeAvailability` is a **pure** function over
already-gathered inputs (`installed`, `configured []ConfiguredProvider`,
`conns map[string]ConnectionState`) — it never runs a script or opens a
connection itself, and `Available` is fail-closed: `!Disabled &&
ProtocolSupported && (Connected || (Local && Installed))`, so a
disabled/unsupported backend descriptor (e.g. a future gated AHP adapter)
never surfaces as a usable runtime choice merely by existing (§5's
three-state capability principle applied at the availability layer). A
`CatalogCache` keyed by `(Backend, Provider, Version)` scopes
model/mode-catalog caching and supports `InvalidateProvider` on
reconnect/capability refresh without touching `StableID`/`AgentRef` — the
stable selection identity is never itself cached, so invalidation cannot
lose it. Layering: `internal/agents` still does **not** import
`internal/agentbackend` (`ConnectionState`/`ConfiguredProvider` are plain
structs, not `agentbackend.LifecycleState`/`AgentRef`); the identity bridge
is one-directional, added to `internal/backendcompat` instead
(`AgentRefFromStableID`), mirroring the existing
`AgentRefFromACPServerName`. `internal/web/handlers/agent_discovery.go`'s
`AgentScanResult` additively exposes `stable_id`; wiring the full
`AvailabilityState` into that endpoint (which needs live connection-state
plumbing) and UI presentation are explicitly deferred to a follow-up —
out of scope here per the bead's own scope note.

## 7. Migration matrix (proposed)

| Surface                                                         | Today                              | Migration rule                                                                                             |
| --------------------------------------------------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `WorkspaceSettings.ACPServer` / `.UUID`                         | server name; UUID independent      | UUID stability preserved; server-name aliasing must reject ambiguous matches rather than silently pick one |
| `session.Metadata.ACPSessionID`                                 | resume-only cursor                 | new protocol adds its own cursor field; existing field untouched                                           |
| Prompt/CEL selectors, CLI `--acp` flag, MCP `acp_server` params | select by server name              | unchanged for ACP; new protocol adds parallel selection, no renaming of existing flags                     |
| REST/WS (`acp_started`/`acp_stopped`/`acp_start_failed`)        | ACP-specific event names           | preserved as-is; a second protocol gets its own event names, not overloaded onto these                     |
| Go/JS SDKs, UI (`SettingsDialog`)                               | ACP-only today                     | additive only; no breaking change to existing public shapes                                                |
| Archived conversations                                          | tied to their original ACP session | **never** silently migrated to a different protocol on unarchive — explicit user action required           |
| Unsupported backend                                             | n/a                                | fail closed with a typed error, never silently fall back to a different backend                            |

Rollback is bounded to configuration (workspace/server settings) — there is
no proposed schema migration of existing `events.jsonl` data.

## 8. Current vs. proposed vs. unverified

**Current (verified against code):** §Context above — SDK leakage in the
three anchor files; existing identity separation in `session.Metadata`,
`WorkspaceSettings`, `internal/agents.AgentDefinition`.

**Proposed (this record, not yet implemented):** §1–§7 above.

**Verified against the AHP spec (mitto-lrt.3, 2026-09-07):** AHP v0.9.0 has
its own capability-discovery mechanism (`initialize` handshake,
`auth/required`), its own session/cursor model (URI-addressed channels,
server-assigned `lastSeenServerSeq` plus client-assigned `ClientSeq`,
explicit sequence-gap detection forcing resubscribe), and a published,
spec-lockstep **Go** client (`github.com/microsoft/agent-host-protocol/clients/go`)
alongside its Rust/TypeScript/Kotlin/Swift clients — the "no Go SDK" premise
in the original mitto-3jr research was incorrect and is corrected here. See
[docs/devel/ahp-feasibility.md](ahp-feasibility.md) for the full evidence
matrix, including the one confirmed architectural mismatch (AHP
creates/mutates resources via a generic `Dispatch(channel, action)` write-ahead
call reconciled by client-side reducers, not typed request/response RPCs like
ACP's `session/new`).

**Remaining unverified:** AHP's model-selection surface (not found in the
inspected Go client API; needs a deeper JSON-Schema read) and whether any
Claude-backed AHP host is reachable outside VS Code's in-process reference
host. These are runtime/environmental gaps, not architectural gaps in the Go
client itself.

## 9. Open questions

1. Does a concrete AHP specification exist yet that this record can be
   checked against, or is "AHP" still aspirational? **Resolved (2026-09-07):**
   yes — spec `v0.9.0` (2026-08-28), pre-1.0 and actively churning (breaking
   changes in every 0.6→0.9 release), with five independently-versioned
   language clients including Go. See
   [docs/devel/ahp-feasibility.md](ahp-feasibility.md).
2. Is "remote attach without local process ownership" (§2) an actual
   near-term requirement, or should the first increment assume every
   `BackendConnection` still spawns a local subprocess (i.e. §2's "Remote"
   column is design headroom, not immediate scope)? **Resolved for AHP
   specifically (2026-09-07):** remote attach is not optional headroom for
   an AHP backend — it is the _only_ mode the protocol models. An AHP client
   always subscribes to host-owned channels; it never spawns the agent
   process itself. §2's "Remote" column is therefore mandatory scope for any
   AHP `BackendConnection`, independent of whether AHP is ultimately adopted
   (mitto-lrt.3 records that adoption itself remains blocked on host/auth
   availability, not on this architectural question).

Both questions above are now resolved against the current AHP spec/SDK
revisions; mitto-lrt.3's blocked-on-runtime-validation decision is a separate,
environmental finding (no reachable host, no dependency authorization yet) and
does not reopen either question.
