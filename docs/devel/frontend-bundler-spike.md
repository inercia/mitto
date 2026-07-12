# Frontend bundler spike (mitto-qcm)

> Timeboxed 1-day spike. Recommendation: **C** (do not adopt Vite) — but
> keep esbuild in the toolbelt as an on-demand rescue lever if a specific
> component ever needs it.

## Context

Six frontend files exceed 3k LOC (see `bd show mitto-qcm` for the table).
The spike answers whether a bundler (Vite specifically) buys us anything
that isn't already achievable by the split-by-concern work already tracked
under `mitto-90f.3/4/5`. Constraints in-tree today:

- `web/static/` is served by Go via `//go:embed static` (`web/embed.go`) — any
  bundle output MUST land under `web/static/` to reach end users.
- Only build step today is `make tailwind` (Tailwind v4 CLI). The frontend has
  **no JS build step**; components ship as raw ESM and are imported at
  runtime from `preact-loader.js` → `app.js` → transitive graph.
- `esbuild@0.28.1` is **already** in `node_modules` and is already used by the
  `vendor:codemirror` script (`npm run vendor:codemirror`) — bundling into
  `web/static/vendor/codemirror/codemirror.js`. Vite is **not** installed
  (the bead's premise that it was is incorrect).

## What we tested

Two throwaway POCs, both targeting the smallest of the six files
(`web/static/components/ChatInput.js`, 3,444 LOC, 14 imports, 1 export).

- **esbuild POC** — `scripts/spike-bundler/esbuild-chatinput.mjs`. Uses the
  already-installed esbuild in library ESM mode. Bundles `ChatInput.js` +
  its transitive `../utils/*` graph; keeps `./` sibling components,
  `../hooks/*`, and `../vendor/*` external so the emitted file is a drop-in
  replacement.
- **Vite POC** — `scripts/spike-bundler/vite-chatinput/` (self-contained
  package.json so the workspace root's `package.json` stays untouched). Same
  externals list, `build.lib` mode, `formats: ["es"]`, minify off for
  apples-to-apples byte comparison with the esbuild POC.

Both scripts write to `scripts/spike-bundler/**/dist/` which is `.gitignore`d.
Both were actually executed; numbers below are from real runs on this host
(node v26.5.0, macOS).

## Evidence

### POC bundle metrics (identical externals)

| Metric                  | esbuild@0.28.1 | Vite@8.1.4 (bundles rolldown)              | Raw source |
| ----------------------- | -------------- | ------------------------------------------ | ---------- |
| Build time              | 8 ms           | 15 ms                                      | —          |
| Input files bundled     | 11             | 12                                         | 1          |
| Output size (bytes)     | 123,461        | 130,080                                    | 134,489    |
| Output size (KiB)       | 120.6          | 127.0                                      | 131.3      |
| gzipped (bytes)         | 24,271         | 28,261                                     | —          |
| gzipped (KiB)           | 23.7           | 27.6                                       | —          |
| Config LOC              | 100 (script)   | 49 (`vite.config.js`)                      | —          |
| Warnings / errors       | 0 / 0          | 0 / 0                                      | —          |
| Externals kept external | 3/3 ✓          | all sibling imports ✓                      | —          |
| `export { ChatInput }`  | ✓              | ✓                                          | ✓          |
| Devtool disk cost (new) | 0 (installed)  | +28 MB (isolated)                          | —          |
| Toolchain adds          | none           | vite, rolldown, lightningcss, postcss, oxc |            |

Notes on the numbers:

- Vite 8 (Aug 2025) now bundles **rolldown** (Rust-based) instead of classic
  rollup, plus lightningcss and postcss. The `node_modules/rollup` and
  `node_modules/esbuild` directories we expected never appeared under the vite
  POC because rolldown replaces both.
- esbuild's output is ~5 % smaller both raw and gzipped, and ~2× faster on
  this size class. The gap is not decisive — both are fine — but it removes
  the "vite is smaller/faster" argument.
- Both bundles verified as drop-in: `head -3` of each still contains
  `import { useResizeHandle } from "../hooks/useResizeHandle.js";` etc., and
  `tail -3` still contains `export { ChatInput };`. Sibling components are
  still resolved at runtime by the browser exactly as before.

### Q1 — Can a bundler build a subset that plugs into the existing HTM/Preact CDN setup?

**Yes, trivially.** Both POCs produced a single-file ESM bundle whose only
observable difference vs the raw file is that `../utils/*` deps were inlined.
`window.preact` / `window.marked` / `window.DOMPurify` continue to work
unchanged because ChatInput never `import`s Preact directly — it reads from
the globals set by `preact-loader.js`. Sibling `./ComponentX.js` and
`../hooks/*.js` imports stay as bare specifiers, so `app.js` and other
components keep importing them via the current graph.

### Q2 — On-disk footprint / does the bundle ship?

The Go binary embeds `web/static` verbatim (`web/embed.go` →
`//go:embed static`). Anything a bundler emits MUST land under
`web/static/` to reach end users, otherwise it isn't served. In practice
that means bundled artifacts either (a) replace the source file in-tree and
are committed (like `web/static/vendor/codemirror/codemirror.js` already
does), or (b) live alongside and are pointed at by the loader.

For the ChatInput POC specifically: raw source is 131 KiB, bundled ESM is
121 KiB — bundling **shrinks** the on-disk cost, because the util files no
longer need to be shipped as separate module fetches. The dev-side footprint
(node_modules) differs sharply between the two tools: esbuild is already
paid for (10 MB, already in tree); adding vite costs an additional 28 MB
of dev-only tooling.

### Q3 — Interaction with `make tailwind`

None. `web/static/tailwind.src.css` uses Tailwind v4's `@source "../static"`
directive to scan the source tree for utility classes. Bundling JS does not
change the class strings that appear in source files — a bundled
`ChatInput.bundle.js` under `web/static/` would still be scanned identically.
The only failure mode would be if the bundler minified/mangled class-name
string literals (esbuild does not; vite in `build.lib` mode with `minify: false`
also does not). If we ever enable minification, we'd want `@source` to
also cover the bundled output directory (already true given `@source
"../static"` is a directory glob).

### Q4 — Per-component migration cost

Externals stay external, sibling imports keep working, and none of the six
files ever import Preact/HTM/marked/DOMPurify directly (all use the globals
set by `preact-loader.js`). That means call sites do NOT change — no
downstream file edits needed.

| File                             |   LOC | `^import` count | Exports            | Est. per-file work                                      |
| -------------------------------- | ----: | --------------: | ------------------ | ------------------------------------------------------- |
| `hooks/useWebSocket.js`          | 6,377 |               8 | 1 (`useWebSocket`) | ~1 h                                                    |
| `components/SettingsDialog.js`   | 6,205 |              15 | 5                  | ~1.5 h (5 exports need entry surface)                   |
| `components/BeadsView.js`        | 5,261 |              11 | 5                  | ~1.5 h                                                  |
| `components/WorkspacesDialog.js` | 4,850 |              13 | 1                  | ~1 h                                                    |
| `app.js`                         | 3,715 |              36 | 0                  | ~2 h (largest import surface, no exports — entry point) |
| `components/ChatInput.js`        | 3,444 |              14 | 1                  | **done in POC, ~1 h**                                   |

Rough cost per file: 1–2 h to add an entry to a bundler config, wire it into
a make target, verify the emitted bundle is a drop-in, and swap the loader
reference. **This is not where the time in a real migration would go** — the
time cost sits in decomposing a 6k-LOC file into smaller modules, which is
exactly what `mitto-90f.3/4/5` already do without a bundler.

### Q5 — WKWebView constraints

None that block modern ESM.

- CSP served by `internal/web/middleware/csp_nonce.go`:
  `script-src 'self' 'nonce-XXX' https://cdn.tailwindcss.com
https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://esm.sh`.
  Bundled JS served from `web/static/` is same-origin (`'self'`), so it is
  allowed with no per-request nonce (nonces are only required for **inline**
  scripts; `<script src>` module fetches are governed by the source URL).
- Main app uses `github.com/webview/webview_go`, which on macOS builds a
  WKWebView with default preferences (no `preferences.javaScriptEnabled=NO`,
  no custom `WKURLSchemeHandler`). The dedicated file-viewer window in
  `cmd/mitto-app/viewer_darwin.m` creates its own `WKWebViewConfiguration`
  but only adds two message handlers (`closeViewer`, `openFileURL`) — no
  restrictions on JS execution or module loading.
- WKWebView on the macOS versions Mitto targets (macOS 11+) supports
  ES2022, `import`/`export`, dynamic `import()`, and source maps. No
  bundle-format constraint applies.

## Recommendation — C (do not adopt Vite)

Reasoning grounded in the evidence:

1. **The 6 large files are a decomposition problem, not a bundler problem.**
   Splitting `WorkspacesDialog.js` (4,850 LOC, 1 export) into sibling ESM
   modules — the `mitto-90f.3/4/5` approach — solves the growth-pressure
   issue directly without adding a build step. A bundler concatenates
   files but does nothing to reduce the cognitive complexity of any
   individual one.
2. **Vite adds a second toolchain for a marginal loss.** Vite@8 pulled
   28 MB of dev-only tooling (vite, rolldown, lightningcss, postcss, oxc)
   and produced a bundle that is 5 % **larger** raw and 16 % larger gzipped
   than esbuild's on the same input. Its `dev serve` story doesn't help us
   either — our runtime is Preact-from-globals + HTM (no JSX transform, no
   HMR-friendly graph), so `vite serve` would need shims and would not
   accelerate iteration vs the current "edit-and-reload" loop.
3. **esbuild is already paid for.** We ship a CodeMirror bundle produced by
   the exact same tool. If a specific file ever needs bundling (e.g. a
   future component that must inline a large dependency), the pattern from
   `scripts/codemirror/entry.js` + `npm run vendor:codemirror` extends
   trivially — the POC script here is the template.
4. **Bundling would not fix `//go:embed`.** The output still ships to end
   users via the embedded FS. Moving to a bundler doesn't change
   deployment; it changes only which JS bytes end up under `web/static/`.

Explicit link to the alternative path already in flight: **mitto-90f.3/4/5**
(split by concern into sibling ES modules — no new toolchain). This spike
recommends staying on that path.

Concrete follow-ups (recorded so the question doesn't get reopened):

- No package.json change. Vite is not added.
- The two POC scripts remain under `scripts/spike-bundler/` as reference
  material — if we ever need to bundle a specific component ad-hoc, adapt
  `esbuild-chatinput.mjs`. Their output directories are gitignored.
- If growth pressure recurs after the mitto-90f.3/4/5 work lands, revisit
  with fresh numbers — not another Vite spike.
