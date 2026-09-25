# ADR 0001: Frontend Stack — Retain Preact/HTM/daisyUI/Tailwind/WKWebView

**Status:** Accepted (Preact, HTM, daisyUI, Tailwind) / Accepted-for-now,
revisit conditions apply (WKWebView) — see per-layer sections below.
**Date:** 2026-09-25
**Bead:** `mitto-sus.10` (epic: `mitto-sus`)

## Context

The `mitto-sus` epic set out to answer whether Mitto's frontend feels less
responsive than VS Code because of the stack (Preact, HTM, Tailwind,
daisyUI, WKWebView) or because of the application's own data-flow and DOM
work. Per the epic's framing, replacing any layer before measuring would be
expensive and would preserve most of the underlying data-flow problems. This
ADR closes that question with evidence from the epic's children, applying a
default-to-Retain rule per layer: **Retain unless** the mitto-sus.2 A/B
classification attributes a material residual share of the responsiveness
gap to that layer **and** a prototype meets budgets **and** the migration
cost has been estimated.

## Decision rule

For each layer: **Retain**, **Replace**, or **Reject-and-revisit** (evidence
inconclusive today; re-open under a named condition). Each entry lists
Decision, Evidence, Alternatives considered, Cost of change, and Revisit
conditions.

## 1. Preact — Retain

- **Evidence:** `docs/devel/frontend-render-domains.md` (`mitto-sus.7` /
  `mitto-b1k` / `mitto-sus.11`) shows the observed re-render churn was
  **application-level** (broad prop-drilling from `App`, shared state
  crossing unrelated component trees) — not a Preact reconciler limitation.
  Migrating `SessionList`/`MessageList`/`ChatInput`/`ToastContainer`/
  `QueueDropdown` onto self-subscribed slices of `stores/sessionsStore.js`
  plus targeted `memo()` closed the isolation gaps directly, pinned by
  `tests/ui/specs/perf/render-isolation.perf.spec.ts`'s hard `expect()`
  gates (background chunk, composer keystroke, idle keepalive, toast,
  queue add/delete, config-option change — all assert `0` or a small bounded
  ceiling for domains that should not reconcile). `mitto-sus.2`'s A/B report
  classifies render-count-driven scenarios (`render.queue-add-delete`,
  `render.set-config-option`, `render.postprocess.*`) as **application-
  dominant, not engine-dominant** (all ≤1.11x WebKit/Chromium ratio).
- **Alternatives considered:** React (larger runtime, same reconciler
  model — would not have prevented the same prop-drilling mistakes), Solid/
  Svelte (fine-grained reactivity would sidestep broad reconciliation by
  construction, but neither has an existing HTM-equivalent no-build story
  compatible with Mitto's `//go:embed` deployment).
- **Cost of change:** High — a full-tree rewrite touching every component
  under `web/static/components/`; no measured evidence justifies it.
- **Revisit conditions:** If a future profiling pass finds Preact's
  reconciler itself (not application state shape) is the dominant cost on a
  budgeted scenario even after full render-domain isolation.

## 2. HTM — Retain

- **Evidence:** HTM (tagged-template JSX-without-a-build-step) is a template-
  literal parser, not a rendering layer — it has no measurable runtime cost
  distinct from Preact's own reconciler in any of the epic's benchmarked
  scenarios. `docs/devel/frontend-bundler-spike.md` (`mitto-qcm`) evaluated
  adding a bundler/JSX toolchain (Vite) and explicitly recommended **against
  adoption** (Option C): the six large files driving perceived complexity are
  a decomposition problem, not a build-step problem, and Vite's own output
  was measured *larger* (raw and gzipped) than the current esbuild-based
  CodeMirror bundle on equivalent input.
- **Alternatives considered:** JSX + a bundler (Vite, esbuild-as-bundler) —
  would require a build step Mitto does not otherwise need (`//go:embed`
  ships `web/static/` as-is); rejected by `mitto-qcm` on cost/benefit, not
  re-litigated here.
- **Cost of change:** Medium — every component file, plus a new build step
  in `make build` / release packaging.
- **Revisit conditions:** If `mitto-90f`'s sibling-module decomposition does
  not relieve growth pressure on the largest files and a bundler becomes
  independently justified — `mitto-qcm` names this exact follow-up.

## 3. daisyUI — Retain

- **Evidence:** No benchmarked scenario in this epic attributes measurable
  responsiveness cost to daisyUI's component classes (they compile to plain
  Tailwind utility CSS at build time, no runtime JS). The drawer-compositing
  GPU bug documented in `.augment/rules/20-web-frontend-core.md` was a CSS
  layering defect with a verified fix, not evidence of a structural
  performance problem with the library.
- **Alternatives considered:** Hand-rolled component CSS — strictly more
  maintenance for no measured responsiveness gain; headless UI libraries
  (Radix-style) — would require a JS runtime dependency Mitto's no-build HTM
  setup does not currently carry.
- **Cost of change:** High — every component's markup and class names.
- **Revisit conditions:** None identified; daisyUI was never implicated by
  any collected metric.

## 4. Tailwind — Retain

- **Evidence:** Same as daisyUI — Tailwind compiles to static CSS
  (`bunx @tailwindcss/cli`, see `package.json`'s `tailwind` script) with zero
  runtime cost; no perf spec or collector in this epic (long-task, frame,
  paint/layout, DOM-node, or render-count) attributes any share of the
  responsiveness gap to CSS generation or utility-class bloat.
- **Alternatives considered:** CSS Modules / hand-authored CSS — more
  maintenance, no measured upside; CSS-in-JS — would add a runtime cost
  Tailwind's static-compile model avoids by construction.
- **Cost of change:** High — every component's class strings.
- **Revisit conditions:** None identified.

## 5. WKWebView — Retain for now; revisit conditions apply

- **Evidence:** `docs/devel/ui-responsiveness-ab.md` (`mitto-sus.2`) ran a
  two-leg Chromium/WebKit A/B (the WKWebView-specific third leg was
  **descoped** by the 2026-09-23 `mitto-sus.2` resolution, so no native-app
  data exists). The two-leg result: the largest gaps
  (`composer.keystroke` 4.67x p50, `history-load.*.prepend-step-*` 1.2–4.4x,
  `session.switch.network` p50 1.88x) are **engine-attributable** (WebKit
  vs Chromium generally) rather than **wrapper-attributable** (WKWebView
  specifically) — the report's explicit conclusion is that a
  WKWebView-replacement decision **cannot be made responsibly without
  WKWebView-specific data**, and names two follow-on paths: (1) run the
  deferred WKWebView playbook, or (2) prioritize the in-flight
  engine-dominant application work regardless, since those gaps would
  persist even under a different wrapper.
- **Decision for this ADR:** **Retain WKWebView now** — replacing it with
  Electron or another wrapper solely on today's evidence would be
  speculative (the epic's own non-goals explicitly reject "replacing
  WKWebView with Electron solely to imitate VS Code"), and the measured
  engine-dominant gaps would not be resolved by a wrapper swap alone (WebKit
  is WebKit whether embedded via WKWebView or a hypothetical alternative
  Apple-platform wrapper).
- **Alternatives considered:** Electron (ships its own Chromium — would
  close the WebKit-specific gaps above, but at the cost the epic's own
  non-goals explicitly reject: a full packaging/distribution model change
  imitating VS Code without evidence the wrapper itself, not the engine, is
  the bottleneck).
- **Cost of change:** Very high — packaging, distribution size, macOS
  entitlements, and `cmd/mitto-app/`'s native bridge code all assume
  WKWebView today.
- **Revisit conditions (unchanged from `mitto-sus.2`):** Re-open this
  section once the deferred WKWebView-specific playbook (leg C) is
  executed and populates `tests/ui/perf/results/latest-wkwebview/` —
  `make bench-ui-ab` folds it into the existing A/B report unchanged, at
  which point a `Rejected` / `Referred` verdict on the wrapper itself
  becomes evidence-based. Until then this decision is **provisional**, not
  a closed question.

## Consequences

- No frontend framework, templating, CSS, or native-wrapper migration is
  scheduled as a result of this epic. All five layers are retained; the
  substantial responsiveness gains delivered by `mitto-sus.3/7/9/11` and the
  regression gates added by `mitto-sus.10.1` are the epic's durable output,
  not a stack change.
- The WKWebView section is intentionally weaker than "Accepted" — it
  documents a **provisional** retain pending optional follow-up data, not a
  closed decision, so a future contributor does not mistake silence for a
  settled question.
- Cross-links: [ui-responsiveness-ab.md](../ui-responsiveness-ab.md),
  [ui-responsiveness-baseline.md](../ui-responsiveness-baseline.md),
  [frontend-render-domains.md](../frontend-render-domains.md),
  [frontend-bundler-spike.md](../frontend-bundler-spike.md),
  [virtualization-spike.md](../virtualization-spike.md).
