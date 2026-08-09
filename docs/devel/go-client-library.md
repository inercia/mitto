# Go Client Library — Design Decision Record

Status: decided (mitto-rwxq.1). Scope: design only — no code ships in this
record; implementation is tracked across the sibling issues of the
`mitto-rwxq` epic (`mitto-rwxq.2`–`.9`), referenced by ID below. This is a
**sibling** record to [JS Client Library](js-client-library.md), not an
extension of it: the two SDKs diverge deliberately (see Context) and the
concerns they do share — the error envelope, stability tiers, and the
deprecation register — are referenced rather than duplicated here.

## Context

`internal/client` already implements roughly 80% of a conversation-centric
Go client across 1476 LOC (`client.go`, `session.go`, `helpers.go`,
`doc.go`; 1644 with `client_test.go`), proven by 50 in-repo importers under
`tests/integration/`. It cannot be used outside this repository (lives
under `internal/`), has no authentication,
does not parse the canonical error envelope, and its realtime layer is a
bare `SessionCallbacks` struct with no reconnect, resync, or keepalive. This
record decides the target shape before `mitto-rwxq.2` relocates it to
`pkg/api` and the rest of the epic fills the gaps.

Unlike the JavaScript SDK — whose primary consumer is the Mitto UI and
therefore needs the full REST surface — the Go SDK's scope is deliberately
**conversation-centric only**: sessions, prompts and streaming, queue, loop,
images. Workspaces, prompts-config, issues, settings, agents, files and
dashboard are added on demand, not up front.

## 1. Package layout

Single flat package, no sub-packages:

```
pkg/api/
  client.go     # Client, Option, New(), REST: sessions, images
  session.go    # Session, SessionCallbacks, WebSocket lifecycle
  queue.go      # queue REST methods
  loop.go       # loop REST methods
  errors.go     # APIError, NetworkError, sentinels — .3
  auth.go       # WithAuth, token/cookie adapters — .4
  stream.go     # iter.Seq2 / channel adapter over SessionCallbacks — .6
  helpers.go    # PromptAndWait and friends
  doc.go        # package documentation — .8
```

No `resources/`/`realtime/` sub-packages, unlike the JS SDK: today's code is
small, `Session` needs unexported access to `Client`'s internals, and
sub-packages would force an exported seam or an import cycle. This also
keeps `.2`'s move a pure `git mv` + import-path rewrite across the 50
importers.
File-per-resource is the organising axis instead of package-per-resource;
unexported helpers may move to `pkg/api/internal/` later if the file count
grows.

## 2. Object model / naming

`Client` (REST) + `Session` (live WebSocket) — both already proven by the
integration suite — are kept. Terminology stays `Session`/`SessionInfo`
(matching REST paths `/api/sessions/...`) even though the product calls
these "conversations"; renaming is out of scope and would break the
mechanical-move property of `.2`.

## 3. Construction — the options pattern

`New(baseURL string, opts ...Option) *Client` with `Option func(*Client)` is
kept as-is. `.4` and later beads add `WithHTTPClient`, `WithAPIPrefix`
(today `/mitto` is hardcoded), `WithAuth`, `WithLogger`, `WithUserAgent`.
Zero-config `New(baseURL)` MUST remain unauthenticated and
behaviour-identical, since that is what all in-repo consumers use.

## 4. Error model (`.3`)

`*APIError{Op string; Status int; Code, Message string; Details map[string]any; Body []byte}`
implements `error`, parsing both the canonical envelope
([REST API Conventions §4](rest-api-conventions.md#4-error-envelope)) and
the legacy flat shape used by `external-stable` endpoints, falling back to
status-derived codes when neither parses. The raw body is always preserved
in `Body` for callers needing custom parsing, and `Op` (a short operation
label such as `"get session"`) prefixes the rendered message.

One sentinel exists per canonical code — `ErrBadRequest`,
`ErrUnauthenticated`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`,
`ErrTooLarge`, `ErrRateLimited`, `ErrUnavailable`, `ErrServerError` — matched
via `errors.Is`; `errors.As(&apiErr)` recovers structured details. Code
values mirror the JS error taxonomy
([§7](js-client-library.md#7-stability-promise)) so both SDKs agree.

`errors.Is` is keyed off **HTTP status, not `Code`**. Codes remain the stable
*contract* (see [§8](#8-stability-promise)), but they are not the right
*match* key: the server legitimately attaches app-specific codes to canonical
statuses (`queue_full` on a 409, see `internal/web/handlers/queue.go`), and
keying on `Code` would make `errors.Is(err, ErrConflict)` false for exactly
the conflicts callers care about. Status is the coarser, total classifier —
every canonical code maps to exactly one status — so sentinel matching stays
exhaustive while `Code` remains available for finer branching.

Transport failures (DNS, connection refused, timeout) are to wrap as a
distinct `*NetworkError`, never `*APIError` — not yet implemented, owned by
`.5`.

## 5. `context.Context` conventions

Every network-touching method becomes ctx-first (`ListSessions(ctx)`, …);
today only `Connect`/`PromptAndWait` take one. The signature migration is
done **wholesale in a single commit**, with no back-compat wrappers: every
caller is in-repo, the change is mechanical, and dual signatures would
double the surface forever. Sequenced **after** `.2` so the rename diff
stays reviewable on its own. `Session` keeps the lifetime `ctx` supplied to
`Connect`; per-call `ctx` is not threaded through the WebSocket write path.

## 6. Realtime: streaming API coexists with `SessionCallbacks` (`.5`/`.6`)

`SessionCallbacks` remains the low-level primitive and the **only** delivery
mechanism — the existing test suite depends on it. The streaming API is a
thin adapter registered over the same read loop, not a second transport.
Go 1.25 is in `go.mod`, so `Session.Events(ctx) iter.Seq2[Event, error]` is
the primary form, plus a `<-chan Event` variant for select-based callers.

Rules: at most **one** active stream per `Session` (a second call returns an
error, avoiding a fan-out race); a caller may still set callbacks for events
the stream does not model; the internal buffer is bounded, and overflow
terminates the sequence with `ErrSlowConsumer` rather than silently
dropping events; both `ctx` cancellation and disconnect terminate the
sequence with a non-nil error. Resilience — reconnect/backoff, keepalive,
sequence resync (`.5`) — is opt-in and **off by default**, so existing
deterministic tests are unaffected.

Concurrency: `Client` and `Session` are safe for concurrent use; callbacks
(and the streaming adapter) are invoked serially from the single read-loop
goroutine, so a slow consumer blocks delivery — document this explicitly,
since today it is accidental rather than designed.

## 7. Semver policy / relation to server version

Same conclusion as the JS SDK
([§6](js-client-library.md#6-semver-policy--relation-to-server-version)):
`pkg/api` ships inside the Mitto module and is lockstep-versioned with the
server — its version _is_ the release tag, so there is no compatibility
matrix to maintain. No nested `go.mod`, no independent tag.

Go-module caveat the JS SDK does not have: under `v0.x` the module path
carries no major-version suffix, and reaching `v1.0.0` freezes the exported
surface under Go's [import compatibility
rule](https://go.dev/doc/modules/version-numbers). The exported surface
should therefore be trimmed to what `.7` actually needs before cutting any
`v1`.

## 8. Stability promise

**May break in a minor release**: unexported identifiers, the shape of
`APIError.Details`, anything under `pkg/api/internal/` (if introduced), the
experimental streaming buffer-size constants.

**Must not break in a minor release**: exported names and signatures in
`pkg/api`, `APIError.Code` values, streaming event type names.

Deprecations use the standard `// Deprecated: use X instead.` doc-comment
convention and MUST get a row in the deprecation register in
[API Stability Tiers and Deprecation Policy §6](api-stability.md#6-deprecation-register)
— that record already lists the Go SDK as a tracked surface and derives
tier from public-export reachability
([§3](api-stability.md#3-tier-is-derived-not-enumerated)); this record
defers to it rather than restating the window.

## Related issues

`.2` relocate `internal/client` → `pkg/api` · `.3` typed error model ·
`.4` authentication · `.5` resilient realtime · `.6` channel/iterator
streaming API · `.7` complete conversation-centric REST surface ·
`.8` documentation and runnable examples · `.9` unit test suite and
fake-server fixtures.
