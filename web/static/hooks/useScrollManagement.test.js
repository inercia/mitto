/**
 * Regression test for mitto-u5r — "Streaming chunks scroll the view up while
 * user is scrolled up reading".
 *
 * Corrected root cause: the scroller (.messages-container-reverse) is a normal
 * top-anchored block container (scrollTop=0 is the visual top); flex-col-reverse
 * only reorders the inner wrapper's children. When the newest (streaming)
 * message grows at the visual bottom, the document extends DOWNWARD below the
 * reading viewport, so a scrolled-up reader's scrollTop stays naturally
 * unchanged in both WebKit and Chromium (verified with a standalone layout
 * fixture, native DRIFT=0px). The earlier fix added `scrollTop += delta` here
 * to "preserve" the reading position; that offset was itself the cause of the
 * per-chunk upward drift (the same fixture showed +528px drift with it), so it
 * was removed.
 *
 * Contract pinned here: while streaming and the user is NOT at the bottom, a
 * scrollHeight increase (bottom growth) must NOT move scrollTop, and the user
 * must remain considered scrolled up (not snapped to the bottom).
 *
 * Uses the real vendored Preact runtime (mirrors
 * CallbackTriggerSection.test.js) with a manually-mocked scroll container
 * (happy-dom does not implement real layout/scroll metrics), since
 * useScrollManagement.js destructures its hooks from window.preact at
 * module-load time.
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  test,
} from "../utils/testing/testGlobals.js";
import { h, render as preactRender } from "../vendor/preact.js";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "../vendor/preact-hooks.js";

const previousPreact = window.preact;
window.preact = {
  useState,
  useRef,
  useEffect,
  useLayoutEffect,
  useCallback,
};
const { useScrollManagement } = await import(
  "./useScrollManagement.js?repro-mitto-u5r"
);
window.preact = previousPreact;

async function flush() {
  // Passive (useEffect) commits and the hook's own requestAnimationFrame
  // initial-scroll-check are scheduled asynchronously by Preact's hooks
  // implementation; a couple of macrotask ticks lets both settle.
  await new Promise((r) => setTimeout(r, 20));
  await new Promise((r) => setTimeout(r, 20));
}

function makeMockContainer({ scrollHeight, clientHeight, scrollTop }) {
  const el = document.createElement("div");
  let _scrollHeight = scrollHeight;
  let _scrollTop = scrollTop;
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: () => _scrollHeight,
  });
  Object.defineProperty(el, "clientHeight", {
    configurable: true,
    get: () => clientHeight,
  });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => _scrollTop,
    set: (v) => {
      _scrollTop = v;
    },
  });
  return {
    el,
    setScrollHeight: (v) => {
      _scrollHeight = v;
    },
    setScrollTop: (v) => {
      _scrollTop = v;
    },
  };
}

function Harness({ resultBox, ...hookProps }) {
  resultBox.current = useScrollManagement(hookProps);
  return null;
}

let mountPoint;

beforeEach(() => {
  mountPoint = document.createElement("div");
  document.body.appendChild(mountPoint);
});

afterEach(() => {
  preactRender(null, mountPoint);
  mountPoint.remove();
});

describe("useScrollManagement — mitto-u5r streaming scroll drift", () => {
  test("does not move scrollTop when content grows during streaming while scrolled up", async () => {
    const mock = makeMockContainer({
      scrollHeight: 2000,
      clientHeight: 500,
      scrollTop: 1500, // at bottom: maxScroll=1500, threshold=50
    });
    const messagesContainerRef = { current: mock.el };
    const scrollPreservationRef = { current: null };
    const resultBox = { current: null };

    const baseProps = {
      resultBox,
      activeSessionId: "session-1",
      mainView: "conversation",
      isLoadingMore: false,
      messagesContainerRef,
      scrollPreservationRef,
    };

    // Initial mount: 3 messages, not streaming, positioned at the bottom.
    let messages = [{ id: 1 }, { id: 2 }, { id: 3 }];
    preactRender(
      h(Harness, { ...baseProps, messages, isStreaming: false }),
      mountPoint,
    );
    await flush();
    expect(resultBox.current.isUserAtBottom).toBe(true);

    // User scrolls up to read earlier messages.
    mock.setScrollTop(200); // maxScroll=1500, threshold=50 -> not at bottom
    mock.el.dispatchEvent(new Event("scroll"));
    await flush();
    expect(resultBox.current.isUserAtBottom).toBe(false);

    // A streaming chunk arrives: the streamed message grows at the visual
    // bottom, increasing scrollHeight by 300px. Because the scroller is
    // top-anchored, the growth extends the document below the reading
    // viewport and must NOT move the reader's scrollTop.
    mock.setScrollHeight(2300);
    messages = [...messages]; // new reference, same length (in-place chunk growth)
    preactRender(
      h(Harness, { ...baseProps, messages, isStreaming: true }),
      mountPoint,
    );
    await flush();

    // Corrected (fixed) behavior: scrollTop is left exactly where the user
    // scrolled to. The hook adds no compensation for bottom growth; the
    // top-anchored layout keeps the reading position stable on its own.
    expect(mock.el.scrollTop).toBe(200);
    // The user should still be considered scrolled up, not snapped to bottom.
    expect(resultBox.current.isUserAtBottom).toBe(false);
  });
});
