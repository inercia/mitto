# AHP Feasibility — Mitto as a Go Client (Evidence Matrix + Decision)

Status: **blocked on runtime validation** (mitto-lrt.3). Scope: bounded,
spec/SDK-based feasibility check of Mitto as a Go client of an external
**Agent Host Protocol** (AHP, Microsoft) host, Claude as the first candidate
provider. This is not a runtime interoperability test and does not authorize
adding the AHP Go module as a dependency — see [Decision](#decision) and
[Limitations](#limitations).

Evaluated at Mitto commit `883f6b6d` (2026-09-07). Supersedes the informal
findings in the closed research bead mitto-3jr where they conflict (see
[Corrections to mitto-3jr](#corrections-to-mitto-3jr)).

## Versions evaluated

- **AHP spec**: `spec/v0.9.0`, released 2026-08-28 (`microsoft/agent-host-protocol` GitHub Releases). Prior releases: v0.6.0 (Jul 20), v0.7.0 (Jul 31), v0.8.0 (Aug 18) — active churn, pre-1.0; each release has Added/Changed/Removed/Fixed sections indicating breaking changes are routine.
- **AHP Go client**: `github.com/microsoft/agent-host-protocol/clients/go` `v0.9.0` (tag `clients/go/v0.9.0`, released 2026-08-28, same commit as the spec tag) — packages `ahptypes` (wire types), `ahp` (async `Client` + reducers + `ahp/hosts` multi-host runtime), `ahpws` (WebSocket transport over `github.com/coder/websocket`). MIT license, Go 1.22+, 8 transitive imports, zero known importers on pkg.go.dev at evaluation time (i.e. unadopted, not necessarily immature — the module mirrors the same generator as the Rust/TS/Kotlin/Swift clients).
- **Mitto today**: `go.mod` declares `github.com/coder/acp-go-sdk v0.13.5` (replaced with a fork) and `github.com/modelcontextprotocol/go-sdk v1.4.1`. No AHP dependency exists; none was added by this investigation (see [Limitations](#limitations)).

## Corrections to mitto-3jr

mitto-3jr (closed earlier the same day) stated _"no native AHP support or
generic Auggie/ACP provider was found"_ for Go and did not surface a Go AHP
SDK at all — its search was scoped to VS Code's reference host
(`agentHostServerMain.ts`, TypeScript) and Auggie's own docs, not the AHP
project's own multi-language client matrix. Re-verification here found the
AHP project publishes and versions a **Go** client alongside Rust/TypeScript/
Kotlin/Swift, released in lockstep with the spec (`clients/go/vX.Y.Z` tags).
**The "no Go SDK" premise from mitto-3jr and the initial plan comment on this
bead is incorrect and is superseded by this document.** This does not change
the Auggie finding (still no verified AHP path for Auggie — VS Code's
reference host only registers a `ClaudeAgent`).

## Evidence matrix

| Concern (per bead description)              | AHP v0.9.0 support                                                                                           | Go SDK surface                                                                                                                                                  | ADR mapping                   | Status                                      | Notes                                                                                                                                                                                                                                    |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Host launch/auth requirements               | `initialize` handshake, `AuthenticateParams`, `auth/required` w/ `ProtectedResourceMetadata`                 | `Client.Initialize(ctx, clientID, protocolVersions, subs)`, `SubscriptionEventAuthRequired`                                                                     | ADR §5 (capability discovery) | spec+sdk-verified                           | Auth is OAuth-shaped (`McpAuthRequirement.oauthClient`); no bearer-token-only fast path found                                                                                                                                            |
| Provider discovery                          | `root` channel lists sessions                                                                                | `SubscriptionEventSessionAdded/Removed/SummaryChanged`                                                                                                          | ADR §1 (`AgentRef`)           | spec+sdk-verified                           | Discovers _sessions_, not necessarily _providers_/models directly — provider selection is host-config, not protocol-visible from a bare client                                                                                           |
| Create/attach                               | Generic `Dispatch(ctx, channel, action)` with a `CreateSession`-shaped `StateAction`, not a typed RPC        | `Client.Dispatch` + `ApplyActionToRoot`/`ApplyActionToSession` reducers                                                                                         | ADR §6 (adapter boundary)     | spec+sdk-verified — **contract mismatch**   | ACP has typed `session/new`; AHP creation is a write-ahead action dispatched into a subscribed channel and reconciled via reducers, not a request/response call. An adapter must translate typed calls into dispatch+reducer round-trips |
| Streamed updates                            | `subscribe` + push `ActionEnvelope`s per URI                                                                 | `Client.Subscribe`, `Subscription.Events()`, `SubscriptionEventAction`                                                                                          | ADR §4 (IDs/cursors)          | spec+sdk-verified                           | Per-channel event stream, not a single global stream (mitigated by `Client.Events()` top-level fan-in)                                                                                                                                   |
| Cancel                                      | `chat/turnCancelled` (spec)                                                                                  | reachable via `Dispatch` on the chat channel (no bespoke `Cancel` method)                                                                                       | ADR §2                        | spec-verified, sdk-inferred                 | Same generic-dispatch pattern as create                                                                                                                                                                                                  |
| Permission responses                        | `chat/toolCallAuthRequired`/`toolCallAuthResolved`, `ToolCallStatus.AuthRequired`                            | reducer support via `ApplyActionToChat`; no typed permission-request method beyond auth flow                                                                    | ADR §2 (permissions)          | spec-verified                               | Narrower than ACP's general permission-request RPC — AHP's is OAuth/tool-auth specific, not a generic allow/deny prompt                                                                                                                  |
| Model selection                             | not found in the inspected Go `Client` API surface                                                           | none identified                                                                                                                                                 | ADR §1 (`AgentRef`)           | **unverified — spec-inspection incomplete** | Likely lives in session-config fields (`SessionConfigCompletions` exists) rather than a dedicated method; needs a deeper read of the JSON Schema, out of this bounded pass                                                               |
| Snapshot/replay                             | `subscribe` returns an initial snapshot                                                                      | `Client.Subscribe` return value                                                                                                                                 | ADR §4                        | spec+sdk-verified                           |                                                                                                                                                                                                                                          |
| Reconnect                                   | `reconnect` command w/ last-seen sequence                                                                    | `Client.Reconnect(ctx, clientID, lastSeenServerSeq, subscriptions)` → `ReconnectResult`; `ErrSequenceGap` forces resubscribe when reconciliation isn't possible | ADR §4 (cursors)              | spec+sdk-verified                           | Explicit, protocol-level answer to "operation ack & reconciliation after ambiguous disconnect" — more structured than ACP's resume-or-fresh-session fallback                                                                             |
| Session/chat identifiers                    | URI-addressed channels (`ahp-session:/...`)                                                                  | `ahptypes.URI` throughout                                                                                                                                       | ADR §4                        | spec+sdk-verified                           | Distinct from Mitto's own `session.Metadata.SessionID`/`ACPSessionID` split — a third adapter-side mapping would be needed                                                                                                               |
| Host cursor scope/epochs                    | per-channel `lastSeenServerSeq`, global `ClientSeq` on dispatch (`DispatchHandle`)                           | `DispatchHandle{ClientSeq}`                                                                                                                                     | ADR §4                        | spec+sdk-verified                           | Two cursor axes (client-assigned dispatch seq vs. server-assigned channel seq) — must not be conflated with Mitto's own `Event.Seq` (ADR §4 already requires this)                                                                       |
| Remote history authority                    | host owns channel state; client mirrors via reducers                                                         | `MultiHostStateMirror`, `ApplyActionTo*`                                                                                                                        | ADR §3 (external actions)     | spec+sdk-verified                           | Reducer-mirrored state is a clean seam for ADR §3's "project once, never double-fire" rule                                                                                                                                               |
| External writers / dedup                    | write-ahead `ActionEnvelope`s carry origin                                                                   | `SubscriptionEventAction.Envelope`                                                                                                                              | ADR §3                        | spec-verified                               | v0.9.0 added an explicit `origin` field on annotations; general action provenance still needs a closer read to confirm it is universal, not annotation-only                                                                              |
| Per-conversation Mitto MCP identity binding | N/A — AHP concern boundary ends at the host; MCP identity binding is Mitto-side                              | —                                                                                                                                                               | ADR §2 (ownership)            | out of AHP's scope                          | Not a protocol gap; flagged so it isn't mistaken for one                                                                                                                                                                                 |
| Remote host reachability                    | transport-agnostic (`Transport` interface); `ahpws` ships WebSocket                                          | `ahpws.Connect(ctx, url)`                                                                                                                                       | ADR §2                        | sdk-verified                                | No reachable Claude-backed AHP host was available to this investigation (see Limitations)                                                                                                                                                |
| Working-directory ownership                 | `multipleWorkingDirectories` capability, `session/workingDirectorySet`/`Removed`, `workingDirectoryReplaced` | modeled as `StateAction`s via `Dispatch`                                                                                                                        | ADR §2                        | spec-verified                               |                                                                                                                                                                                                                                          |
| File ownership                              | symmetric resource RPCs client can also serve                                                                | `Client.Resource{Read,Write,List,Copy,Delete,Move,Mkdir,Resolve,Request}`, `ResourceRequestHandlers` for the reverse direction                                  | ADR §2                        | spec+sdk-verified                           | The Go client can act as the file-owning side (`ResourceRequestHandlers`), matching Mitto's current local-file-ownership model                                                                                                           |
| Terminal ownership                          | `TerminalSessionClaim` requires the owning chat URI; explicit running/exited lifecycle                       | `ApplyActionToTerminal`                                                                                                                                         | ADR §2                        | spec-verified                               |                                                                                                                                                                                                                                          |

## Decision

**Blocked**, not "no-go": the earlier premise that no Go AHP SDK exists is
**wrong** and is corrected above — a versioned, spec-lockstep Go client
exists and its API surface plausibly covers the ADR's ownership/capability
concerns (with the create/attach dispatch-vs-RPC mismatch as the main
adapter-design wrinkle). The remaining blockers are **environmental, not
architectural**:

1. No reachable AHP host was available to this investigation — Claude is only
   known to be exposed through AHP inside VS Code's in-process reference host
   (`agentHostServerMain.ts` / `claudeAgentSdkService.ts`), not as a
   standalone endpoint a Go client could dial.
2. Adding `github.com/microsoft/agent-host-protocol/clients/go` as a
   dependency was **not done** — per repository policy and this bead's own
   guardrails, a new dependency requires explicit user authorization before
   any code (even a disposable harness) can import it.

**Recommendation, gated on authorization**: a bounded, Claude-only spike is
plausible once (a) a dependency addition is approved and (b) an isolated AHP
host + Claude auth is provisioned — consistent with mitto-3jr's original
recommendation, now with a confirmed Go SDK path instead of an assumed gap.
Do not claim production AHP readiness or any Auggie/AHP path from this
document; Auggie remains ACP-only.

## Reproducible instructions

1. Re-check the current spec/Go-client versions: `https://github.com/microsoft/agent-host-protocol/releases` (tag patterns `spec/vX.Y.Z`, `clients/go/vX.Y.Z`) and `https://pkg.go.dev/github.com/microsoft/agent-host-protocol/clients/go/ahp`.
2. Confirm `go.mod`/`go.sum` are unchanged by this investigation: `git diff go.mod go.sum` (must be empty).
3. To runtime-validate (only after authorization + a provisioned isolated host): `go get github.com/microsoft/agent-host-protocol/clients/go@v0.9.0` in a disposable module/branch, follow the `ahp` package Quickstart (`ahpws.Connect` → `ahp.Connect` → `Client.Initialize` → `Client.Subscribe`), and exercise the destructive-fault scenarios (agent exit, host restart, network disconnect mid-turn) only against that isolated host, never a user's live host.

## Limitations

- No dependency was added; the evidence matrix's "sdk-verified" rows are
  based on published API documentation (pkg.go.dev godoc), not a compiled or
  executed program against this repository.
- "Model selection" is unverified — the inspected `Client` method set did not
  surface it; a full JSON-Schema read was out of scope for this bounded pass.
- No live AHP host was reachable, so every row is spec/SDK-verified, not
  runtime-verified; no lost/duplicated-output or reconnect-fidelity data was
  collected (the bead's fault-injection requirements are entirely deferred to
  the gated spike above).
