/**
 * Unit tests for LoopFrequencyPanel.handleConfirmRestore (bead mitto-5cj)
 * and its SDK-migrated call shape (mitto-7gta.17 slice S5).
 *
 * Because the component imports window.preact globals at module load, it
 * cannot be imported under jsdom. Instead the key logic is duplicated here
 * and tested directly — the same pattern used by PromptParameterDialog.test.js,
 * BeadsView.test.js and Message.test.js.
 *
 * Bug (mitto-5cj): when a paused/stopped loop is restored via the play
 * button, `handleConfirmRestore` PATCHes `{enabled: true}` but does NOT
 * follow up with a POST to run-now — so the loop schedule is re-armed but
 * no prompt is fired. The user has to click play a second time (which
 * routes to handleConfirmImmediateDelivery, the run-now path) to actually
 * get a prompt.
 *
 * These tests assert the EXPECTED post-fix behavior: after a successful
 * restore PATCH, run-now is also invoked with `resetTimer: true`.
 *
 * mitto-7gta.17 slice S5: the real component now calls
 * `getSdkClient().sessions.loop.update(id, patch)` /
 * `.runNow(id, resetTimer)` instead of raw `secureFetch(endpoints...)`.
 * Both throw on any non-2xx (MittoApiError) instead of returning
 * `{ok: false}`, so the duplicated handler below mirrors that
 * throw-on-error contract: a rejected `update()` call is caught and
 * surfaces the same "Failed to restore..." message, and a rejected
 * `runNow()` call is still swallowed non-fatally.
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
 * LoopFrequencyPanel.js (post mitto-7gta.17 slice S5). `loopClient` mirrors
 * `getSdkClient().sessions.loop` — `{update(id, patch), runNow(id,
 * resetTimer)}` — both of which throw on any non-2xx (MittoApiError)
 * instead of returning `{ok: false}`. Other dependencies (setters,
 * conversation state) are injected so the handler is unit-testable without
 * importing the Preact component.
 */
function makeHandleConfirmRestore({
  sessionId,
  stoppedReason,
  resetCounters,
  loopClient,
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
      await loopClient.update(sessionId, body);
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
          await loopClient.runNow(sessionId, true);
        } catch (_runNowErr) {
          // Non-fatal: the loop is enabled; the next tick will fire.
        }
      }
      setShowRestoreDialog(false);
    } catch (_err) {
      setErrorMessage("Failed to restore the loop schedule. Please try again.");
    } finally {
      setIsSavingEnabled(false);
    }
  };
}

// =============================================================================
// Duplicated toStableTriggersList logic (bead mitto-987y.7)
// Mirrors web/static/components/LoopFrequencyPanel.js L506-509 — keep in sync.
// =============================================================================

/**
 * Mirrors `toStableTriggersList` from LoopFrequencyPanel.js (fixed,
 * mitto-987y.7). Builds the wire-payload `triggers` array from the
 * armed-triggers Set: known triggers first in canonical order (schedule,
 * onCompletion, onTasks), then any other armed trigger (e.g. a future
 * `onChild`) appended afterward instead of dropped.
 *
 * Per internal/session/loop.go, a non-nil `triggers` field REPLACES the
 * stored trigger list wholesale (not a merge), so silently omitting an
 * armed-but-unrecognized trigger would permanently disarm it on the server.
 */
const KNOWN_TRIGGERS = ["schedule", "onCompletion", "onTasks"];
function toStableTriggersList(set) {
  const known = KNOWN_TRIGGERS.filter((t) => set.has(t));
  const unknown = [...set].filter((t) => !KNOWN_TRIGGERS.includes(t));
  return [...known, ...unknown];
}

// =============================================================================
// Test fixtures
// =============================================================================

/** Build a mock `getSdkClient().sessions.loop`-shaped client whose
 *  `update`/`runNow` both resolve successfully. */
function makeLoopClientOK() {
  return {
    update: jest.fn(async () => ({})),
    runNow: jest.fn(async () => ({})),
  };
}

// =============================================================================
// Tests — bug reproduction
// =============================================================================

describe("handleConfirmRestore — mitto-5cj: restore must also fire the prompt", () => {
  test("PATCH-only baseline: enables the loop (current behavior)", async () => {
    const loopClient = makeLoopClientOK();
    const onLoopEnabledChange = jest.fn();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      loopClient,
      onLoopEnabledChange,
    });

    await handler();

    // The PATCH always happens — that part already works.
    expect(loopClient.update).toHaveBeenCalledWith("s1", { enabled: true });
    expect(onLoopEnabledChange).toHaveBeenCalledWith(true);
  });

  test("bug: after a successful restore PATCH, runNow must ALSO be called", async () => {
    const loopClient = makeLoopClientOK();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      loopClient,
      onLoopEnabledChange: () => {},
    });

    await handler();

    // Expected post-fix: update() then runNow() — so a paused/stopped loop
    // actually fires a prompt on the FIRST play click.
    expect(loopClient.update).toHaveBeenCalledWith("s1", { enabled: true });
    expect(loopClient.runNow).toHaveBeenCalledWith("s1", true);
    // update() must resolve (and thus be observed) before runNow() fires.
    expect(loopClient.update.mock.invocationCallOrder[0]).toBeLessThan(
      loopClient.runNow.mock.invocationCallOrder[0],
    );
  });

  test("no run-now fire when the PATCH itself fails", async () => {
    // Guard the fix: if the enable PATCH fails, we must NOT still try to
    // trigger run-now (that would fire a prompt on a still-paused loop).
    const loopClient = {
      update: jest.fn(async () => {
        throw new Error("update failed");
      }),
      runNow: jest.fn(async () => ({})),
    };

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      loopClient,
      onLoopEnabledChange: () => {},
    });

    await handler();

    expect(loopClient.runNow).not.toHaveBeenCalled();
  });

  test("cap-stopped WITHOUT reset_counters skips the run-now fire", async () => {
    // If the loop was auto-stopped by maxIterations/maxDuration and the
    // user did NOT tick "reset counters", firing run-now would immediately
    // re-stop the loop — surface that as a no-op instead.
    const loopClient = makeLoopClientOK();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "maxIterations",
      resetCounters: false,
      loopClient,
      onLoopEnabledChange: () => {},
    });

    await handler();

    expect(loopClient.update).toHaveBeenCalledWith("s1", { enabled: true });
    expect(loopClient.runNow).not.toHaveBeenCalled();
  });

  test("cap-stopped WITH reset_counters DOES fire run-now (loop can resume)", async () => {
    const loopClient = makeLoopClientOK();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "maxDuration",
      resetCounters: true,
      loopClient,
      onLoopEnabledChange: () => {},
    });

    await handler();

    // And the PATCH body carries reset_counters so the backend clears the cap.
    expect(loopClient.update).toHaveBeenCalledWith("s1", {
      enabled: true,
      reset_counters: true,
    });
    expect(loopClient.runNow).toHaveBeenCalledWith("s1", true);
  });

  test("run-now failure is swallowed silently (loop is already enabled)", async () => {
    // If run-now throws (e.g. a 409 "agent streaming" MittoApiError) or
    // otherwise fails, the dialog should still close and no error should be
    // surfaced — the loop is enabled and the next tick will fire.
    const loopClient = {
      update: jest.fn(async () => ({})),
      runNow: jest.fn(async () => {
        throw new Error("network");
      }),
    };
    const setErrorMessage = jest.fn();
    const setShowRestoreDialog = jest.fn();
    const onLoopEnabledChange = jest.fn();

    const handler = makeHandleConfirmRestore({
      sessionId: "s1",
      stoppedReason: "pausedByUser",
      resetCounters: false,
      loopClient,
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

// =============================================================================
// Tests — regression guard (mitto-987y.7, fixed)
// =============================================================================

describe("toStableTriggersList — mitto-987y.7: unknown triggers must survive save", () => {
  // `onChild` (epic mitto-987y) is not implemented yet on the backend — no
  // occurrences exist in internal/ or web/static/ (confirmed during
  // investigation), so it is used here purely as an opaque, currently-unknown
  // trigger string. The fix is at the frontend/payload boundary: whatever
  // string is armed must round-trip through save, regardless of whether the
  // backend recognizes it today.
  test("fixed: an armed unknown trigger (onChild) survives into the saved list", () => {
    const armed = new Set(["onTasks", "onChild"]);

    const result = toStableTriggersList(armed);

    // Previously (buggy): toStableTriggersList only knew
    // schedule/onCompletion/onTasks, so "onChild" was filtered out.
    expect(result).toContain("onChild");
  });

  test("fixed: PATCH payload.triggers preserves the unknown trigger, keeping it armed", () => {
    // Mirrors the payload shape built in performSave() (LoopFrequencyPanel.js
    // L528-536): { triggers: toStableTriggersList(localTriggers), ... }.
    const localTriggers = new Set(["schedule", "onChild"]);
    const payload = { triggers: toStableTriggersList(localTriggers) };
    const body = JSON.stringify(payload);

    // internal/session/loop.go: a non-nil `triggers` field REPLACES the
    // stored list wholesale, so if "onChild" were missing here it would not
    // be merely absent from this save — it would be disarmed server-side.
    expect(JSON.parse(body).triggers).toContain("onChild");
  });

  test("known triggers keep canonical order alongside a preserved unknown trigger", () => {
    // Fix shape: canonical-first, then append unknowns — so Normalize()
    // still derives the legacy scalar Trigger from Triggers[0] correctly
    // (loop.go L528-529).
    const armed = new Set(["onChild", "onCompletion", "schedule"]);

    const result = toStableTriggersList(armed);

    expect(result).toEqual(["schedule", "onCompletion", "onChild"]);
  });
});
