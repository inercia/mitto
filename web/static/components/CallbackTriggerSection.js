// Mitto Web Interface - external callback controls for loop conversations.

const { html, useEffect, useRef, useState } = window.preact;

import { ConfirmDialog } from "./ConfirmDialog.js";
import { getSdkClient } from "../utils/sdkClient.js";

/**
 * Manages the callback credential independently from the loop trigger list.
 * The URL is intentionally never rendered; it can only be copied.
 */
export function CallbackTriggerSection({ sessionId, loopEnabled }) {
  const [callbackConfig, setCallbackConfig] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadedSessionId, setLoadedSessionId] = useState(null);
  const [busyAction, setBusyAction] = useState("");
  const [copied, setCopied] = useState(false);
  const [confirmAction, setConfirmAction] = useState(null);
  const activeSessionRef = useRef(sessionId);
  const mountedRef = useRef(true);
  const copiedTimerRef = useRef(null);

  activeSessionRef.current = sessionId;

  const clearCopiedTimer = () => {
    if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
    copiedTimerRef.current = null;
  };

  const isCurrentSession = (targetSessionId) =>
    mountedRef.current && activeSessionRef.current === targetSessionId;

  useEffect(() => {
    return () => {
      mountedRef.current = false;
      clearCopiedTimer();
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    clearCopiedTimer();
    setCallbackConfig(null);
    setCopied(false);
    setBusyAction("");
    setConfirmAction(null);

    if (!sessionId) {
      setLoading(false);
      setLoadedSessionId(null);
      return () => {
        cancelled = true;
      };
    }

    setLoading(true);
    getSdkClient()
      .sessions.getCallback(sessionId)
      .then((config) => {
        if (!cancelled) setCallbackConfig(config?.callback_url ? config : null);
      })
      .catch(() => {
        if (!cancelled) setCallbackConfig(null);
      })
      .finally(() => {
        if (!cancelled) {
          setLoadedSessionId(sessionId);
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  const copyUrl = async (url, targetSessionId) => {
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      if (!isCurrentSession(targetSessionId)) return;
      clearCopiedTimer();
      setCopied(true);
      copiedTimerRef.current = setTimeout(() => {
        if (isCurrentSession(targetSessionId)) setCopied(false);
      }, 2000);
    } catch (_err) {
      // Clipboard access is best effort, matching the previous controls.
    }
  };

  const createCallback = async (action) => {
    const targetSessionId = sessionId;
    if (
      !targetSessionId ||
      !loopEnabled ||
      loading ||
      loadedSessionId !== targetSessionId ||
      busyAction
    )
      return;
    setBusyAction(action);
    try {
      const config =
        await getSdkClient().sessions.createCallback(targetSessionId);
      if (!isCurrentSession(targetSessionId)) return;
      setCallbackConfig(config);
      await copyUrl(config?.callback_url, targetSessionId);
    } catch (_err) {
      // Callback mutations intentionally remain tolerant of SDK failures.
    } finally {
      if (isCurrentSession(targetSessionId)) setBusyAction("");
    }
  };

  const revokeCallback = async () => {
    const targetSessionId = sessionId;
    if (
      !targetSessionId ||
      loading ||
      loadedSessionId !== targetSessionId ||
      busyAction
    )
      return;
    setBusyAction("revoke");
    try {
      await getSdkClient().sessions.revokeCallback(targetSessionId);
      if (!isCurrentSession(targetSessionId)) return;
      clearCopiedTimer();
      setCopied(false);
      setCallbackConfig(null);
    } catch (_err) {
      // Callback mutations intentionally remain tolerant of SDK failures.
    } finally {
      if (isCurrentSession(targetSessionId)) setBusyAction("");
    }
  };

  const confirmMutation = () => {
    const action = confirmAction;
    setConfirmAction(null);
    if (action === "rotate") createCallback("rotate");
    if (action === "revoke") revokeCallback();
  };

  const loadingCurrentSession =
    !!sessionId && (loading || loadedSessionId !== sessionId);
  const configured = !!callbackConfig?.callback_url;
  const status = loadingCurrentSession
    ? { label: "Loading", className: "badge badge-sm badge-ghost" }
    : !configured
      ? { label: "Not configured", className: "badge badge-sm badge-ghost" }
      : loopEnabled
        ? {
            label: "Active",
            className: "badge badge-sm badge-success badge-soft",
          }
        : {
            label: "Inactive",
            className: "badge badge-sm badge-warning badge-soft",
          };
  const busy = !!busyAction;

  return html`
    <div
      class="card border border-mitto-border bg-mitto-surface-2 p-4"
      data-testid="callback-trigger-section"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <h3 class="font-medium text-mitto-text-strong">External callback</h3>
          <p class="text-xs text-mitto-text-muted">
            Trigger this loop using a credentialed URL.
          </p>
        </div>
        <span class=${status.className} data-testid="callback-status"
          >${status.label}</span
        >
      </div>

      ${loadingCurrentSession
        ? html`<div
            class="flex items-center gap-2 pt-4 text-sm text-mitto-text-muted"
          >
            <span class="loading loading-spinner loading-xs"></span>
            Loading callback status…
          </div>`
        : html`<div class="pt-4">
            ${configured
              ? html`
                  ${!loopEnabled &&
                  html`<p class="text-xs text-mitto-text-muted mb-3 italic">
                    Configured but inactive while the loop is paused.
                  </p>`}
                  <div class="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      class="btn btn-sm btn-soft"
                      disabled=${busy}
                      onClick=${() =>
                        copyUrl(callbackConfig.callback_url, sessionId)}
                      data-testid="callback-copy"
                    >
                      ${copied ? "Copied" : "Copy URL"}
                    </button>
                    ${loopEnabled &&
                    html`<button
                      type="button"
                      class="btn btn-sm btn-soft"
                      disabled=${busy}
                      onClick=${() => setConfirmAction("rotate")}
                      data-testid="callback-rotate"
                    >
                      ${busyAction === "rotate" ? "Rotating…" : "Rotate URL"}
                    </button>`}
                    <button
                      type="button"
                      class="btn btn-sm btn-soft btn-error"
                      disabled=${busy}
                      onClick=${() => setConfirmAction("revoke")}
                      data-testid="callback-revoke"
                    >
                      ${busyAction === "revoke" ? "Revoking…" : "Revoke URL"}
                    </button>
                  </div>
                `
              : loopEnabled
                ? html`<button
                    type="button"
                    class="btn btn-sm btn-soft"
                    disabled=${busy}
                    onClick=${() => createCallback("enable")}
                    data-testid="callback-enable"
                  >
                    ${busyAction === "enable"
                      ? "Generating…"
                      : "Generate callback URL"}
                  </button>`
                : html`<p class="text-xs text-mitto-text-muted">
                    No callback URL configured. Resume the loop to generate one.
                  </p>`}
          </div>`}
    </div>

    <${ConfirmDialog}
      isOpen=${!!confirmAction}
      title=${confirmAction === "rotate"
        ? "Rotate Callback URL"
        : "Revoke Callback URL"}
      message=${confirmAction === "rotate"
        ? "Rotate callback URL? The old URL will stop working immediately."
        : "Revoke callback URL? It will stop working immediately."}
      confirmLabel=${confirmAction === "rotate" ? "Rotate" : "Revoke"}
      confirmVariant="danger"
      onConfirm=${confirmMutation}
      onCancel=${() => setConfirmAction(null)}
    />
  `;
}
