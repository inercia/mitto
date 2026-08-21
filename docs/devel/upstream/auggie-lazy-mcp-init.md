# Upstream feature request draft — Auggie: lazy / bounded MCP-server initialization

**Status:** DRAFT — not yet filed with the Auggie team.
**Owner bead:** `mitto-agt` (parent epic: `mitto-ammz`; predecessor epic: `mitto-54k`).
**Purpose:** ready-to-file text that a human can copy-paste into the appropriate
Auggie feedback channel (support portal, GitHub discussion, private issue) once the
channel is confirmed. The "Where to file" placeholder below is the single item that
still needs a human decision before submission; everything else is drafted.

---

## Suggested title

> Lazy / bounded MCP-server initialization to avoid gating the first token on all
> configured servers finishing `initialize`

## Context

Auggie currently gates the first token of the first prompt in each session on
**every** configured MCP server completing its `initialize` handshake. There is no
CLI flag or config knob to change this: `auggie --help` (0.32.0) exposes only
`--mcp`, `--mcp-config`, `--retry-timeout`, and `--wait-for-indexing` — none of
these affect the MCP-init gate.

In practice this hard gate produces multi-minute first-token hangs under two
concrete situations we have observed and traced repeatedly in production
telemetry:

1. **Concurrent cold `session/new` from multiple sessions sharing one working
   directory.** When several workspaces / loops open sessions on the same cold
   shared Auggie process, their `session/new` calls each trigger a full MCP-init
   sweep, and they starve each other's init budget. Observed outcomes on the
   Mitto side (from the phase tracer): `deferred_handshake_failed` at
   `total_ms=240089`, `shared_resume_failed` at `total_ms=285002`, "session/new:
   context cancelled before attempt 2: context deadline exceeded".
2. **Cold-init fork storms for stdio MCP servers.** Even a single server whose
   command has a heavy cold cost (e.g. `uvx mcp-atlassian` ≈ 11.5 s cold on an
   empty `uv` cache) is multiplied across ~6 duplicate spawns of the fork burst
   Auggie issues on cold start. The first token cannot emit until the slowest of
   the resulting duplicate handshakes settles.

Both situations resolve themselves once the gate clears, but the user-visible
symptom is a silent multi-minute "Waiting for MCP servers..." with no incremental
progress and no way to opt out.

## What we would like

Any one of the following would meaningfully reduce the first-token gate; ideally
all three, opt-in:

1. **Lazy per-server init.** Do not `initialize` an MCP server until the first
   tool from that server is actually referenced by the model. Servers that are
   configured but never called in a session pay zero first-token cost.
2. **Per-server init timeout (bounded init).** After a configurable per-server
   deadline, let the first token proceed; late-arriving servers attach and
   become callable once their handshake completes. A late server is strictly
   better than a session that never emits a token.
3. **Non-blocking init flag.** An opt-in, session-scoped
   `--mcp-init=nonblocking` (or equivalent config field) that combines the two
   above: `initialize` fires immediately for every server, but the first token
   does not wait on any of them; tool calls to a still-initializing server
   block only that call until it is ready.

Any of (1)–(3) closes the specific failure mode we are seeing today.

## Impact on Mitto users

Mitto is an ACP host that runs Auggie for a large fraction of its users. The
cold-start MCP wedge has been the single most visible reliability issue for
Mitto workspaces that run multiple concurrent sessions or that configure
`stdio` MCP servers with heavy cold-init costs. All Mitto-side mitigations that
are safely available to us have already been landed (see the "Mitto-side
mitigations already in place" section below) and they close most, but not all,
of the wedge. The residual failures are all downstream of the first-token gate.

## Reproduction (Mitto side)

- Auggie **0.32.0**.
- Any workspace with two or more concurrent Mitto sessions sharing one
  `working_dir` and at least one `stdio` MCP server with a cold cost > 5 s (a
  fresh `uvx mcp-atlassian` invocation is the canonical example — `uv` cache
  cold, network install ~11.5 s).
- Trigger a cold Auggie process (kill any existing Auggie process for the
  workspace, or restart Mitto).
- Send one prompt in each of two sessions within a few seconds of each other.

Expected (with the requested feature): both sessions produce a first token
within a few seconds; MCP tool calls block only if they actually reference an
uninitialised server.

Observed today: neither session produces a first token until the slowest
handshake of the fork burst settles, typically 30 s–4 min depending on how
loaded the host is.

## Evidence and traces

Mitto has an in-house cold-start phase tracer (`mitto.log`, `cold_start_id=…`,
`phase=session_load_failed|session_new_failed|deferred_handshake_failed|ready`,
`elapsed_ms`, `rpc_ms`). We can share trace excerpts on request; the exact
figures used above are drawn from those traces on the affected workspaces.

Root-cause records (internal, condensed):

- Concurrent `session/new` starving MCP init budgets (multi-workspace, one
  working dir): cold-start "stacked LoadSession + NewSession" family.
- Cold-init fork burst of stdio servers, even for a single-server config: cold
  auggie initialize replicas at t=0, one replica does not cleanly settle, the
  client's own exponential backoff manifests as multi-minute silence.
- Auggie's client-side backoff between `initialize` retries against a healthy
  MCP endpoint has been directly observed (inter-attempt gaps
  0,0,4.4,3.2,0.1,33.3,166.4,0.2,0.4,2.8 s → the 166 s gap is the "hang" from
  the user's perspective).

## Mitto-side mitigations already in place

For context, so it is clear we are not asking Auggie to fix Mitto's problems:

- `mitto-6hr` — Streamable-HTTP handler configured with
  `JSONResponse: true` so POST responses resolve inline and do not ride the SSE
  GET stream (fixed an unrelated Mitto-side SSE stall that was previously
  conflated with this).
- `mitto-clc` — proactive keep-warm pin disabled (removed a Mitto-side
  concurrency amplifier).
- `mitto-cgc` — auxiliary-session creation staggered so cold `session/new`
  calls are serialised where possible.
- `mitto-z70` — auxiliary `session/new` bails immediately when the shared
  process is saturated, instead of piling more retries on a wedged agent.
- `mitto-y1g` — stale persisted `acp_session_id` is auto-cleared on load
  failure so we do not repeatedly re-try a doomed `session/load`.
- `mitto-1ut` — stale-session `session/load` probe capped at 45 s (down from
  240 s) so it fails fast into `session/new` fallback.

These land the reliability floor at ~98–100 % cold-start success on our
current sweeps, but they do not remove the first-token MCP-init gate itself —
only the requested Auggie change does.

## Where to file (needs human confirmation before submission)

The Auggie team's preferred channel for a feature request like this is not
currently documented in-repo. Candidates to pick from:

- **augmentcode.com** support portal (docs.augmentcode.com or equivalent).
- **Public GitHub discussion** on the Auggie / Augment Code repository, if a
  public repo with issues/discussions enabled exists.
- **Private issue** through an existing Augment Code support contact.

Once the channel is picked and the request is filed, record the resulting URL
as a comment on `mitto-agt` so `AC1` ("An upstream feature request is filed,
with a link recorded here") can be closed out. Until then this file is the
authoritative draft.
