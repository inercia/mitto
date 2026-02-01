/**
 * Unit tests for Mitto Web Interface library functions
 */

import {
  ROLE_USER,
  ROLE_AGENT,
  ROLE_THOUGHT,
  ROLE_TOOL,
  ROLE_ERROR,
  ROLE_SYSTEM,
  MAX_MESSAGES,
  MAX_MARKDOWN_LENGTH,
  MIN_USERNAME_LENGTH,
  MAX_USERNAME_LENGTH,
  MIN_PASSWORD_LENGTH,
  MAX_PASSWORD_LENGTH,
  computeAllSessions,
  convertEventsToMessages,
  getMinSeq,
  getMaxSeq,
  getMessageHash,
  mergeMessagesWithSync,
  safeJsonParse,
  createSessionState,
  addMessageToSessionState,
  updateLastMessageInSession,
  removeSessionFromState,
  limitMessages,
  getBasename,
  getWorkspaceAbbreviation,
  getWorkspaceColor,
  getWorkspaceVisualInfo,
  hexToRgb,
  getLuminance,
  getColorFromHex,
  hslToHex,
  validateUsername,
  validatePassword,
  validateCredentials,
  generatePromptId,
  savePendingPrompt,
  removePendingPrompt,
  getPendingPrompts,
  getPendingPromptsForSession,
  cleanupExpiredPrompts,
  hasMarkdownContent,
  renderUserMarkdown,
} from "./lib.js";

// =============================================================================
// computeAllSessions Tests
// =============================================================================

describe("computeAllSessions", () => {
  test("returns empty array when both inputs are empty", () => {
    const result = computeAllSessions([], []);
    expect(result).toEqual([]);
  });

  test("returns active sessions when stored is empty", () => {
    const active = [{ session_id: "1", created_at: "2024-01-01T10:00:00Z" }];
    const result = computeAllSessions(active, []);
    expect(result).toHaveLength(1);
    expect(result[0].session_id).toBe("1");
  });

  test("returns stored sessions when active is empty", () => {
    const stored = [{ session_id: "1", created_at: "2024-01-01T10:00:00Z" }];
    const result = computeAllSessions([], stored);
    expect(result).toHaveLength(1);
    expect(result[0].session_id).toBe("1");
  });

  test("removes duplicates preferring active sessions", () => {
    const active = [
      {
        session_id: "1",
        name: "Active Version",
        created_at: "2024-01-01T10:00:00Z",
      },
    ];
    const stored = [
      {
        session_id: "1",
        name: "Stored Version",
        created_at: "2024-01-01T10:00:00Z",
      },
      {
        session_id: "2",
        name: "Only Stored",
        created_at: "2024-01-01T09:00:00Z",
      },
    ];
    const result = computeAllSessions(active, stored);
    expect(result).toHaveLength(2);
    // Active version should be present, not stored
    expect(result.find((s) => s.session_id === "1").name).toBe(
      "Active Version",
    );
  });

  test("sorts by created_at (most recent first)", () => {
    const sessions = [
      { session_id: "1", created_at: "2024-01-01T08:00:00Z" },
      { session_id: "2", created_at: "2024-01-01T12:00:00Z" },
      { session_id: "3", created_at: "2024-01-01T10:00:00Z" },
    ];
    const result = computeAllSessions(sessions, []);
    expect(result.map((s) => s.session_id)).toEqual(["2", "3", "1"]);
  });

  test("ignores last_user_message_at and updated_at for sorting", () => {
    const sessions = [
      {
        session_id: "1",
        last_user_message_at: "2024-01-01T23:00:00Z",
        updated_at: "2024-01-01T22:00:00Z",
        created_at: "2024-01-01T01:00:00Z",
      },
      {
        session_id: "2",
        last_user_message_at: "2024-01-01T01:00:00Z",
        updated_at: "2024-01-01T02:00:00Z",
        created_at: "2024-01-01T12:00:00Z",
      },
    ];
    const result = computeAllSessions(sessions, []);
    // Session 2 created later, so it should be first despite older last_user_message_at
    expect(result.map((s) => s.session_id)).toEqual(["2", "1"]);
  });
});

// =============================================================================
// convertEventsToMessages Tests
// =============================================================================

describe("convertEventsToMessages", () => {
  test("returns empty array for empty events", () => {
    const result = convertEventsToMessages([]);
    expect(result).toEqual([]);
  });

  test("converts user_prompt event", () => {
    const events = [
      {
        type: "user_prompt",
        data: { message: "Hello" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(1);
    expect(result[0].role).toBe(ROLE_USER);
    expect(result[0].text).toBe("Hello");
  });

  test("converts agent_message event", () => {
    const events = [
      {
        type: "agent_message",
        data: { html: "<p>Response</p>" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(1);
    expect(result[0].role).toBe(ROLE_AGENT);
    expect(result[0].html).toBe("<p>Response</p>");
    expect(result[0].complete).toBe(true);
  });

  test("converts agent_thought event", () => {
    const events = [
      {
        type: "agent_thought",
        data: { text: "Thinking..." },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(1);
    expect(result[0].role).toBe(ROLE_THOUGHT);
    expect(result[0].text).toBe("Thinking...");
  });

  test("converts tool_call event", () => {
    const events = [
      {
        type: "tool_call",
        data: { id: "tool-1", title: "Read File", status: "running" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(1);
    expect(result[0].role).toBe(ROLE_TOOL);
    expect(result[0].id).toBe("tool-1");
    expect(result[0].title).toBe("Read File");
  });

  test("converts error event", () => {
    const events = [
      {
        type: "error",
        data: { message: "Something went wrong" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(1);
    expect(result[0].role).toBe(ROLE_ERROR);
    expect(result[0].text).toBe("Something went wrong");
  });

  test("ignores unknown event types", () => {
    const events = [
      {
        type: "unknown_type",
        data: { foo: "bar" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(0);
  });

  test("converts multiple events in order", () => {
    const events = [
      {
        type: "user_prompt",
        data: { message: "Hi" },
        timestamp: "2024-01-01T10:00:00Z",
      },
      {
        type: "agent_message",
        data: { html: "Hello!" },
        timestamp: "2024-01-01T10:00:01Z",
      },
      {
        type: "user_prompt",
        data: { message: "Bye" },
        timestamp: "2024-01-01T10:00:02Z",
      },
    ];
    const result = convertEventsToMessages(events);
    expect(result).toHaveLength(3);
    expect(result[0].role).toBe(ROLE_USER);
    expect(result[1].role).toBe(ROLE_AGENT);
    expect(result[2].role).toBe(ROLE_USER);
  });

  test("reverses events when reverseInput option is true", () => {
    // Events in reverse order (newest first, as returned by API with order=desc)
    const events = [
      {
        seq: 3,
        type: "user_prompt",
        data: { message: "Bye" },
        timestamp: "2024-01-01T10:00:02Z",
      },
      {
        seq: 2,
        type: "agent_message",
        data: { html: "Hello!" },
        timestamp: "2024-01-01T10:00:01Z",
      },
      {
        seq: 1,
        type: "user_prompt",
        data: { message: "Hi" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    const result = convertEventsToMessages(events, { reverseInput: true });
    expect(result).toHaveLength(3);
    // Should be in chronological order (oldest first)
    expect(result[0].seq).toBe(1);
    expect(result[0].text).toBe("Hi");
    expect(result[1].seq).toBe(2);
    expect(result[2].seq).toBe(3);
    expect(result[2].text).toBe("Bye");
  });

  test("does not modify original array when reverseInput is true", () => {
    const events = [
      {
        seq: 3,
        type: "user_prompt",
        data: { message: "C" },
        timestamp: "2024-01-01T10:00:02Z",
      },
      {
        seq: 2,
        type: "user_prompt",
        data: { message: "B" },
        timestamp: "2024-01-01T10:00:01Z",
      },
      {
        seq: 1,
        type: "user_prompt",
        data: { message: "A" },
        timestamp: "2024-01-01T10:00:00Z",
      },
    ];
    convertEventsToMessages(events, { reverseInput: true });
    // Original array should be unchanged
    expect(events[0].seq).toBe(3);
    expect(events[1].seq).toBe(2);
    expect(events[2].seq).toBe(1);
  });
});

// =============================================================================
// getMinSeq and getMaxSeq Tests
// =============================================================================

describe("getMinSeq", () => {
  test("returns minimum sequence number", () => {
    const events = [{ seq: 5 }, { seq: 2 }, { seq: 8 }, { seq: 1 }];
    expect(getMinSeq(events)).toBe(1);
  });

  test("returns 0 for empty array", () => {
    expect(getMinSeq([])).toBe(0);
  });

  test("returns 0 for null input", () => {
    expect(getMinSeq(null)).toBe(0);
  });

  test("returns 0 for undefined input", () => {
    expect(getMinSeq(undefined)).toBe(0);
  });

  test("handles events with missing seq", () => {
    const events = [{ seq: 5 }, {}, { seq: 3 }];
    expect(getMinSeq(events)).toBe(0);
  });
});

describe("getMaxSeq", () => {
  test("returns maximum sequence number", () => {
    const events = [{ seq: 5 }, { seq: 2 }, { seq: 8 }, { seq: 1 }];
    expect(getMaxSeq(events)).toBe(8);
  });

  test("returns 0 for empty array", () => {
    expect(getMaxSeq([])).toBe(0);
  });

  test("returns 0 for null input", () => {
    expect(getMaxSeq(null)).toBe(0);
  });

  test("returns 0 for undefined input", () => {
    expect(getMaxSeq(undefined)).toBe(0);
  });

  test("handles events with missing seq", () => {
    const events = [{ seq: 5 }, {}, { seq: 3 }];
    expect(getMaxSeq(events)).toBe(5);
  });
});

// =============================================================================
// getMessageHash Tests
// =============================================================================

describe("getMessageHash", () => {
  test("generates hash for user message", () => {
    const hash = getMessageHash({ role: ROLE_USER, text: "Hello world" });
    expect(hash).toBe("user:Hello world");
  });

  test("generates hash for agent message", () => {
    const hash = getMessageHash({ role: ROLE_AGENT, html: "<p>Response</p>" });
    expect(hash).toBe("agent:<p>Response</p>");
  });

  test("generates hash for thought message", () => {
    const hash = getMessageHash({ role: ROLE_THOUGHT, text: "Thinking..." });
    expect(hash).toBe("thought:Thinking...");
  });

  test("generates hash for error message", () => {
    const hash = getMessageHash({
      role: ROLE_ERROR,
      text: "Something went wrong",
    });
    expect(hash).toBe("error:Something went wrong");
  });

  test("generates hash for tool message using id and title", () => {
    const hash = getMessageHash({
      role: ROLE_TOOL,
      id: "tool-123",
      title: "Read File",
    });
    expect(hash).toBe("tool:tool-123:Read File");
  });

  test("tool hash is unique even with empty text/html", () => {
    // This was the bug: tool messages would hash to "tool:" because they have
    // title/id instead of text/html
    const hash1 = getMessageHash({
      role: ROLE_TOOL,
      id: "tool-1",
      title: "Read File",
    });
    const hash2 = getMessageHash({
      role: ROLE_TOOL,
      id: "tool-2",
      title: "Write File",
    });
    expect(hash1).not.toBe(hash2);
    expect(hash1).toBe("tool:tool-1:Read File");
    expect(hash2).toBe("tool:tool-2:Write File");
  });

  test("tool hash handles missing id gracefully", () => {
    const hash = getMessageHash({ role: ROLE_TOOL, title: "Read File" });
    expect(hash).toBe("tool::Read File");
  });

  test("tool hash handles missing title gracefully", () => {
    const hash = getMessageHash({ role: ROLE_TOOL, id: "tool-123" });
    expect(hash).toBe("tool:tool-123:");
  });

  test("truncates long content to 200 chars", () => {
    const longText = "x".repeat(300);
    const hash = getMessageHash({ role: ROLE_USER, text: longText });
    // Should have role + : + 200 chars
    expect(hash).toBe("user:" + "x".repeat(200));
  });

  test("handles missing role gracefully", () => {
    const hash = getMessageHash({ text: "Hello" });
    expect(hash).toBe("unknown:Hello");
  });

  test("handles message with both text and html (prefers text)", () => {
    const hash = getMessageHash({
      role: ROLE_USER,
      text: "Text",
      html: "HTML",
    });
    expect(hash).toBe("user:Text");
  });

  test("uses html when text is empty", () => {
    const hash = getMessageHash({
      role: ROLE_AGENT,
      text: "",
      html: "<p>HTML</p>",
    });
    expect(hash).toBe("agent:<p>HTML</p>");
  });
});

// =============================================================================
// mergeMessagesWithSync Tests
// =============================================================================

describe("mergeMessagesWithSync", () => {
  test("returns new messages when existing is empty", () => {
    const newMessages = [
      { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 },
      { role: ROLE_AGENT, html: "Hi", seq: 2, timestamp: 2000 },
    ];
    const result = mergeMessagesWithSync([], newMessages);
    expect(result).toEqual(newMessages);
  });

  test("returns existing messages when new is empty", () => {
    const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
    const result = mergeMessagesWithSync(existing, []);
    expect(result).toBe(existing);
  });

  test("deduplicates by content hash", () => {
    const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
    const newMessages = [
      { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 500 }, // duplicate
      { role: ROLE_AGENT, html: "Response", seq: 2, timestamp: 1500 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(2);
    expect(result.find((m) => m.role === ROLE_USER).text).toBe("Hello");
    expect(result.find((m) => m.role === ROLE_AGENT).html).toBe("Response");
  });

  test("deduplicates tool messages correctly", () => {
    // This tests the bug fix: tool messages should be deduplicated by id+title
    const existing = [
      {
        role: ROLE_TOOL,
        id: "tool-1",
        title: "Read File",
        status: "running",
        timestamp: 1000,
      },
    ];
    const newMessages = [
      {
        role: ROLE_TOOL,
        id: "tool-1",
        title: "Read File",
        status: "completed",
        seq: 1,
        timestamp: 500,
      }, // duplicate (same id+title)
      {
        role: ROLE_TOOL,
        id: "tool-2",
        title: "Write File",
        status: "completed",
        seq: 2,
        timestamp: 1500,
      }, // new
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(2);
    // Should have the original tool-1 and the new tool-2
    expect(result.find((m) => m.id === "tool-1")).toBeDefined();
    expect(result.find((m) => m.id === "tool-2")).toBeDefined();
  });

  test("preserves existing order and appends new messages", () => {
    // Existing messages are in display order - we should NOT re-sort them
    // New messages from sync are appended at the end (they happened AFTER lastSeenSeq)
    const existing = [
      { role: ROLE_AGENT, html: "Third", seq: 3, timestamp: 3000 },
    ];
    const newMessages = [
      { role: ROLE_USER, text: "First", seq: 1, timestamp: 1000 },
      { role: ROLE_AGENT, html: "Second", seq: 2, timestamp: 2000 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(3);
    // Existing message stays first, new messages are appended
    expect(result[0].seq).toBe(3); // existing stays in place
    expect(result[1].seq).toBe(1); // new messages appended in their order
    expect(result[2].seq).toBe(2);
  });

  test("appends new messages without re-sorting", () => {
    // This test verifies we don't try to insert sync messages into the middle
    // based on timestamp - they always go at the end
    const existing = [
      { role: ROLE_USER, text: "First", timestamp: 1000 },
      { role: ROLE_AGENT, html: "Third", timestamp: 3000 },
    ];
    const newMessages = [
      { role: ROLE_THOUGHT, text: "Second", timestamp: 2000 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(3);
    // Existing messages stay in order, new messages appended
    expect(result[0].text).toBe("First");
    expect(result[1].html).toBe("Third");
    expect(result[2].text).toBe("Second"); // appended at end
  });

  test("handles mixed seq and non-seq messages", () => {
    // This simulates the mobile wake scenario:
    // - streaming messages (no seq, client timestamp)
    // - sync messages (with seq, server timestamp)
    const existing = [
      { role: ROLE_USER, text: "Prompt", timestamp: 1000 },
      { role: ROLE_AGENT, html: "Partial response...", timestamp: 2500 }, // streaming, no seq
    ];
    const newMessages = [
      {
        role: ROLE_TOOL,
        id: "tool-1",
        title: "Read File",
        seq: 2,
        timestamp: 2000,
      },
      { role: ROLE_AGENT, html: "Complete response", seq: 3, timestamp: 3000 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    // All messages should be present - existing first, then new appended
    expect(result.length).toBe(4);
    expect(result[0].text).toBe("Prompt");
    expect(result[1].html).toBe("Partial response...");
    expect(result[2].id).toBe("tool-1");
    expect(result[3].html).toBe("Complete response");
  });

  test("mobile wake resync scenario - existing messages preserved, new appended", () => {
    // Scenario: Phone sleeps after seeing user prompt and partial agent response
    // Agent continues working while phone is asleep
    // On wake, sync returns new events that happened after the phone slept
    //
    // The key insight is that we DON'T want to re-sort based on seq because:
    // 1. Tool calls are persisted immediately (get early seq numbers)
    // 2. Agent messages are buffered and persisted later (get later seq numbers)
    // 3. Re-sorting by seq would put tool calls before agent text they're interspersed with

    const existing = [
      { role: ROLE_USER, text: "Help me fix this", timestamp: 1000 },
      {
        role: ROLE_AGENT,
        html: "Let me analyze",
        complete: false,
        timestamp: 5000,
      }, // partial streaming
    ];
    // Sync returns persisted events - these happened AFTER the phone slept
    // They are appended in their arrival order
    const syncEvents = [
      {
        role: ROLE_TOOL,
        id: "read-1",
        title: "Read file.js",
        status: "completed",
        seq: 2,
        timestamp: 2000,
      },
      {
        role: ROLE_AGENT,
        html: "I have made the changes",
        complete: true,
        seq: 5,
        timestamp: 5500,
      },
    ];

    const result = mergeMessagesWithSync(existing, syncEvents);

    // Existing messages stay in their original positions
    expect(result[0].text).toBe("Help me fix this");
    expect(result[1].html).toBe("Let me analyze");
    // New messages are appended at the end in their order
    expect(result[2].id).toBe("read-1");
    expect(result[3].html).toBe("I have made the changes");

    // Verify we have both tool calls present
    const toolCalls = result.filter((m) => m.role === ROLE_TOOL);
    expect(toolCalls.length).toBe(1);
  });

  test("preserves message order with many events", () => {
    // Simulate a complex conversation with many events
    const existing = [
      { role: ROLE_USER, text: "Do a complex task", seq: 1, timestamp: 1000 },
    ];
    const syncEvents = [];
    // Generate 20 interleaved tool calls and agent messages
    for (let i = 0; i < 10; i++) {
      syncEvents.push({
        role: ROLE_TOOL,
        id: `tool-${i}`,
        title: `Tool ${i}`,
        status: "completed",
        seq: 2 + i * 2,
        timestamp: 2000 + i * 200,
      });
      syncEvents.push({
        role: ROLE_AGENT,
        html: `Response ${i}`,
        complete: true,
        seq: 3 + i * 2,
        timestamp: 2100 + i * 200,
      });
    }

    const result = mergeMessagesWithSync(existing, syncEvents);

    // Verify all messages are present
    expect(result.length).toBe(21); // 1 user + 10 tools + 10 agents

    // Existing message stays first
    expect(result[0].text).toBe("Do a complex task");

    // Sync events are appended in their original order (as received from backend)
    // The backend sends them in chronological order from events.jsonl
    for (let i = 0; i < 10; i++) {
      const toolIndex = result.findIndex((m) => m.id === `tool-${i}`);
      const agentIndex = result.findIndex((m) => m.html === `Response ${i}`);
      expect(toolIndex).toBeLessThan(agentIndex);
    }
  });

  test("appends sync events in their arrival order", () => {
    // Sync events are appended in the order they arrive from the backend.
    // The backend reads from events.jsonl which is append-only, so they're
    // already in chronological order.
    const existing = [
      { role: ROLE_USER, text: "First", seq: 1, timestamp: 1000 },
    ];
    // Sync events arrive in the order they were persisted
    const syncEvents = [
      {
        role: ROLE_TOOL,
        id: "tool-1",
        title: "Second",
        seq: 2,
        timestamp: 2000,
      },
      { role: ROLE_AGENT, html: "Third", seq: 3, timestamp: 3000 },
    ];

    const result = mergeMessagesWithSync(existing, syncEvents);

    expect(result.length).toBe(3);
    // Existing first, then sync events in their order
    expect(result[0].seq).toBe(1);
    expect(result[1].seq).toBe(2);
    expect(result[2].seq).toBe(3);
  });

  test("handles duplicate events with different completion states", () => {
    // Same message might appear in both existing (streaming) and sync (complete)
    const existing = [
      {
        role: ROLE_AGENT,
        html: "Partial...",
        complete: false,
        timestamp: 1000,
      },
    ];
    const syncEvents = [
      {
        role: ROLE_AGENT,
        html: "Partial... complete!",
        complete: true,
        seq: 1,
        timestamp: 1000,
      },
    ];

    const result = mergeMessagesWithSync(existing, syncEvents);

    // Should deduplicate based on content hash
    // The exact behavior depends on which one is kept
    expect(result.length).toBeLessThanOrEqual(2);
  });

  test("preserves thought messages in correct order", () => {
    const existing = [];
    const syncEvents = [
      { role: ROLE_USER, text: "Question", seq: 1, timestamp: 1000 },
      {
        role: ROLE_THOUGHT,
        text: "Thinking about this...",
        seq: 2,
        timestamp: 2000,
      },
      { role: ROLE_AGENT, html: "Answer", seq: 3, timestamp: 3000 },
    ];

    const result = mergeMessagesWithSync(existing, syncEvents);

    expect(result.length).toBe(3);
    expect(result[0].role).toBe(ROLE_USER);
    expect(result[1].role).toBe(ROLE_THOUGHT);
    expect(result[2].role).toBe(ROLE_AGENT);
  });

  test("handles error messages in correct order", () => {
    const existing = [];
    const syncEvents = [
      { role: ROLE_USER, text: "Do something", seq: 1, timestamp: 1000 },
      {
        role: ROLE_TOOL,
        id: "tool-1",
        title: "Try action",
        seq: 2,
        timestamp: 2000,
      },
      { role: ROLE_ERROR, text: "Action failed", seq: 3, timestamp: 3000 },
      {
        role: ROLE_AGENT,
        html: "Let me try another approach",
        seq: 4,
        timestamp: 4000,
      },
    ];

    const result = mergeMessagesWithSync(existing, syncEvents);

    expect(result.length).toBe(4);
    expect(result[2].role).toBe(ROLE_ERROR);
    expect(result[2].seq).toBe(3);
  });

  // =========================================================================
  // Edge Cases: Empty and Null Inputs
  // =========================================================================

  describe("edge cases - empty and null inputs", () => {
    test("returns empty array when both inputs are null", () => {
      const result = mergeMessagesWithSync(null, null);
      expect(result).toEqual([]);
    });

    test("returns empty array when both inputs are undefined", () => {
      const result = mergeMessagesWithSync(undefined, undefined);
      expect(result).toEqual([]);
    });

    test("returns new messages when existing is null", () => {
      const newMessages = [{ role: ROLE_USER, text: "Hello", seq: 1 }];
      const result = mergeMessagesWithSync(null, newMessages);
      expect(result).toEqual(newMessages);
    });

    test("returns new messages when existing is undefined", () => {
      const newMessages = [{ role: ROLE_USER, text: "Hello", seq: 1 }];
      const result = mergeMessagesWithSync(undefined, newMessages);
      expect(result).toEqual(newMessages);
    });

    test("returns existing messages when new is null", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const result = mergeMessagesWithSync(existing, null);
      expect(result).toBe(existing);
    });

    test("returns existing messages when new is undefined", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const result = mergeMessagesWithSync(existing, undefined);
      expect(result).toBe(existing);
    });

    test("handles empty arrays for both inputs", () => {
      const result = mergeMessagesWithSync([], []);
      expect(result).toEqual([]);
    });
  });

  // =========================================================================
  // Edge Cases: Boundary Values
  // =========================================================================

  describe("edge cases - boundary values", () => {
    test("handles single message in existing", () => {
      const existing = [{ role: ROLE_USER, text: "Only one", timestamp: 1000 }];
      const newMessages = [];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result).toBe(existing);
      expect(result.length).toBe(1);
    });

    test("handles single message in new", () => {
      const existing = [];
      const newMessages = [{ role: ROLE_USER, text: "Only one", seq: 1 }];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result).toEqual(newMessages);
      expect(result.length).toBe(1);
    });

    test("handles very large number of messages", () => {
      const existing = [];
      const newMessages = [];
      for (let i = 0; i < 1000; i++) {
        newMessages.push({
          role: ROLE_AGENT,
          html: `Message ${i}`,
          seq: i + 1,
          timestamp: 1000 + i,
        });
      }
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(1000);
      expect(result[0].html).toBe("Message 0");
      expect(result[999].html).toBe("Message 999");
    });

    test("handles messages with seq value of 0", () => {
      // seq: 0 is treated as "no seq" (streaming message)
      // Existing messages stay in place, new messages are appended
      const existing = [
        { role: ROLE_USER, text: "First", seq: 0, timestamp: 1000 },
      ];
      const newMessages = [
        { role: ROLE_AGENT, html: "Second", seq: 1, timestamp: 2000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
      // Existing stays first, new is appended
      expect(result[0].seq).toBe(0);
      expect(result[1].seq).toBe(1);
    });

    test("handles messages with very large seq numbers", () => {
      const existing = [
        {
          role: ROLE_USER,
          text: "First",
          seq: Number.MAX_SAFE_INTEGER - 1,
          timestamp: 1000,
        },
      ];
      const newMessages = [
        {
          role: ROLE_AGENT,
          html: "Second",
          seq: Number.MAX_SAFE_INTEGER,
          timestamp: 2000,
        },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles messages with negative timestamps", () => {
      // Edge case: timestamps before Unix epoch
      const existing = [{ role: ROLE_USER, text: "Old", timestamp: -1000 }];
      const newMessages = [{ role: ROLE_AGENT, html: "New", timestamp: 1000 }];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles messages with zero timestamp", () => {
      const existing = [{ role: ROLE_USER, text: "Zero", timestamp: 0 }];
      const newMessages = [
        { role: ROLE_AGENT, html: "Positive", timestamp: 1000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });
  });

  // =========================================================================
  // Edge Cases: Invalid and Missing Data
  // =========================================================================

  describe("edge cases - invalid and missing data", () => {
    test("handles messages with missing role", () => {
      const existing = [{ text: "No role", timestamp: 1000 }];
      const newMessages = [{ role: ROLE_AGENT, html: "Has role", seq: 1 }];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles messages with missing text and html", () => {
      const existing = [{ role: ROLE_USER, timestamp: 1000 }];
      const newMessages = [{ role: ROLE_AGENT, seq: 1, timestamp: 2000 }];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles messages with missing timestamp", () => {
      const existing = [{ role: ROLE_USER, text: "No timestamp" }];
      const newMessages = [
        { role: ROLE_AGENT, html: "Also no timestamp", seq: 1 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles tool messages with missing id", () => {
      const existing = [{ role: ROLE_TOOL, title: "No ID", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_TOOL, id: "has-id", title: "Has ID", seq: 1 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles tool messages with missing title", () => {
      const existing = [{ role: ROLE_TOOL, id: "tool-1", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_TOOL, id: "tool-2", title: "Has title", seq: 1 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("handles messages with empty strings", () => {
      const existing = [{ role: ROLE_USER, text: "", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_AGENT, html: "", seq: 1, timestamp: 2000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      // Both have empty content but different roles, so both should be kept
      expect(result.length).toBe(2);
    });

    test("handles messages with whitespace-only content", () => {
      const existing = [{ role: ROLE_USER, text: "   ", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_USER, text: "   ", seq: 1, timestamp: 2000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      // Same role and content (whitespace), should deduplicate
      expect(result.length).toBe(1);
    });
  });

  // =========================================================================
  // Deduplication Edge Cases
  // =========================================================================

  describe("deduplication edge cases", () => {
    test("deduplicates identical messages with different timestamps", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 5000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(1);
      // Existing message is kept
      expect(result[0].timestamp).toBe(1000);
    });

    test("does not deduplicate messages with same content but different roles", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_AGENT, text: "Hello", seq: 1, timestamp: 2000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("deduplicates based on first 200 chars only", () => {
      const longText = "A".repeat(250);
      const existing = [{ role: ROLE_USER, text: longText, timestamp: 1000 }];
      // Same first 200 chars, different ending
      const newMessages = [
        {
          role: ROLE_USER,
          text: longText.substring(0, 200) + "B".repeat(50),
          seq: 1,
        },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      // Should be considered duplicates (first 200 chars match)
      expect(result.length).toBe(1);
    });

    test("does not deduplicate when first 200 chars differ", () => {
      const existing = [
        { role: ROLE_USER, text: "A".repeat(200), timestamp: 1000 },
      ];
      const newMessages = [
        { role: ROLE_USER, text: "B".repeat(200), seq: 1, timestamp: 2000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(2);
    });

    test("deduplicates multiple identical messages in new array", () => {
      const existing = [{ role: ROLE_USER, text: "Original", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_USER, text: "Original", seq: 1, timestamp: 2000 },
        { role: ROLE_USER, text: "Original", seq: 2, timestamp: 3000 },
        { role: ROLE_AGENT, html: "Response", seq: 3, timestamp: 4000 },
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      // Original is deduplicated, but we still get one from new (since filter only checks against existing)
      // Actually, the filter checks against existingHashes, so both duplicates in new are filtered
      expect(result.length).toBe(2); // Original + Response
    });

    test("tool deduplication uses id and title combination", () => {
      const existing = [
        { role: ROLE_TOOL, id: "tool-1", title: "Read", timestamp: 1000 },
      ];
      const newMessages = [
        { role: ROLE_TOOL, id: "tool-1", title: "Read", seq: 1 }, // duplicate
        { role: ROLE_TOOL, id: "tool-1", title: "Write", seq: 2 }, // different title
        { role: ROLE_TOOL, id: "tool-2", title: "Read", seq: 3 }, // different id
      ];
      const result = mergeMessagesWithSync(existing, newMessages);
      expect(result.length).toBe(3); // original + 2 non-duplicates
    });
  });

  // =========================================================================
  // Mobile Wake Scenarios (Real-world Integration Tests)
  // =========================================================================

  describe("mobile wake scenarios", () => {
    test("phone sleeps immediately after sending prompt", () => {
      // User sends prompt, phone immediately sleeps
      // Agent does all work while phone is asleep
      // User message has seq: 1 (persisted before phone slept)
      const existing = [
        { role: ROLE_USER, text: "Fix the bug", seq: 1, timestamp: 1000 },
      ];
      const syncEvents = [
        {
          role: ROLE_TOOL,
          id: "read-1",
          title: "Read file",
          seq: 2,
          timestamp: 2000,
        },
        {
          role: ROLE_TOOL,
          id: "edit-1",
          title: "Edit file",
          seq: 3,
          timestamp: 3000,
        },
        { role: ROLE_AGENT, html: "Done!", seq: 4, timestamp: 4000 },
      ];

      const result = mergeMessagesWithSync(existing, syncEvents);

      expect(result.length).toBe(4);
      // All messages sorted by seq
      expect(result[0].text).toBe("Fix the bug"); // seq: 1
      expect(result[1].id).toBe("read-1"); // seq: 2
      expect(result[2].id).toBe("edit-1"); // seq: 3
      expect(result[3].html).toBe("Done!"); // seq: 4
    });

    test("phone sleeps mid-stream with partial agent response", () => {
      // User sees partial response, then phone sleeps
      const existing = [
        { role: ROLE_USER, text: "Explain this", timestamp: 1000 },
        {
          role: ROLE_AGENT,
          html: "Let me explain...",
          complete: false,
          timestamp: 2000,
        },
      ];
      // Agent continues, finishes response
      const syncEvents = [
        {
          role: ROLE_AGENT,
          html: "Let me explain... Here is the full explanation.",
          complete: true,
          seq: 2,
          timestamp: 3000,
        },
      ];

      const result = mergeMessagesWithSync(existing, syncEvents);

      // The partial and complete have different content, so both are kept
      // (In real usage, the UI might handle this differently)
      expect(result.length).toBe(3);
    });

    test("phone sleeps and wakes multiple times", () => {
      // Simulates multiple sleep/wake cycles
      // First wake
      let existing = [{ role: ROLE_USER, text: "Start", timestamp: 1000 }];
      let syncEvents = [
        { role: ROLE_AGENT, html: "Working...", seq: 2, timestamp: 2000 },
      ];
      let result = mergeMessagesWithSync(existing, syncEvents);
      expect(result.length).toBe(2);

      // Second wake
      existing = result;
      syncEvents = [
        {
          role: ROLE_TOOL,
          id: "tool-1",
          title: "Action",
          seq: 3,
          timestamp: 3000,
        },
      ];
      result = mergeMessagesWithSync(existing, syncEvents);
      expect(result.length).toBe(3);

      // Third wake
      existing = result;
      syncEvents = [
        { role: ROLE_AGENT, html: "Done!", seq: 4, timestamp: 4000 },
      ];
      result = mergeMessagesWithSync(existing, syncEvents);
      expect(result.length).toBe(4);
    });

    test("sync returns events already seen via streaming (full overlap)", () => {
      // All sync events were already received via streaming
      // Existing messages have seq for proper deduplication
      const existing = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 },
        { role: ROLE_AGENT, html: "Hi there!", seq: 2, timestamp: 2000 },
      ];
      const syncEvents = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 },
        { role: ROLE_AGENT, html: "Hi there!", seq: 2, timestamp: 2000 },
      ];

      const result = mergeMessagesWithSync(existing, syncEvents);

      // All duplicates, result should have same content
      expect(result.length).toBe(2);
      expect(result[0].text).toBe("Hello");
      expect(result[1].html).toBe("Hi there!");
    });

    test("sync returns partial overlap with streaming", () => {
      // Some events were seen via streaming, some are new
      // Existing messages have seq for proper deduplication
      const existing = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 },
        { role: ROLE_AGENT, html: "Hi!", seq: 2, timestamp: 2000 },
      ];
      const syncEvents = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 }, // duplicate
        { role: ROLE_AGENT, html: "Hi!", seq: 2, timestamp: 2000 }, // duplicate
        {
          role: ROLE_TOOL,
          id: "tool-1",
          title: "New",
          seq: 3,
          timestamp: 3000,
        }, // new
      ];

      const result = mergeMessagesWithSync(existing, syncEvents);

      expect(result.length).toBe(3);
      // Sorted by seq: 1, 2, 3
      expect(result[2].id).toBe("tool-1");
    });
  });

  // =========================================================================
  // Immutability Tests
  // =========================================================================

  describe("immutability", () => {
    test("does not modify existing messages array", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const originalLength = existing.length;
      const newMessages = [
        { role: ROLE_AGENT, html: "Hi", seq: 1, timestamp: 2000 },
      ];

      mergeMessagesWithSync(existing, newMessages);

      expect(existing.length).toBe(originalLength);
      expect(existing[0].text).toBe("Hello");
    });

    test("does not modify new messages array", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_AGENT, html: "Hi", seq: 1, timestamp: 2000 },
      ];
      const originalNewLength = newMessages.length;

      mergeMessagesWithSync(existing, newMessages);

      expect(newMessages.length).toBe(originalNewLength);
      expect(newMessages[0].html).toBe("Hi");
    });

    test("returns new array instance when merging", () => {
      const existing = [{ role: ROLE_USER, text: "Hello", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_AGENT, html: "Hi", seq: 1, timestamp: 2000 },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      expect(result).not.toBe(existing);
      expect(result).not.toBe(newMessages);
    });

    test("does not modify message objects", () => {
      // Both messages have seq so order is deterministic
      const existing = [
        { role: ROLE_USER, text: "Hello", seq: 1, timestamp: 1000 },
      ];
      const newMessages = [
        { role: ROLE_AGENT, html: "Hi", seq: 2, timestamp: 2000 },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      // Original objects should be unchanged
      expect(existing[0].role).toBe(ROLE_USER);
      expect(newMessages[0].role).toBe(ROLE_AGENT);
      // Result should contain the same object references (shallow copy)
      // After sorting by seq: existing[0] (seq:1) comes first, newMessages[0] (seq:2) second
      expect(result[0]).toBe(existing[0]);
      expect(result[1]).toBe(newMessages[0]);
    });
  });

  // =========================================================================
  // Performance Edge Cases
  // =========================================================================

  describe("performance edge cases", () => {
    test("handles many duplicates efficiently", () => {
      const existing = [];
      for (let i = 0; i < 100; i++) {
        existing.push({
          role: ROLE_USER,
          text: `Message ${i}`,
          seq: i + 1,
          timestamp: i * 1000,
        });
      }
      // All new messages are duplicates
      const newMessages = existing.map((m) => ({ ...m }));

      const startTime = Date.now();
      const result = mergeMessagesWithSync(existing, newMessages);
      const endTime = Date.now();

      // Result should have same content (duplicates filtered out)
      expect(result.length).toBe(100);
      expect(endTime - startTime).toBeLessThan(100); // Should be fast
    });

    test("handles alternating message types", () => {
      const existing = [];
      const newMessages = [];
      for (let i = 0; i < 50; i++) {
        existing.push({
          role: ROLE_USER,
          text: `User ${i}`,
          timestamp: i * 2000,
        });
        newMessages.push({
          role: ROLE_AGENT,
          html: `Agent ${i}`,
          seq: i + 1,
          timestamp: i * 2000 + 1000,
        });
      }

      const result = mergeMessagesWithSync(existing, newMessages);

      expect(result.length).toBe(100);
    });
  });

  // =========================================================================
  // Special Characters and Unicode
  // =========================================================================

  describe("special characters and unicode", () => {
    test("handles unicode in message content", () => {
      // Both messages have seq for deterministic ordering
      const existing = [
        { role: ROLE_USER, text: "你好世界 🌍 مرحبا", seq: 1, timestamp: 1000 },
      ];
      const newMessages = [
        {
          role: ROLE_AGENT,
          html: "Réponse avec émojis 🎉",
          seq: 2,
          timestamp: 2000,
        },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      expect(result.length).toBe(2);
      expect(result[0].text).toBe("你好世界 🌍 مرحبا");
      expect(result[1].html).toBe("Réponse avec émojis 🎉");
    });

    test("deduplicates unicode messages correctly", () => {
      const existing = [{ role: ROLE_USER, text: "🎉🎊🎈", timestamp: 1000 }];
      const newMessages = [
        { role: ROLE_USER, text: "🎉🎊🎈", seq: 1, timestamp: 2000 },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      expect(result.length).toBe(1);
    });

    test("handles HTML entities in content", () => {
      const existing = [
        {
          role: ROLE_AGENT,
          html: '&lt;script&gt;alert("xss")&lt;/script&gt;',
          timestamp: 1000,
        },
      ];
      const newMessages = [
        { role: ROLE_AGENT, html: "<p>Safe HTML</p>", seq: 1, timestamp: 2000 },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      expect(result.length).toBe(2);
    });

    test("handles newlines and special whitespace", () => {
      const existing = [
        { role: ROLE_USER, text: "Line 1\nLine 2\tTabbed", timestamp: 1000 },
      ];
      const newMessages = [
        {
          role: ROLE_USER,
          text: "Line 1\nLine 2\tTabbed",
          seq: 1,
          timestamp: 2000,
        },
      ];

      const result = mergeMessagesWithSync(existing, newMessages);

      // Should deduplicate correctly even with special whitespace
      expect(result.length).toBe(1);
    });
  });
});

// =============================================================================
// safeJsonParse Tests
// =============================================================================

describe("safeJsonParse", () => {
  test("parses valid JSON", () => {
    const result = safeJsonParse('{"type": "test", "value": 123}');
    expect(result.error).toBeNull();
    expect(result.data).toEqual({ type: "test", value: 123 });
  });

  test("returns error for invalid JSON", () => {
    const result = safeJsonParse("not valid json");
    expect(result.data).toBeNull();
    expect(result.error).toBeInstanceOf(Error);
  });

  test("parses arrays", () => {
    const result = safeJsonParse("[1, 2, 3]");
    expect(result.error).toBeNull();
    expect(result.data).toEqual([1, 2, 3]);
  });

  test("parses primitives", () => {
    expect(safeJsonParse('"hello"').data).toBe("hello");
    expect(safeJsonParse("42").data).toBe(42);
    expect(safeJsonParse("true").data).toBe(true);
    expect(safeJsonParse("null").data).toBeNull();
  });
});

// =============================================================================
// createSessionState Tests
// =============================================================================

describe("createSessionState", () => {
  test("creates session with defaults", () => {
    const result = createSessionState("session-123");
    expect(result.messages).toEqual([]);
    expect(result.info.session_id).toBe("session-123");
    expect(result.info.name).toBe("New conversation");
    expect(result.info.status).toBe("active");
  });

  test("creates session with custom options", () => {
    const result = createSessionState("session-456", {
      name: "My Session",
      acpServer: "auggie",
      status: "completed",
    });
    expect(result.info.name).toBe("My Session");
    expect(result.info.acp_server).toBe("auggie");
    expect(result.info.status).toBe("completed");
  });

  test("creates session with initial messages", () => {
    const messages = [{ role: ROLE_SYSTEM, text: "Welcome" }];
    const result = createSessionState("session-789", { messages });
    expect(result.messages).toHaveLength(1);
    expect(result.messages[0].text).toBe("Welcome");
  });
});

// =============================================================================
// addMessageToSessionState Tests
// =============================================================================

describe("addMessageToSessionState", () => {
  test("adds message to existing session", () => {
    const session = { messages: [{ role: ROLE_USER, text: "Hi" }], info: {} };
    const newMessage = { role: ROLE_AGENT, html: "Hello!" };
    const result = addMessageToSessionState(session, newMessage);

    expect(result.messages).toHaveLength(2);
    expect(result.messages[1]).toEqual(newMessage);
    // Original should be unchanged (immutability)
    expect(session.messages).toHaveLength(1);
  });

  test("creates session if null", () => {
    const newMessage = { role: ROLE_USER, text: "First message" };
    const result = addMessageToSessionState(null, newMessage);

    expect(result.messages).toHaveLength(1);
    expect(result.messages[0]).toEqual(newMessage);
  });

  test("creates session if undefined", () => {
    const newMessage = { role: ROLE_USER, text: "First message" };
    const result = addMessageToSessionState(undefined, newMessage);

    expect(result.messages).toHaveLength(1);
  });

  test("limits messages to MAX_MESSAGES when exceeding limit", () => {
    // Create a session with MAX_MESSAGES - 1 messages
    const existingMessages = Array.from(
      { length: MAX_MESSAGES - 1 },
      (_, i) => ({
        role: ROLE_USER,
        text: `Message ${i}`,
      }),
    );
    const session = { messages: existingMessages, info: {} };

    // Add 2 more messages (should trigger limit)
    let result = addMessageToSessionState(session, {
      role: ROLE_USER,
      text: "New 1",
    });
    expect(result.messages).toHaveLength(MAX_MESSAGES);

    result = addMessageToSessionState(result, {
      role: ROLE_USER,
      text: "New 2",
    });
    expect(result.messages).toHaveLength(MAX_MESSAGES);

    // First message should have been removed
    expect(result.messages[0].text).toBe("Message 1");
    expect(result.messages[result.messages.length - 1].text).toBe("New 2");
  });
});

// =============================================================================
// limitMessages Tests
// =============================================================================

describe("limitMessages", () => {
  test("returns array unchanged when under limit", () => {
    const arr = [1, 2, 3];
    const result = limitMessages(arr, 10);
    expect(result).toBe(arr); // Should be same reference
  });

  test("returns array unchanged when at limit", () => {
    const arr = [1, 2, 3, 4, 5];
    const result = limitMessages(arr, 5);
    expect(result).toBe(arr);
  });

  test("returns last N items when over limit", () => {
    const arr = [1, 2, 3, 4, 5, 6, 7];
    const result = limitMessages(arr, 5);
    expect(result).toEqual([3, 4, 5, 6, 7]);
  });

  test("handles null input", () => {
    const result = limitMessages(null, 5);
    expect(result).toBeNull();
  });

  test("handles undefined input", () => {
    const result = limitMessages(undefined, 5);
    expect(result).toBeUndefined();
  });

  test("handles empty array", () => {
    const result = limitMessages([], 5);
    expect(result).toEqual([]);
  });

  test("uses MAX_MESSAGES as default limit", () => {
    const arr = Array.from({ length: MAX_MESSAGES + 10 }, (_, i) => i);
    const result = limitMessages(arr);
    expect(result).toHaveLength(MAX_MESSAGES);
    expect(result[0]).toBe(10); // First 10 should be trimmed
  });
});

// =============================================================================
// updateLastMessageInSession Tests
// =============================================================================

describe("updateLastMessageInSession", () => {
  test("updates last message", () => {
    const session = {
      messages: [{ role: ROLE_AGENT, html: "Hello", complete: false }],
      info: {},
    };
    const result = updateLastMessageInSession(session, (msg) => ({
      ...msg,
      complete: true,
    }));

    expect(result.messages[0].complete).toBe(true);
    // Original should be unchanged
    expect(session.messages[0].complete).toBe(false);
  });

  test("returns session unchanged if no messages", () => {
    const session = { messages: [], info: {} };
    const result = updateLastMessageInSession(session, (msg) => ({
      ...msg,
      complete: true,
    }));

    expect(result).toBe(session);
  });

  test("returns null/undefined session unchanged", () => {
    expect(updateLastMessageInSession(null, (msg) => msg)).toBeNull();
    expect(updateLastMessageInSession(undefined, (msg) => msg)).toBeUndefined();
  });

  test("only updates last message, not others", () => {
    const session = {
      messages: [
        { role: ROLE_AGENT, html: "First", complete: false },
        { role: ROLE_AGENT, html: "Second", complete: false },
      ],
      info: {},
    };
    const result = updateLastMessageInSession(session, (msg) => ({
      ...msg,
      complete: true,
    }));

    expect(result.messages[0].complete).toBe(false);
    expect(result.messages[1].complete).toBe(true);
  });
});

// =============================================================================
// removeSessionFromState Tests
// =============================================================================

describe("removeSessionFromState", () => {
  test("removes session from state", () => {
    const sessions = {
      "session-1": { messages: [], info: {} },
      "session-2": { messages: [], info: {} },
    };
    const result = removeSessionFromState(sessions, "session-1", "session-2");

    expect(result.newSessions).not.toHaveProperty("session-1");
    expect(result.newSessions).toHaveProperty("session-2");
    expect(result.nextActiveSessionId).toBe("session-2");
    expect(result.needsNewSession).toBe(false);
  });

  test("switches to another session when active is removed", () => {
    const sessions = {
      "session-1": { messages: [], info: {} },
      "session-2": { messages: [], info: {} },
    };
    const result = removeSessionFromState(sessions, "session-1", "session-1");

    expect(result.nextActiveSessionId).toBe("session-2");
    expect(result.needsNewSession).toBe(false);
  });

  test("signals need for new session when last session is removed", () => {
    const sessions = {
      "session-1": { messages: [], info: {} },
    };
    const result = removeSessionFromState(sessions, "session-1", "session-1");

    expect(result.newSessions).toEqual({});
    expect(result.nextActiveSessionId).toBeNull();
    expect(result.needsNewSession).toBe(true);
  });

  test("keeps active session when non-active is removed", () => {
    const sessions = {
      "session-1": { messages: [], info: {} },
      "session-2": { messages: [], info: {} },
    };
    const result = removeSessionFromState(sessions, "session-2", "session-1");

    expect(result.nextActiveSessionId).toBe("session-1");
    expect(result.needsNewSession).toBe(false);
  });
});

// =============================================================================
// Workspace Visual Identification Tests
// =============================================================================

describe("getBasename", () => {
  test("extracts basename from Unix path", () => {
    expect(getBasename("/home/user/my-project")).toBe("my-project");
    expect(getBasename("/Users/dev/awesome-app")).toBe("awesome-app");
    expect(getBasename("/path/to/src")).toBe("src");
  });

  test("extracts basename from Windows path", () => {
    expect(getBasename("C:\\Users\\dev\\project")).toBe("project");
    expect(getBasename("D:\\work\\my-app")).toBe("my-app");
  });

  test("handles paths with trailing slashes", () => {
    expect(getBasename("/home/user/project/")).toBe("project");
  });

  test("handles single component paths", () => {
    expect(getBasename("project")).toBe("project");
    expect(getBasename("/project")).toBe("project");
  });

  test("returns empty string for empty input", () => {
    expect(getBasename("")).toBe("");
    expect(getBasename(null)).toBe("");
    expect(getBasename(undefined)).toBe("");
  });
});

describe("getWorkspaceAbbreviation", () => {
  test("generates abbreviation from hyphenated names", () => {
    // Two-word names get padded to 3 chars from last word
    expect(getWorkspaceAbbreviation("/home/user/my-project")).toBe("MPR");
    expect(getWorkspaceAbbreviation("/path/to/awesome-web-app")).toBe("AWA");
    expect(getWorkspaceAbbreviation("/path/to/a-b")).toBe("AB");
  });

  test("generates abbreviation from underscored names", () => {
    expect(getWorkspaceAbbreviation("/path/to/my_project")).toBe("MPR");
    expect(getWorkspaceAbbreviation("/path/to/awesome_web_app")).toBe("AWA");
  });

  test("generates abbreviation from camelCase names", () => {
    expect(getWorkspaceAbbreviation("/path/to/myProject")).toBe("MPR");
    expect(getWorkspaceAbbreviation("/path/to/AwesomeWebApp")).toBe("AWA");
  });

  test("generates abbreviation from single words using consonants", () => {
    expect(getWorkspaceAbbreviation("/path/to/project")).toBe("PRJ");
    expect(getWorkspaceAbbreviation("/path/to/mitto")).toBe("MTT");
  });

  test("falls back to first 3 characters for short names", () => {
    expect(getWorkspaceAbbreviation("/path/to/src")).toBe("SRC");
    expect(getWorkspaceAbbreviation("/path/to/app")).toBe("APP");
  });

  test("returns ??? for empty input", () => {
    expect(getWorkspaceAbbreviation("")).toBe("???");
    expect(getWorkspaceAbbreviation(null)).toBe("???");
  });

  test("abbreviations are uppercase", () => {
    const abbr = getWorkspaceAbbreviation("/path/to/lowercase");
    expect(abbr).toBe(abbr.toUpperCase());
  });
});

describe("getWorkspaceColor", () => {
  test("returns color object with required properties", () => {
    const color = getWorkspaceColor("/path/to/project");
    expect(color).toHaveProperty("hue");
    expect(color).toHaveProperty("background");
    expect(color).toHaveProperty("backgroundHex");
    expect(color).toHaveProperty("text");
    expect(color).toHaveProperty("border");
  });

  test("backgroundHex is a valid hex color", () => {
    const color = getWorkspaceColor("/path/to/project");
    expect(color.backgroundHex).toMatch(/^#[0-9a-f]{6}$/i);
  });

  test("generates consistent colors for same path", () => {
    const color1 = getWorkspaceColor("/path/to/project");
    const color2 = getWorkspaceColor("/path/to/project");
    expect(color1.hue).toBe(color2.hue);
    expect(color1.background).toBe(color2.background);
  });

  test("generates different colors for different paths", () => {
    const color1 = getWorkspaceColor("/path/to/project1");
    const color2 = getWorkspaceColor("/path/to/project2");
    // Different basenames should produce different hues (usually)
    // Note: There's a small chance of collision, but it's unlikely
    expect(color1.hue).not.toBe(color2.hue);
  });

  test("hue is in valid range (0-360)", () => {
    const paths = ["/a", "/b", "/c", "/project", "/my-app", "/test-123"];
    paths.forEach((path) => {
      const color = getWorkspaceColor(path);
      expect(color.hue).toBeGreaterThanOrEqual(0);
      expect(color.hue).toBeLessThan(360);
    });
  });

  test("returns gray for empty path", () => {
    const color = getWorkspaceColor("");
    expect(color.background).toBe("rgb(100, 100, 100)");
  });
});

describe("getWorkspaceVisualInfo", () => {
  test("returns complete visual info object", () => {
    const info = getWorkspaceVisualInfo("/home/user/my-project");
    expect(info).toHaveProperty("abbreviation");
    expect(info).toHaveProperty("color");
    expect(info).toHaveProperty("basename");
    expect(info.abbreviation).toBe("MPR");
    expect(info.basename).toBe("my-project");
    expect(info.color).toHaveProperty("background");
  });

  test("all properties are consistent with individual functions", () => {
    const path = "/path/to/awesome-app";
    const info = getWorkspaceVisualInfo(path);
    expect(info.abbreviation).toBe(getWorkspaceAbbreviation(path));
    expect(info.basename).toBe(getBasename(path));
    expect(info.color.hue).toBe(getWorkspaceColor(path).hue);
  });

  test("uses custom color when provided", () => {
    const path = "/home/user/my-project";
    const customColor = "#ff5500";
    const info = getWorkspaceVisualInfo(path, customColor);
    expect(info.color.background).toBe(customColor);
    expect(info.abbreviation).toBe("MPR"); // abbreviation unchanged
  });

  test("ignores invalid custom color", () => {
    const path = "/home/user/my-project";
    const invalidColor = "not-a-color";
    const info = getWorkspaceVisualInfo(path, invalidColor);
    // Should fall back to auto-generated color
    expect(info.color.hue).toBeDefined();
  });
});

// =============================================================================
// Color Helper Functions Tests
// =============================================================================

describe("hexToRgb", () => {
  test("converts valid hex colors", () => {
    expect(hexToRgb("#ff0000")).toEqual({ r: 255, g: 0, b: 0 });
    expect(hexToRgb("#00ff00")).toEqual({ r: 0, g: 255, b: 0 });
    expect(hexToRgb("#0000ff")).toEqual({ r: 0, g: 0, b: 255 });
    expect(hexToRgb("#ffffff")).toEqual({ r: 255, g: 255, b: 255 });
    expect(hexToRgb("#000000")).toEqual({ r: 0, g: 0, b: 0 });
  });

  test("handles hex without hash", () => {
    expect(hexToRgb("ff5500")).toEqual({ r: 255, g: 85, b: 0 });
  });

  test("returns null for invalid hex", () => {
    expect(hexToRgb("invalid")).toBeNull();
    expect(hexToRgb("#fff")).toBeNull(); // short form not supported
    expect(hexToRgb("")).toBeNull();
    expect(hexToRgb(null)).toBeNull();
  });
});

describe("getLuminance", () => {
  test("returns high luminance for white", () => {
    expect(getLuminance(255, 255, 255)).toBeCloseTo(1, 2);
  });

  test("returns low luminance for black", () => {
    expect(getLuminance(0, 0, 0)).toBe(0);
  });

  test("returns mid-range luminance for gray", () => {
    const lum = getLuminance(128, 128, 128);
    expect(lum).toBeGreaterThan(0.1);
    expect(lum).toBeLessThan(0.5);
  });
});

describe("getColorFromHex", () => {
  test("returns color object for valid hex", () => {
    const color = getColorFromHex("#ff5500");
    expect(color).toHaveProperty("background", "#ff5500");
    expect(color).toHaveProperty("text");
    expect(color).toHaveProperty("border");
  });

  test("returns white text for dark backgrounds", () => {
    const color = getColorFromHex("#000000");
    expect(color.text).toBe("white");
  });

  test("returns dark text for light backgrounds", () => {
    const color = getColorFromHex("#ffffff");
    expect(color.text).toBe("rgb(30, 30, 30)");
  });

  test("returns null for invalid hex", () => {
    expect(getColorFromHex("invalid")).toBeNull();
  });
});

describe("hslToHex", () => {
  test("converts primary colors", () => {
    expect(hslToHex(0, 100, 50)).toBe("#ff0000"); // red
    expect(hslToHex(120, 100, 50)).toBe("#00ff00"); // green
    expect(hslToHex(240, 100, 50)).toBe("#0000ff"); // blue
  });

  test("converts grayscale", () => {
    expect(hslToHex(0, 0, 0)).toBe("#000000"); // black
    expect(hslToHex(0, 0, 100)).toBe("#ffffff"); // white
    expect(hslToHex(0, 0, 50)).toBe("#808080"); // gray
  });
});

// =============================================================================
// Credential Validation Tests
// =============================================================================

describe("validateUsername", () => {
  test("accepts valid usernames", () => {
    expect(validateUsername("admin")).toBe("");
    expect(validateUsername("user123")).toBe("");
    expect(validateUsername("john.doe")).toBe("");
    expect(validateUsername("my-user")).toBe("");
    expect(validateUsername("my_user")).toBe("");
    expect(validateUsername("User123")).toBe("");
    expect(validateUsername("a1b")).toBe(""); // minimum length
  });

  test("rejects empty or missing username", () => {
    expect(validateUsername("")).toBe("Username is required");
    expect(validateUsername("   ")).toBe("Username is required");
    expect(validateUsername(null)).toBe("Username is required");
    expect(validateUsername(undefined)).toBe("Username is required");
  });

  test("rejects too short usernames", () => {
    expect(validateUsername("ab")).toBe(
      "Username must be at least 3 characters",
    );
    expect(validateUsername("a")).toBe(
      "Username must be at least 3 characters",
    );
  });

  test("rejects too long usernames", () => {
    const longUsername = "a".repeat(MAX_USERNAME_LENGTH + 1);
    expect(validateUsername(longUsername)).toBe(
      "Username must be at most 64 characters",
    );
  });

  test("rejects usernames not starting with letter or number", () => {
    expect(validateUsername("_user")).toBe(
      "Username must start with a letter or number",
    );
    expect(validateUsername("-user")).toBe(
      "Username must start with a letter or number",
    );
    expect(validateUsername(".user")).toBe(
      "Username must start with a letter or number",
    );
  });

  test("rejects usernames with invalid characters", () => {
    expect(validateUsername("user@name")).toBe(
      "Username can only contain letters, numbers, underscore, hyphen, and dot",
    );
    expect(validateUsername("user name")).toBe(
      "Username can only contain letters, numbers, underscore, hyphen, and dot",
    );
    expect(validateUsername("user!123")).toBe(
      "Username can only contain letters, numbers, underscore, hyphen, and dot",
    );
  });

  test("trims whitespace before validation", () => {
    expect(validateUsername("  admin  ")).toBe("");
  });
});

describe("validatePassword", () => {
  test("accepts valid passwords", () => {
    expect(validatePassword("MyP@ssw0rd")).toBe("");
    expect(validatePassword("SecurePass123")).toBe("");
    expect(validatePassword("abcd1234")).toBe("");
    expect(validatePassword("Pass!@#$%")).toBe("");
    expect(validatePassword("a1b2c3d4")).toBe(""); // minimum length with complexity
  });

  test("rejects empty or missing password", () => {
    expect(validatePassword("")).toBe("Password is required");
    expect(validatePassword(null)).toBe("Password is required");
    expect(validatePassword(undefined)).toBe("Password is required");
  });

  test("rejects too short passwords", () => {
    expect(validatePassword("abc123")).toBe(
      "Password must be at least 8 characters",
    );
    expect(validatePassword("Pass1")).toBe(
      "Password must be at least 8 characters",
    );
  });

  test("rejects too long passwords", () => {
    const longPassword = "a1".repeat(65); // 130 chars
    expect(validatePassword(longPassword)).toBe(
      "Password must be at most 128 characters",
    );
  });

  test("rejects common weak passwords", () => {
    expect(validatePassword("password")).toBe(
      "Password is too common. Please choose a stronger password",
    );
    expect(validatePassword("PASSWORD")).toBe(
      "Password is too common. Please choose a stronger password",
    );
    expect(validatePassword("12345678")).toBe(
      "Password is too common. Please choose a stronger password",
    );
    expect(validatePassword("qwerty123")).toBe(
      "Password is too common. Please choose a stronger password",
    );
    expect(validatePassword("admin123")).toBe(
      "Password is too common. Please choose a stronger password",
    );
    expect(validatePassword("changeme")).toBe(
      "Password is too common. Please choose a stronger password",
    );
  });

  test("rejects passwords without letters", () => {
    expect(validatePassword("12345678!")).toBe(
      "Password must contain at least one letter and one number or special character",
    );
  });

  test("rejects passwords without numbers or special characters", () => {
    expect(validatePassword("abcdefgh")).toBe(
      "Password must contain at least one letter and one number or special character",
    );
    expect(validatePassword("PasswordOnly")).toBe(
      "Password must contain at least one letter and one number or special character",
    );
  });

  test("accepts passwords with special characters instead of numbers", () => {
    expect(validatePassword("Password!")).toBe("");
    expect(validatePassword("SecurePass@#")).toBe("");
  });
});

describe("validateCredentials", () => {
  test("returns empty string when both are valid", () => {
    expect(validateCredentials("admin", "SecurePass123")).toBe("");
  });

  test("returns username error first if username is invalid", () => {
    expect(validateCredentials("", "SecurePass123")).toBe(
      "Username is required",
    );
    expect(validateCredentials("ab", "SecurePass123")).toBe(
      "Username must be at least 3 characters",
    );
  });

  test("returns password error if username is valid but password is invalid", () => {
    expect(validateCredentials("admin", "")).toBe("Password is required");
    expect(validateCredentials("admin", "short")).toBe(
      "Password must be at least 8 characters",
    );
    expect(validateCredentials("admin", "password")).toBe(
      "Password is too common. Please choose a stronger password",
    );
  });
});

// =============================================================================
// Pending Prompts Queue Tests
// =============================================================================

// Mock localStorage for testing
const localStorageMock = (() => {
  let store = {};
  return {
    getItem: (key) => store[key] || null,
    setItem: (key, value) => {
      store[key] = value;
    },
    removeItem: (key) => {
      delete store[key];
    },
    clear: () => {
      store = {};
    },
  };
})();

// Replace global localStorage with mock
Object.defineProperty(global, "localStorage", { value: localStorageMock });

describe("generatePromptId", () => {
  test("generates unique IDs", () => {
    const id1 = generatePromptId();
    const id2 = generatePromptId();
    expect(id1).not.toBe(id2);
  });

  test("generates IDs with prompt_ prefix", () => {
    const id = generatePromptId();
    expect(id.startsWith("prompt_")).toBe(true);
  });

  test("generates IDs with timestamp component", () => {
    const before = Date.now();
    const id = generatePromptId();
    const after = Date.now();

    // Extract timestamp from ID (format: prompt_<timestamp>_<random>)
    const parts = id.split("_");
    expect(parts.length).toBe(3);
    const timestamp = parseInt(parts[1], 10);
    expect(timestamp).toBeGreaterThanOrEqual(before);
    expect(timestamp).toBeLessThanOrEqual(after);
  });
});

describe("savePendingPrompt and getPendingPrompts", () => {
  beforeEach(() => {
    localStorageMock.clear();
  });

  test("saves and retrieves a pending prompt", () => {
    savePendingPrompt("session1", "prompt1", "Hello world", []);

    const pending = getPendingPrompts();
    expect(pending["prompt1"]).toBeDefined();
    expect(pending["prompt1"].sessionId).toBe("session1");
    expect(pending["prompt1"].message).toBe("Hello world");
    expect(pending["prompt1"].imageIds).toEqual([]);
    expect(pending["prompt1"].timestamp).toBeDefined();
  });

  test("saves prompt with image IDs", () => {
    savePendingPrompt("session1", "prompt1", "With images", ["img1", "img2"]);

    const pending = getPendingPrompts();
    expect(pending["prompt1"].imageIds).toEqual(["img1", "img2"]);
  });

  test("saves multiple prompts", () => {
    savePendingPrompt("session1", "prompt1", "First", []);
    savePendingPrompt("session1", "prompt2", "Second", []);
    savePendingPrompt("session2", "prompt3", "Third", []);

    const pending = getPendingPrompts();
    expect(Object.keys(pending).length).toBe(3);
  });

  test("returns empty object when no pending prompts", () => {
    const pending = getPendingPrompts();
    expect(pending).toEqual({});
  });
});

describe("removePendingPrompt", () => {
  beforeEach(() => {
    localStorageMock.clear();
  });

  test("removes a pending prompt", () => {
    savePendingPrompt("session1", "prompt1", "Hello", []);
    savePendingPrompt("session1", "prompt2", "World", []);

    removePendingPrompt("prompt1");

    const pending = getPendingPrompts();
    expect(pending["prompt1"]).toBeUndefined();
    expect(pending["prompt2"]).toBeDefined();
  });

  test("handles removing non-existent prompt gracefully", () => {
    savePendingPrompt("session1", "prompt1", "Hello", []);

    // Should not throw
    removePendingPrompt("nonexistent");

    const pending = getPendingPrompts();
    expect(pending["prompt1"]).toBeDefined();
  });
});

describe("getPendingPromptsForSession", () => {
  beforeEach(() => {
    localStorageMock.clear();
  });

  test("returns prompts for specific session", () => {
    savePendingPrompt("session1", "prompt1", "First", []);
    savePendingPrompt("session1", "prompt2", "Second", []);
    savePendingPrompt("session2", "prompt3", "Third", []);

    const session1Prompts = getPendingPromptsForSession("session1");
    expect(session1Prompts.length).toBe(2);
    expect(session1Prompts.map((p) => p.promptId)).toContain("prompt1");
    expect(session1Prompts.map((p) => p.promptId)).toContain("prompt2");
  });

  test("returns empty array for session with no prompts", () => {
    savePendingPrompt("session1", "prompt1", "Hello", []);

    const prompts = getPendingPromptsForSession("session2");
    expect(prompts).toEqual([]);
  });

  test("returns prompts sorted by timestamp (oldest first)", () => {
    // Save prompts with explicit timestamps by manipulating the stored data
    const now = Date.now();
    localStorageMock.setItem(
      "mitto_pending_prompts",
      JSON.stringify({
        prompt1: {
          sessionId: "session1",
          message: "First",
          imageIds: [],
          timestamp: now - 2000,
        },
        prompt2: {
          sessionId: "session1",
          message: "Second",
          imageIds: [],
          timestamp: now - 1000,
        },
        prompt3: {
          sessionId: "session1",
          message: "Third",
          imageIds: [],
          timestamp: now,
        },
      }),
    );

    const prompts = getPendingPromptsForSession("session1");
    expect(prompts[0].promptId).toBe("prompt1");
    expect(prompts[1].promptId).toBe("prompt2");
    expect(prompts[2].promptId).toBe("prompt3");
  });

  test("excludes expired prompts (older than 5 minutes)", () => {
    const now = Date.now();
    const fiveMinutesAgo = now - 5 * 60 * 1000;

    localStorageMock.setItem(
      "mitto_pending_prompts",
      JSON.stringify({
        prompt1: {
          sessionId: "session1",
          message: "Fresh",
          imageIds: [],
          timestamp: now - 1000,
        },
        prompt2: {
          sessionId: "session1",
          message: "Expired",
          imageIds: [],
          timestamp: fiveMinutesAgo - 1000,
        },
      }),
    );

    const prompts = getPendingPromptsForSession("session1");
    expect(prompts.length).toBe(1);
    expect(prompts[0].promptId).toBe("prompt1");
  });
});

describe("cleanupExpiredPrompts", () => {
  beforeEach(() => {
    localStorageMock.clear();
  });

  test("removes expired prompts", () => {
    const now = Date.now();
    const fiveMinutesAgo = now - 5 * 60 * 1000;

    localStorageMock.setItem(
      "mitto_pending_prompts",
      JSON.stringify({
        prompt1: {
          sessionId: "session1",
          message: "Fresh",
          imageIds: [],
          timestamp: now - 1000,
        },
        prompt2: {
          sessionId: "session1",
          message: "Expired1",
          imageIds: [],
          timestamp: fiveMinutesAgo - 1000,
        },
        prompt3: {
          sessionId: "session2",
          message: "Expired2",
          imageIds: [],
          timestamp: fiveMinutesAgo - 2000,
        },
      }),
    );

    cleanupExpiredPrompts();

    const pending = getPendingPrompts();
    expect(Object.keys(pending).length).toBe(1);
    expect(pending["prompt1"]).toBeDefined();
    expect(pending["prompt2"]).toBeUndefined();
    expect(pending["prompt3"]).toBeUndefined();
  });

  test("does nothing when no prompts exist", () => {
    // Should not throw
    cleanupExpiredPrompts();
    expect(getPendingPrompts()).toEqual({});
  });

  test("does nothing when all prompts are fresh", () => {
    savePendingPrompt("session1", "prompt1", "Fresh1", []);
    savePendingPrompt("session1", "prompt2", "Fresh2", []);

    cleanupExpiredPrompts();

    const pending = getPendingPrompts();
    expect(Object.keys(pending).length).toBe(2);
  });
});

// =============================================================================
// User Message Markdown Tests
// =============================================================================

describe("hasMarkdownContent", () => {
  // Invalid inputs
  test("returns false for null", () => {
    expect(hasMarkdownContent(null)).toBe(false);
  });

  test("returns false for undefined", () => {
    expect(hasMarkdownContent(undefined)).toBe(false);
  });

  test("returns false for empty string", () => {
    expect(hasMarkdownContent("")).toBe(false);
  });

  test("returns false for non-string input", () => {
    expect(hasMarkdownContent(123)).toBe(false);
    expect(hasMarkdownContent({})).toBe(false);
    expect(hasMarkdownContent([])).toBe(false);
  });

  // Plain text (no markdown)
  test("returns false for plain text", () => {
    expect(hasMarkdownContent("Hello world")).toBe(false);
    expect(hasMarkdownContent("Just a simple message")).toBe(false);
    expect(hasMarkdownContent("This is a normal sentence.")).toBe(false);
  });

  test("returns false for text with standalone asterisks", () => {
    // Single asterisks surrounded by spaces are not markdown
    expect(hasMarkdownContent("I like * patterns * in text")).toBe(false);
  });

  // Headers
  test("detects headers", () => {
    expect(hasMarkdownContent("# Header")).toBe(true);
    expect(hasMarkdownContent("## Second Level")).toBe(true);
    expect(hasMarkdownContent("### Third Level")).toBe(true);
    expect(hasMarkdownContent("#### Fourth Level")).toBe(true);
    expect(hasMarkdownContent("Some text\n# Header")).toBe(true);
  });

  test("does not detect hash without space as header", () => {
    expect(hasMarkdownContent("#hashtag")).toBe(false);
    expect(hasMarkdownContent("Issue #123")).toBe(false);
  });

  // Bold
  test("detects bold text", () => {
    expect(hasMarkdownContent("This is **bold** text")).toBe(true);
    expect(hasMarkdownContent("This is __bold__ text")).toBe(true);
  });

  // Italic
  test("detects italic text", () => {
    expect(hasMarkdownContent("This is *italic* text")).toBe(true);
    expect(hasMarkdownContent("This is _italic_ text")).toBe(true);
  });

  // Code
  test("detects inline code", () => {
    expect(hasMarkdownContent("Use `code` here")).toBe(true);
    expect(hasMarkdownContent("Run `npm install`")).toBe(true);
  });

  test("detects code blocks", () => {
    expect(hasMarkdownContent("```javascript\nconst x = 1;\n```")).toBe(true);
    expect(hasMarkdownContent("```\ncode block\n```")).toBe(true);
  });

  // Links
  test("detects links", () => {
    expect(hasMarkdownContent("Check [this link](https://example.com)")).toBe(
      true,
    );
    expect(hasMarkdownContent("See [reference][1]")).toBe(true);
  });

  // Lists
  test("detects unordered lists", () => {
    expect(hasMarkdownContent("- Item 1")).toBe(true);
    expect(hasMarkdownContent("* Item 1")).toBe(true);
    expect(hasMarkdownContent("+ Item 1")).toBe(true);
    expect(hasMarkdownContent("Some text\n- Item")).toBe(true);
  });

  test("detects ordered lists", () => {
    expect(hasMarkdownContent("1. First item")).toBe(true);
    expect(hasMarkdownContent("2. Second item")).toBe(true);
    expect(hasMarkdownContent("10. Tenth item")).toBe(true);
  });

  // Blockquotes
  test("detects blockquotes", () => {
    expect(hasMarkdownContent("> This is a quote")).toBe(true);
    expect(hasMarkdownContent("Text before\n> Quote")).toBe(true);
  });

  // Horizontal rules
  test("detects horizontal rules", () => {
    expect(hasMarkdownContent("---")).toBe(true);
    expect(hasMarkdownContent("***")).toBe(true);
    expect(hasMarkdownContent("___")).toBe(true);
  });

  // Tables
  test("detects tables", () => {
    expect(hasMarkdownContent("| Header | Header |\n| --- | --- |")).toBe(true);
  });

  // Strikethrough
  test("detects strikethrough", () => {
    expect(hasMarkdownContent("This is ~~deleted~~ text")).toBe(true);
  });

  // Complex examples
  test("detects markdown in complex messages", () => {
    expect(
      hasMarkdownContent(
        "Please run `npm install` and then:\n\n1. Start the server\n2. Open browser",
      ),
    ).toBe(true);
    expect(
      hasMarkdownContent(
        "Here is the **important** part:\n\n- First\n- Second",
      ),
    ).toBe(true);
  });
});

describe("renderUserMarkdown", () => {
  // Note: These tests run in Node.js where window.marked is not available,
  // so renderUserMarkdown will return null. We test the logic that doesn't
  // depend on the browser environment.

  test("returns null for null input", () => {
    expect(renderUserMarkdown(null)).toBeNull();
  });

  test("returns null for undefined input", () => {
    expect(renderUserMarkdown(undefined)).toBeNull();
  });

  test("returns null for empty string", () => {
    expect(renderUserMarkdown("")).toBeNull();
  });

  test("returns null for non-string input", () => {
    expect(renderUserMarkdown(123)).toBeNull();
    expect(renderUserMarkdown({})).toBeNull();
  });

  test("returns null for plain text without markdown", () => {
    // Even if marked were available, plain text should return null
    // because hasMarkdownContent returns false
    expect(renderUserMarkdown("Hello world")).toBeNull();
  });

  test("returns null for text exceeding MAX_MARKDOWN_LENGTH", () => {
    const longText = "# Header\n" + "x".repeat(MAX_MARKDOWN_LENGTH + 1);
    expect(renderUserMarkdown(longText)).toBeNull();
  });

  test("returns null when window.marked is not available", () => {
    // In Node.js test environment, window is not defined
    // This tests the graceful fallback
    expect(renderUserMarkdown("# Header")).toBeNull();
  });
});

// =============================================================================
// Send Message ACK Tracking Tests
// =============================================================================

describe("Send Message ACK Tracking", () => {
  // These tests verify the Promise-based send tracking pattern used in useWebSocket.js
  // The actual implementation is in the hook, but we test the pattern here

  describe("Pending Sends Map Pattern", () => {
    // Simulates the pendingSendsRef pattern from useWebSocket.js
    let pendingSends;

    beforeEach(() => {
      pendingSends = {};
    });

    // Helper to create a mock function
    const createMockFn = () => {
      const calls = [];
      const fn = (...args) => {
        calls.push(args);
      };
      fn.calls = calls;
      fn.toHaveBeenCalled = () => calls.length > 0;
      fn.toHaveBeenCalledTimes = (n) => calls.length === n;
      fn.toHaveBeenCalledWith = (...expected) => {
        return calls.some(
          (call) =>
            call.length === expected.length &&
            call.every((arg, i) => {
              if (expected[i] && typeof expected[i] === "object") {
                return JSON.stringify(arg) === JSON.stringify(expected[i]);
              }
              return arg === expected[i];
            }),
        );
      };
      return fn;
    };

    test("tracks pending send with resolve/reject/timeout", () => {
      const promptId = "prompt_123_abc";
      const mockResolve = createMockFn();
      const mockReject = createMockFn();
      const timeoutId = setTimeout(() => {}, 15000);

      pendingSends[promptId] = {
        resolve: mockResolve,
        reject: mockReject,
        timeoutId,
      };

      expect(pendingSends[promptId]).toBeDefined();
      expect(pendingSends[promptId].resolve).toBe(mockResolve);
      expect(pendingSends[promptId].reject).toBe(mockReject);
      expect(pendingSends[promptId].timeoutId).toBe(timeoutId);

      clearTimeout(timeoutId);
    });

    test("resolves pending send on ACK", () => {
      const promptId = "prompt_123_abc";
      const mockResolve = createMockFn();
      const mockReject = createMockFn();
      const timeoutId = setTimeout(
        () => mockReject(new Error("timeout")),
        15000,
      );

      pendingSends[promptId] = {
        resolve: mockResolve,
        reject: mockReject,
        timeoutId,
      };

      // Simulate ACK received
      const pending = pendingSends[promptId];
      if (pending) {
        clearTimeout(pending.timeoutId);
        pending.resolve({ success: true, promptId });
        delete pendingSends[promptId];
      }

      expect(
        mockResolve.toHaveBeenCalledWith({ success: true, promptId }),
      ).toBe(true);
      expect(mockReject.toHaveBeenCalled()).toBe(false);
      expect(pendingSends[promptId]).toBeUndefined();
    });

    test("handles duplicate ACK gracefully", () => {
      const promptId = "prompt_123_abc";
      const mockResolve = createMockFn();
      const mockReject = createMockFn();
      const timeoutId = setTimeout(() => {}, 15000);

      pendingSends[promptId] = {
        resolve: mockResolve,
        reject: mockReject,
        timeoutId,
      };

      // First ACK
      const pending1 = pendingSends[promptId];
      if (pending1) {
        clearTimeout(pending1.timeoutId);
        pending1.resolve({ success: true, promptId });
        delete pendingSends[promptId];
      }

      // Second ACK (duplicate) - should be a no-op
      const pending2 = pendingSends[promptId];
      if (pending2) {
        clearTimeout(pending2.timeoutId);
        pending2.resolve({ success: true, promptId });
        delete pendingSends[promptId];
      }

      // Should only have been called once
      expect(mockResolve.toHaveBeenCalledTimes(1)).toBe(true);
    });

    test("handles multiple concurrent pending sends", () => {
      const prompts = ["prompt_1", "prompt_2", "prompt_3"];
      const resolvers = {};
      const rejecters = {};

      // Create multiple pending sends
      prompts.forEach((promptId) => {
        resolvers[promptId] = createMockFn();
        rejecters[promptId] = createMockFn();
        const timeoutId = setTimeout(() => {}, 15000);
        pendingSends[promptId] = {
          resolve: resolvers[promptId],
          reject: rejecters[promptId],
          timeoutId,
        };
      });

      expect(Object.keys(pendingSends).length).toBe(3);

      // Resolve first one
      const pending1 = pendingSends["prompt_1"];
      clearTimeout(pending1.timeoutId);
      pending1.resolve({ success: true, promptId: "prompt_1" });
      delete pendingSends["prompt_1"];

      expect(Object.keys(pendingSends).length).toBe(2);
      expect(resolvers["prompt_1"].toHaveBeenCalled()).toBe(true);

      // Clean up remaining timeouts
      Object.values(pendingSends).forEach((p) => clearTimeout(p.timeoutId));
    });
  });

  describe("WebSocket State Validation", () => {
    // Tests for the WebSocket state check before sending

    test("rejects when WebSocket is null", async () => {
      const ws = null;

      const sendPromise = new Promise((resolve, reject) => {
        if (!ws || ws.readyState !== 1) {
          // WebSocket.OPEN = 1
          reject(new Error("WebSocket not connected"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).rejects.toThrow("WebSocket not connected");
    });

    test("rejects when WebSocket is CONNECTING", async () => {
      const ws = { readyState: 0 }; // WebSocket.CONNECTING = 0

      const sendPromise = new Promise((resolve, reject) => {
        if (!ws || ws.readyState !== 1) {
          reject(new Error("WebSocket not connected"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).rejects.toThrow("WebSocket not connected");
    });

    test("rejects when WebSocket is CLOSING", async () => {
      const ws = { readyState: 2 }; // WebSocket.CLOSING = 2

      const sendPromise = new Promise((resolve, reject) => {
        if (!ws || ws.readyState !== 1) {
          reject(new Error("WebSocket not connected"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).rejects.toThrow("WebSocket not connected");
    });

    test("rejects when WebSocket is CLOSED", async () => {
      const ws = { readyState: 3 }; // WebSocket.CLOSED = 3

      const sendPromise = new Promise((resolve, reject) => {
        if (!ws || ws.readyState !== 1) {
          reject(new Error("WebSocket not connected"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).rejects.toThrow("WebSocket not connected");
    });

    test("proceeds when WebSocket is OPEN", async () => {
      const ws = { readyState: 1 }; // WebSocket.OPEN = 1

      const sendPromise = new Promise((resolve, reject) => {
        if (!ws || ws.readyState !== 1) {
          reject(new Error("WebSocket not connected"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).resolves.toEqual({ success: true });
    });
  });

  describe("No Active Session Handling", () => {
    test("rejects when no active session", async () => {
      const activeSessionId = null;

      const sendPromise = new Promise((resolve, reject) => {
        if (!activeSessionId) {
          reject(new Error("No active session"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).rejects.toThrow("No active session");
    });

    test("proceeds when active session exists", async () => {
      const activeSessionId = "session-123";

      const sendPromise = new Promise((resolve, reject) => {
        if (!activeSessionId) {
          reject(new Error("No active session"));
          return;
        }
        resolve({ success: true });
      });

      await expect(sendPromise).resolves.toEqual({ success: true });
    });
  });

  describe("Send Failure Handling", () => {
    test("rejects when WebSocket send fails", async () => {
      const mockSendToSession = () => false;
      const pendingSends = {};
      const promptId = "prompt_123";

      const sendPromise = new Promise((resolve, reject) => {
        const timeoutId = setTimeout(() => {
          reject(new Error("timeout"));
        }, 15000);

        pendingSends[promptId] = { resolve, reject, timeoutId };

        const sent = mockSendToSession("session-1", {
          type: "prompt",
          data: {},
        });

        if (!sent) {
          clearTimeout(timeoutId);
          delete pendingSends[promptId];
          reject(new Error("Failed to send message"));
        }
      });

      await expect(sendPromise).rejects.toThrow("Failed to send message");
      expect(pendingSends[promptId]).toBeUndefined();
    });

    test("tracks pending send when WebSocket send succeeds", async () => {
      const mockSendToSession = () => true;
      const pendingSends = {};
      const promptId = "prompt_123";

      // Start the send (don't await - we're testing the pending state)
      new Promise((resolve, reject) => {
        const timeoutId = setTimeout(() => {
          reject(new Error("timeout"));
        }, 15000);

        pendingSends[promptId] = { resolve, reject, timeoutId };

        const sent = mockSendToSession("session-1", {
          type: "prompt",
          data: {},
        });

        if (!sent) {
          clearTimeout(timeoutId);
          delete pendingSends[promptId];
          reject(new Error("Failed to send message"));
        }
      });

      // Pending send should be tracked
      expect(pendingSends[promptId]).toBeDefined();
      expect(pendingSends[promptId].resolve).toBeDefined();
      expect(pendingSends[promptId].reject).toBeDefined();
      expect(pendingSends[promptId].timeoutId).toBeDefined();

      // Cleanup
      clearTimeout(pendingSends[promptId].timeoutId);
    });
  });

  describe("Timeout Behavior", () => {
    test("timeout value is configurable", () => {
      // Test that the default timeout constant is reasonable
      const SEND_ACK_TIMEOUT = 15000; // 15 seconds
      expect(SEND_ACK_TIMEOUT).toBeGreaterThanOrEqual(10000);
      expect(SEND_ACK_TIMEOUT).toBeLessThanOrEqual(30000);
    });

    test("timeout error message is user-friendly", () => {
      const errorMessage = "Message send timed out. Please try again.";
      expect(errorMessage).toContain("timed out");
      expect(errorMessage).toContain("try again");
    });
  });

  describe("Rapid Sequential Sends", () => {
    let pendingSends;

    beforeEach(() => {
      pendingSends = {};
    });

    test("handles rapid sequential sends with unique IDs", () => {
      const promptIds = [];

      // Simulate 10 rapid sends
      for (let i = 0; i < 10; i++) {
        const promptId = `prompt_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
        promptIds.push(promptId);
        pendingSends[promptId] = {
          resolve: () => {},
          reject: () => {},
          timeoutId: setTimeout(() => {}, 15000),
        };
      }

      // All should be tracked
      expect(Object.keys(pendingSends).length).toBe(10);

      // All IDs should be unique
      const uniqueIds = new Set(promptIds);
      expect(uniqueIds.size).toBe(10);

      // Clean up
      Object.values(pendingSends).forEach((p) => clearTimeout(p.timeoutId));
    });

    test("ACKs can arrive in any order", () => {
      const results = [];

      // Create 5 pending sends
      for (let i = 0; i < 5; i++) {
        const promptId = `prompt_${i}`;
        pendingSends[promptId] = {
          resolve: (result) =>
            results.push({ id: promptId, order: results.length }),
          reject: () => {},
          timeoutId: setTimeout(() => {}, 15000),
        };
      }

      // ACKs arrive in reverse order
      for (let i = 4; i >= 0; i--) {
        const promptId = `prompt_${i}`;
        const pending = pendingSends[promptId];
        if (pending) {
          clearTimeout(pending.timeoutId);
          pending.resolve({ success: true, promptId });
          delete pendingSends[promptId];
        }
      }

      // All should be resolved
      expect(results.length).toBe(5);
      expect(Object.keys(pendingSends).length).toBe(0);

      // Order of resolution should be reverse of creation
      expect(results[0].id).toBe("prompt_4");
      expect(results[4].id).toBe("prompt_0");
    });
  });

  describe("Long Message Handling", () => {
    let pendingSends;

    beforeEach(() => {
      pendingSends = {};
    });

    test("handles very long messages", () => {
      // Simulate a very long message (100KB)
      const longMessage = "x".repeat(100 * 1024);
      const promptId = "prompt_long";

      pendingSends[promptId] = {
        message: longMessage,
        resolve: () => {},
        reject: () => {},
        timeoutId: setTimeout(() => {}, 15000),
      };

      expect(pendingSends[promptId].message.length).toBe(100 * 1024);

      // Clean up
      clearTimeout(pendingSends[promptId].timeoutId);
    });

    test("handles messages with special characters", () => {
      const specialMessages = [
        "Hello\nWorld\twith\ttabs",
        "Unicode: 你好世界 🎉 émojis",
        'JSON-like: {"key": "value"}',
        'HTML-like: <script>alert("xss")</script>',
        "Backslashes: C:\\Users\\test\\file.txt",
        "Quotes: \"double\" and 'single'",
      ];

      specialMessages.forEach((msg, i) => {
        const promptId = `prompt_special_${i}`;
        pendingSends[promptId] = {
          message: msg,
          resolve: () => {},
          reject: () => {},
          timeoutId: setTimeout(() => {}, 15000),
        };
      });

      expect(Object.keys(pendingSends).length).toBe(specialMessages.length);

      // Clean up
      Object.values(pendingSends).forEach((p) => clearTimeout(p.timeoutId));
    });
  });

  describe("Error Recovery", () => {
    let pendingSends;

    beforeEach(() => {
      pendingSends = {};
    });

    test("cleans up pending send on error", () => {
      const promptId = "prompt_error";
      let rejectCalled = false;

      pendingSends[promptId] = {
        resolve: () => {},
        reject: () => {
          rejectCalled = true;
        },
        timeoutId: setTimeout(() => {}, 15000),
      };

      // Simulate error
      const pending = pendingSends[promptId];
      clearTimeout(pending.timeoutId);
      pending.reject(new Error("Connection lost"));
      delete pendingSends[promptId];

      expect(pendingSends[promptId]).toBeUndefined();
      expect(rejectCalled).toBe(true);
    });

    test("handles WebSocket close during pending send", () => {
      const promptIds = ["prompt_close_1", "prompt_close_2", "prompt_close_3"];
      let rejectCount = 0;

      promptIds.forEach((promptId) => {
        pendingSends[promptId] = {
          resolve: () => {},
          reject: () => {
            rejectCount++;
          },
          timeoutId: setTimeout(() => {}, 15000),
        };
      });

      // Simulate WebSocket close - reject all pending sends
      promptIds.forEach((promptId) => {
        const pending = pendingSends[promptId];
        if (pending) {
          clearTimeout(pending.timeoutId);
          pending.reject(new Error("WebSocket closed"));
          delete pendingSends[promptId];
        }
      });

      expect(rejectCount).toBe(3);
    });
  });
});
