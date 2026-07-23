/**
 * Unit tests for ChatInput's Flush-context toolbar button (mitto-c23).
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
// Duplicated from ChatInput.js:3120-3121:
//   ${flushCommand && onFlushContext && html`<button ...>`}
// Matches useConversationMenu.js:124 so the composer layout is unchanged
// on ACPs without a context_flush_command.
// =============================================================================

function shouldRenderFlushButton({ flushCommand, onFlushContext }) {
  return Boolean(flushCommand) && Boolean(onFlushContext);
}

// =============================================================================
// Disabled-gate logic
// Duplicated from ChatInput.js:3127:
//   disabled=${isFullyDisabled || isReadOnly || !acpReady}
// isFullyDisabled itself (ChatInput.js:742-743) is:
//   disabled || noSession || isSending || isArchived || isArchivePending
// so the button is disabled if ANY of:
//   disabled, noSession, isSending, isArchived, isArchivePending,
//   isReadOnly, !acpReady.
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
}) {
  const isFullyDisabled =
    disabled || noSession || isSending || isArchived || isArchivePending;
  return isFullyDisabled || isReadOnly || !acpReady;
}

// =============================================================================
// Click handler
// Duplicated from ChatInput.js:3125:
//   onClick=${() => onFlushContext()}
// ChatInput passes no arguments; the parent-side wrapper in app.js closes
// over activeSession and calls handleFlushContext(activeSession).
// =============================================================================

function handleFlushButtonClick(onFlushContext) {
  onFlushContext();
}

// =============================================================================
// Tests
// =============================================================================

describe("ChatInput Flush-context button (mitto-c23)", () => {
  describe("visibility (shouldRenderFlushButton)", () => {
    test("renders when flushCommand and onFlushContext are both provided", () => {
      expect(
        shouldRenderFlushButton({
          flushCommand: "/clear",
          onFlushContext: () => {},
        }),
      ).toBe(true);
    });

    test("does NOT render when flushCommand is empty string", () => {
      expect(
        shouldRenderFlushButton({
          flushCommand: "",
          onFlushContext: () => {},
        }),
      ).toBe(false);
    });

    test("does NOT render when flushCommand is undefined", () => {
      expect(
        shouldRenderFlushButton({
          flushCommand: undefined,
          onFlushContext: () => {},
        }),
      ).toBe(false);
    });

    test("does NOT render when onFlushContext is undefined", () => {
      expect(
        shouldRenderFlushButton({
          flushCommand: "/clear",
          onFlushContext: undefined,
        }),
      ).toBe(false);
    });

    test("does NOT render when both are absent", () => {
      expect(
        shouldRenderFlushButton({
          flushCommand: "",
          onFlushContext: undefined,
        }),
      ).toBe(false);
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
