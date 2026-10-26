// Shared Play lifecycle for both loop controls. Only the backend decides whether
// an allowance is exhausted; display stop reasons and local clocks are not policy.
import { getSdkClient } from "./sdkClient.js";
import { errorMessage, errorStatus } from "./sdkErrors.js";

function confirmationFor(loop) {
  if (loop?.enabled) {
    throw new Error(
      "The loop is already running. Refresh its state before trying again.",
    );
  }
  const budget = loop?.budget;
  if (
    typeof budget?.exhausted !== "boolean" ||
    budget.resumable !== !budget.exhausted
  ) {
    throw new Error(
      "Could not verify the loop allowance. Refresh and try again.",
    );
  }
  return budget.exhausted
    ? {
        title: "Restart with fresh limits",
        message:
          "Restart this loop with a fresh duration and iteration allowance? Both counters will reset; configured limits and conversation history stay unchanged. The prompt will run now.",
        resetCounters: true,
      }
    : {
        title: "Resume remaining allowance",
        message:
          "Resume this loop and run its prompt now? The remaining duration and iteration allowance will be preserved; counters and configured limits stay unchanged.",
        resetCounters: false,
      };
}

function isDispatchBusy(error) {
  // This is run-now's existing busy response, not PATCH's restart-required 409
  // or an arbitrary conflict. Never retry run-now after bootstrap wins admission.
  return (
    errorStatus(error) === 409 &&
    error.code === "conflict" &&
    /^(Session is currently processing a prompt|Loop bootstrap already dispatched)[.!]?$/i.test(
      error.message,
    )
  );
}

/** Small stateful action seam: tests execute the very action used by both UIs. */
export function createLoopPlay(sessionId, getLoopClient, onChange, onEnabled) {
  let state = { confirmation: null, busy: false, error: "" };
  let disposed = false;
  const publish = (patch) => {
    if (disposed) return;
    state = { ...state, ...patch };
    onChange?.();
  };
  const refresh = async () =>
    confirmationFor(await getLoopClient().get(sessionId));
  const perform = async (operation) => {
    if (!sessionId || disposed || state.busy) return;
    publish({ busy: true, error: "" });
    try {
      await operation();
    } catch (error) {
      publish({
        confirmation: null,
        error: errorMessage(
          error,
          "Failed to start the loop. Refresh and try again.",
        ),
      });
    } finally {
      publish({ busy: false });
    }
  };
  return {
    getState: () => state,
    dispose: () => {
      disposed = true;
    },
    cancel: () => {
      if (!state.busy) publish({ confirmation: null });
    },
    clearError: () => publish({ error: "" }),
    open: () =>
      perform(async () => {
        const confirmation = await refresh();
        publish({ confirmation });
      }),
    confirm: () => {
      const confirmation = state.confirmation;
      if (!confirmation) return;
      return perform(async () => {
        const patch = { enabled: true };
        if (confirmation.resetCounters) patch.reset_counters = true;
        try {
          await getLoopClient().update(sessionId, patch);
        } catch (error) {
          if (error.code !== "loop_restart_required") throw error;
          const refreshed = await refresh();
          publish({
            confirmation: {
              ...refreshed,
              message: `The loop allowance changed. Review and confirm again. ${refreshed.message}`,
            },
          });
          return; // New authorization requires another user confirmation.
        }
        if (disposed) return;
        publish({ confirmation: null });
        onEnabled?.(true);
        try {
          await getLoopClient().runNow(sessionId, true);
        } catch (error) {
          if (!isDispatchBusy(error)) {
            throw new Error(
              `The loop is enabled, but its immediate run failed: ${errorMessage(error, "Please check the loop state and try again.")}`,
            );
          }
        }
      });
    },
  };
}

export function useLoopPlay(sessionId, onEnabled) {
  const { useState, useMemo, useRef, useLayoutEffect } = window.preact;
  const [, rerender] = useState(0);
  const enabledCallback = useRef(onEnabled);
  enabledCallback.current = onEnabled;
  const action = useMemo(
    () =>
      createLoopPlay(
        sessionId,
        () => getSdkClient().sessions.loop,
        () => rerender((value) => value + 1),
        (enabled) => enabledCallback.current?.(enabled),
      ),
    [sessionId],
  );
  useLayoutEffect(() => () => action.dispose(), [action]);
  return { ...action.getState(), ...action };
}
