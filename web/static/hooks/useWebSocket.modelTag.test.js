import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";
import { createModelTagRequests } from "../utils/modelTagRequests.js";

const source = readFileSync(new URL("./useWebSocket.js", import.meta.url), "utf8");
function router(bindings) {
  const marker = "const handleSessionMessage = useCallback(";
  const start = source.indexOf(marker);
  const end = source.indexOf("}, []);", start);
  return new Function(...Object.keys(bindings), `return ${source.slice(start + marker.length, end + 1)};`)(...Object.values(bindings));
}

describe("model tag result routing", () => {
  test("stable router invokes the current callback with socket ID, without mutating model state", () => {
    const calls = [];
    const ref = { current: (id, data) => calls.push(["old", id, data]) };
    const sessions = { A: { info: { config_options: [] } } };
    const before = JSON.stringify(sessions);
    const route = router({
      handleModelTagAppliedRef: ref,
      sessionUpdateSchedulerRef: { current: { flushSession() {} } },
      COALESCED_BACKGROUND_MESSAGE_TYPES: new Set(),
      console: { log() {} },
      setSessions: () => { throw new Error("result must not write config"); },
    });
    const data = { session_id: "payload-not-socket", model_id: "not-authority" };
    route("A", { type: "model_tag_applied", data });
    ref.current = (id, value) => calls.push(["current", id, value]);
    route("B", { type: "model_tag_applied", data });
    expect(calls).toEqual([["old", "A", data], ["current", "B", data]]);
    expect(JSON.stringify(sessions)).toBe(before);
    ref.current = null;
    expect(() => route("A", { type: "model_tag_applied", data })).not.toThrow();
  });

  for (const resultFirst of [true, false]) {
    test(`config events remain authoritative, result first=${resultFirst}`, async () => {
      let sessions = { A: { info: { config_options: [{ id: "model", current_value: "old" }] } } };
      let command;
      const controller = createModelTagRequests({ sendToSession: (_, message) => { command = message; return true; } });
      const route = router({
        handleModelTagAppliedRef: { current: controller.handleModelTagApplied },
        sessionUpdateSchedulerRef: { current: { flushSession() {} } },
        COALESCED_BACKGROUND_MESSAGE_TYPES: new Set(), console: { log() {} },
        setSessions: (update) => { sessions = update(sessions); },
      });
      const pending = controller.applyModelTag("A", "Tag");
      const result = () => route("A", { type: "model_tag_applied", data: {
        ...command.data, session_id: "A", status: "applied", model_id: "ignored",
      } });
      const config = () => route("A", { type: "config_option_changed", data: {
        config_id: "model", value: "authoritative",
      } });
      if (resultFirst) { result(); config(); } else { config(); result(); }
      expect(await pending).toEqual({ status: "applied" });
      expect(sessions.A.info.config_options[0].current_value).toBe("authoritative");
      controller.dispose();
    });
  }

  test("hook wiring has a pre-router ref, current assignment and public explicit-target command", () => {
    expect(source.indexOf("const handleModelTagAppliedRef = useRef(null)")).toBeLessThan(source.indexOf("const handleSessionMessage = useCallback("));
    expect(source).toContain("handleModelTagAppliedRef.current = handleModelTagApplied;");
    expect(source).toContain("const { setConfigOption, applyModelTag, handleModelTagApplied } = useWSConfigOptions(");
    expect(source.slice(source.lastIndexOf("return {"))).toContain("applyModelTag,");
  });
});
