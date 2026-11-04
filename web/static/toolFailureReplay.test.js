import { describe, expect, test } from "bun:test";
import { convertEventsToMessages, ROLE_TOOL } from "./lib.js";

describe("failed tool call history", () => {
  test("replays a failed update and its diagnostic on the original tool card", () => {
    const messages = convertEventsToMessages([
      {
        type: "tool_call",
        seq: 1,
        timestamp: "2024-01-01T10:00:00Z",
        data: { tool_call_id: "call-1", title: "question", status: "in_progress" },
      },
      {
        type: "tool_call_update",
        seq: 2,
        timestamp: "2024-01-01T10:00:01Z",
        data: { tool_call_id: "call-1", status: "failed", detail: "Interactive question is unavailable" },
      },
    ]);

    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe(ROLE_TOOL);
    expect(messages[0].id).toBe("call-1");
    expect(messages[0].status).toBe("failed");
    expect(messages[0].detail).toBe("Interactive question is unavailable");
  });

  test("retains a diagnostic supplied on an initially failed tool call", () => {
    const messages = convertEventsToMessages([{
      type: "tool_call",
      seq: 1,
      timestamp: "2024-01-01T10:00:00Z",
      data: { tool_call_id: "call-2", title: "question", status: "failed", detail: "Interactive question is unavailable" },
    }]);

    expect(messages).toHaveLength(1);
    expect(messages[0].status).toBe("failed");
    expect(messages[0].detail).toBe("Interactive question is unavailable");
  });
});
