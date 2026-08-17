// Mitto Web Interface - external callback controls for loop conversations.

const { html, useEffect, useRef, useState } = window.preact;

import { CheckIcon, CopyIcon } from "./Icons.js";
import { getSdkClient } from "../utils/sdkClient.js";

/**
 * Manages the callback credential independently from the loop trigger list.
 * The credential is visually truncated in the panel but copied in full.
 */
export function CallbackTriggerSection({ sessionId, loopEnabled }) {
  const [callbackConfig, setCallbackConfig] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadedSessionId, setLoadedSessionId] = useState(null);
  const [busyAction, setBusyAction] = useState("");
  const [copied, setCopied] = useState(false);
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

  const createCallback = async () => {
    const targetSessionId = sessionId;
    if (
      !targetSessionId ||
      !loopEnabled ||
      loading ||
      loadedSessionId !== targetSessionId ||
      busyAction
    )
      return;
    setBusyAction("enable");
    try {
      const config =
        await getSdkClient().sessions.createCallback(targetSessionId);
      if (!isCurrentSession(targetSessionId)) return;
      setCallbackConfig(config);
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

  const loadingCurrentSession =
    !!sessionId && (loading || loadedSessionId !== sessionId);
  const configured = !!callbackConfig?.callback_url;
  const busy = !!busyAction;
  const checked =
    (configured && busyAction !== "revoke") || busyAction === "enable";
  const expanded = configured || busyAction === "enable";
  const toggleDisabled =
    loadingCurrentSession || busy || (!loopEnabled && !configured);

  const toggleCallback = (enabled) => {
    if (enabled) createCallback();
    else revokeCallback();
  };

  return html`
    <div
      class="collapse border border-mitto-border bg-mitto-surface-2 ${expanded
        ? "collapse-open"
        : "collapse-close"}"
      data-testid="callback-trigger-section"
    >
      <div class="collapse-title">
        <label class="label cursor-pointer gap-3 justify-start">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked=${checked}
            disabled=${toggleDisabled}
            onChange=${(event) => toggleCallback(event.target.checked)}
            aria-label="On callback"
            data-testid="callback-toggle"
          />
          <span class="min-w-0">
            <span class="font-medium text-mitto-text-strong">On callback</span>
            <span class="block text-xs text-mitto-text-muted">
              ${!loopEnabled
                ? configured
                  ? "Inactive while the loop is paused"
                  : "Resume the loop to enable this trigger"
                : "Trigger this loop using a credentialed URL"}
            </span>
          </span>
        </label>
      </div>
      <div class="collapse-content">
        ${busyAction === "enable"
          ? html`<div
              class="flex items-center gap-2 text-sm text-mitto-text-muted"
            >
              <span class="loading loading-spinner loading-xs"></span>
              Generating callback URL…
            </div>`
          : configured &&
            html`<div class="flex items-center gap-2 min-w-0">
              <span
                class="min-w-0 flex-1 truncate text-xs font-mono text-mitto-text-secondary"
                aria-label="Callback URL"
                data-testid="callback-url"
                >${callbackConfig.callback_url}</span
              >
              <button
                type="button"
                class="btn btn-ghost btn-square btn-sm shrink-0"
                aria-label=${copied
                  ? "Callback URL copied"
                  : "Copy callback URL"}
                disabled=${busy}
                onClick=${() => copyUrl(callbackConfig.callback_url, sessionId)}
                data-testid="callback-copy"
              >
                ${copied
                  ? html`<${CheckIcon}
                      className="w-4 h-4 text-mitto-success"
                    />`
                  : html`<${CopyIcon} className="w-4 h-4" />`}
              </button>
            </div>`}
      </div>
    </div>
  `;
}
