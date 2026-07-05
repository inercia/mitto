/**
 * Tests for useConversationSeeding.js
 */

import { jest } from "@jest/globals";
import {
  buildSeedQueueBody,
  seedConversationWithPrompt,
  configureLoopSchedule,
  decideLoopAction,
  makeLoopNow,
  useConversationSeeding,
  parseDurationToSeconds,
} from "./useConversationSeeding.js";
import { promptResolveAsLoop } from "../utils/prompts.js";

// Provide a minimal window.preact stub so the module-level destructure doesn't throw.
global.window = global.window || {};
window.preact = { useCallback: (fn) => fn };
window.mittoApiPrefix = "";

// Minimal document.cookie stub for csrf.js
if (typeof document === "undefined") {
  global.document = { cookie: "" };
} else {
  Object.defineProperty(document, "cookie", {
    value: "",
    writable: true,
    configurable: true,
  });
}

afterEach(() => {
  jest.restoreAllMocks();
});

// =============================================================================
// buildSeedQueueBody
// =============================================================================

describe("buildSeedQueueBody", () => {
  const prompt = { name: "my-prompt", prompt: "DO NOT INCLUDE THIS" };

  test("returns { prompt_name } from prompt.name", () => {
    const body = buildSeedQueueBody(prompt);
    expect(body.prompt_name).toBe("my-prompt");
  });

  test("never includes message field", () => {
    const body = buildSeedQueueBody(prompt);
    expect(body).not.toHaveProperty("message");
  });

  test("never includes prompt.prompt body", () => {
    const body = buildSeedQueueBody(prompt);
    expect(JSON.stringify(body)).not.toContain("DO NOT INCLUDE THIS");
  });

  test("includes arguments when provided as non-empty object", () => {
    const body = buildSeedQueueBody(prompt, { arguments: { key: "val" } });
    expect(body.arguments).toEqual({ key: "val" });
  });

  test("omits arguments when not provided", () => {
    const body = buildSeedQueueBody(prompt);
    expect(body).not.toHaveProperty("arguments");
  });

  test("omits arguments when empty object", () => {
    const body = buildSeedQueueBody(prompt, { arguments: {} });
    expect(body).not.toHaveProperty("arguments");
  });
});

// =============================================================================
// seedConversationWithPrompt
// =============================================================================

describe("seedConversationWithPrompt", () => {
  const prompt = { name: "test-prompt", prompt: "FULL BODY TEXT" };

  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("returns invalid_request when sessionId is missing", async () => {
    const result = await seedConversationWithPrompt(null, prompt);
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("returns invalid_request when prompt.name is missing", async () => {
    const result = await seedConversationWithPrompt("sess-1", {
      prompt: "body",
    });
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("POSTs to correct URL with prompt_name and no message field", async () => {
    const fetchImpl = makeFetch(201, { id: "msg-abc" });
    const result = await seedConversationWithPrompt("sess-1", prompt, {
      fetchImpl,
    });

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const [url, opts] = fetchImpl.mock.calls[0];
    expect(url).toContain("/api/sessions/sess-1/queue");
    expect(opts.method).toBe("POST");

    const sentBody = JSON.parse(opts.body);
    expect(sentBody.prompt_name).toBe("test-prompt");
    expect(sentBody).not.toHaveProperty("message");
    expect(JSON.stringify(sentBody)).not.toContain("FULL BODY TEXT");

    expect(result).toEqual({ success: true, messageId: "msg-abc" });
  });

  test("includes arguments in body when provided", async () => {
    const fetchImpl = makeFetch(200, { id: "msg-xyz" });
    await seedConversationWithPrompt("sess-1", prompt, {
      arguments: { foo: "bar" },
      fetchImpl,
    });
    const sentBody = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(sentBody.arguments).toEqual({ foo: "bar" });
  });

  test("returns success:false on non-ok response", async () => {
    const fetchImpl = makeFetch(400, { error: "bad_request" });
    const result = await seedConversationWithPrompt("sess-1", prompt, {
      fetchImpl,
    });
    expect(result.success).toBe(false);
    expect(result.error).toBe("bad_request");
  });

  test("returns success:false with request_failed on network error", async () => {
    const fetchImpl = jest.fn(() =>
      Promise.reject(new Error("network failure")),
    );
    const result = await seedConversationWithPrompt("sess-1", prompt, {
      fetchImpl,
    });
    expect(result).toEqual({ success: false, error: "request_failed" });
  });

  test("returns success:true on 200 response", async () => {
    const fetchImpl = makeFetch(200, { id: "msg-200" });
    const result = await seedConversationWithPrompt("sess-1", prompt, {
      fetchImpl,
    });
    expect(result).toEqual({ success: true, messageId: "msg-200" });
  });
});

// =============================================================================
// useConversationSeeding — startConversationWithPrompt (single-call path)
// =============================================================================

describe("useConversationSeeding — startConversationWithPrompt", () => {
  test("calls newSession with initialPromptName and arguments, returns sessionId", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-9" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      arguments: { ISSUE_ID: "mitto-1" },
      workingDir: "/w",
      acpServer: "X",
      name: "N",
      beadsIssue: "mitto-1",
    });

    expect(newSession).toHaveBeenCalledTimes(1);
    const callArg = newSession.mock.calls[0][0];
    expect(callArg.initialPromptName).toBe("p1");
    expect(callArg.arguments).toEqual({ ISSUE_ID: "mitto-1" });
    expect(result).toEqual({ sessionId: "sess-9", reused: false });
  });

  test("passes workingDir, acpServer, name, beadsIssue through to newSession", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-9" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "p1" },
      workingDir: "/my/dir",
      acpServer: "auggie",
      name: "MyConvo",
      beadsIssue: "mitto-42",
    });

    const callArg = newSession.mock.calls[0][0];
    expect(callArg.workingDir).toBe("/my/dir");
    expect(callArg.acpServer).toBe("auggie");
    expect(callArg.name).toBe("MyConvo");
    expect(callArg.beadsIssue).toBe("mitto-42");
  });

  test("forwards originPromptName (= prompt.name) to newSession", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-9" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "Reevaluate all issues" },
      workingDir: "/w",
    });

    const callArg = newSession.mock.calls[0][0];
    expect(callArg.originPromptName).toBe("Reevaluate all issues");
  });

  test("does NOT call seedConversationWithPrompt — single-call path only invokes newSession", async () => {
    // The new implementation calls newSession only; it does NOT call seedConversationWithPrompt.
    // We verify this by confirming newSession is the sole mock and the result is clean (no seedError).
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-9" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      workingDir: "/w",
    });

    // Single call to newSession, no extra calls
    expect(newSession).toHaveBeenCalledTimes(1);
    // Result has sessionId and no seedError (old two-call path would set seedError on failure)
    expect(result).toEqual({ sessionId: "sess-9", reused: false });
    expect(result).not.toHaveProperty("seedError");
  });

  test("returns error when newSession returns no sessionId", async () => {
    const newSession = jest.fn().mockResolvedValue({ error: "boom" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      workingDir: "/w",
    });

    expect(result).toEqual({ error: "boom" });
  });

  test("returns session_creation_failed when newSession returns empty object", async () => {
    const newSession = jest.fn().mockResolvedValue({});
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
    });

    expect(result).toEqual({ error: "session_creation_failed" });
  });

  test("surfaces reused:true from newSession result", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-9", reused: true });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });
    const result = await startConversationWithPrompt({
      workingDir: "/x",
      acpServer: "acp",
      name: "n",
      prompt: { name: "p" },
    });
    expect(result).toEqual({ sessionId: "sess-9", reused: true });
  });
});

// =============================================================================
// configureLoopSchedule
// =============================================================================

describe("configureLoopSchedule", () => {
  const prompt = { name: "daily-standup" };

  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("PUTs to /api/sessions/{id}/loop with correct body for hours", async () => {
    const fetchImpl = makeFetch(200, {});
    await configureLoopSchedule(
      "sess-1",
      prompt,
      { value: 2, unit: "hours" },
      { fetchImpl },
    );

    const [url, opts] = fetchImpl.mock.calls[0];
    expect(url).toContain("/api/sessions/sess-1/loop");
    expect(opts.method).toBe("PUT");

    const body = JSON.parse(opts.body);
    expect(body.prompt_name).toBe("daily-standup");
    expect(body.enabled).toBe(true);
    expect(body.frequency.value).toBe(2);
    expect(body.frequency.unit).toBe("hours");
    expect(body.frequency).not.toHaveProperty("at");
  });

  test("includes 'at' in frequency only for days unit", async () => {
    const fetchImpl = makeFetch(200, {});
    await configureLoopSchedule(
      "sess-2",
      prompt,
      { value: 1, unit: "days", at: "09:00" },
      { fetchImpl },
    );

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.frequency.unit).toBe("days");
    expect(body.frequency.at).toBe("09:00");
  });

  test("omits 'at' for minutes unit even when provided", async () => {
    const fetchImpl = makeFetch(200, {});
    await configureLoopSchedule(
      "sess-3",
      prompt,
      { value: 30, unit: "minutes", at: "09:00" },
      { fetchImpl },
    );

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.frequency.unit).toBe("minutes");
    expect(body.frequency).not.toHaveProperty("at");
  });

  test("returns success:true on 200 response", async () => {
    const fetchImpl = makeFetch(200, {});
    const result = await configureLoopSchedule(
      "sess-4",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    expect(result).toEqual({ success: true });
  });

  test("returns success:false with loop_setup_failed on non-ok response", async () => {
    const fetchImpl = makeFetch(400, { error: "bad_request" });
    const result = await configureLoopSchedule(
      "sess-5",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    expect(result.success).toBe(false);
    expect(result.error).toBeDefined();
  });

  test("returns success:false on network error", async () => {
    const fetchImpl = jest.fn(() => Promise.reject(new Error("net fail")));
    const result = await configureLoopSchedule(
      "sess-6",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    expect(result.success).toBe(false);
    expect(result.error).toBe("loop_setup_failed");
  });
});

// =============================================================================
// useConversationSeeding — startConversationWithPrompt loop path
// =============================================================================

describe("useConversationSeeding — startConversationWithPrompt loop path", () => {
  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("loop: does NOT pass initialPromptName to newSession", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-loop" });
    const fetchImpl = makeFetch(200, {});
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      loop: { value: 1, unit: "hours" },
      fetchImpl,
    });

    const callArg = newSession.mock.calls[0][0];
    expect(callArg).not.toHaveProperty("initialPromptName");
    expect(callArg).not.toHaveProperty("arguments");
  });

  test("loop: PUTs loop config after session creation", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-loop" });
    const fetchImpl = makeFetch(200, {});
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      loop: { value: 1, unit: "days", at: "09:00" },
      fetchImpl,
    });

    expect(newSession).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    const [url, opts] = fetchImpl.mock.calls[0];
    expect(url).toContain("/api/sessions/sess-loop/loop");
    expect(opts.method).toBe("PUT");

    const body = JSON.parse(opts.body);
    expect(body.prompt_name).toBe("daily-standup");
    expect(body.enabled).toBe(true);
    expect(body.frequency.at).toBe("09:00");

    expect(result).toEqual({ sessionId: "sess-loop", reused: false });
  });

  test("loop: returns error if loop PUT fails", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-fail" });
    const fetchImpl = makeFetch(500, { error: "server_error" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      workingDir: "/w",
      loop: { value: 1, unit: "hours" },
      fetchImpl,
    });

    expect(result.error).toBeDefined();
    expect(result).not.toHaveProperty("sessionId");
  });

  test("non-loop: still passes initialPromptName (unchanged behavior)", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-one-time" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      arguments: { X: "y" },
      workingDir: "/w",
    });

    const callArg = newSession.mock.calls[0][0];
    expect(callArg.initialPromptName).toBe("p1");
    expect(callArg.arguments).toEqual({ X: "y" });
    expect(result).toEqual({ sessionId: "sess-one-time", reused: false });
  });
});

// =============================================================================
// decideLoopAction
// =============================================================================

describe("decideLoopAction", () => {
  test("returns new-loop when session is null", () => {
    expect(decideLoopAction(null)).toBe("new-loop");
  });

  test("returns new-loop when session is undefined", () => {
    expect(decideLoopAction(undefined)).toBe("new-loop");
  });

  test("returns new-loop when session has no session_id", () => {
    expect(decideLoopAction({ name: "foo" })).toBe("new-loop");
  });

  test("returns one-shot when session is loop_enabled", () => {
    expect(
      decideLoopAction({ session_id: "s1", loop_enabled: true }),
    ).toBe("one-shot");
  });

  test("returns one-shot when session is loop_configured (but not enabled)", () => {
    expect(
      decideLoopAction({ session_id: "s1", loop_configured: true }),
    ).toBe("one-shot");
  });

  test("returns one-shot when session has parent_session_id (child conversation)", () => {
    expect(
      decideLoopAction({ session_id: "s1", parent_session_id: "parent-1" }),
    ).toBe("one-shot");
  });

  test("returns make-loop for a regular running conversation", () => {
    expect(decideLoopAction({ session_id: "s1" })).toBe("make-loop");
  });

  test("returns make-loop even when loop_enabled is false/undefined", () => {
    expect(
      decideLoopAction({ session_id: "s1", loop_enabled: false }),
    ).toBe("make-loop");
  });
});

// =============================================================================
// makeLoopNow
// =============================================================================

describe("makeLoopNow", () => {
  const prompt = {
    name: "daily-standup",
    loop: { value: 1, unit: "hours", maxIterations: 5 },
  };

  function makeFetchSequence(...responses) {
    let i = 0;
    return jest.fn(() => {
      const r = responses[i++] || responses[responses.length - 1];
      return Promise.resolve(r);
    });
  }

  function makeResp(status, data = {}) {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(data),
    };
  }

  test("returns invalid_request when sessionId is missing", async () => {
    const result = await makeLoopNow(null, prompt);
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("returns invalid_request when prompt.name is missing", async () => {
    const result = await makeLoopNow("sess-1", { loop: {} });
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("PUTs loop config with correct body including max_iterations", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const [putUrl, putOpts] = fetchImpl.mock.calls[0];
    expect(putUrl).toContain("/api/sessions/sess-1/loop");
    expect(putOpts.method).toBe("PUT");

    const body = JSON.parse(putOpts.body);
    expect(body.prompt_name).toBe("daily-standup");
    expect(body.enabled).toBe(true);
    expect(body.frequency.value).toBe(1);
    expect(body.frequency.unit).toBe("hours");
    expect(body.max_iterations).toBe(5);
  });

  test("POSTs run-now with reset_timer:true after successful PUT", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    const result = await makeLoopNow("sess-1", prompt, { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledTimes(2);
    const [runUrl, runOpts] = fetchImpl.mock.calls[1];
    expect(runUrl).toContain("/api/sessions/sess-1/loop/run-now");
    expect(runOpts.method).toBe("POST");

    const runBody = JSON.parse(runOpts.body);
    expect(runBody.reset_timer).toBe(true);

    expect(result).toEqual({ success: true });
  });

  test("does NOT call run-now when PUT fails", async () => {
    const fetchImpl = makeFetchSequence(
      makeResp(500, { error: "server_error" }),
    );
    const result = await makeLoopNow("sess-1", prompt, { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(result.success).toBe(false);
    expect(result.error).toBeDefined();
  });

  test("includes 'at' in frequency only for days unit", async () => {
    const promptWithDays = {
      name: "daily-report",
      loop: { value: 1, unit: "days", at: "09:00", maxIterations: 0 },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-2", promptWithDays, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.frequency.at).toBe("09:00");
  });

  test("sends max_iterations:0 when prompt has no maxIterations", async () => {
    const noMaxPrompt = {
      name: "simple",
      loop: { value: 2, unit: "hours" },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-3", noMaxPrompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(0);
  });

  test("returns error when run-now fails", async () => {
    const fetchImpl = makeFetchSequence(
      makeResp(200),
      makeResp(500, { error: "server_error" }),
    );
    const result = await makeLoopNow("sess-4", prompt, { fetchImpl });
    expect(result.success).toBe(false);
    expect(result.error).toBeDefined();
  });

  test("treats run-now 409 (session busy) as success after PUT succeeds", async () => {
    // The PUT already persisted the loop config; a 409 means a run is already
    // in flight (e.g. enabling a schedule fired its first run). Not a failure.
    const fetchImpl = makeFetchSequence(
      makeResp(200),
      makeResp(409, { error: "busy" }),
    );
    const result = await makeLoopNow("sess-5", prompt, { fetchImpl });
    expect(result).toEqual({ success: true });
  });
});

// =============================================================================
// configureLoopSchedule — max_iterations support
// =============================================================================

describe("configureLoopSchedule — max_iterations", () => {
  const prompt = { name: "my-prompt", loop: { maxIterations: 10 } };

  function makeFetch(status) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve({}),
      }),
    );
  }

  test("includes max_iterations from loop.maxIterations when positive", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p" },
      { value: 1, unit: "hours", maxIterations: 7 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(7);
  });

  test("falls back to prompt.loop.maxIterations when loop.maxIterations is absent", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(10);
  });

  test("sends max_iterations:0 when both are absent/zero (unlimited)", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p" },
      { value: 1, unit: "hours", maxIterations: 0 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(0);
  });

  test("loop.maxIterations takes priority over prompt.loop.maxIterations", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours", maxIterations: 3 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(3);
  });
});

// =============================================================================
// ChatInput.handlePredefinedPrompt routing — loop branch
//
// Tests the pure routing decision: when a prompt has .loop set and
// onLoopPrompt is provided, it must be called; otherwise the normal path runs.
// This mirrors what ChatInput.handlePredefinedPrompt does after the shiftKey check.
// =============================================================================

describe("ChatInput loop routing — onLoopPrompt delegation", () => {
  /**
   * Minimal simulation of the ChatInput.handlePredefinedPrompt routing logic.
   * Extracted here so we can test without mounting the full ChatInput component.
   * Mirrors the real code: const asLoop = prompt && promptResolveAsLoop(prompt);
   * if (asLoop && onLoopPrompt) { onLoopPrompt(prompt); return; } (mitto-92x.3).
   */
  function routePrompt(prompt, { onLoopPrompt, onSend } = {}) {
    const asLoop = prompt && promptResolveAsLoop(prompt);
    if (asLoop && onLoopPrompt) {
      onLoopPrompt(prompt);
      return "loop";
    }
    if (onSend && prompt?.name) {
      onSend(prompt.name);
      return "send";
    }
    return "noop";
  }

  test("calls onLoopPrompt for a loop-flagged prompt", () => {
    const onLoopPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = {
      name: "daily-standup",
      loop: { value: 1, unit: "hours" },
    };

    const result = routePrompt(prompt, { onLoopPrompt, onSend });

    expect(onLoopPrompt).toHaveBeenCalledTimes(1);
    expect(onLoopPrompt).toHaveBeenCalledWith(prompt);
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("loop");
  });

  test("does NOT call onLoopPrompt for a non-loop prompt — falls through to onSend", () => {
    const onLoopPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = { name: "regular-prompt", prompt: "do something" };

    const result = routePrompt(prompt, { onLoopPrompt, onSend });

    expect(onLoopPrompt).not.toHaveBeenCalled();
    expect(onSend).toHaveBeenCalledWith("regular-prompt");
    expect(result).toBe("send");
  });

  test("falls through to onSend when onLoopPrompt is absent (even for loop prompt)", () => {
    const onSend = jest.fn();
    const prompt = { name: "daily", loop: { value: 1, unit: "hours" } };

    const result = routePrompt(prompt, { onSend });

    expect(onSend).toHaveBeenCalledWith("daily");
    expect(result).toBe("send");
  });

  test("does nothing when prompt has no name and no loop", () => {
    const onLoopPrompt = jest.fn();
    const onSend = jest.fn();

    const result = routePrompt({}, { onLoopPrompt, onSend });

    expect(onLoopPrompt).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("noop");
  });

  // mitto-92x.3: routing now flows through promptResolveAsLoop (mode-aware),
  // not a bare `prompt.loop` presence check.
  test("mode: always (no explicit mode) routes to onLoopPrompt — unchanged behavior", () => {
    const onLoopPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = { name: "daily", loop: { value: 1, unit: "hours" } };

    const result = routePrompt(prompt, { onLoopPrompt, onSend });

    expect(onLoopPrompt).toHaveBeenCalledWith(prompt);
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("loop");
  });

  test("mode: optional, default:false resolves to one-shot — falls through to onSend (no override in ChatInput yet)", () => {
    const onLoopPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = {
      name: "maybe-loop",
      loop: { mode: "optional", default: false },
    };

    const result = routePrompt(prompt, { onLoopPrompt, onSend });

    expect(onLoopPrompt).not.toHaveBeenCalled();
    expect(onSend).toHaveBeenCalledWith("maybe-loop");
    expect(result).toBe("send");
  });
});

// =============================================================================
// parseDurationToSeconds
// =============================================================================

describe("parseDurationToSeconds", () => {
  test("number → clamped to >= 0 seconds", () => {
    expect(parseDurationToSeconds(120)).toBe(120);
    expect(parseDurationToSeconds(0)).toBe(0);
    expect(parseDurationToSeconds(-5)).toBe(0);
  });

  test("'30s' → 30 seconds", () => {
    expect(parseDurationToSeconds("30s")).toBe(30);
  });

  test("'30m' → 1800 seconds", () => {
    expect(parseDurationToSeconds("30m")).toBe(1800);
  });

  test("'2h' → 7200 seconds", () => {
    expect(parseDurationToSeconds("2h")).toBe(7200);
  });

  test("'1d' → 86400 seconds", () => {
    expect(parseDurationToSeconds("1d")).toBe(86400);
  });

  test("case-insensitive: '4H' → 14400", () => {
    expect(parseDurationToSeconds("4H")).toBe(14400);
  });

  test("undefined → 0", () => {
    expect(parseDurationToSeconds(undefined)).toBe(0);
  });

  test("null → 0", () => {
    expect(parseDurationToSeconds(null)).toBe(0);
  });

  test("empty string → 0", () => {
    expect(parseDurationToSeconds("")).toBe(0);
  });

  test("invalid string → 0", () => {
    expect(parseDurationToSeconds("bad")).toBe(0);
    expect(parseDurationToSeconds("2 hours")).toBe(0);
    expect(parseDurationToSeconds("1.5h")).toBe(0);
  });
});

// =============================================================================
// configureLoopSchedule — trigger/delay/maxDuration fields
// =============================================================================

describe("configureLoopSchedule — trigger/delay/maxDuration fields", () => {
  const prompt = { name: "my-prompt" };

  function makeFetch(status) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve({}),
      }),
    );
  }

  test("includes trigger, delay_seconds, max_duration_seconds in PUT body", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      {
        value: 1,
        unit: "hours",
        trigger: "onCompletion",
        delaySeconds: 10,
        maxDurationSeconds: 3600,
      },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onCompletion");
    expect(body.delay_seconds).toBe(10);
    expect(body.max_duration_seconds).toBe(3600);
  });

  test("defaults trigger to 'schedule' and delay/maxDuration to 0 when absent", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("schedule");
    expect(body.delay_seconds).toBe(0);
    expect(body.max_duration_seconds).toBe(0);
  });

  test("falls back to prompt.loop.trigger when loop.trigger absent", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { trigger: "onCompletion" } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onCompletion");
  });

  test("falls back to prompt.loop.delay for delay_seconds when absent from loop", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { delay: 15 } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.delay_seconds).toBe(15);
  });

  test("parses prompt.loop.maxDuration string ('2h') into max_duration_seconds", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { maxDuration: "2h" } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_duration_seconds).toBe(7200);
  });

  test("loop.maxDurationSeconds takes priority over prompt.loop.maxDuration", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { maxDuration: "2h" } },
      { value: 1, unit: "hours", maxDurationSeconds: 300 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_duration_seconds).toBe(300);
  });
});

// =============================================================================
// configureLoopSchedule — onTasks condition field (mitto-pei)
// =============================================================================

describe("configureLoopSchedule — onTasks condition field", () => {
  function makeFetch(status) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve({}),
      }),
    );
  }

  test("trigger=onTasks: falls back to prompt.loop.condition when dialog result has none", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { trigger: "onTasks", condition: "size(x) > 0" } },
      { value: 1, unit: "hours", trigger: "onTasks" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onTasks");
    expect(body.condition).toBe("size(x) > 0");
  });

  test("trigger=onTasks: dialog loop.condition overrides the prompt default", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { trigger: "onTasks", condition: "prompt default" } },
      {
        value: 1,
        unit: "hours",
        trigger: "onTasks",
        condition: "dialog override",
      },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.condition).toBe("dialog override");
  });

  test("non-onTasks trigger: condition is not sent even if prompt declares one", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { trigger: "onTasks", condition: "size(x) > 0" } },
      { value: 1, unit: "hours", trigger: "schedule" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("condition");
  });

  test("trigger=onTasks with no condition anywhere: sends empty string", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p" },
      { value: 1, unit: "hours", trigger: "onTasks" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.condition).toBe("");
  });

  test("conditionPreset is never sent (out of scope for mitto-pei)", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      { name: "p", loop: { trigger: "onTasks", condition: "size(x) > 0" } },
      { value: 1, unit: "hours", trigger: "onTasks" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("condition_preset");
    expect(body).not.toHaveProperty("conditionPreset");
  });
});

// =============================================================================
// makeLoopNow — trigger/delay/maxDuration fields
// =============================================================================

describe("makeLoopNow — trigger/delay/maxDuration fields", () => {
  function makeFetchSequence(...responses) {
    let i = 0;
    return jest.fn(() => {
      const r = responses[i++] || responses[responses.length - 1];
      return Promise.resolve(r);
    });
  }

  function makeResp(status, data = {}) {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(data),
    };
  }

  test("includes trigger from prompt.loop in PUT body", async () => {
    const prompt = {
      name: "p",
      loop: {
        value: 1,
        unit: "hours",
        trigger: "onCompletion",
        delay: 10,
        maxDuration: "1h",
      },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onCompletion");
    expect(body.delay_seconds).toBe(10);
    expect(body.max_duration_seconds).toBe(3600);
  });

  test("defaults trigger to 'schedule' and delay/maxDuration to 0 when absent", async () => {
    const prompt = { name: "p", loop: { value: 1, unit: "hours" } };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("schedule");
    expect(body.delay_seconds).toBe(0);
    expect(body.max_duration_seconds).toBe(0);
  });
});

// =============================================================================
// makeLoopNow — onTasks condition field (mitto-pei)
// =============================================================================

describe("makeLoopNow — onTasks condition field", () => {
  function makeFetchSequence(...responses) {
    let i = 0;
    return jest.fn(() => {
      const r = responses[i++] || responses[responses.length - 1];
      return Promise.resolve(r);
    });
  }

  function makeResp(status, data = {}) {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(data),
    };
  }

  test("trigger=onTasks: includes condition from prompt.loop.condition", async () => {
    const prompt = {
      name: "p",
      loop: {
        value: 1,
        unit: "hours",
        trigger: "onTasks",
        condition: "size(x) > 0",
      },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onTasks");
    expect(body.condition).toBe("size(x) > 0");
  });

  test("non-onTasks trigger: condition is not sent even if prompt declares one", async () => {
    const prompt = {
      name: "p",
      loop: {
        value: 1,
        unit: "hours",
        trigger: "schedule",
        condition: "size(x) > 0",
      },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("condition");
  });

  test("trigger=onTasks with no prompt condition: sends empty string", async () => {
    const prompt = {
      name: "p",
      loop: { value: 1, unit: "hours", trigger: "onTasks" },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.condition).toBe("");
  });
});

// =============================================================================
// makeLoopNow — arguments forwarding
// =============================================================================

describe("makeLoopNow — arguments forwarding", () => {
  const prompt = {
    name: "daily-standup",
    loop: { value: 1, unit: "hours" },
  };

  function makeFetchSequence(...responses) {
    let i = 0;
    return jest.fn(() => {
      const r = responses[i++] || responses[responses.length - 1];
      return Promise.resolve(r);
    });
  }

  function makeResp(status, data = {}) {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(data),
    };
  }

  test("includes arguments in PUT body when non-empty map is supplied", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, {
      arguments: { ENV: "prod", REGION: "us-east" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.arguments).toEqual({ ENV: "prod", REGION: "us-east" });
  });

  test("omits arguments from PUT body when empty object is supplied", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { arguments: {}, fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });

  test("omits arguments from PUT body when undefined (no opts supplied)", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makeLoopNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });
});

// =============================================================================
// configureLoopSchedule — arguments forwarding
// =============================================================================

describe("configureLoopSchedule — arguments forwarding", () => {
  const prompt = { name: "daily-standup" };

  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("includes arguments in PUT body when non-empty map is supplied", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours" },
      { arguments: { KEY: "val" }, fetchImpl },
    );

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.arguments).toEqual({ KEY: "val" });
  });

  test("omits arguments from PUT body when empty object is supplied", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours" },
      { arguments: {}, fetchImpl },
    );

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });

  test("omits arguments from PUT body when not supplied", async () => {
    const fetchImpl = makeFetch(200);
    await configureLoopSchedule(
      "s1",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });
});

// =============================================================================
// startConversationWithPrompt loop path — arguments forwarding
// =============================================================================

describe("useConversationSeeding — startConversationWithPrompt loop path — arguments", () => {
  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("loop: forwards arguments into the PUT body when supplied", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      loop: { value: 1, unit: "hours" },
      arguments: { TEAM: "backend" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.arguments).toEqual({ TEAM: "backend" });
  });

  test("loop: omits arguments from PUT body when not supplied", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      loop: { value: 1, unit: "hours" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });

  test("loop: omits arguments from PUT body when empty object", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      loop: { value: 1, unit: "hours" },
      arguments: {},
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });
});
