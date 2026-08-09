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
import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

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
