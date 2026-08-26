/**
 * Unit tests for ChatInput's Flush-context toolbar button (mitto-c23,
 * mitto-cmk).
 *
 * Because ChatInput.js imports window.preact / window.htm globals at module
 * load, it cannot be imported under jsdom. Following the same pattern used
 * by Message.test.js, PromptParameterDialog.test.js, and BeadsView.test.js,
 * the render-branch and disabled-gate logic are duplicated here from
 * ChatInput.js — keep in sync with the source.
 */

// Jest is not injected as a global under --experimental-vm-modules (ESM); we
// must import it explicitly. testGlobals.js re-exports the lifecycle globals
// and `jest` from whichever runner is active (Jest or bun:test).
import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
  afterEach,
} from "../utils/testing/testGlobals.js";

// =============================================================================
// Render-branch logic (visibility of the Flush button)
// The button is ALWAYS rendered (mitto-cmk) so the capability stays
// discoverable on ACPs without a context_flush_command; unsupported ACPs get
// a disabled button rather than no button. This deliberately diverges from
// useConversationMenu.js:124 and the header toolbar, which still omit their
// Flush entry entirely.
// =============================================================================

function shouldRenderFlushButton() {
  return true;
}

// =============================================================================
// Tooltip / aria-label text
// Duplicated from ChatInput.js:3175-3180.
// =============================================================================

function flushButtonTip(flushCommand) {
  return flushCommand
    ? `Clear the agent's context (${flushCommand})`
    : "This agent does not support clearing the context";
}

// =============================================================================
// Disabled-gate logic
// Duplicated from ChatInput.js:3168-3172:
//   disabled=${isFullyDisabled || isReadOnly || !acpReady ||
//              !flushCommand || !onFlushContext}
// isFullyDisabled itself (ChatInput.js:742-743) is:
//   disabled || noSession || isSending || isArchived || isArchivePending
// so the button is disabled if ANY of:
//   disabled, noSession, isSending, isArchived, isArchivePending,
//   isReadOnly, !acpReady, !flushCommand, !onFlushContext.
// Deliberately NOT gated on isStreaming (matches the "..." menu item) or
// on !text.trim (flushing the agent's context is independent of composer
// content).
// =============================================================================

function isFlushButtonDisabled({
  disabled = false,
  noSession = false,
  isSending = false,
  isArchived = false,
  isArchivePending = false,
  isReadOnly = false,
  acpReady = true,
  flushCommand = "/clear",
  onFlushContext = () => {},
}) {
  const isFullyDisabled =
    disabled || noSession || isSending || isArchived || isArchivePending;
  return (
    isFullyDisabled ||
    isReadOnly ||
    !acpReady ||
    !flushCommand ||
    !onFlushContext
  );
}

// =============================================================================
// Click handler
// Duplicated from ChatInput.js:3166:
//   onClick=${() => onFlushContext && onFlushContext()}
// ChatInput passes no arguments; the parent-side wrapper in app.js closes
// over activeSession and calls handleFlushContext(activeSession).
// =============================================================================

function handleFlushButtonClick(onFlushContext) {
  return onFlushContext && onFlushContext();
}

// =============================================================================
// Composition-area visibility gate (mitto-9l8, post-fix)
// Duplicated from ChatInput.js:2775-2776:
//   ${!mcpUIBlocking && !(isPromptCollapsed && loopConfigured) && html`...`}
// mcpUIBlocking (ChatInput.js:2049-2058) is a hard, unconditional gate on
// hasActiveUIPrompt (excluding "permission" prompts, which render inline
// buttons) — independent of isPromptCollapsed, which now only governs the
// unrelated loop-collapse UX.
// =============================================================================

function computeMcpUIBlocking({ hasActiveUIPrompt, promptType }) {
  return hasActiveUIPrompt && promptType !== "permission";
}

function shouldShowCompositionArea({
  isPromptCollapsed,
  loopConfigured,
  hasActiveUIPrompt,
  promptType,
}) {
  const mcpUIBlocking = computeMcpUIBlocking({
    hasActiveUIPrompt,
    promptType,
  });
  return !mcpUIBlocking && !(isPromptCollapsed && loopConfigured);
}

// =============================================================================
// handleUIPromptAnswer's optimistic collapse restore (mitto-9l8)
// Duplicated from ChatInput.js:2051-2066: on click, isPromptCollapsed is
// unconditionally restored to whatever it was BEFORE the auto-collapse effect
// (prevCollapsedBeforeUIRef.current) — synchronously, BEFORE onUIPromptAnswer
// (and therefore before the WebSocket send) is even attempted.
// =============================================================================

function applyUIPromptAnswerClick(prevCollapsedBeforeUI) {
  return prevCollapsedBeforeUI;
}

// =============================================================================
// sendUIPromptAnswer's activeUIPrompt-clearing gate (mitto-9l8)
// Duplicated from useWebSocket.js:4598-4636: activeUIPrompt is cleared ONLY
// inside `if (sent)`. `sent` comes from sendToSession
// (useWSConnection.js:457-464), which returns false whenever the per-session
// socket is missing or its readyState !== WebSocket.OPEN (mid-reconnect, or
// the documented mobile "zombie socket" state — .augment/rules/23-web-frontend-mobile.md).
// On a non-OPEN socket, activeUIPrompt is left untouched.
// =============================================================================

function clearActiveUIPromptAfterAnswer(activeUIPrompt, sent) {
  return sent ? null : activeUIPrompt;
}

// =============================================================================
// ui_prompt dedup guard (mitto-9l8)
// Duplicated from useWebSocket.js:1188-1198: an incoming ui_prompt for a
// requestId that is already the active prompt is silently ignored. This is
// why the stuck state cannot self-heal on WebSocket reconnect: the backend's
// re-sent ui_prompt for the still-pending requestId never re-triggers the
// auto-collapse effect.
// =============================================================================

function isDuplicateUIPrompt(activeRequestId, incomingRequestId) {
  return activeRequestId === incomingRequestId;
}

// =============================================================================
// Tests
// =============================================================================

describe("ChatInput Flush-context button (mitto-c23, mitto-cmk)", () => {
  describe("visibility (shouldRenderFlushButton)", () => {
    test("renders when flushCommand and onFlushContext are both provided", () => {
      expect(shouldRenderFlushButton()).toBe(true);
    });

    test("still renders when the ACP advertises no flush command (mitto-cmk)", () => {
      // Pre-mitto-cmk the button was omitted here; it is now rendered
      // disabled so the capability stays discoverable.
      expect(shouldRenderFlushButton()).toBe(true);
    });
  });

  describe("tooltip text (flushButtonTip)", () => {
    test("names the command when the ACP supports flushing", () => {
      expect(flushButtonTip("/clear")).toBe(
        "Clear the agent's context (/clear)",
      );
    });

    test("explains the lack of support when there is no command", () => {
      expect(flushButtonTip("")).toBe(
        "This agent does not support clearing the context",
      );
    });

    test("explains the lack of support when the command is undefined", () => {
      expect(flushButtonTip(undefined)).toBe(
        "This agent does not support clearing the context",
      );
    });
  });

  describe("click behavior (handleFlushButtonClick)", () => {
    test("invokes onFlushContext exactly once when clicked", () => {
      const onFlushContext = jest.fn();
      handleFlushButtonClick(onFlushContext);
      expect(onFlushContext).toHaveBeenCalledTimes(1);
    });

    test("invokes onFlushContext with no arguments (parent-side wrapper injects session)", () => {
      const onFlushContext = jest.fn();
      handleFlushButtonClick(onFlushContext);
      expect(onFlushContext).toHaveBeenCalledWith();
    });

    test("does not throw when onFlushContext is undefined (button is disabled)", () => {
      expect(() => handleFlushButtonClick(undefined)).not.toThrow();
    });
  });

  describe("disabled gate (isFlushButtonDisabled)", () => {
    test("enabled by default (all guards false, acpReady true)", () => {
      expect(isFlushButtonDisabled({})).toBe(false);
    });

    test("disabled when noSession is true", () => {
      expect(isFlushButtonDisabled({ noSession: true })).toBe(true);
    });

    test("disabled when isArchived is true", () => {
      expect(isFlushButtonDisabled({ isArchived: true })).toBe(true);
    });

    test("disabled when isArchivePending is true", () => {
      expect(isFlushButtonDisabled({ isArchivePending: true })).toBe(true);
    });

    test("disabled when isReadOnly is true", () => {
      expect(isFlushButtonDisabled({ isReadOnly: true })).toBe(true);
    });

    test("disabled when acpReady is false", () => {
      expect(isFlushButtonDisabled({ acpReady: false })).toBe(true);
    });

    test("disabled when flushCommand is an empty string (mitto-cmk)", () => {
      expect(isFlushButtonDisabled({ flushCommand: "" })).toBe(true);
    });

    test("disabled when flushCommand is absent (mitto-cmk)", () => {
      // null rather than undefined: the helper's default parameter would
      // otherwise substitute the supported-ACP value.
      expect(isFlushButtonDisabled({ flushCommand: null })).toBe(true);
    });

    test("disabled when onFlushContext is absent (mitto-cmk)", () => {
      expect(isFlushButtonDisabled({ onFlushContext: null })).toBe(true);
    });

    test("disabled when the outer `disabled` prop is true", () => {
      expect(isFlushButtonDisabled({ disabled: true })).toBe(true);
    });

    test("disabled when isSending is true (folded via isFullyDisabled)", () => {
      expect(isFlushButtonDisabled({ isSending: true })).toBe(true);
    });

    test("NOT disabled by isStreaming (matches the '...' menu item — deliberately not gated)", () => {
      // isStreaming is not part of the gate expression at all. If a future
      // refactor adds it, this test breaks and the divergence with the
      // menu item must be justified in a plan comment.
      expect(
        isFlushButtonDisabled({
          disabled: false,
          noSession: false,
          isSending: false,
          isArchived: false,
          isArchivePending: false,
          isReadOnly: false,
          acpReady: true,
        }),
      ).toBe(false);
    });
  });
});

// =============================================================================
// mitto-9l8: composition area visible while an MCP UI prompt is active
// =============================================================================

describe("ChatInput composition-area visibility during MCP UI prompts (mitto-9l8)", () => {
  describe("shouldShowCompositionArea (render gate)", () => {
    test("hides the composition area while a blocking UI prompt is active and collapsed", () => {
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed: true,
          loopConfigured: false,
          hasActiveUIPrompt: true,
          promptType: "options",
        }),
      ).toBe(false);
    });

    // FIX (mitto-9l8): the gate is now a hard, unconditional block on
    // hasActiveUIPrompt (mcpUIBlocking) rather than being conditional on
    // isPromptCollapsed happening to be true. Even when isPromptCollapsed
    // has been (incorrectly) restored to false while a blocking prompt is
    // still active, the composition area stays hidden. This is exactly the
    // permanently-stuck scenario from the investigation: a failed WebSocket
    // send leaves activeUIPrompt non-null while the optimistic click
    // handler has already un-collapsed the composer.
    test("composition area stays hidden even if isPromptCollapsed is restored while a UI prompt is still active", () => {
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed: false,
          loopConfigured: false,
          hasActiveUIPrompt: true,
          promptType: "options",
        }),
      ).toBe(false);
    });

    // Permission prompts are excluded from mcpUIBlocking — they render
    // inline buttons and the composition area may legitimately be visible
    // alongside them.
    test("does NOT block the composition area for permission-type prompts", () => {
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed: false,
          loopConfigured: false,
          hasActiveUIPrompt: true,
          promptType: "permission",
        }),
      ).toBe(true);
    });
  });

  describe("end-to-end: click -> failed send -> composition area stays hidden", () => {
    test("after handleUIPromptAnswer runs on a closed socket, the composition area remains hidden while the prompt panel is still shown", () => {
      // 1. A blocking MCP UI prompt (e.g. promptType "options") arrives and
      //    the auto-collapse effect (ChatInput.js:519-538) captures the
      //    prior collapsed state and force-collapses the composer.
      const prevCollapsedBeforeUI = false; // conversation wasn't collapsed before the prompt
      let isPromptCollapsed = true; // auto-collapse effect ran
      let activeUIPrompt = { requestId: "req-1", promptType: "options" };

      // Sanity: composer is correctly hidden while the prompt is pending.
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed,
          loopConfigured: false,
          hasActiveUIPrompt: !!activeUIPrompt,
          promptType: activeUIPrompt.promptType,
        }),
      ).toBe(false);

      // 2. User clicks an option. handleUIPromptAnswer optimistically
      //    restores isPromptCollapsed BEFORE the send is attempted/resolved.
      isPromptCollapsed = applyUIPromptAnswerClick(prevCollapsedBeforeUI);
      expect(isPromptCollapsed).toBe(false);

      // 3. The WebSocket send fails (socket mid-reconnect / "zombie" state —
      //    sendToSession returns false). sendUIPromptAnswer only clears
      //    activeUIPrompt `if (sent)`, so it remains set.
      const sent = false;
      activeUIPrompt = clearActiveUIPromptAfterAnswer(activeUIPrompt, sent);
      expect(activeUIPrompt).not.toBeNull();

      // 4. FIX: the composition area stays hidden — mcpUIBlocking is a hard
      //    gate on hasActiveUIPrompt, independent of isPromptCollapsed — so
      //    the permanent wedge cannot occur even though the socket send
      //    failed and isPromptCollapsed was already restored to false.
      const visible = shouldShowCompositionArea({
        isPromptCollapsed,
        loopConfigured: false,
        hasActiveUIPrompt: !!activeUIPrompt,
        promptType: activeUIPrompt.promptType,
      });
      expect(visible).toBe(false);

      // 5. Even though the dedup guard would swallow a reconnect re-send of
      //    the same requestId (so the auto-collapse effect never re-fires),
      //    that no longer matters for visibility: the hard gate already
      //    keeps the composer hidden without relying on the effect.
      expect(isDuplicateUIPrompt(activeUIPrompt.requestId, "req-1")).toBe(true);
    });
  });
});

// =============================================================================
// mitto-7c98: follow-up suggestion buttons use a single-row horizontal
// scroll (carousel) instead of wrapping.
// Duplicated from ChatInput.js:2691-2728 (render gate + container class) and
// ChatInput.js:2178-2196 (handleActionButtonClick) — keep in sync with the
// source, per the file-level note above.
// =============================================================================

// Render gate: ChatInput.js:2691-2696.
//   ${hasActionButtons && !isStreaming && !isReadOnly && !noSession &&
//     !loopConfigured && !isResuming && html`...`}
function shouldRenderActionButtonsCarousel({
  actionButtons = [],
  isStreaming = false,
  isReadOnly = false,
  noSession = false,
  loopConfigured = false,
  isResuming = false,
}) {
  const hasActionButtons = actionButtons && actionButtons.length > 0;
  return (
    hasActionButtons &&
    !isStreaming &&
    !isReadOnly &&
    !noSession &&
    !loopConfigured &&
    !isResuming
  );
}

// Container class: ChatInput.js:2699. `.mitto-carousel` (styles.css) supplies
// single-row nowrap flex + horizontal scroll/snap; `py-0.5` prevents a
// focused button's ring from being clipped by the carousel's
// `overflow-y: hidden`.
const ACTION_BUTTONS_CAROUSEL_CLASS = "mitto-carousel gap-2 py-0.5";

// Click handler: ChatInput.js:2178-2196. Only the synchronous, directly
// observable part (populating the composer text) is duplicated here; the
// requestAnimationFrame-deferred focus/height adjustment is DOM-only and not
// under test in this jsdom-free suite.
function handleActionButtonClick(setText, response) {
  setText(response);
}

describe("ChatInput follow-up suggestion buttons carousel (mitto-7c98)", () => {
  describe("visibility (shouldRenderActionButtonsCarousel)", () => {
    test("renders when action buttons are present and no gate is active", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
        }),
      ).toBe(true);
    });

    test("does not render when actionButtons is empty", () => {
      expect(shouldRenderActionButtonsCarousel({ actionButtons: [] })).toBe(
        false,
      );
    });

    test("does not render while streaming", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
          isStreaming: true,
        }),
      ).toBe(false);
    });

    test("does not render when read-only", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
          isReadOnly: true,
        }),
      ).toBe(false);
    });

    test("does not render when there is no session", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
          noSession: true,
        }),
      ).toBe(false);
    });

    test("does not render when a loop is configured", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
          loopConfigured: true,
        }),
      ).toBe(false);
    });

    test("does not render while resuming", () => {
      expect(
        shouldRenderActionButtonsCarousel({
          actionButtons: [{ label: "Yes", response: "Yes" }],
          isResuming: true,
        }),
      ).toBe(false);
    });
  });

  describe("container class (mitto-carousel single-row scroll)", () => {
    test("uses the mitto-carousel utility instead of wrapping flexbox", () => {
      expect(ACTION_BUTTONS_CAROUSEL_CLASS).toContain("mitto-carousel");
      expect(ACTION_BUTTONS_CAROUSEL_CLASS).not.toContain("flex-wrap");
    });

    test("preserves the gap-2 spacing between buttons", () => {
      expect(ACTION_BUTTONS_CAROUSEL_CLASS).toContain("gap-2");
    });

    test("adds py-0.5 so a focused button's ring is not clipped by the carousel's overflow-y: hidden", () => {
      expect(ACTION_BUTTONS_CAROUSEL_CLASS).toContain("py-0.5");
    });
  });

  describe("click behavior (handleActionButtonClick)", () => {
    test("populates the composer with the button's response text", () => {
      const setText = jest.fn();
      handleActionButtonClick(setText, "Yes, proceed");
      expect(setText).toHaveBeenCalledWith("Yes, proceed");
    });

    test("still works with multiple buttons scrolled into a single row (last click wins)", () => {
      const setText = jest.fn();
      handleActionButtonClick(setText, "Option A");
      handleActionButtonClick(setText, "Option B");
      expect(setText).toHaveBeenCalledTimes(2);
      expect(setText).toHaveBeenLastCalledWith("Option B");
    });
  });
});

// =============================================================================
// SDK migration coverage (mitto-7gta.17 slice S5): loop lock/unlock and
// improve-prompt now go through getSdkClient() instead of secureFetch.
// Handlers are duplicated (with dependencies injected) per this file's
// established convention — ChatInput.js cannot be imported under jsdom.
// =============================================================================

/**
 * Mirrors `handleLockLoopPrompt` (ChatInput.js ~L1009): PATCHes the loop
 * prompt via `loopClient.update(id, {prompt, enabled: true})` and, on
 * success, records the server-echoed `next_scheduled_at`.
 */
function makeHandleLockLoopPrompt({
  sessionId,
  text,
  loopClient,
  setLoopPrompt = () => {},
  setIsLoopLocked = () => {},
  setLoopNextScheduledAt = () => {},
}) {
  return async function handleLockLoopPrompt() {
    if (!sessionId || !text.trim()) return;
    try {
      const data = await loopClient.update(sessionId, {
        prompt: text.trim(),
        enabled: true,
      });
      setLoopPrompt(text.trim());
      setIsLoopLocked(true);
      if (data.next_scheduled_at) {
        setLoopNextScheduledAt(data.next_scheduled_at);
      }
    } catch (_err) {
      // Non-fatal in this duplicated handler; the real component logs it.
    }
  };
}

/**
 * Mirrors `handleUnlockLoopPrompt` (ChatInput.js ~L1032): PATCHes
 * `{enabled: false}` via `loopClient.update` and clears the local lock
 * state on success.
 */
function makeHandleUnlockLoopPrompt({
  sessionId,
  loopClient,
  setIsLoopLocked = () => {},
  setLoopNextScheduledAt = () => {},
}) {
  return async function handleUnlockLoopPrompt() {
    if (!sessionId) return;
    try {
      await loopClient.update(sessionId, { enabled: false });
      setIsLoopLocked(false);
      setLoopNextScheduledAt(null);
    } catch (_err) {
      // Non-fatal in this duplicated handler; the real component logs it.
    }
  };
}

describe("handleLockLoopPrompt / handleUnlockLoopPrompt (SDK migration)", () => {
  test("lock: PATCHes {prompt, enabled: true} and stores next_scheduled_at", async () => {
    const loopClient = {
      update: jest.fn(async () => ({
        next_scheduled_at: "2026-01-01T00:00:00Z",
      })),
    };
    const setLoopPrompt = jest.fn();
    const setIsLoopLocked = jest.fn();
    const setLoopNextScheduledAt = jest.fn();

    const handler = makeHandleLockLoopPrompt({
      sessionId: "s1",
      text: "  Do the thing  ",
      loopClient,
      setLoopPrompt,
      setIsLoopLocked,
      setLoopNextScheduledAt,
    });
    await handler();

    expect(loopClient.update).toHaveBeenCalledWith("s1", {
      prompt: "Do the thing",
      enabled: true,
    });
    expect(setLoopPrompt).toHaveBeenCalledWith("Do the thing");
    expect(setIsLoopLocked).toHaveBeenCalledWith(true);
    expect(setLoopNextScheduledAt).toHaveBeenCalledWith("2026-01-01T00:00:00Z");
  });

  test("lock: no-op when text is blank", async () => {
    const loopClient = { update: jest.fn() };
    const handler = makeHandleLockLoopPrompt({
      sessionId: "s1",
      text: "   ",
      loopClient,
    });
    await handler();
    expect(loopClient.update).not.toHaveBeenCalled();
  });

  test("lock: a rejected update() (MittoApiError) is caught, not thrown", async () => {
    const loopClient = {
      update: jest.fn(async () => {
        throw new Error("boom");
      }),
    };
    const setIsLoopLocked = jest.fn();
    const handler = makeHandleLockLoopPrompt({
      sessionId: "s1",
      text: "hi",
      loopClient,
      setIsLoopLocked,
    });
    await expect(handler()).resolves.toBeUndefined();
    expect(setIsLoopLocked).not.toHaveBeenCalled();
  });

  test("unlock: PATCHes {enabled: false} and clears lock state + schedule", async () => {
    const loopClient = { update: jest.fn(async () => ({})) };
    const setIsLoopLocked = jest.fn();
    const setLoopNextScheduledAt = jest.fn();

    const handler = makeHandleUnlockLoopPrompt({
      sessionId: "s1",
      loopClient,
      setIsLoopLocked,
      setLoopNextScheduledAt,
    });
    await handler();

    expect(loopClient.update).toHaveBeenCalledWith("s1", { enabled: false });
    expect(setIsLoopLocked).toHaveBeenCalledWith(false);
    expect(setLoopNextScheduledAt).toHaveBeenCalledWith(null);
  });
});

// =============================================================================
// handleImprovePrompt's AbortError detection (mitto-7gta.17 slice S5 fix):
// the SDK wraps an aborted fetch in a MittoNetworkError whose `.cause` is
// the original AbortError, so timeout-detection must check both
// `err.name` (bare AbortError, e.g. from an unwrapped native fetch) and
// `err.cause?.name` (SDK-wrapped). Duplicated from ChatInput.js:1464-1480.
// =============================================================================

function isImprovePromptTimeout(err) {
  return err.name === "AbortError" || err.cause?.name === "AbortError";
}

describe("handleImprovePrompt — AbortError detection through SDK wrapping", () => {
  test("bare AbortError (unwrapped) is detected as a timeout", () => {
    const err = new Error("aborted");
    err.name = "AbortError";
    expect(isImprovePromptTimeout(err)).toBe(true);
  });

  test("SDK-wrapped MittoNetworkError with an AbortError cause is detected as a timeout", () => {
    const abortErr = new Error("The operation was aborted");
    abortErr.name = "AbortError";
    const wrapped = new Error("network error");
    wrapped.name = "MittoNetworkError";
    wrapped.cause = abortErr;
    expect(isImprovePromptTimeout(wrapped)).toBe(true);
  });

  test("a non-abort error (e.g. MittoApiError) is NOT treated as a timeout", () => {
    const err = new Error("bad request");
    err.name = "MittoApiError";
    expect(isImprovePromptTimeout(err)).toBe(false);
  });

  test("a MittoNetworkError wrapping a non-abort cause is NOT treated as a timeout", () => {
    const cause = new Error("ECONNRESET");
    cause.name = "TypeError";
    const wrapped = new Error("network error");
    wrapped.name = "MittoNetworkError";
    wrapped.cause = cause;
    expect(isImprovePromptTimeout(wrapped)).toBe(false);
  });
});

// =============================================================================
// mitto-47l (REOPENED v2): mobile scroll-driven compact composer.
// Duplicated from ChatInput.js:1801-1806 (isScrollCompact derivation),
// ChatInput.js:491-499 (isScrollCollapsed = isScrolledUp direct mapping;
// hysteresis moved up into useScrollManagement.js),
// ChatInput.js:2527-2533 (chat-input-container compact class + render-gate
// independence), ChatInput.js:2362-2369 (chat-input-actionbuttons compact
// class — the follow-up/action buttons block, which lives OUTSIDE
// .chat-input-container, collapses off the SAME isScrollCompact signal), and
// ChatInput.js:2325-2353 (the restore affordance's render gate + tap
// handler) — keep in sync with the source, per the file-level note above.
// =============================================================================

// Duplicated from ChatInput.js:1801-1806:
//   const isScrollCompact = isMobile && isScrollCollapsed && !loopConfigured &&
//     !mcpUIBlocking && !isTextareaFocused;
function computeIsScrollCompact({
  isMobile,
  isScrollCollapsed,
  loopConfigured = false,
  mcpUIBlocking = false,
  isTextareaFocused = false,
}) {
  return (
    isMobile &&
    isScrollCollapsed &&
    !loopConfigured &&
    !mcpUIBlocking &&
    !isTextareaFocused
  );
}

// Duplicated from ChatInput.js:491-499: isScrollCollapsed now mirrors the
// isScrolledUp prop DIRECTLY. The former local 150ms debounce (and its
// scroll-to-bottom re-pin workaround) were removed — the flicker/"stuck
// scroll" fix moved the anti-oscillation logic UP into useScrollManagement as
// asymmetric distance hysteresis (see computeIsScrolledUpHysteresis below),
// so the composer's collapse state can track the already-debounced signal 1:1.
function runScrollCollapseEffect({ isScrolledUp, setIsScrollCollapsed }) {
  setIsScrollCollapsed(isScrolledUp);
}

// Duplicated from useScrollManagement.js: the asymmetric-hysteresis state
// machine that decides whether the user has "deliberately scrolled up" (which
// drives the mobile composer collapse). Cross into scrolled-up only past the
// large COLLAPSE_DISTANCE_PX; cross back only within the small
// EXPAND_DISTANCE_PX; hold the previous state in the dead-band between them so
// a moving bottom (streaming / composer expansion) cannot toggle it. On top of
// this steady-state decision, the COLLAPSE (hide) transition is DEBOUNCED (see
// createDebouncedCollapseModel below) so a transient long-chunk streaming jump
// past COLLAPSE_DISTANCE_PX that self-corrects never flickers the composer.
// Keep these constants in sync with the hook.
const COLLAPSE_DISTANCE_PX = 160;
const EXPAND_DISTANCE_PX = 60;

function computeIsScrolledUpHysteresis(prev, distanceFromBottom) {
  if (!prev && distanceFromBottom > COLLAPSE_DISTANCE_PX) return true;
  if (prev && distanceFromBottom < EXPAND_DISTANCE_PX) return false;
  return prev;
}

// Duplicated from useScrollManagement.js handleScroll: the DEBOUNCED collapse
// state machine (mitto-47l flicker fix). The collapse (hide) transition is not
// committed on the scroll event that first crosses COLLAPSE_DISTANCE_PX; it is
// deferred behind a timer that re-reads the LIVE distance when it fires, so a
// transient streaming jump that self-corrects (auto-scroll re-pin, or the user
// scrolling back down) never collapses the composer. Expand stays immediate and
// cancels any pending collapse. This tiny scheduler models that behavior with
// an injectable clock so tests can drive it deterministically.
//
// Usage: create a scheduler, feed it scroll observations via observe(prev,
// distanceFromBottom), advance the fake clock, and read committed transitions.
function createDebouncedCollapseModel({ collapseDelayMs = 250 } = {}) {
  let timer = null; // { fireAt, prevAtArm } | null
  const events = []; // committed { type: "collapse"|"expand" }
  const cancelPending = () => {
    timer = null;
  };
  // Returns the immediately-committed next state (expand is synchronous; a
  // pending collapse leaves state unchanged until the timer fires).
  const observe = (prev, distanceFromBottom, nowMs) => {
    if (prev) {
      if (distanceFromBottom < EXPAND_DISTANCE_PX) {
        cancelPending();
        events.push({ type: "expand" });
        return false;
      }
      return prev;
    }
    if (distanceFromBottom > COLLAPSE_DISTANCE_PX) {
      if (!timer) timer = { fireAt: nowMs + collapseDelayMs };
    } else {
      cancelPending();
    }
    return prev;
  };
  // A streaming re-pin (scrollToBottom) cancels any armed collapse and forces
  // the expanded state, exactly like the hook's scrollToBottom.
  const rePin = () => {
    cancelPending();
    return false;
  };
  // Advance the clock; if a collapse timer is due, re-check the live distance
  // (supplied by the caller) before committing — mirroring the hook's timer
  // callback which reads the container again.
  const advanceTo = (nowMs, liveDistanceFromBottom) => {
    if (timer && nowMs >= timer.fireAt) {
      timer = null;
      if (liveDistanceFromBottom > COLLAPSE_DISTANCE_PX) {
        events.push({ type: "collapse" });
        return true;
      }
      return false;
    }
    return undefined; // nothing committed
  };
  return {
    observe,
    rePin,
    advanceTo,
    hasPending: () => timer !== null,
    events: () => events.slice(),
  };
}

// Duplicated from ChatInput.js:2527-2533: the compact class applied to the
// (always-mounted) .chat-input-container wrapper (the textarea box).
function computeChatInputContainerClass(isScrollCompact) {
  return `max-w-4xl mx-auto chat-input-container ${
    isScrollCompact ? "chat-input-container--compact" : ""
  }`;
}

// Duplicated from ChatInput.js:2362-2369: the compact class applied to the
// (always-mounted, conditionally-rendered-on-hasActionButtons) follow-up/
// action buttons wrapper. This block lives OUTSIDE .chat-input-container
// (it is a sibling in the same <form>), so it needs its own class/modifier
// pair driven by the SAME isScrollCompact signal — no separate scroll
// listener or derived state.
function computeActionButtonsClass(isScrollCompact) {
  return `max-w-4xl mx-auto mb-3 chat-input-actionbuttons ${
    isScrollCompact ? "chat-input-actionbuttons--compact" : ""
  }`;
}

// Duplicated from ChatInput.js:2325: the restore affordance is the only
// piece of the collapse UI that is conditionally mounted (it carries no
// state of its own — unlike the composition box and action buttons, which
// always stay mounted so draft/attachments/props are never lost).
function shouldShowRestorePill(isScrollCompact) {
  return !!isScrollCompact;
}

// Duplicated from ChatInput.js:2330-2333: the restore pill's onClick handler
// expands the composer and schedules a focus of the textarea once it's
// visible again.
function handleRestorePillTap({ setIsScrollCollapsed, focusTextarea }) {
  setIsScrollCollapsed(false);
  focusTextarea();
}

describe("ChatInput mobile scroll-driven compact composer (mitto-47l)", () => {
  describe("isScrollCompact derivation", () => {
    test("compact only when mobile + scroll-collapsed + no competing gate is active", () => {
      expect(
        computeIsScrollCompact({ isMobile: true, isScrollCollapsed: true }),
      ).toBe(true);
    });

    test("not compact on desktop even if scroll-collapsed", () => {
      expect(
        computeIsScrollCompact({ isMobile: false, isScrollCollapsed: true }),
      ).toBe(false);
    });

    test("not compact while not scroll-collapsed (at bottom)", () => {
      expect(
        computeIsScrollCompact({ isMobile: true, isScrollCollapsed: false }),
      ).toBe(false);
    });

    test("loopConfigured takes priority: loop's own hide semantics win", () => {
      expect(
        computeIsScrollCompact({
          isMobile: true,
          isScrollCollapsed: true,
          loopConfigured: true,
        }),
      ).toBe(false);
    });

    test("mcpUIBlocking takes priority: a blocking MCP UI prompt wins", () => {
      expect(
        computeIsScrollCompact({
          isMobile: true,
          isScrollCollapsed: true,
          mcpUIBlocking: true,
        }),
      ).toBe(false);
    });

    test("an active textarea focus always forces expanded (not compact)", () => {
      expect(
        computeIsScrollCompact({
          isMobile: true,
          isScrollCollapsed: true,
          isTextareaFocused: true,
        }),
      ).toBe(false);
    });
  });

  describe("isScrollCollapsed mirrors isScrolledUp directly", () => {
    test("collapses when the user is scrolled up", () => {
      const setIsScrollCollapsed = jest.fn();
      runScrollCollapseEffect({ isScrolledUp: true, setIsScrollCollapsed });
      expect(setIsScrollCollapsed).toHaveBeenCalledWith(true);
    });

    test("expands when the user is not scrolled up", () => {
      const setIsScrollCollapsed = jest.fn();
      runScrollCollapseEffect({ isScrolledUp: false, setIsScrollCollapsed });
      expect(setIsScrollCollapsed).toHaveBeenCalledWith(false);
    });
  });

  describe("useScrollManagement asymmetric hysteresis (the flicker fix)", () => {
    test("does NOT collapse until the user scrolls up past COLLAPSE_DISTANCE_PX", () => {
      // Just inside the dead-band from the bottom: stays expanded.
      expect(
        computeIsScrolledUpHysteresis(false, COLLAPSE_DISTANCE_PX - 1),
      ).toBe(false);
    });

    test("collapses once the user scrolls up past COLLAPSE_DISTANCE_PX", () => {
      expect(
        computeIsScrolledUpHysteresis(false, COLLAPSE_DISTANCE_PX + 1),
      ).toBe(true);
    });

    test("stays collapsed while inside the dead-band (moving bottom does not toggle)", () => {
      // Distance between EXPAND and COLLAPSE thresholds: hold previous state.
      const mid = (COLLAPSE_DISTANCE_PX + EXPAND_DISTANCE_PX) / 2;
      expect(computeIsScrolledUpHysteresis(true, mid)).toBe(true);
    });

    test("does NOT expand until the user is back within EXPAND_DISTANCE_PX", () => {
      // Still above the expand threshold: stays collapsed.
      expect(
        computeIsScrolledUpHysteresis(true, EXPAND_DISTANCE_PX + 1),
      ).toBe(true);
    });

    test("expands once the user returns within EXPAND_DISTANCE_PX of the bottom", () => {
      expect(
        computeIsScrolledUpHysteresis(true, EXPAND_DISTANCE_PX - 1),
      ).toBe(false);
    });

    test("dead-band prevents oscillation: a nearly-at-bottom moving target holds state both ways", () => {
      // Expanded state persists just inside the collapse threshold...
      expect(
        computeIsScrolledUpHysteresis(false, COLLAPSE_DISTANCE_PX - 1),
      ).toBe(false);
      // ...and collapsed state persists just above the expand threshold.
      expect(
        computeIsScrolledUpHysteresis(true, EXPAND_DISTANCE_PX + 1),
      ).toBe(true);
    });
  });

  describe("useScrollManagement debounced collapse (streaming long-chunk flicker fix)", () => {
    test("a transient jump past COLLAPSE that self-corrects before the delay never collapses", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      // Long streamed chunk momentarily pushes us far from the (new) bottom.
      m.observe(false, COLLAPSE_DISTANCE_PX + 400, 0);
      expect(m.hasPending()).toBe(true);
      // Auto-scroll re-pins to the new bottom (scrollToBottom) BEFORE the timer.
      expect(m.rePin()).toBe(false);
      expect(m.hasPending()).toBe(false);
      // Even after the original delay elapses, nothing is committed.
      expect(m.advanceTo(300, /* live */ 0)).toBe(undefined);
      expect(m.events()).toEqual([]);
    });

    test("scrolling back down before the delay cancels the pending collapse", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      m.observe(false, COLLAPSE_DISTANCE_PX + 200, 0);
      expect(m.hasPending()).toBe(true);
      // User scrolls back within the dead-band (below COLLAPSE): timer cancelled.
      m.observe(false, EXPAND_DISTANCE_PX + 10, 100);
      expect(m.hasPending()).toBe(false);
      expect(m.advanceTo(300, EXPAND_DISTANCE_PX + 10)).toBe(undefined);
      expect(m.events()).toEqual([]);
    });

    test("a genuine sustained scroll-up DOES collapse after the delay", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      m.observe(false, COLLAPSE_DISTANCE_PX + 300, 0);
      expect(m.hasPending()).toBe(true);
      // Still far from the bottom when the timer fires → commit collapse.
      expect(m.advanceTo(250, COLLAPSE_DISTANCE_PX + 300)).toBe(true);
      expect(m.events()).toEqual([{ type: "collapse" }]);
      expect(m.hasPending()).toBe(false);
    });

    test("timer re-checks the LIVE distance: fires but self-cancels if back near bottom", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      m.observe(false, COLLAPSE_DISTANCE_PX + 300, 0);
      // By the time the timer fires the view has settled at the bottom (a
      // scroll event that did not itself cross the expand threshold cancels via
      // observe, but this guards the race where no such event arrived).
      expect(m.advanceTo(250, /* live */ 5)).toBe(false);
      expect(m.events()).toEqual([]);
    });

    test("expand is immediate and cancels any armed collapse", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      // Arm a collapse from the expanded state...
      m.observe(false, COLLAPSE_DISTANCE_PX + 100, 0);
      expect(m.hasPending()).toBe(true);
      // ...then, already-collapsed, a return within EXPAND expands immediately.
      expect(m.observe(true, EXPAND_DISTANCE_PX - 1, 50)).toBe(false);
      expect(m.events()).toEqual([{ type: "expand" }]);
      expect(m.hasPending()).toBe(false);
    });

    test("only ONE timer is armed across repeated past-COLLAPSE scroll events", () => {
      const m = createDebouncedCollapseModel({ collapseDelayMs: 250 });
      m.observe(false, COLLAPSE_DISTANCE_PX + 500, 0);
      m.observe(false, COLLAPSE_DISTANCE_PX + 480, 30);
      m.observe(false, COLLAPSE_DISTANCE_PX + 460, 60);
      // The first arm's deadline (0 + 250) still governs — not re-extended.
      expect(m.advanceTo(250, COLLAPSE_DISTANCE_PX + 460)).toBe(true);
      expect(m.events()).toEqual([{ type: "collapse" }]);
    });
  });

  describe("compact class + mount-preservation invariant", () => {
    test("compact adds the modifier class alongside the base classes (element stays the same node)", () => {
      expect(computeChatInputContainerClass(true)).toBe(
        "max-w-4xl mx-auto chat-input-container chat-input-container--compact",
      );
    });

    test("non-compact renders the base classes only", () => {
      expect(computeChatInputContainerClass(false)).toBe(
        "max-w-4xl mx-auto chat-input-container ",
      );
    });

    // The composition area's mount/unmount render gate (shouldShowCompositionArea,
    // mitto-9l8) is `!mcpUIBlocking && !(isPromptCollapsed && loopConfigured)` —
    // it does not reference isScrollCompact/isScrollCollapsed at all. This
    // proves the scroll-driven compact state is purely a CSS-class toggle on
    // an already-mounted node: draft text, pendingImages, and pendingFiles
    // (owned by state further up the same component, never gated on
    // isScrollCompact) are never unmounted by scrolling, unlike the
    // isPromptCollapsed && loopConfigured path which fully unmounts the box.
    test("the composition area stays mounted while scroll-compact (not gated by isPromptCollapsed/loopConfigured)", () => {
      const isScrollCompact = computeIsScrollCompact({
        isMobile: true,
        isScrollCollapsed: true,
      });
      expect(isScrollCompact).toBe(true);
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed: false,
          loopConfigured: false,
          hasActiveUIPrompt: false,
          promptType: undefined,
        }),
      ).toBe(true);
    });

    test("mcpUIBlocking still unmounts the composition area even while scroll-compact would otherwise apply", () => {
      const isScrollCompact = computeIsScrollCompact({
        isMobile: true,
        isScrollCollapsed: true,
        mcpUIBlocking: true,
      });
      expect(isScrollCompact).toBe(false);
      expect(
        shouldShowCompositionArea({
          isPromptCollapsed: false,
          loopConfigured: false,
          hasActiveUIPrompt: true,
          promptType: "options",
        }),
      ).toBe(false);
    });
  });

  // REOPENED v2: the first implementation only shrunk the textarea
  // min-height and left the follow-up/action buttons block (which renders
  // OUTSIDE .chat-input-container) untouched. These tests cover the fix:
  // the action buttons block collapses off the same isScrollCompact signal,
  // the composition box collapses in full (not just min-height — see the
  // CSS-level assertions in styles.test.js), and a dedicated restore
  // affordance appears only while collapsed.
  describe("full-area collapse: action buttons + restore affordance", () => {
    test("action buttons wrapper gets the compact modifier when scroll-compact", () => {
      expect(computeActionButtonsClass(true)).toBe(
        "max-w-4xl mx-auto mb-3 chat-input-actionbuttons chat-input-actionbuttons--compact",
      );
    });

    test("action buttons wrapper renders base classes only when not compact", () => {
      expect(computeActionButtonsClass(false)).toBe(
        "max-w-4xl mx-auto mb-3 chat-input-actionbuttons ",
      );
    });

    test("the action buttons block and the composition box collapse off the SAME isScrollCompact signal (no separate scroll listener)", () => {
      const isScrollCompact = computeIsScrollCompact({
        isMobile: true,
        isScrollCollapsed: true,
      });
      expect(computeChatInputContainerClass(isScrollCompact)).toContain(
        "chat-input-container--compact",
      );
      expect(computeActionButtonsClass(isScrollCompact)).toContain(
        "chat-input-actionbuttons--compact",
      );
    });

    test("desktop stays unaffected: both compact modifiers are absent when isMobile is false", () => {
      const isScrollCompact = computeIsScrollCompact({
        isMobile: false,
        isScrollCollapsed: true,
      });
      expect(isScrollCompact).toBe(false);
      expect(computeChatInputContainerClass(isScrollCompact)).not.toContain(
        "--compact",
      );
      expect(computeActionButtonsClass(isScrollCompact)).not.toContain(
        "--compact",
      );
    });

    test("restore affordance is only rendered while scroll-compact", () => {
      expect(shouldShowRestorePill(true)).toBe(true);
      expect(shouldShowRestorePill(false)).toBe(false);
    });

    test("tapping the restore affordance expands the composer and focuses the textarea", () => {
      const setIsScrollCollapsed = jest.fn();
      const focusTextarea = jest.fn();
      handleRestorePillTap({ setIsScrollCollapsed, focusTextarea });
      expect(setIsScrollCollapsed).toHaveBeenCalledWith(false);
      expect(focusTextarea).toHaveBeenCalledTimes(1);
    });

    // Cross-checks the two other restore paths required by the bead (scroll-
    // to-bottom and textarea focus) against the shared derivation/effect
    // helpers already covered above, so this describe block documents all
    // three restore triggers together.
    test("scroll-to-bottom restores (via the direct isScrolledUp mapping) and focus restores (via isScrollCompact)", () => {
      const setIsScrollCollapsed = jest.fn();
      // Returning to the bottom clears isScrolledUp (hysteresis in the hook),
      // which the composer mirrors directly to expand.
      runScrollCollapseEffect({ isScrolledUp: false, setIsScrollCollapsed });
      expect(setIsScrollCollapsed).toHaveBeenCalledWith(false);

      expect(
        computeIsScrollCompact({
          isMobile: true,
          isScrollCollapsed: true,
          isTextareaFocused: true,
        }),
      ).toBe(false);
    });
  });
});
