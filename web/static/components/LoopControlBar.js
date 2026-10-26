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
import { useLoopPlay } from "../utils/loopPlay.js";

export function LoopControlBar({
  isOpen,
  sessionId,
  enabled = false,
  isPromptAreaVisible = false,
  onTogglePromptArea,
  onLoopEnabledChange,
  onOpenSettings,
}) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const play = useLoopPlay(sessionId, onLoopEnabledChange);

  const pause = useCallback(async () => {
    if (!sessionId || busy || play.busy || !enabled) return;
    setBusy("pause");
    try {
      await getSdkClient().sessions.loop.update(sessionId, { enabled: false });
      onLoopEnabledChange?.(false);
    } catch (_err) {
      setError("Failed to pause the loop. Please try again.");
    } finally {
      setBusy("");
    }
  }, [sessionId, busy, play.busy, enabled, onLoopEnabledChange]);

  if (!isOpen) return null;

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
          disabled=${busy !== "" || play.busy}
          onClick=${() => {
            if (enabled) {
              pause();
            } else {
              play.open();
            }
          }}
          title=${enabled ? "Pause loop runs" : "Resume or restart loop"}
          aria-label=${enabled ? "Pause loop runs" : "Resume or restart loop"}
          data-testid="loop-pause-resume-button"
        >
          ${
            busy === "pause" || play.busy
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
        isOpen=${!!play.confirmation}
        title=${play.confirmation?.title}
        message=${play.confirmation?.message}
        confirmLabel=${play.confirmation?.title}
        isLoading=${play.busy}
        onConfirm=${play.confirm}
        onCancel=${play.cancel}
      />

      <${ConfirmDialog}
        isOpen=${error !== "" || play.error !== ""}
        title="Error"
        message=${error || play.error}
        confirmLabel="OK"
        onConfirm=${() => {
          setError("");
          play.clearError();
        }}
        onCancel=${() => {
          setError("");
          play.clearError();
        }}
      />
    </${Fragment}>
  `;
}
