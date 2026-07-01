/**
 * Tests for useConversationSeeding.js
 */

import { jest } from "@jest/globals";
import {
  buildSeedQueueBody,
  seedConversationWithPrompt,
  configurePeriodicSchedule,
  decidePeriodicAction,
  makePeriodicNow,
  useConversationSeeding,
  parseDurationToSeconds,
} from "./useConversationSeeding.js";
import { promptResolveAsPeriodic } from "../utils/prompts.js";

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
// configurePeriodicSchedule
// =============================================================================

describe("configurePeriodicSchedule", () => {
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

  test("PUTs to /api/sessions/{id}/periodic with correct body for hours", async () => {
    const fetchImpl = makeFetch(200, {});
    await configurePeriodicSchedule(
      "sess-1",
      prompt,
      { value: 2, unit: "hours" },
      { fetchImpl },
    );

    const [url, opts] = fetchImpl.mock.calls[0];
    expect(url).toContain("/api/sessions/sess-1/periodic");
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
    await configurePeriodicSchedule(
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
    await configurePeriodicSchedule(
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
    const result = await configurePeriodicSchedule(
      "sess-4",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    expect(result).toEqual({ success: true });
  });

  test("returns success:false with periodic_setup_failed on non-ok response", async () => {
    const fetchImpl = makeFetch(400, { error: "bad_request" });
    const result = await configurePeriodicSchedule(
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
    const result = await configurePeriodicSchedule(
      "sess-6",
      prompt,
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    expect(result.success).toBe(false);
    expect(result.error).toBe("periodic_setup_failed");
  });
});

// =============================================================================
// useConversationSeeding — startConversationWithPrompt periodic path
// =============================================================================

describe("useConversationSeeding — startConversationWithPrompt periodic path", () => {
  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("periodic: does NOT pass initialPromptName to newSession", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-periodic" });
    const fetchImpl = makeFetch(200, {});
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      periodic: { value: 1, unit: "hours" },
      fetchImpl,
    });

    const callArg = newSession.mock.calls[0][0];
    expect(callArg).not.toHaveProperty("initialPromptName");
    expect(callArg).not.toHaveProperty("arguments");
  });

  test("periodic: PUTs periodic config after session creation", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "sess-periodic" });
    const fetchImpl = makeFetch(200, {});
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      periodic: { value: 1, unit: "days", at: "09:00" },
      fetchImpl,
    });

    expect(newSession).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    const [url, opts] = fetchImpl.mock.calls[0];
    expect(url).toContain("/api/sessions/sess-periodic/periodic");
    expect(opts.method).toBe("PUT");

    const body = JSON.parse(opts.body);
    expect(body.prompt_name).toBe("daily-standup");
    expect(body.enabled).toBe(true);
    expect(body.frequency.at).toBe("09:00");

    expect(result).toEqual({ sessionId: "sess-periodic", reused: false });
  });

  test("periodic: returns error if periodic PUT fails", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-fail" });
    const fetchImpl = makeFetch(500, { error: "server_error" });
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    const result = await startConversationWithPrompt({
      prompt: { name: "p1" },
      workingDir: "/w",
      periodic: { value: 1, unit: "hours" },
      fetchImpl,
    });

    expect(result.error).toBeDefined();
    expect(result).not.toHaveProperty("sessionId");
  });

  test("non-periodic: still passes initialPromptName (unchanged behavior)", async () => {
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
// decidePeriodicAction
// =============================================================================

describe("decidePeriodicAction", () => {
  test("returns new-periodic when session is null", () => {
    expect(decidePeriodicAction(null)).toBe("new-periodic");
  });

  test("returns new-periodic when session is undefined", () => {
    expect(decidePeriodicAction(undefined)).toBe("new-periodic");
  });

  test("returns new-periodic when session has no session_id", () => {
    expect(decidePeriodicAction({ name: "foo" })).toBe("new-periodic");
  });

  test("returns one-shot when session is periodic_enabled", () => {
    expect(
      decidePeriodicAction({ session_id: "s1", periodic_enabled: true }),
    ).toBe("one-shot");
  });

  test("returns one-shot when session is periodic_configured (but not enabled)", () => {
    expect(
      decidePeriodicAction({ session_id: "s1", periodic_configured: true }),
    ).toBe("one-shot");
  });

  test("returns one-shot when session has parent_session_id (child conversation)", () => {
    expect(
      decidePeriodicAction({ session_id: "s1", parent_session_id: "parent-1" }),
    ).toBe("one-shot");
  });

  test("returns make-periodic for a regular running conversation", () => {
    expect(decidePeriodicAction({ session_id: "s1" })).toBe("make-periodic");
  });

  test("returns make-periodic even when periodic_enabled is false/undefined", () => {
    expect(
      decidePeriodicAction({ session_id: "s1", periodic_enabled: false }),
    ).toBe("make-periodic");
  });
});

// =============================================================================
// makePeriodicNow
// =============================================================================

describe("makePeriodicNow", () => {
  const prompt = {
    name: "daily-standup",
    periodic: { value: 1, unit: "hours", maxIterations: 5 },
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
    const result = await makePeriodicNow(null, prompt);
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("returns invalid_request when prompt.name is missing", async () => {
    const result = await makePeriodicNow("sess-1", { periodic: {} });
    expect(result).toEqual({ success: false, error: "invalid_request" });
  });

  test("PUTs periodic config with correct body including max_iterations", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-1", prompt, { fetchImpl });

    const [putUrl, putOpts] = fetchImpl.mock.calls[0];
    expect(putUrl).toContain("/api/sessions/sess-1/periodic");
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
    const result = await makePeriodicNow("sess-1", prompt, { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledTimes(2);
    const [runUrl, runOpts] = fetchImpl.mock.calls[1];
    expect(runUrl).toContain("/api/sessions/sess-1/periodic/run-now");
    expect(runOpts.method).toBe("POST");

    const runBody = JSON.parse(runOpts.body);
    expect(runBody.reset_timer).toBe(true);

    expect(result).toEqual({ success: true });
  });

  test("does NOT call run-now when PUT fails", async () => {
    const fetchImpl = makeFetchSequence(
      makeResp(500, { error: "server_error" }),
    );
    const result = await makePeriodicNow("sess-1", prompt, { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(result.success).toBe(false);
    expect(result.error).toBeDefined();
  });

  test("includes 'at' in frequency only for days unit", async () => {
    const promptWithDays = {
      name: "daily-report",
      periodic: { value: 1, unit: "days", at: "09:00", maxIterations: 0 },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-2", promptWithDays, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.frequency.at).toBe("09:00");
  });

  test("sends max_iterations:0 when prompt has no maxIterations", async () => {
    const noMaxPrompt = {
      name: "simple",
      periodic: { value: 2, unit: "hours" },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-3", noMaxPrompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(0);
  });

  test("returns error when run-now fails", async () => {
    const fetchImpl = makeFetchSequence(
      makeResp(200),
      makeResp(500, { error: "server_error" }),
    );
    const result = await makePeriodicNow("sess-4", prompt, { fetchImpl });
    expect(result.success).toBe(false);
    expect(result.error).toBeDefined();
  });

  test("treats run-now 409 (session busy) as success after PUT succeeds", async () => {
    // The PUT already persisted the periodic config; a 409 means a run is already
    // in flight (e.g. enabling a schedule fired its first run). Not a failure.
    const fetchImpl = makeFetchSequence(
      makeResp(200),
      makeResp(409, { error: "busy" }),
    );
    const result = await makePeriodicNow("sess-5", prompt, { fetchImpl });
    expect(result).toEqual({ success: true });
  });
});

// =============================================================================
// configurePeriodicSchedule — max_iterations support
// =============================================================================

describe("configurePeriodicSchedule — max_iterations", () => {
  const prompt = { name: "my-prompt", periodic: { maxIterations: 10 } };

  function makeFetch(status) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve({}),
      }),
    );
  }

  test("includes max_iterations from periodic.maxIterations when positive", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
      "s1",
      { name: "p" },
      { value: 1, unit: "hours", maxIterations: 7 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(7);
  });

  test("falls back to prompt.periodic.maxIterations when periodic.maxIterations is absent", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
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
    await configurePeriodicSchedule(
      "s1",
      { name: "p" },
      { value: 1, unit: "hours", maxIterations: 0 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_iterations).toBe(0);
  });

  test("periodic.maxIterations takes priority over prompt.periodic.maxIterations", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
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
// ChatInput.handlePredefinedPrompt routing — periodic branch
//
// Tests the pure routing decision: when a prompt has .periodic set and
// onPeriodicPrompt is provided, it must be called; otherwise the normal path runs.
// This mirrors what ChatInput.handlePredefinedPrompt does after the shiftKey check.
// =============================================================================

describe("ChatInput periodic routing — onPeriodicPrompt delegation", () => {
  /**
   * Minimal simulation of the ChatInput.handlePredefinedPrompt routing logic.
   * Extracted here so we can test without mounting the full ChatInput component.
   * Mirrors the real code: const asPeriodic = prompt && promptResolveAsPeriodic(prompt);
   * if (asPeriodic && onPeriodicPrompt) { onPeriodicPrompt(prompt); return; } (mitto-92x.3).
   */
  function routePrompt(prompt, { onPeriodicPrompt, onSend } = {}) {
    const asPeriodic = prompt && promptResolveAsPeriodic(prompt);
    if (asPeriodic && onPeriodicPrompt) {
      onPeriodicPrompt(prompt);
      return "periodic";
    }
    if (onSend && prompt?.name) {
      onSend(prompt.name);
      return "send";
    }
    return "noop";
  }

  test("calls onPeriodicPrompt for a periodic-flagged prompt", () => {
    const onPeriodicPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = {
      name: "daily-standup",
      periodic: { value: 1, unit: "hours" },
    };

    const result = routePrompt(prompt, { onPeriodicPrompt, onSend });

    expect(onPeriodicPrompt).toHaveBeenCalledTimes(1);
    expect(onPeriodicPrompt).toHaveBeenCalledWith(prompt);
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("periodic");
  });

  test("does NOT call onPeriodicPrompt for a non-periodic prompt — falls through to onSend", () => {
    const onPeriodicPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = { name: "regular-prompt", prompt: "do something" };

    const result = routePrompt(prompt, { onPeriodicPrompt, onSend });

    expect(onPeriodicPrompt).not.toHaveBeenCalled();
    expect(onSend).toHaveBeenCalledWith("regular-prompt");
    expect(result).toBe("send");
  });

  test("falls through to onSend when onPeriodicPrompt is absent (even for periodic prompt)", () => {
    const onSend = jest.fn();
    const prompt = { name: "daily", periodic: { value: 1, unit: "hours" } };

    const result = routePrompt(prompt, { onSend });

    expect(onSend).toHaveBeenCalledWith("daily");
    expect(result).toBe("send");
  });

  test("does nothing when prompt has no name and no periodic", () => {
    const onPeriodicPrompt = jest.fn();
    const onSend = jest.fn();

    const result = routePrompt({}, { onPeriodicPrompt, onSend });

    expect(onPeriodicPrompt).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("noop");
  });

  // mitto-92x.3: routing now flows through promptResolveAsPeriodic (mode-aware),
  // not a bare `prompt.periodic` presence check.
  test("mode: always (no explicit mode) routes to onPeriodicPrompt — unchanged behavior", () => {
    const onPeriodicPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = { name: "daily", periodic: { value: 1, unit: "hours" } };

    const result = routePrompt(prompt, { onPeriodicPrompt, onSend });

    expect(onPeriodicPrompt).toHaveBeenCalledWith(prompt);
    expect(onSend).not.toHaveBeenCalled();
    expect(result).toBe("periodic");
  });

  test("mode: optional, default:false resolves to one-shot — falls through to onSend (no override in ChatInput yet)", () => {
    const onPeriodicPrompt = jest.fn();
    const onSend = jest.fn();
    const prompt = {
      name: "maybe-periodic",
      periodic: { mode: "optional", default: false },
    };

    const result = routePrompt(prompt, { onPeriodicPrompt, onSend });

    expect(onPeriodicPrompt).not.toHaveBeenCalled();
    expect(onSend).toHaveBeenCalledWith("maybe-periodic");
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
// configurePeriodicSchedule — trigger/delay/maxDuration fields
// =============================================================================

describe("configurePeriodicSchedule — trigger/delay/maxDuration fields", () => {
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
    await configurePeriodicSchedule(
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
    await configurePeriodicSchedule(
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

  test("falls back to prompt.periodic.trigger when periodic.trigger absent", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
      "s1",
      { name: "p", periodic: { trigger: "onCompletion" } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onCompletion");
  });

  test("falls back to prompt.periodic.delay for delay_seconds when absent from periodic", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
      "s1",
      { name: "p", periodic: { delay: 15 } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.delay_seconds).toBe(15);
  });

  test("parses prompt.periodic.maxDuration string ('2h') into max_duration_seconds", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
      "s1",
      { name: "p", periodic: { maxDuration: "2h" } },
      { value: 1, unit: "hours" },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_duration_seconds).toBe(7200);
  });

  test("periodic.maxDurationSeconds takes priority over prompt.periodic.maxDuration", async () => {
    const fetchImpl = makeFetch(200);
    await configurePeriodicSchedule(
      "s1",
      { name: "p", periodic: { maxDuration: "2h" } },
      { value: 1, unit: "hours", maxDurationSeconds: 300 },
      { fetchImpl },
    );
    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.max_duration_seconds).toBe(300);
  });
});

// =============================================================================
// makePeriodicNow — trigger/delay/maxDuration fields
// =============================================================================

describe("makePeriodicNow — trigger/delay/maxDuration fields", () => {
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

  test("includes trigger from prompt.periodic in PUT body", async () => {
    const prompt = {
      name: "p",
      periodic: {
        value: 1,
        unit: "hours",
        trigger: "onCompletion",
        delay: 10,
        maxDuration: "1h",
      },
    };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("onCompletion");
    expect(body.delay_seconds).toBe(10);
    expect(body.max_duration_seconds).toBe(3600);
  });

  test("defaults trigger to 'schedule' and delay/maxDuration to 0 when absent", async () => {
    const prompt = { name: "p", periodic: { value: 1, unit: "hours" } };
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.trigger).toBe("schedule");
    expect(body.delay_seconds).toBe(0);
    expect(body.max_duration_seconds).toBe(0);
  });
});

// =============================================================================
// makePeriodicNow — arguments forwarding
// =============================================================================

describe("makePeriodicNow — arguments forwarding", () => {
  const prompt = {
    name: "daily-standup",
    periodic: { value: 1, unit: "hours" },
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
    await makePeriodicNow("sess-1", prompt, {
      arguments: { ENV: "prod", REGION: "us-east" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.arguments).toEqual({ ENV: "prod", REGION: "us-east" });
  });

  test("omits arguments from PUT body when empty object is supplied", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-1", prompt, { arguments: {}, fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });

  test("omits arguments from PUT body when undefined (no opts supplied)", async () => {
    const fetchImpl = makeFetchSequence(makeResp(200), makeResp(200));
    await makePeriodicNow("sess-1", prompt, { fetchImpl });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });
});

// =============================================================================
// configurePeriodicSchedule — arguments forwarding
// =============================================================================

describe("configurePeriodicSchedule — arguments forwarding", () => {
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
    await configurePeriodicSchedule(
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
    await configurePeriodicSchedule(
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
    await configurePeriodicSchedule(
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
// startConversationWithPrompt periodic path — arguments forwarding
// =============================================================================

describe("useConversationSeeding — startConversationWithPrompt periodic path — arguments", () => {
  function makeFetch(status, data = {}) {
    return jest.fn(() =>
      Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(data),
      }),
    );
  }

  test("periodic: forwards arguments into the PUT body when supplied", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      periodic: { value: 1, unit: "hours" },
      arguments: { TEAM: "backend" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body.arguments).toEqual({ TEAM: "backend" });
  });

  test("periodic: omits arguments from PUT body when not supplied", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      periodic: { value: 1, unit: "hours" },
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });

  test("periodic: omits arguments from PUT body when empty object", async () => {
    const newSession = jest.fn().mockResolvedValue({ sessionId: "sess-p" });
    const fetchImpl = makeFetch(200);
    const { startConversationWithPrompt } = useConversationSeeding({
      newSession,
    });

    await startConversationWithPrompt({
      prompt: { name: "daily-standup" },
      workingDir: "/w",
      periodic: { value: 1, unit: "hours" },
      arguments: {},
      fetchImpl,
    });

    const body = JSON.parse(fetchImpl.mock.calls[0][1].body);
    expect(body).not.toHaveProperty("arguments");
  });
});
