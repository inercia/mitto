import { describe, test, expect } from "./testing/testGlobals.js";
import { createModelTagRequests } from "./modelTagRequests.js";

function harness(send) {
  const timers = new Map();
  const cleared = [];
  const sent = [];
  let timerId = 0;
  const controller = createModelTagRequests({
    makeRequestId: () => "test",
    setTimeoutFn: (fn, ms) => {
      expect(ms).toBe(100000);
      timers.set(++timerId, fn);
      return timerId;
    },
    clearTimeoutFn: (id) => { cleared.push(id); timers.delete(id); },
    sendToSession: (id, message) => {
      sent.push([id, message]);
      return send ? send(id, message, controller) : true;
    },
  });
  return {
    ...controller, timers, cleared, sent,
    result(index, status = "applied", overrides = {}, socket) {
      const [sessionId, message] = sent[index];
      controller.handleModelTagApplied(socket ?? sessionId, {
        session_id: sessionId, ...message.data, status, ...overrides,
      });
    },
  };
}

describe("model tag request lifecycle", () => {
  test("default IDs are nonempty and distinct across controller recreation", async () => {
    const ids = [];
    const create = () => createModelTagRequests({
      sendToSession: (_, message) => { ids.push(message.data.request_id); return true; },
      setTimeoutFn: () => 1,
      clearTimeoutFn() {},
    });
    const first = create();
    const a = first.applyModelTag("A", "Tag");
    first.dispose();
    const second = create();
    const b = second.applyModelTag("A", "Tag");
    second.dispose();
    expect(ids.every((id) => typeof id === "string" && id.length > 0)).toBe(true);
    expect(new Set(ids).size).toBe(2);
    expect(await a).toEqual({ status: "cancelled" });
    expect(await b).toEqual({ status: "cancelled" });
  });

  test("sends once with unique IDs, trimmed tags and no payload target", async () => {
    const h = harness();
    const first = h.applyModelTag("A", " Custom  Tag ");
    const second = h.applyModelTag("A", "Custom  Tag");
    expect(h.sent[0][1]).toEqual({ type: "apply_model_tag", data: {
      model_tag: "Custom  Tag", request_id: "test-1",
    } });
    expect(h.sent[1][1].data.request_id).not.toBe(h.sent[0][1].data.request_id);
    h.result(1, "already_active");
    h.result(0);
    expect(await first).toEqual({ status: "applied" });
    expect(await second).toEqual({ status: "already_active" });
    expect(h.timers.size).toBe(0);
    expect(h.cleared).toEqual([2, 1]);
    expect(h.sent.length).toBe(2);
  });

  test("registers identity and timer before synchronous responses", async () => {
    const h = harness((id, message, controller) => {
      controller.handleModelTagApplied(id, { ...message.data, session_id: id, status: "applied" });
      return true;
    });
    expect(await h.applyModelTag("A", "Tag")).toEqual({ status: "applied" });
    expect(h.timers.size).toBe(0);
    expect(h.cleared).toEqual([1]);
  });

  test("strict identity rejects mismatches, duplicates, late and missing IDs", async () => {
    const h = harness();
    const a = h.applyModelTag("A", "Tag");
    const b = h.applyModelTag("B", "Tag");
    for (const overrides of [
      { request_id: undefined }, { request_id: "unknown" },
      { session_id: "B" }, { model_tag: "tag" }, { model_tag: " Tag " },
    ]) h.result(0, "applied", overrides);
    h.result(0, "applied", {}, "B");
    h.handleModelTagApplied("A", null);
    expect(h.timers.size).toBe(2);
    h.result(1, "no_match");
    h.result(0);
    h.result(0, "internal_error");
    expect(await b).toEqual({ status: "no_match" });
    expect(await a).toEqual({ status: "applied" });
    expect(h.cleared.length).toBe(2);
  });

  for (const status of ["applied", "already_active", "no_match", "catalog_unavailable",
    "invalid_tag", "invalid_request", "missing_session", "internal_error", undefined, null, "new_status", {}]) {
    test(`settles status ${String(status)} without interpreting raw text`, async () => {
      const h = harness();
      const p = h.applyModelTag("A", "Tag");
      h.result(0, "applied", { status, message: "arbitrary secret" });
      expect(await p).toEqual({ status: typeof status === "string" && status !== "new_status" ? status : "protocol_error" });
      expect(h.timers.size).toBe(0);
      expect(h.cleared.length).toBe(1);
    });
  }

  test("invalid local targets/tags never send or allocate timers", async () => {
    const h = harness();
    for (const id of [null, "", "  ", 1]) {
      expect(await h.applyModelTag(id, "Tag")).toEqual({ status: "invalid_target" });
    }
    for (const tag of [null, "", "  ", 1]) {
      expect(await h.applyModelTag("A", tag)).toEqual({ status: "invalid_tag" });
    }
    expect(h.sent).toEqual([]);
    expect(h.timers.size).toBe(0);
  });

  for (const send of [() => false, () => { throw new Error("socket"); }]) {
    test("false/throw send settles and clears once, without retry", async () => {
      const h = harness(send);
      expect(await h.applyModelTag("A", "Tag")).toEqual({ status: "send_failed" });
      h.result(0);
      h.dispose();
      expect(h.sent.length).toBe(1);
      expect(h.cleared).toEqual([1]);
      expect(h.timers.size).toBe(0);
    });
  }

  test("lost/disconnected confirmation times out once, with no replay", async () => {
    const h = harness();
    const p = h.applyModelTag("A", "Tag");
    h.timers.get(1)();
    expect(await p).toEqual({ status: "timeout" });
    h.result(0);
    expect(h.cleared).toEqual([1]);
    expect(h.sent.length).toBe(1);
  });

  test("dispose cancels all, refuses sends; a fresh controller works", async () => {
    const h = harness();
    const a = h.applyModelTag("A", "Tag");
    const b = h.applyModelTag("B", "Tag");
    h.dispose(); h.dispose();
    expect(await a).toEqual({ status: "cancelled" });
    expect(await b).toEqual({ status: "cancelled" });
    expect(await h.applyModelTag("A", "Tag")).toEqual({ status: "cancelled" });
    expect(h.cleared).toEqual([1, 2]);
    expect(h.sent.length).toBe(2);
    const fresh = harness();
    const p = fresh.applyModelTag("A", "Tag");
    fresh.result(0);
    expect(await p).toEqual({ status: "applied" });
  });
});
