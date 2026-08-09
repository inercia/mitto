# JavaScript Client Library — Design Decision Record

Status: decided (mitto-7gta.1). Scope: design only — no code ships in this
record; implementation is tracked across the sibling issues of the
`mitto-7gta` epic (`mitto-7gta.2`–`.28`), referenced by ID below.

## Context

The Mitto UI currently talks to the backend through ad-hoc call sites: 180+
raw `fetch`/`authFetch` calls across 43 files (`web/static/utils/csrf.js`,
`endpoints.js`) plus a 4800+ LOC `useWebSocket.js` fused to Preact. There is
no reusable, documented, testable client — for the UI or for third parties /
future CLI tools. This record defines the shape of the reusable SDK before
any code is written.

## 1. Package layout

All SDK source lives under `web/static/sdk/`:

```
web/static/sdk/
  index.js            # the ONLY public entrypoint (see §5)
  package.json        # type: "module", npm metadata (see §3)
  core/                # config, injectable environment, errors, transport — .2/.3/.4
    endpoints.js       # canonical URL registry, adopted from utils/endpoints.js — .6
  env/                 # explicit opt-in environment presets (e.g. browser) — .2
  auth/                # browser cookie+CSRF, shared bearer token, none — .5
  resources/           # one module per REST resource — .7–.12
  realtime/            # SessionStream, EventsStream, sync, typed events (events.js) — .13–.16
  types/               # generated .d.ts, not hand-written — .20
```

## 2. Module / file naming

Lowercase kebab-case `.js`, one resource per file, named after the REST
resource noun it wraps (`sessions.js`, `queue.js`, `issues.js`, …) so each
module maps obviously to a path prefix in
[REST API Conventions §7](rest-api-conventions.md#7-current--target-endpoint-mapping).
This 1:1 mapping is what the `.24` route-coverage gate checks against
`routes.go`.

## 3. Distribution

**Already solved by existing infrastructure — no new plumbing needed.**
`web/embed.go` embeds `static` **recursively** (`//go:embed static`), and
`internal/web/server.go` serves it via `fs.Sub(StaticFS, "static")` mounted
at both `/` and `<apiPrefix>/` (server.go:1626,1639). Anything placed under
`web/static/sdk/` is therefore served automatically at `/sdk/index.js` (and
`<apiPrefix>/sdk/index.js`), version-matched with the running server by
construction — no separate embed directive, no build step.

Consumption paths:

- **Browser, in-tree**: the Mitto UI imports directly from `/sdk/index.js`.
- **Browser, third party**: `import { createClient } from "https://<host>/sdk/index.js"`.
- **Node / Bun**: fetch or vendor the `sdk/` tree (plain ESM, zero runtime
  dependencies — no bundler required either way, consistent with the
  no-build-step conclusion in
  [Frontend Bundler Spike](frontend-bundler-spike.md#recommendation--c-do-not-adopt-vite)).
- **npm (future)**: `package.json` declares `"type": "module"` and an
  `exports` map that publishes **only** `index.js`; deep paths are not
  resolvable through `exports`, enforcing §5 at the package-manager level.

## 4. Environment-agnostic contract

The SDK assumes only ESM plus a `fetch`-shaped and `WebSocket`-shaped API.
Everything environment-specific is **injected** through one config object:

```js
createClient({
  baseUrl,
  fetch,
  WebSocket,
  storage,
  auth,
  logger,
  onUnauthorized,
});
```

Forbidden inside `sdk/`: `window`, `document`, `document.cookie`,
`localStorage`, `location`, `native.js`, or bare `console.*` calls. Browser
defaults are resolved lazily from `globalThis` so a browser caller can pass
an empty config and still work.

Two concrete couplings from today's code are explicitly displaced:

- `window.mittoApiPrefix` (injected into `index.html`/`auth.html` as
  `{{API_PREFIX}}`, read by `utils/api.js`) → becomes the caller-supplied
  `baseUrl`. The SDK never reads the global itself.
- The 401 → `redirectToLogin()` side effect in `utils/csrf.js` is **policy,
  not transport**, and does **not** move into the SDK: the SDK raises a
  typed `MittoAuthError` (a `MittoApiError` specialization, `.3`), and the
  browser host wires the redirect via the `onUnauthorized` hook. Auth
  adapters themselves (cookie+CSRF, shared bearer token, none) are `.5`.
- The seq watermark and pending-prompt queues that `utils/storage.js` keeps
  in `localStorage` (`mitto_last_seen_seq:*`, `mitto_pending_prompts`) →
  become the injected `seqStore` / `pendingPromptStore` adapters accepted by
  `SessionStream` (`.14`). Both default to in-memory implementations;
  `createStorageSeqStore(storage)` and
  `createStoragePendingPromptStore(storage)` build persistent variants on
  the same injected `storage` contract, so the SDK still never touches
  `localStorage`.

The `auth` adapter (`sdk/auth/`, `.5`) implements up to three methods:
`authorize({ method, url, headers })` returning a `{ headers?, credentials? }`
patch merged into every request; the optional `authorizeWebSocket({ url })`
returning `{ protocols?, options? }` passed to the `WebSocket` constructor;
and the optional `onUnauthorized(error)` called on every 401 before the
host-level `onUnauthorized` hook. Three implementations ship:
`browserCookieAuth` (double-submit cookie + `X-CSRF-Token` on state-changing
methods, cookie read injected via `browserCookieReader()`), `sharedTokenAuth`
(`Authorization: Bearer <token>` from a lazy token supplier — never a string,
never logged, never in a URL or query string; the WS handshake is
header-only, so it applies to non-browser `WebSocket` implementations only),
and `noneAuth` (the default, adds nothing).

Deduplication inside `SessionStream` is **non-destructive by default**: a
duplicate seq is annotated (`{ duplicate: true }` on the `message` event)
rather than dropped, because dropping unconditionally at transport level
races with `events_loaded` and silently swallows legitimate messages (see
[.augment/rules/24-web-frontend-sync.md](../../.augment/rules/24-web-frontend-sync.md)).
Hosts that want the older drop behavior opt in via `dropDuplicates: true`
and listen for the separate `duplicate` event.

## 5. Public vs internal boundary

The public surface is **exactly** what `index.js` re-exports. Everything
else (`sdk/core/transport.js`, `sdk/resources/*.js`, …) is a deep import and
is internal/unsupported — it may change in any release without notice. This
is enforced two ways: the npm `exports` map (§3) blocks deep resolution at
install time, and the `.19` lint rule / CI gate blocks deep imports from
outside `sdk/` at source-review time.

## 6. Semver policy / relation to server version

Mitto has **no compiled-in version string** today (`Makefile` `LDFLAGS` is
only `-s -w`; there is no `internal/version` package) — releases are git
tags only (latest `v0.3.0` at the time of this record).

- **Embedded copy**: lockstep with the server. Its version _is_ the Mitto
  release tag it ships inside; there is no compatibility matrix to maintain
  because it is always served by the exact server it targets.
- **npm copy** (future): its own independent semver, cut from a specific
  Mitto tag, declaring the minimum server version it requires.
- Both expose a `VERSION` constant from `index.js` so a client pairing a
  mismatched SDK/server combination can detect it at runtime.

## 7. Stability promise

**May break in a minor release**: internal modules, any deep-import path,
the shape of an error's `details` payload, anything not re-exported from
`index.js`.

**Must not break in a minor release**: `index.js` export names and
signatures, error class names and `code` values (aligned with the error
envelope in
[REST API Conventions §4](rest-api-conventions.md#4-error-envelope)),
realtime event names. The pinned error taxonomy (`.3`): `MittoError` (base),
`ConfigError` (`code: "invalid_config"`), `MittoApiError`, `MittoAuthError`
(401/403 specialization of `MittoApiError`), `MittoNetworkError`
(`code: "network_error"`).

Endpoint-level stability tiers and deprecation windows (e.g. the
`external-stable` exception list in
[REST API Conventions §6](rest-api-conventions.md#6-exception-list--external-stable))
are a separate concern, defined in
[API Stability Tiers and Deprecation Policy](api-stability.md), and are
referenced rather than duplicated here.

## Related issues

`.2` core config/env · `.3` typed errors · `.4` transport · `.5` auth
adapters · `.6` endpoints.js adoption · `.7`–`.12` REST resource modules ·
`.13`–`.16` realtime · `.17`–`.18` UI migration · `.19` lint/CI enforcement ·
`.20` typings · `.21` `docs/api/` reference · `.22` browser/CLI examples ·
`.23`–`.25` testing · `.26` shared-token backend auth · `.27` CORS/origin
policy · `.28` API stability tiers.
