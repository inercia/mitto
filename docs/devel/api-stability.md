# API Stability Tiers and Deprecation Policy

This is the canonical decision record for **endpoint-level stability tiers and
deprecation windows** across every Mitto API surface: the REST API
(`internal/web`), the WebSocket protocol
([protocol-spec.md](websockets/protocol-spec.md)), the JavaScript SDK
(`web/static/sdk/`, see [JS Client Library](js-client-library.md)), and the
Go SDK (`pkg/api`, tracked under `mitto-rwxq`).

It complements, and does not duplicate, two existing documents:

- [REST API Conventions](rest-api-conventions.md) owns path/method naming and
  the current → target endpoint **migration** mapping (a different axis from
  stability).
- [JS Client Library](js-client-library.md) §7 owns the SDK **export**
  stability promise (which symbols may change shape in a minor release).

This record owns the tier a given endpoint/message/export sits in, and the
rules for deprecating and removing it.

## 1. The three tiers

| Tier           | Meaning                                                                                                                            |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `stable`       | Public contract. Breaking changes require the deprecation window (§3).                                                             |
| `experimental` | Opt-in, may change or be removed without the full window. Must be explicitly listed with an expiry date; not covered unless named. |
| `internal`     | Everything else. No stability promise. **This is the default.**                                                                    |

There is **no fourth tier**. `external-stable` (§2) is a marker layered on
top of `stable`, not a competing vocabulary.

## 2. `external-stable` reconciles with `stable`

[REST API Conventions §6](rest-api-conventions.md#6-exception-list--external-stable)
lists 11 endpoints called by callers that bypass any SDK entirely (the native
macOS app, load balancers, viewer pages, webhook callers). Those endpoints
are `stable`, plus the extra constraint that their **path cannot even be
renamed** — normal `stable` entries may be renamed with a deprecation window
(old path returns the §6 headers pointing at the new one); `external-stable`
entries may not, because there is no SDK indirection to redirect through.
Removal of an `external-stable` endpoint requires a major version, not just
the standard window (§3).

## 3. Tier is derived, not enumerated

Hand-tagging ~100 REST routes and ~66 WebSocket message types would rot the
moment a new resource module lands. Instead, tier is a **derived property**
of the client-library surface:

> An endpoint or WebSocket message is `stable` **iff** it is reachable
> through a public export of a client library (the JS SDK's `index.js`, or
> the Go SDK's `pkg/api`) **or** appears in the `external-stable` table
> (§6 of REST API Conventions). Everything else is `internal` by default.
> `experimental` requires an explicit, separately-listed entry with an
> expiry date — it is never a fallback.

This makes stability self-synchronising as SDK resource modules land
(`mitto-7gta.7`–`.16`, `mitto-rwxq.7`) and gives `mitto-7gta.24`'s
route-coverage gate (which already walks `internal/web/routes.go`) a
mechanical hook: any route reachable from a public SDK export is `stable`
by construction, with no separate tier field required on `apiRoute` today.

## 4. Deprecation window

A deprecation must carry a visible notice for **at least 2 minor releases
and at least 90 days**, whichever is longer, before removal. Removal itself
may only happen in a minor or major release per semver (never a patch).

- **`external-stable` entries**: removal only in a **major** release,
  regardless of elapsed time — the native app ships on its own cadence and
  cannot be assumed to have picked up an interim minor.
- **Counting for 0.x**: Mitto has no `internal/version` package; releases
  are git tags only (latest `v0.3.0` at the time of writing), and the SDK is
  lockstep-versioned with the server ([JS Client Library §6](js-client-library.md#6-semver-policy--relation-to-server-version)).
  While the tag stays `0.x`, "minor release" means an increment of the `0.Y`
  component (`0.3.0` → `0.4.0` counts as one minor); an increment of `Y` in
  `0.Y.Z` is what starts the 2-release clock, not the patch component `Z`.

## 5. Signalling, one mechanism per surface

- **REST**: the deprecated response carries `Deprecation: true` ([RFC
  9745](https://www.rfc-editor.org/rfc/rfc9745)) and `Sunset: <HTTP-date>`
  ([RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)) headers, plus
  `Link: <replacement-url>; rel="deprecation"` when a direct replacement
  exists. Emitted by one shared `internal/web` helper so every handler
  stays consistent — never set ad hoc per-handler.
- **WebSocket**: a `deprecated` (bool) and `sunset` (date string) pair added
  to the message envelope's metadata, mirroring the REST headers. The
  existing "Legacy Messages" table in
  [protocol-spec.md](websockets/protocol-spec.md#legacy-messages) is folded
  into the register (§6) as the source of truth; that table now links here.
- **JS SDK**: a `@deprecated` JSDoc tag on the export (which propagates into
  the generated `.d.ts` per `mitto-7gta.20`), plus a **once-per-symbol**
  warning emitted through the SDK's injected logger — never a direct
  `console.*` call, since the SDK is environment-agnostic.
- **Go SDK**: the standard `// Deprecated: use X instead.` doc-comment
  convention (surfaced by `go vet`, staticcheck, and editor tooling).
- **Docs**: the deprecation register below is the single canonical list;
  other documents reference it rather than keeping their own copy.

## 6. Deprecation register

The normative list of everything currently deprecated, across all surfaces.
Nothing may be deprecated, promoted, or removed without a row here.

| Surface   | Identifier          | Tier     | Deprecated in | Sunset | Replacement                                  |
| --------- | ------------------- | -------- | ------------- | ------ | -------------------------------------------- |
| WebSocket | `permission`        | internal | pre-v0.3.0    | TBD    | `ui_prompt` with `prompt_type: "permission"` |
| WebSocket | `permission_answer` | internal | pre-v0.3.0    | TBD    | `ui_prompt_answer`                           |
| WebSocket | `sync_session`      | internal | pre-v0.3.0    | TBD    | `load_events` with `after_seq`               |
| WebSocket | `session_sync`      | internal | pre-v0.3.0    | TBD    | `events_loaded`                              |

These four predate this policy (no recorded deprecation date, hence no
enforceable sunset yet) and are tiered `internal` today — they were never
exported from a client SDK. `TBD` sunset dates must be back-filled with a
real date before any of them can actually be removed (§4 still applies from
that date forward).

## 7. Tier-change process

- **Promotion** (`internal` → `experimental` → `stable`) is additive and
  always allowed — it happens automatically when a symbol is exported from
  a client SDK (§3), or explicitly when adding an `experimental` entry.
- **Demotion** (`stable` → `internal`, or removing an `external-stable`
  marker) is a removal and requires a register entry (§6) plus the full
  window (§4).
- Enforcement — a `tier` field on `apiRoute` and a CI gate checking it — is
  intentionally **not** part of this record; it belongs to the existing
  `mitto-7gta.24` (route-coverage gate) and `mitto-7gta.19` (lint/CI
  enforcement), which this record's derived-tier rule (§3) is designed to
  feed.

## Related issues

`mitto-7gta` (JS SDK epic) · `mitto-7gta.19` (lint/CI enforcement, tier-gate
owner) · `mitto-7gta.20` (typings, `@deprecated` → `.d.ts` propagation) ·
`mitto-7gta.24` (route-coverage gate, tier-derivation owner) · `mitto-rwxq`
(Go SDK epic, `pkg/api`).
