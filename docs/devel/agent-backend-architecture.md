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

## 6. Incremental package-dependency diagram (proposed)

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

**Unverified (AHP assumptions — not confirmed against any AHP spec):**
whether AHP has its own capability-discovery mechanism, its own
session/cursor model, and whether "remote attach" is even a real AHP
operation as opposed to always spawning a local subprocess. These are
flagged, not assumed, below.

## 9. Open questions

1. Does a concrete AHP specification exist yet that this record can be
   checked against, or is "AHP" still aspirational? _(blocks any contract
   implementation — file `bd create --parent mitto-lrt` if unresolved when
   implementation work starts.)_
2. Is "remote attach without local process ownership" (§2) an actual
   near-term requirement, or should the first increment assume every
   `BackendConnection` still spawns a local subprocess (i.e. §2's "Remote"
   column is design headroom, not immediate scope)?

Each question above either gets resolved inline in a future revision of
this record, or is filed as an explicit blocker bead under `mitto-lrt`
before any contract implementation begins.
