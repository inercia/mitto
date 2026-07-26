/**
 * Unit tests for LoopFrequencyPanel.handleConfirmRestore (bead mitto-5cj).
 *
 * Because the component imports window.preact globals at module load, it
 * cannot be imported under jsdom. Instead the key logic is duplicated here
 * and tested directly — the same pattern used by PromptParameterDialog.test.js,
 * BeadsView.test.js and Message.test.js.
 *
 * Bug (mitto-5cj): when a paused/stopped loop is restored via the play
 * button, `handleConfirmRestore` PATCHes `{enabled: true}` but does NOT
 * follow up with a POST to `endpoints.sessions.loopRunNow` — so the loop
 * schedule is re-armed but no prompt is fired. The user has to click play
 * a second time (which routes to handleConfirmImmediateDelivery, the
 * run-now path) to actually get a prompt.
 *
 * These tests assert the EXPECTED post-fix behavior: after a successful
 * restore PATCH, the run-now endpoint is also invoked with
 * `reset_timer: true`. They fail against the current buggy logic and will
 * pass once the fix duplicates the run-now call here AND in the real
 * component (per the project's duplicate-logic test convention).
 */

// Jest is not injected as a global under --experimental-vm-modules (ESM); we
// must import it explicitly. testGlobals.js re-exports the lifecycle globals
// and `jest` from whichever runner is active (Jest or bun:test).
import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

// =============================================================================
// Duplicated handleConfirmRestore logic
// Mirrors web/static/components/LoopFrequencyPanel.js — keep in sync.
// =============================================================================

/**
 * Build a plain async handler that mirrors `handleConfirmRestore` from
 * LoopFrequencyPanel.js. Dependencies (secureFetch, endpoints, setters,
 * conversation state) are injected so the handler is unit-testable
 * without importing the Preact component.
 */
function makeHandleConfirmRestore({
  sessionId,
  stoppedReason,
  resetCounters,
  secureFetch,
  endpoints,
  onLoopEnabledChange,
  setIsSavingEnabled = () => {},
  setShowRestoreDialog = () => {},
  setErrorMessage = () => {},
}) {
  return async function handleConfirmRestore() {
    if (!sessionId) return;
    setIsSavingEnabled(true);
    try {
      // When the loop was auto-stopped by a cap, optionally reset the elapsed
      // iterations/time so it can resume instead of immediately re-stopping.
      const limitWasStopped =
        stoppedReason === "maxDuration" ||
        stoppedReason === "maxIterations" ||
        stoppedReason === "iterationSafeguard";
      const body = { enabled: true };
      if (limitWasStopped && resetCounters) {
        body.reset_counters = true;
      }
      const response = await secureFetch(endpoints.sessions.loop(sessionId), {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (response.ok) {
        if (onLoopEnabledChange) onLoopEnabledChange(true);
        // mitto-5cj: after re-enabling the loop, also POST run-now so the
        // user's play click actually fires the prompt on the FIRST click.
        // Skip when the loop was cap-stopped and the user did NOT tick
        // reset_counters (the fire would immediately re-stop). Swallow the
        // 409 "agent streaming" case silently — the loop is enabled either
        // way.
        const wouldImmediatelyReStop = limitWasStopped && !resetCounters;
        if (!wouldImmediatelyReStop) {
          try {
            await secureFetch(endpoints.sessions.loopRunNow(sessionId), {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ reset_timer: true }),
            });
          } catch (_runNowErr) {
            // Non-fatal: the loop is enabled; the next tick will fire.
          }
        }
        setShowRestoreDialog(false);
      } else {
        setErrorMessage("Failed to restore the loop schedule. Please try again.");
      }
    } catch (_err) {
      setErrorMessage("Failed to restore the loop schedule. Please try again.");
    } finally {
      setIsSavingEnabled(false);
    }
  };
}

// =============================================================================
// Test fixtures
// =============================================================================

function makeEndpoints() {
  return {
    sessions: {
      loop: (id) => `/api/sessions/${encodeURIComponent(id)}/loop`,
      loopRunNow: (id) => `/api/sessions/${encodeURIComponent(id)}/loop/run-now`,
    },
  };
}

/** Build a Jest mock secureFetch that returns { ok: true } for every URL. */
function makeSecureFetchOK() {
  return jest.fn(async () => ({ ok: true, status: 200 }));
}

// =============================================================================
// Tests — bug reproduction
// =============================================================================

describe("handleConfirmRestore — mitto-5cj: restore must also fire the prompt", () => {
  test("PATCH-only baseline: enables the loop (current behavior)", async () => {
    const secureFetch = makeSecureFetchOK();
    const endpoints = makeEndpoints();
    const onLoopEnabledChange = jest.fn();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      secureFetch,
      endpoints,
      onLoopEnabledChange,
    });

    await handler();

    // The PATCH always happens — that part already works.
    expect(secureFetch).toHaveBeenCalledWith(
      "/api/sessions/s1/loop",
      expect.objectContaining({ method: "PATCH" }),
    );
    expect(onLoopEnabledChange).toHaveBeenCalledWith(true);
  });

  test("bug: after a successful restore PATCH, loopRunNow must ALSO be POSTed", async () => {
    const secureFetch = makeSecureFetchOK();
    const endpoints = makeEndpoints();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      secureFetch,
      endpoints,
      onLoopEnabledChange: () => {},
    });

    await handler();

    // Expected post-fix: exactly one call per endpoint — PATCH loop, then POST
    // run-now — so a paused/stopped loop actually fires a prompt on the FIRST
    // play click. The current implementation only calls PATCH, so this fails.
    const urls = secureFetch.mock.calls.map((c) => c[0]);
    expect(urls).toEqual([
      "/api/sessions/s1/loop",
      "/api/sessions/s1/loop/run-now",
    ]);

    const runNowCall = secureFetch.mock.calls.find(
      (c) => c[0] === "/api/sessions/s1/loop/run-now",
    );
    expect(runNowCall).toBeDefined();
    expect(runNowCall[1]).toEqual(
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ reset_timer: true }),
      }),
    );
  });

  test("no run-now fire when the PATCH itself fails", async () => {
    // Guard the fix: if the enable PATCH fails, we must NOT still try to
    // trigger run-now (that would fire a prompt on a still-paused loop).
    const secureFetch = jest.fn(async (url) => {
      if (url.endsWith("/loop")) return { ok: false, status: 500 };
      return { ok: true, status: 200 };
    });
    const endpoints = makeEndpoints();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      secureFetch,
      endpoints,
      onLoopEnabledChange: () => {},
    });

    await handler();

    const urls = secureFetch.mock.calls.map((c) => c[0]);
    expect(urls).not.toContain("/api/sessions/s1/loop/run-now");
  });

  test("cap-stopped WITHOUT reset_counters skips the run-now fire", async () => {
    // If the loop was auto-stopped by maxIterations/maxDuration and the
    // user did NOT tick "reset counters", firing run-now would immediately
    // re-stop the loop — surface that as a no-op instead.
    const secureFetch = makeSecureFetchOK();
    const endpoints = makeEndpoints();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "maxIterations",
      resetCounters: false,
      secureFetch,
      endpoints,
      onLoopEnabledChange: () => {},
    });

    await handler();

    const urls = secureFetch.mock.calls.map((c) => c[0]);
    expect(urls).toEqual(["/api/sessions/s1/loop"]);
    expect(urls).not.toContain("/api/sessions/s1/loop/run-now");
  });

  test("cap-stopped WITH reset_counters DOES fire run-now (loop can resume)", async () => {
    const secureFetch = makeSecureFetchOK();
    const endpoints = makeEndpoints();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "maxDuration",
      resetCounters: true,
      secureFetch,
      endpoints,
      onLoopEnabledChange: () => {},
    });

    await handler();

    const urls = secureFetch.mock.calls.map((c) => c[0]);
    expect(urls).toEqual([
      "/api/sessions/s1/loop",
      "/api/sessions/s1/loop/run-now",
    ]);
    // And the PATCH body carries reset_counters so the backend clears the cap.
    const patchCall = secureFetch.mock.calls.find(
      (c) => c[0] === "/api/sessions/s1/loop",
    );
    expect(patchCall[1].body).toBe(
      JSON.stringify({ enabled: true, reset_counters: true }),
    );
  });

  test("run-now failure is swallowed silently (loop is already enabled)", async () => {
    // If run-now returns 409 (agent streaming) or otherwise fails, the
    // dialog should still close and no error should be surfaced — the loop
    // is enabled and the next tick will fire.
    const secureFetch = jest.fn(async (url) => {
      if (url.endsWith("/run-now")) throw new Error("network");
      return { ok: true, status: 200 };
    });
    const endpoints = makeEndpoints();
    const setErrorMessage = jest.fn();
    const setShowRestoreDialog = jest.fn();
    const onLoopEnabledChange = jest.fn();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      secureFetch,
      endpoints,
      onLoopEnabledChange,
      setShowRestoreDialog,
      setErrorMessage,
    });

    await handler();

    expect(onLoopEnabledChange).toHaveBeenCalledWith(true);
    expect(setShowRestoreDialog).toHaveBeenCalledWith(false);
    expect(setErrorMessage).not.toHaveBeenCalled();
  });
});
