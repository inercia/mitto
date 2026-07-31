/**
 * Unit tests for the MessageEnter wrapper component (mitto-e5k).
 *
 * MessageEnter lives inside Message.js which imports Preact globals via
 * `window.preact` — unavailable in the Jest jsdom environment. Following the
 * same "duplicate the pure logic" pattern used by Message.test.js, this file
 * mirrors the wrapper's finalClass derivation and useEffect body so both can
 * be exercised directly against jsdom's real DOM.
 *
 * Acceptance-criteria mapping (bead mitto-e5k):
 *  - After fadeIn animationend, `.message-enter` class is removed → element
 *    drops out of the compositor's finished-but-not-retired animation set,
 *    so document.getAnimations().length is O(1) not O(N).
 *  - The initial fadeIn still fires visually (class is present at mount
 *    time; only stripped afterwards).
 *  - No listener/timeout leaks across rapid remounts.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";

// ---- finalClass derivation ---------------------------------------------------
// Mirror of the className-merging logic at the top of MessageEnter.
function buildFinalClass(props) {
  const className = props.class || props.className || "";
  return className ? `message-enter ${className}` : "message-enter";
}

describe("MessageEnter finalClass derivation", () => {
  test("returns 'message-enter' alone when no class prop given", () => {
    expect(buildFinalClass({})).toBe("message-enter");
  });

  test("prepends 'message-enter' before caller's `class`", () => {
    expect(buildFinalClass({ class: "flex justify-end mb-3" })).toBe(
      "message-enter flex justify-end mb-3",
    );
  });

  test("accepts React-style `className` prop as fallback", () => {
    expect(buildFinalClass({ className: "flex justify-start" })).toBe(
      "message-enter flex justify-start",
    );
  });

  test("prefers `class` over `className` when both are supplied", () => {
    expect(buildFinalClass({ class: "a", className: "b" })).toBe(
      "message-enter a",
    );
  });

  test("empty string class is treated as 'no class'", () => {
    expect(buildFinalClass({ class: "" })).toBe("message-enter");
  });
});

// ---- useEffect body ----------------------------------------------------------
// Mirror of the effect body attached to the wrapper div by MessageEnter.
// Takes the target element directly so it can be tested against any HTMLElement.
function installMessageEnterEffect(el) {
  let done = false;
  const strip = () => {
    if (done) return;
    done = true;
    el.classList.remove("message-enter");
  };
  const onEnd = (e) => {
    if (e.animationName === "fadeIn") strip();
  };
  el.addEventListener("animationend", onEnd);
  const timeoutId = setTimeout(strip, 250);
  return () => {
    el.removeEventListener("animationend", onEnd);
    clearTimeout(timeoutId);
  };
}

function makeEl(extra = "flex justify-start mb-3") {
  const el = document.createElement("div");
  el.className = extra ? `message-enter ${extra}` : "message-enter";
  document.body.appendChild(el);
  return el;
}

// AnimationEvent may not be constructible in every jsdom version; fall back
// to a plain Event with animationName glued on.
function fireAnimationEnd(el, animationName) {
  let evt;
  try {
    evt = new AnimationEvent("animationend", { animationName });
  } catch (_) {
    evt = new Event("animationend");
    Object.defineProperty(evt, "animationName", {
      value: animationName,
      configurable: true,
    });
  }
  el.dispatchEvent(evt);
}

describe("MessageEnter effect — animationend cleanup", () => {
  let el;
  beforeEach(() => {
    jest.useFakeTimers();
    el = makeEl();
  });
  afterEach(() => {
    jest.useRealTimers();
    if (el && el.parentNode) el.parentNode.removeChild(el);
    el = null;
  });

  test("removes .message-enter when fadeIn animationend fires", () => {
    installMessageEnterEffect(el);
    expect(el.classList.contains("message-enter")).toBe(true);
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(false);
    // sibling classes preserved
    expect(el.classList.contains("flex")).toBe(true);
    expect(el.classList.contains("justify-start")).toBe(true);
  });

  test("ignores animationend events for other animations", () => {
    installMessageEnterEffect(el);
    fireAnimationEnd(el, "slideIn");
    expect(el.classList.contains("message-enter")).toBe(true);
    fireAnimationEnd(el, "spin");
    expect(el.classList.contains("message-enter")).toBe(true);
    // still retirable via the real event
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(false);
  });

  test("250 ms safety-net strips the class when animationend never fires", () => {
    installMessageEnterEffect(el);
    expect(el.classList.contains("message-enter")).toBe(true);
    jest.advanceTimersByTime(249);
    expect(el.classList.contains("message-enter")).toBe(true);
    jest.advanceTimersByTime(1);
    expect(el.classList.contains("message-enter")).toBe(false);
  });
});


describe("MessageEnter effect — idempotency & cleanup", () => {
  let el;
  beforeEach(() => {
    jest.useFakeTimers();
    el = makeEl();
  });
  afterEach(() => {
    jest.useRealTimers();
    if (el && el.parentNode) el.parentNode.removeChild(el);
    el = null;
  });

  test("safety-net timeout after animationend is a no-op (idempotent)", () => {
    installMessageEnterEffect(el);
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(false);
    el.classList.add("marker");
    jest.advanceTimersByTime(300);
    expect(el.classList.contains("marker")).toBe(true);
    // Re-adding the class after the strip must NOT be undone by the
    // safety-net timer (which already ran once with `done=true`).
    el.classList.add("message-enter");
    jest.advanceTimersByTime(300);
    expect(el.classList.contains("message-enter")).toBe(true);
  });

  test("multiple fadeIn animationend events strip only once (idempotent)", () => {
    installMessageEnterEffect(el);
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(false);
    // Simulate a spurious second event — must not act
    el.classList.add("message-enter");
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(true);
  });

  test("cleanup detaches animationend listener before it can fire", () => {
    const cleanup = installMessageEnterEffect(el);
    cleanup();
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(true);
  });

  test("cleanup cancels the safety-net timeout", () => {
    const cleanup = installMessageEnterEffect(el);
    cleanup();
    jest.advanceTimersByTime(500);
    expect(el.classList.contains("message-enter")).toBe(true);
  });

  test("rapid remount does not leak listeners across cycles", () => {
    const cleanup1 = installMessageEnterEffect(el);
    cleanup1();
    const cleanup2 = installMessageEnterEffect(el);
    fireAnimationEnd(el, "fadeIn");
    expect(el.classList.contains("message-enter")).toBe(false);
    expect(() => cleanup2()).not.toThrow();
  });
});
