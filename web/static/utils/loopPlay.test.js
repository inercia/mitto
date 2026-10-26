import { describe, test, expect, jest } from "./testing/testGlobals.js";
import { createLoopPlay } from "./loopPlay.js";
import { MittoApiError, errorFromResponse } from "../sdk/core/errors.js";

const loopState = (exhausted, reason = "disabledByAgent") => ({
  enabled: false,
  stopped_reason: reason,
  max_iterations: 20,
  max_duration_seconds: 14400,
  arguments: { TASK: "preserve" },
  triggers: ["onCompletion", "onTasks"],
  budget: { exhausted, resumable: !exhausted },
});

function fixture(initial = loopState(false)) {
  const loop = {
    get: jest.fn(async () => initial),
    update: jest.fn(async () => ({})),
    runNow: jest.fn(async () => ({})),
  };
  const onEnabled = jest.fn();
  const action = createLoopPlay("s1", () => loop, jest.fn(), onEnabled);
  return { loop, action, onEnabled };
}

const restartRequired = () =>
  errorFromResponse({
    status: 409,
    body: {
      error: {
        code: "loop_restart_required",
        message: "Refresh and confirm Restart with fresh limits.",
      },
    },
  });

describe("shared production Play lifecycle — mitto-wb0", () => {
  for (const reason of ["maxDuration", "maxIterations", "iterationSafeguard"]) {
    test(`${reason}: authoritative exhaustion explicitly resets, even with overwritten reason`, async () => {
      const state = loopState(true);
      state.budget.exhausted_reason = reason;
      const { loop, action, onEnabled } = fixture(state);
      const original = JSON.stringify(state);
      await action.open();
      expect(loop.get).toHaveBeenCalledWith("s1");
      expect(loop.update).not.toHaveBeenCalled();
      expect(action.getState().confirmation.title).toBe(
        "Restart with fresh limits",
      );
      expect(action.getState().confirmation.message).toContain(
        "configured limits and conversation history stay unchanged",
      );
      await action.confirm();
      expect(loop.update).toHaveBeenCalledWith("s1", {
        enabled: true,
        reset_counters: true,
      });
      expect(loop.update).toHaveBeenCalledTimes(1);
      expect(onEnabled).toHaveBeenCalledWith(true);
      expect(loop.runNow).toHaveBeenCalledWith("s1", true);
      expect(loop.runNow).toHaveBeenCalledTimes(1);
      expect(loop.update.mock.invocationCallOrder[0]).toBeLessThan(
        loop.runNow.mock.invocationCallOrder[0],
      );
      expect(action.getState()).toEqual({
        busy: false,
        confirmation: null,
        error: "",
      });
      expect(JSON.stringify(state)).toBe(original);
    });
  }

  test("remaining allowance resumes without reset, even with a misleading cap badge", async () => {
    const { loop, action } = fixture(loopState(false, "maxDuration"));
    await action.open();
    expect(action.getState().confirmation.title).toBe(
      "Resume remaining allowance",
    );
    expect(action.getState().confirmation.message).toContain(
      "will be preserved",
    );
    await action.confirm();
    expect(loop.update).toHaveBeenCalledWith("s1", { enabled: true });
    expect(loop.runNow).toHaveBeenCalledTimes(1);
  });

  test("expired stale confirmation refreshes/reconfirms, never silently escalates reset", async () => {
    const { loop, action, onEnabled } = fixture();
    await action.open();
    loop.update.mockRejectedValueOnce(restartRequired());
    loop.get.mockResolvedValueOnce(loopState(true));
    await action.confirm();
    expect(loop.update).toHaveBeenCalledTimes(1);
    expect(loop.update).toHaveBeenLastCalledWith("s1", { enabled: true });
    expect(loop.get).toHaveBeenCalledTimes(2);
    expect(loop.runNow).not.toHaveBeenCalled();
    expect(onEnabled).not.toHaveBeenCalled();
    expect(action.getState().confirmation.title).toBe(
      "Restart with fresh limits",
    );
    expect(action.getState().confirmation.message).toContain(
      "Review and confirm again",
    );
    await action.confirm(); // A second, explicit user authorization.
    expect(loop.update).toHaveBeenLastCalledWith("s1", {
      enabled: true,
      reset_counters: true,
    });
    expect(loop.runNow).toHaveBeenCalledTimes(1);
  });

  test("stale rejection uses refreshed authority even if another actor leaves remaining allowance", async () => {
    const { loop, action } = fixture();
    await action.open();
    loop.update.mockRejectedValueOnce(restartRequired());
    await action.confirm();
    expect(action.getState().confirmation.title).toBe(
      "Resume remaining allowance",
    );
    expect(loop.update).toHaveBeenCalledTimes(1);
    expect(loop.runNow).not.toHaveBeenCalled();
  });

  for (const message of [
    "Session is currently processing a prompt",
    "Loop bootstrap already dispatched",
  ]) {
    test(`bootstrap/busy 409 is tolerated once without a duplicate run-now: ${message}`, async () => {
      const { loop, action, onEnabled } = fixture();
      await action.open();
      loop.runNow.mockRejectedValueOnce(
        new MittoApiError(message, { status: 409, code: "conflict" }),
      );
      await action.confirm();
      await action.confirm(); // Closed confirmation cannot fire again.
      expect(onEnabled).toHaveBeenCalledTimes(1);
      expect(loop.update).toHaveBeenCalledTimes(1);
      expect(loop.runNow).toHaveBeenCalledTimes(1);
      expect(action.getState().error).toBe("");
    });
  }

  for (const error of [
    new Error("network unavailable"),
    new MittoApiError("Archive restriction", {
      status: 403,
      code: "forbidden",
    }),
    new MittoApiError("Unexpected conflict", { status: 409, code: "conflict" }),
    restartRequired(),
  ]) {
    test(`run-now actionable error is surfaced without retry: ${error.message}`, async () => {
      const { loop, action, onEnabled } = fixture();
      await action.open();
      loop.runNow.mockRejectedValueOnce(error);
      await action.confirm();
      expect(onEnabled).toHaveBeenCalledWith(true);
      expect(action.getState().error).toContain("loop is enabled");
      expect(action.getState().error).toContain(error.message);
      expect(loop.runNow).toHaveBeenCalledTimes(1);
      expect(loop.get).toHaveBeenCalledTimes(1);
      expect(action.getState().confirmation).toBeNull();
    });
  }

  test("failed PATCH surfaces the error and never runs a still-paused loop", async () => {
    const { loop, action, onEnabled } = fixture();
    await action.open();
    loop.update.mockRejectedValueOnce(
      new MittoApiError("Session archived", { status: 403, code: "forbidden" }),
    );
    await action.confirm();
    expect(action.getState().error).toBe("Session archived");
    expect(loop.runNow).not.toHaveBeenCalled();
    expect(onEnabled).not.toHaveBeenCalled();
  });

  test("failed stale-state refresh never resets or dispatches", async () => {
    const { loop, action } = fixture();
    await action.open();
    loop.update.mockRejectedValueOnce(restartRequired());
    loop.get.mockRejectedValueOnce(new Error("Refresh unavailable"));
    await action.confirm();
    expect(action.getState().error).toBe("Refresh unavailable");
    expect(action.getState().confirmation).toBeNull();
    expect(loop.update).toHaveBeenCalledTimes(1);
    expect(loop.runNow).not.toHaveBeenCalled();
  });

  for (const state of [
    { stopped_reason: "maxDuration" },
    { ...loopState(false), enabled: true },
  ]) {
    test("missing authority/already running fails closed before any write", async () => {
      const { loop, action } = fixture(state);
      await action.open();
      expect(action.getState().confirmation).toBeNull();
      expect(action.getState().error).not.toBe("");
      await action.confirm();
      expect(loop.update).not.toHaveBeenCalled();
      expect(loop.runNow).not.toHaveBeenCalled();
    });
  }

  test("cancel grants no allowance and reopening refreshes authoritative state", async () => {
    const { loop, action } = fixture();
    await action.open();
    action.cancel();
    await action.confirm();
    expect(loop.update).not.toHaveBeenCalled();
    loop.get.mockResolvedValueOnce(loopState(true));
    await action.open();
    expect(loop.get).toHaveBeenCalledTimes(2);
    expect(action.getState().confirmation.title).toBe(
      "Restart with fresh limits",
    );
  });

  test("rapid repeated open/confirm cannot duplicate update or initial run-now", async () => {
    const { loop, action } = fixture(loopState(true));
    let releaseGet;
    loop.get.mockReturnValueOnce(
      new Promise((resolve) => {
        releaseGet = resolve;
      }),
    );
    const opening = action.open();
    await action.open();
    expect(loop.get).toHaveBeenCalledTimes(1);
    releaseGet(loopState(true));
    await opening;
    let releaseUpdate;
    loop.update.mockReturnValueOnce(
      new Promise((resolve) => {
        releaseUpdate = resolve;
      }),
    );
    const confirming = action.confirm();
    await action.confirm();
    action.cancel(); // Busy confirmation cannot be dismissed and re-entered.
    expect(action.getState().busy).toBe(true);
    expect(loop.runNow).not.toHaveBeenCalled();
    releaseUpdate({});
    await confirming;
    expect(loop.update).toHaveBeenCalledTimes(1);
    expect(loop.runNow).toHaveBeenCalledTimes(1);
  });

  test("unmounted/session-switched action cannot dispatch after its pending PATCH", async () => {
    const { loop, action, onEnabled } = fixture();
    await action.open();
    let release;
    loop.update.mockReturnValueOnce(
      new Promise((resolve) => {
        release = resolve;
      }),
    );
    const confirming = action.confirm();
    action.dispose();
    release({});
    await confirming;
    expect(onEnabled).not.toHaveBeenCalled();
    expect(loop.runNow).not.toHaveBeenCalled();
  });
});
