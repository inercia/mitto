/**
 * Reproduction test for mitto-mzvc:
 *   Mouse-driven swipe-to-archive/delete collapses immediately due to a
 *   trailing native `click` after `mouseup`.
 *
 * Root cause (see the mitto-mzvc Investigation comment): `handleDragEnd`'s
 * reveal branch clears `isSwipingRef.current = false` SYNCHRONOUSLY together
 * with `setIsRevealed(true)`. By the time the browser dispatches the
 * trailing native `click` on the same element, Preact has already re-bound
 * `onClick` to a fresh closure that sees `isSwipingRef.current === false`
 * AND `isRevealed === true` — so the click handler's guard is a no-op and
 * falls through to the `isRevealed` branch, calling `reset()` and collapsing
 * the just-revealed affordance. Both `SessionItem.js` and `BeadsView.js`
 * wire an identical `handleClick` around this hook's `isSwipingRef`/
 * `isRevealed`/`reset`, so the bug is reproduced here at the hook level to
 * cover both consumers with a single test.
 *
 * `useSwipeToAction` destructures `useState`/`useEffect`/`useRef`/
 * `useCallback` from `window.preact` at module-load time (same limitation as
 * every other hook test in this directory). Rather than the usual
 * capture-only stub, this test needs REAL re-render semantics: Preact
 * rebinds the DOM `onClick` handler to a fresh closure whenever state
 * changes, and that rebinding — happening synchronously inside the
 * `mouseup` handler, before the trailing `click` arrives — is the crux of
 * the bug. So `useState`/`useEffect` below are backed by a minimal but real
 * hook engine: `useState` persists a value per call-site index and
 * synchronously re-invokes the whole hook body on every update (mirroring
 * Preact's synchronous re-render + DOM rebind), `useRef` persists a stable
 * mutable object per call-site index, and `useEffect` re-runs its body
 * (with cleanup) whenever its deps array changed since the last render —
 * exactly like a real effect, just flushed synchronously instead of
 * scheduled. `useCallback` is a pass-through (always returns a fresh
 * closure), which is what forces the mousemove/mouseup effect to always
 * rebind against the latest state — again matching real behavior here since
 * none of this hook's callbacks are referentially stable across state
 * changes anyway.
 *
 * The test then wires a minimal consumer — mirroring `SessionItem.js` /
 * `BeadsView.js`'s `handleClick` exactly — onto a real DOM element, and
 * drives a real mousedown → mousemove(s) → mouseup → click sequence via
 * `dispatchEvent`, so the actual `document`-level listeners the hook
 * installs (via its real `useEffect`) fire natively, same as a browser.
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

global.window = global.window || {};

// --- Minimal-but-real hook engine (see file header) -------------------

let stateValues;
let stateIdx;
let refValues;
let refIdx;
let effectEntries;
let effectIdx;
let rerender; // set by the harness; re-invokes the hook + rebinds the DOM

function depsEqual(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
    return false;
  }
  return a.every((v, i) => Object.is(v, b[i]));
}

window.preact = {
  useState: (initial) => {
    const idx = stateIdx++;
    if (stateValues.length <= idx) {
      stateValues[idx] = typeof initial === "function" ? initial() : initial;
    }
    const setState = (next) => {
      const prev = stateValues[idx];
      const value = typeof next === "function" ? next(prev) : next;
      if (!Object.is(prev, value)) {
        stateValues[idx] = value;
        rerender(); // synchronous re-render, rebinding onclick immediately
      }
    };
    return [stateValues[idx], setState];
  },
  useRef: (initial) => {
    const idx = refIdx++;
    if (refValues.length <= idx) refValues[idx] = { current: initial };
    return refValues[idx];
  },
  useCallback: (fn) => fn,
  useEffect: (cb, deps) => {
    const idx = effectIdx++;
    const prev = effectEntries[idx];
    if (!prev || !depsEqual(prev.deps, deps)) {
      if (prev && typeof prev.cleanup === "function") prev.cleanup();
      effectEntries[idx] = { deps, cleanup: cb() };
    }
  },
};

async function loadHook() {
  const mod = await import("./useSwipeToDelete.js");
  return mod.useSwipeToAction;
}

// Mounts the hook plus a SessionItem/BeadsView-style consumer on a real DOM
// element, re-rendering (and rebinding the element's onclick/onmousedown to
// fresh closures, exactly as Preact would) on every state change.
async function mountSwipeHarness(hookOptions, { onSelect }) {
  const useSwipeToAction = await loadHook();

  stateValues = [];
  stateIdx = 0;
  refValues = [];
  refIdx = 0;
  effectEntries = [];
  effectIdx = 0;

  const el = document.createElement("div");
  document.body.appendChild(el);

  let latest;

  function render() {
    stateIdx = 0;
    refIdx = 0;
    effectIdx = 0;
    latest = useSwipeToAction(hookOptions);
    // No real Preact renderer runs here to wire `ref` callbacks/objects onto
    // the DOM element, so do it ourselves — `calculateOffset` reads
    // `containerRef.current` (via `containerProps.ref`) on every drag move.
    latest.containerProps.ref.current = el;

    el.onmousedown = latest.containerProps.onMouseDown;
    el.ontouchstart = latest.containerProps.onTouchStart;
    // Mirrors SessionItem.js:424-431 / BeadsView.js:927-934 exactly.
    el.onclick = () => {
      if (latest.isSwipingRef.current) return;
      if (latest.isRevealed) {
        latest.reset();
        return;
      }
      onSelect();
    };
  }

  rerender = render;
  render();

  return {
    el,
    getState: () => latest,
  };
}

describe("useSwipeToAction — mouse-driven trailing click (mitto-mzvc)", () => {
  test("reveal survives a trailing click on the SAME element after mouseup", async () => {
    const onSelect = jest.fn();
    const onAction = jest.fn();
    const { el, getState } = await mountSwipeHarness(
      { onAction, threshold: 0.5, revealWidth: 80, disabled: false },
      { onSelect },
    );

    const startX = 300;
    const y = 50;

    el.dispatchEvent(
      new MouseEvent("mousedown", {
        bubbles: true,
        clientX: startX,
        clientY: y,
        button: 0,
      }),
    );
    for (const dx of [20, 60, 100, 140]) {
      document.dispatchEvent(
        new MouseEvent("mousemove", {
          bubbles: true,
          clientX: startX - dx,
          clientY: y,
        }),
      );
    }
    document.dispatchEvent(
      new MouseEvent("mouseup", {
        bubbles: true,
        clientX: startX - 140,
        clientY: y,
      }),
    );

    // The gesture must have revealed the affordance before the trailing
    // click arrives — otherwise the test below would pass vacuously.
    expect(getState().isRevealed).toBe(true);
    expect(getState().swipeOffset).toBe(-80);

    // Preact flushes its re-render (rebinding onclick) in the microtask
    // checkpoint between the mouseup and click dispatches; the harness
    // above rebinds synchronously, so this tick is documentation of the
    // real-world race rather than a requirement of the harness.
    await Promise.resolve();

    // The browser's synthetic trailing click on the same element.
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    // BUG (mitto-mzvc): the click's guard `isSwipingRef.current` is already
    // false and `isRevealed` is already true, so handleClick falls through
    // to `reset()` and the just-revealed affordance collapses immediately.
    expect(getState().isRevealed).toBe(true);
    expect(getState().swipeOffset).toBe(-80);
    expect(onSelect).not.toHaveBeenCalled();
    expect(onAction).not.toHaveBeenCalled();
  });

  test("control: a genuine tap (no movement) selects and never reveals", async () => {
    // Proves the harness itself is faithful — without this, the test above
    // could be passing (or failing) for the wrong reason.
    const onSelect = jest.fn();
    const { el, getState } = await mountSwipeHarness(
      { threshold: 0.5, revealWidth: 80, disabled: false },
      { onSelect },
    );

    el.dispatchEvent(
      new MouseEvent("mousedown", {
        bubbles: true,
        clientX: 300,
        clientY: 50,
        button: 0,
      }),
    );
    document.dispatchEvent(
      new MouseEvent("mouseup", { bubbles: true, clientX: 300, clientY: 50 }),
    );
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(getState().isRevealed).toBe(false);
    expect(onSelect).toHaveBeenCalledTimes(1);
  });
});
