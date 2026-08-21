// Mitto Web Interface - compact controls for loop conversations.

const { html, useCallback, useState, Fragment } = window.preact;

import {
  ChatBubbleIcon,
  LoopFilledIcon,
  PauseFilledIcon,
  PlayFilledIcon,
  SettingsIcon,
} from "./Icons.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";

const CAP_STOP_REASONS = new Set([
  "maxDuration",
  "maxIterations",
  "iterationSafeguard",
]);

export function LoopControlBar({
  isOpen,
  sessionId,
  enabled = false,
  isStreaming = false,
  stoppedReason = "",
  maxDurationSeconds = 0,
  maxIterations = 0,
  isPromptAreaVisible = false,
  onTogglePromptArea,
  onLoopEnabledChange,
  onOpenSettings,
}) {
  const [dialog, setDialog] = useState("");
  const [busy, setBusy] = useState("");
  const [resetTimer, setResetTimer] = useState(true);
  const [resetCounters, setResetCounters] = useState(true);
  const [error, setError] = useState("");

  const runNow = useCallback(async () => {
    if (!sessionId || busy) return;
    setBusy("run");
    try {
      await getSdkClient().sessions.loop.runNow(sessionId, resetTimer);
      setDialog("");
    } catch (err) {
      setDialog("");
      setError(
        errorStatus(err) === 409
          ? "Session is currently processing a prompt. Please wait and try again."
          : "Failed to run the loop now. Please try again.",
      );
    } finally {
      setBusy("");
    }
  }, [sessionId, busy, resetTimer]);

  const pause = useCallback(async () => {
    if (!sessionId || busy || !enabled) return;
    setBusy("pause");
    try {
      await getSdkClient().sessions.loop.update(sessionId, { enabled: false });
      onLoopEnabledChange?.(false);
    } catch (_err) {
      setError("Failed to pause the loop. Please try again.");
    } finally {
      setBusy("");
    }
  }, [sessionId, busy, enabled, onLoopEnabledChange]);

  const restore = useCallback(async () => {
    if (!sessionId || busy) return;
    setBusy("restore");
    const limitStopped = CAP_STOP_REASONS.has(stoppedReason);
    try {
      const patch = { enabled: true };
      if (limitStopped && resetCounters) patch.reset_counters = true;
      await getSdkClient().sessions.loop.update(sessionId, patch);
      onLoopEnabledChange?.(true);
      if (!(limitStopped && !resetCounters)) {
        try {
          await getSdkClient().sessions.loop.runNow(sessionId, true);
        } catch (_err) {
          // Restoring succeeded; a busy agent can run the enabled loop later.
        }
      }
      setDialog("");
    } catch (_err) {
      setDialog("");
      setError("Failed to restore the loop schedule. Please try again.");
    } finally {
      setBusy("");
    }
  }, [sessionId, busy, stoppedReason, resetCounters, onLoopEnabledChange]);

  if (!isOpen) return null;

  const limitStopped = CAP_STOP_REASONS.has(stoppedReason);
  const resetLabel =
    maxDurationSeconds > 0 && maxIterations > 0
      ? "Reset elapsed time and iteration count"
      : maxDurationSeconds > 0
        ? "Reset elapsed time"
        : "Reset iteration count";
  const controlClass =
    "btn btn-sm btn-square btn-ghost border border-mitto-border-2";

  return html`
    <${Fragment}>
      <div
        class="h-11 px-2 flex items-center gap-1 rounded-lg border border-mitto-border bg-mitto-surface-3/95"
        data-testid="loop-control-bar"
      >
        <${LoopFilledIcon} className="w-5 h-5 text-mitto-accent shrink-0" />
        <span class="text-sm font-medium truncate">Loop</span>
        <span class="badge badge-sm ${enabled ? "badge-success badge-soft" : "badge-warning badge-soft"}">
          ${enabled ? "Running" : "Paused"}
        </span>
        <div class="flex-1"></div>
        <button
          type="button"
          class=${controlClass}
          disabled=${busy !== "" || !enabled || isStreaming}
          onClick=${() => {
            setResetTimer(true);
            setDialog("run");
          }}
          title="Run this loop prompt now"
          aria-label="Run this loop prompt now"
          data-testid="loop-run-now-button"
        >
          ${
            busy === "run" || busy === "restore"
              ? html`<span class="loading loading-spinner loading-xs"></span>`
              : html`<${PlayFilledIcon} className="w-4 h-4" />`
          }
        </button>
        <button
          type="button"
          class=${controlClass}
          disabled=${busy !== ""}
          onClick=${() => {
            if (enabled) {
              pause();
            } else {
              setResetCounters(true);
              setDialog("restore");
            }
          }}
          title=${enabled ? "Pause loop runs" : "Restore loop schedule"}
          aria-label=${enabled ? "Pause loop runs" : "Restore loop schedule"}
          data-testid="loop-pause-resume-button"
        >
          ${
            busy === "pause"
              ? html`<span class="loading loading-spinner loading-xs"></span>`
              : enabled
                ? html`<${PauseFilledIcon} className="w-4 h-4" />`
                : html`<${PlayFilledIcon} className="w-4 h-4" />`
          }
        </button>
        ${
          onTogglePromptArea &&
          html`
            <button
              type="button"
              class=${controlClass}
              onClick=${onTogglePromptArea}
              title=${isPromptAreaVisible
                ? "Hide message input"
                : "Show message input"}
              aria-label=${isPromptAreaVisible
                ? "Hide message input"
                : "Show message input"}
              data-testid="loop-toggle-prompt-area"
            >
              <${ChatBubbleIcon} className="w-4 h-4" />
            </button>
          `
        }
        <button
          type="button"
          class=${controlClass}
          onClick=${onOpenSettings}
          title="Open loop settings"
          aria-label="Open loop settings"
          data-testid="loop-open-settings"
        >
          <${SettingsIcon} className="w-4 h-4" />
        </button>
      </div>

      <${ConfirmDialog}
        isOpen=${dialog === "run"}
        title="Run now"
        message="Do you want to send this loop prompt now?"
        confirmLabel="Send"
        isLoading=${busy === "run"}
        onConfirm=${runNow}
        onCancel=${() => !busy && setDialog("")}
      >
        <label class="label cursor-pointer justify-start gap-3">
          <input type="checkbox" class="checkbox checkbox-sm" checked=${resetTimer}
            onChange=${(event) => setResetTimer(event.target.checked)} />
          Reset countdown for the next scheduled run
        </label>
      </${ConfirmDialog}>

      <${ConfirmDialog}
        isOpen=${dialog === "restore"}
        title="Restore loop schedule"
        message=${
          limitStopped
            ? "This loop stopped after reaching a configured safety limit. Restore it to keep iterating."
            : "Restore this loop and run its prompt now?"
        }
        confirmLabel="Restore"
        isLoading=${busy === "restore"}
        onConfirm=${restore}
        onCancel=${() => !busy && setDialog("")}
      >
        ${
          limitStopped &&
          html`
            <label class="label cursor-pointer justify-start gap-3">
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${resetCounters}
                onChange=${(event) => setResetCounters(event.target.checked)}
              />
              ${resetLabel}
            </label>
          `
        }
      </${ConfirmDialog}>

      <${ConfirmDialog}
        isOpen=${error !== ""}
        title="Error"
        message=${error}
        confirmLabel="OK"
        onConfirm=${() => setError("")}
        onCancel=${() => setError("")}
      />
    </${Fragment}>
  `;
}
