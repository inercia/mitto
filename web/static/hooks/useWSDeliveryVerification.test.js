import {
  afterEach,
  beforeEach,
  describe,
  expect,
  jest,
  test,
} from "../utils/testing/testGlobals.js";
import { getPendingPromptsForSession } from "../lib.js";

// Execute the real hook with only Preact's memoization stubbed. No copied
// delivery algorithm: timers and ACK settlement exercise the production path.
const originalPreact = window.preact;
window.preact = { ...originalPreact, useCallback: (fn) => fn };
const { useWSDeliveryVerification } =
  await import("./useWSDeliveryVerification.js");
window.preact = originalPreact;

async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

function harness(isMobileDevice = false) {
  const sessionId = "preflight-session";
  const ws = { state: "open", close: jest.fn() };
  const pendingSendsRef = { current: {} };
  const lastConfirmedPromptRef = { current: {} };
  const sessionWsRefs = { current: { [sessionId]: ws } };
  const sendToSession = jest.fn(() => true);
  const waitForSessionConnection = jest.fn(async () => {
    sessionWsRefs.current[sessionId] = ws;
    return ws;
  });
  const hook = useWSDeliveryVerification({
    activeSessionId: sessionId,
    addMessageToSession: jest.fn(),
    updateLastMessage: jest.fn(),
    clearActionButtons: jest.fn(),
    setSessions: jest.fn(),
    sendToSession,
    waitForSessionConnection,
    isConnectionHealthy: () => true,
    sessionWsRefs,
    isMobileDevice,
    pendingSendsRef,
    lastConfirmedPromptRef,
  });
  const promptId = () => sendToSession.mock.calls[0][1].data.prompt_id;
  const acknowledge = () => {
    const id = promptId();
    const pending = pendingSendsRef.current[id];
    clearTimeout(pending.timeoutId);
    pending.resolve({ success: true, promptId: id });
    delete pendingSendsRef.current[id];
  };
  return {
    ...hook,
    sessionId,
    ws,
    sendToSession,
    waitForSessionConnection,
    pendingSendsRef,
    lastConfirmedPromptRef,
    promptId,
    acknowledge,
  };
}

beforeEach(() => {
  jest.useFakeTimers();
  jest.setSystemTime(1000000);
  localStorage.clear();
});

afterEach(() => {
  jest.useRealTimers();
  localStorage.clear();
});

describe("useWSDeliveryVerification preflight budget", () => {
  for (const mobile of [false, true]) {
    test(`waits for durable ACK through startup + model preflight (mobile=${mobile})`, async () => {
      const h = harness(mobile);
      let settled = false;
      const delivery = h.sendPrompt("prompt", [], [], {
        promptName: "model prompt",
      });
      delivery.then(
        () => {
          settled = true;
        },
        () => {
          settled = true;
        },
      );
      // Covers the former 10s failure, the 90s switch, and most of a preceding
      // 90s startup barrier. No ACK, reconnect or duplicate send is justified.
      for (const elapsed of [10000, 80000, 89000]) {
        jest.advanceTimersByTime(elapsed);
        await flush();
        expect(settled).toBe(false);
        expect(h.ws.close).not.toHaveBeenCalled();
        expect(h.waitForSessionConnection).not.toHaveBeenCalled();
        expect(h.sendToSession).toHaveBeenCalledTimes(1);
        expect(getPendingPromptsForSession(h.sessionId)).toHaveLength(1);
      }
      h.acknowledge();
      await expect(delivery).resolves.toEqual({
        success: true,
        promptId: h.promptId(),
      });
      expect(getPendingPromptsForSession(h.sessionId)).toHaveLength(0);
    });
  }

  test("reconnect verification still resolves a persisted prompt without resending", async () => {
    const h = harness();
    const delivery = h.sendPrompt("prompt");
    h.lastConfirmedPromptRef.current[h.sessionId] = {
      promptId: h.promptId(),
      seq: 7,
    };
    jest.advanceTimersByTime(180000);
    await flush();
    jest.advanceTimersByTime(100);
    await flush();
    await expect(delivery).resolves.toMatchObject({
      success: true,
      verifiedOnReconnect: true,
    });
    expect(h.ws.close).toHaveBeenCalledTimes(1);
    expect(h.sendToSession).toHaveBeenCalledTimes(1);
  });

  test("unconfirmed delivery retries once and fails at the bounded total budget", async () => {
    const h = harness();
    const delivery = h.sendPrompt("prompt");
    let failure;
    const caught = delivery.catch((err) => {
      failure = err;
    });
    jest.advanceTimersByTime(180000);
    await flush();
    expect(h.waitForSessionConnection).toHaveBeenCalledWith(h.sessionId, 4000);
    jest.advanceTimersByTime(100);
    await flush();
    expect(h.sendToSession).toHaveBeenCalledTimes(2);
    expect(h.sendToSession.mock.calls[1][1].data.prompt_id).toBe(h.promptId());
    jest.advanceTimersByTime(9899);
    await flush();
    expect(failure).toBeUndefined();
    jest.advanceTimersByTime(1);
    await caught;
    expect(failure.message).toContain("could not be confirmed after retry");
    expect(Object.keys(h.pendingSendsRef.current)).toHaveLength(0);
    // Failure is not delivery proof: retain the durable local pending copy.
    expect(getPendingPromptsForSession(h.sessionId)).toHaveLength(1);
  });

  test("a prompt-scoped preparation error still rejects immediately", async () => {
    const h = harness();
    const delivery = h.sendPrompt("prompt");
    const pending = h.pendingSendsRef.current[h.promptId()];
    clearTimeout(pending.timeoutId);
    pending.reject(new Error("Failed to send prompt: context canceled"));
    delete h.pendingSendsRef.current[h.promptId()];
    await expect(delivery).rejects.toThrow("context canceled");
    expect(h.waitForSessionConnection).not.toHaveBeenCalled();
    expect(h.sendToSession).toHaveBeenCalledTimes(1);
  });
});
