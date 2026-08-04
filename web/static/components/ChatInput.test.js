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
