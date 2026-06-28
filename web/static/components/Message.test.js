/**
 * Unit tests for Message component logic
 *
 * Tests cover:
 * - isModelErrorThought detection patterns
 * - False positive avoidance for normal thinking text
 */

// =============================================================================
// Model Error Thought Detection Tests
// =============================================================================

/**
 * Check if a thought message appears to be reporting an upstream model/API error.
 * Duplicated from Message.js for testing (component imports window.preact globals).
 */
function isModelErrorThought(text) {
  if (!text) return false;
  const patterns = [
    /\bmodel\s+error\b/i,
    /\bapi\s+error\b/i,
    /\brate[\s-]?limit/i,
    /\boverloaded\b/i,
    /\bservice[\s_]unavailable\b/i,
    /\bfailed\s+due\s+to\b.*\b(?:model|api|upstream)\b/i,
  ];
  return patterns.some((p) => p.test(text));
}

describe("isModelErrorThought", () => {
  describe("detects model/API error patterns", () => {
    test("detects 'model error'", () => {
      expect(
        isModelErrorThought(
          "The agent failed due to a model error. Let me just...",
        ),
      ).toBe(true);
    });

    test("detects 'API error'", () => {
      expect(
        isModelErrorThought("I encountered an API error while processing"),
      ).toBe(true);
    });

    test("detects 'rate limit'", () => {
      expect(isModelErrorThought("I hit a rate limit, waiting...")).toBe(true);
    });

    test("detects 'rate-limit' with hyphen", () => {
      expect(isModelErrorThought("Got rate-limited on the request")).toBe(true);
    });

    test("detects 'ratelimit' without space", () => {
      expect(isModelErrorThought("A ratelimit was hit")).toBe(true);
    });

    test("detects 'overloaded'", () => {
      expect(
        isModelErrorThought("The model is overloaded right now"),
      ).toBe(true);
    });

    test("detects 'service unavailable'", () => {
      expect(
        isModelErrorThought("Got a service unavailable response"),
      ).toBe(true);
    });

    test("detects 'service_unavailable'", () => {
      expect(isModelErrorThought("Error: service_unavailable")).toBe(true);
    });

    test("detects 'failed due to' with 'model'", () => {
      expect(
        isModelErrorThought("The request failed due to a model issue"),
      ).toBe(true);
    });

    test("detects 'failed due to' with 'api'", () => {
      expect(
        isModelErrorThought("Request failed due to an api timeout"),
      ).toBe(true);
    });

    test("detects 'failed due to' with 'upstream'", () => {
      expect(
        isModelErrorThought("The call failed due to an upstream error"),
      ).toBe(true);
    });

    test("case insensitive - 'Model Error'", () => {
      expect(isModelErrorThought("A Model Error occurred")).toBe(true);
    });

    test("case insensitive - 'API ERROR'", () => {
      expect(isModelErrorThought("Got an API ERROR response")).toBe(true);
    });

    test("case insensitive - 'Rate Limit'", () => {
      expect(isModelErrorThought("Hit a Rate Limit")).toBe(true);
    });
  });

  describe("avoids false positives on normal thinking text", () => {
    test("does not match 'I think the error is in line 42'", () => {
      expect(
        isModelErrorThought("I think the error is in line 42"),
      ).toBe(false);
    });

    test("does not match discussion about fixing bugs", () => {
      expect(
        isModelErrorThought("Let me fix the error in the database query"),
      ).toBe(false);
    });

    test("does not match 'the error handling code needs updating'", () => {
      expect(
        isModelErrorThought("The error handling code needs updating"),
      ).toBe(false);
    });

    test("does not match 'this function returns an error'", () => {
      expect(
        isModelErrorThought("This function returns an error when the input is invalid"),
      ).toBe(false);
    });

    test("does not match 'the user reported an error'", () => {
      expect(
        isModelErrorThought("The user reported an error in the form"),
      ).toBe(false);
    });

    test("does not match 'I need to add error logging'", () => {
      expect(
        isModelErrorThought("I need to add error logging to this endpoint"),
      ).toBe(false);
    });

    test("does not match 'failed due to a missing dependency'", () => {
      expect(
        isModelErrorThought("The test failed due to a missing dependency"),
      ).toBe(false);
    });

    test("does not match 'failed due to a timeout'", () => {
      expect(
        isModelErrorThought("The build failed due to a timeout"),
      ).toBe(false);
    });

    test("does not match general thinking about code", () => {
      expect(
        isModelErrorThought(
          "Let me think about how to implement the validation logic",
        ),
      ).toBe(false);
    });

    test("does not match empty string", () => {
      expect(isModelErrorThought("")).toBe(false);
    });

    test("does not match null", () => {
      expect(isModelErrorThought(null)).toBe(false);
    });

    test("does not match undefined", () => {
      expect(isModelErrorThought(undefined)).toBe(false);
    });
  });
});

// =============================================================================
// Argument Count Badge Visibility Logic Tests
// =============================================================================

/**
 * Mirror of the NamedPromptPill argument count badge condition from Message.js.
 * The badge is shown when message.argumentCount is a positive integer.
 */
function shouldShowArgCountBadge(message) {
  return message.argumentCount > 0;
}

describe("NamedPromptPill argument count badge", () => {
  test("shows badge when argumentCount > 0", () => {
    expect(shouldShowArgCountBadge({ argumentCount: 1 })).toBe(true);
    expect(shouldShowArgCountBadge({ argumentCount: 3 })).toBe(true);
    expect(shouldShowArgCountBadge({ argumentCount: 10 })).toBe(true);
  });

  test("does not show badge when argumentCount is 0", () => {
    expect(shouldShowArgCountBadge({ argumentCount: 0 })).toBe(false);
  });

  test("does not show badge when argumentCount is undefined", () => {
    expect(shouldShowArgCountBadge({ argumentCount: undefined })).toBe(false);
  });

  test("does not show badge when argumentCount is absent", () => {
    expect(shouldShowArgCountBadge({})).toBe(false);
  });

  test("does not show badge when argumentCount is null", () => {
    expect(shouldShowArgCountBadge({ argumentCount: null })).toBe(false);
  });
});

// =============================================================================
// NamedPromptPill Tooltip Text Tests
// =============================================================================

/**
 * Mirror of the NamedPromptPill tooltip fallback chain from Message.js.
 * 1. message.meta.arguments (array of {name, value}) → "name=value, name=value"
 * 2. message.meta.argument_names (array of strings) → "Arguments: A, B"
 * 3. fallback → "N argument(s)"
 */
function buildArgTip(message) {
  const argPairs =
    message.meta && Array.isArray(message.meta.arguments)
      ? message.meta.arguments
      : null;
  const argNames =
    message.meta && Array.isArray(message.meta.argument_names)
      ? message.meta.argument_names
      : null;
  if (argPairs && argPairs.length > 0) {
    return argPairs.map((a) => `${a.name}=${a.value}`).join(", ");
  }
  if (argNames && argNames.length > 0) {
    return `Arguments: ${argNames.join(", ")}`;
  }
  return `${message.argumentCount} argument(s)`;
}

describe("NamedPromptPill tooltip", () => {
  test("renders name=value pairs when meta.arguments is non-empty", () => {
    expect(
      buildArgTip({
        argumentCount: 2,
        meta: {
          arguments: [
            { name: "ISSUE_ID", value: "mitto-42" },
            { name: "TITLE", value: "Fix the thing" },
          ],
        },
      }),
    ).toBe("ISSUE_ID=mitto-42, TITLE=Fix the thing");
  });

  test("renders single name=value pair", () => {
    expect(
      buildArgTip({
        argumentCount: 1,
        meta: { arguments: [{ name: "FOO", value: "bar" }] },
      }),
    ).toBe("FOO=bar");
  });

  test("falls back to names when meta.arguments is absent but argument_names present", () => {
    expect(
      buildArgTip({
        argumentCount: 2,
        meta: { argument_names: ["A", "B"] },
      }),
    ).toBe("Arguments: A, B");
  });

  test("falls back to count when both meta.arguments and argument_names are absent", () => {
    expect(buildArgTip({ argumentCount: 3 })).toBe("3 argument(s)");
  });

  test("falls back to count when meta is absent entirely", () => {
    expect(buildArgTip({ argumentCount: 5, meta: undefined })).toBe(
      "5 argument(s)",
    );
  });

  test("falls back when meta.arguments is an empty array (uses names)", () => {
    expect(
      buildArgTip({
        argumentCount: 2,
        meta: { arguments: [], argument_names: ["X", "Y"] },
      }),
    ).toBe("Arguments: X, Y");
  });

  test("falls back to count when meta.arguments is empty and no names", () => {
    expect(
      buildArgTip({ argumentCount: 4, meta: { arguments: [] } }),
    ).toBe("4 argument(s)");
  });

  test("falls back when meta.argument_names is an empty array", () => {
    expect(
      buildArgTip({ argumentCount: 2, meta: { argument_names: [] } }),
    ).toBe("2 argument(s)");
  });

  test("ignores non-array meta.arguments", () => {
    expect(
      buildArgTip({
        argumentCount: 1,
        meta: { arguments: "not-an-array", argument_names: ["A"] },
      }),
    ).toBe("Arguments: A");
  });

  test("preserves value strings verbatim (already truncated/redacted upstream)", () => {
    expect(
      buildArgTip({
        argumentCount: 1,
        meta: {
          arguments: [{ name: "LONG", value: "abc…(truncated)" }],
        },
      }),
    ).toBe("LONG=abc…(truncated)");
  });
});

// =============================================================================
// messagePropsAreEqual (memo comparator) Tests
// =============================================================================

/**
 * Mirror of messagePropsAreEqual from Message.js for isolated unit testing.
 * Returns true when props are equal (memo should skip re-render).
 */
function messagePropsAreEqual(prev, next) {
  return (
    prev.message.html === next.message.html &&
    prev.message.text === next.message.text &&
    prev.message.status === next.message.status &&
    prev.message.title === next.message.title &&
    prev.message.images === next.message.images &&
    prev.message.complete === next.message.complete &&
    prev.isLast === next.isLast &&
    prev.isStreaming === next.isStreaming &&
    prev.onRetry === next.onRetry
  );
}

function makeProps(overrides = {}) {
  return {
    message: {
      html: "<p>hello</p>",
      text: "hello",
      status: "completed",
      title: "Tool call",
      images: null,
      complete: true,
      ...(overrides.message || {}),
    },
    isLast: false,
    isStreaming: false,
    onRetry: null,
    ...overrides,
  };
}

describe("messagePropsAreEqual (memo comparator)", () => {
  test("returns true when all relevant props are identical", () => {
    const p = makeProps();
    expect(messagePropsAreEqual(p, makeProps())).toBe(true);
  });

  test("returns false when message.html changes (streaming chunk)", () => {
    const prev = makeProps();
    const next = makeProps({ message: { html: "<p>updated</p>" } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when message.text changes", () => {
    const prev = makeProps();
    const next = makeProps({ message: { text: "changed" } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when message.status changes", () => {
    const prev = makeProps();
    const next = makeProps({ message: { status: "running" } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when message.title changes", () => {
    const prev = makeProps();
    const next = makeProps({ message: { title: "New tool" } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when message.complete changes", () => {
    const prev = makeProps({ message: { complete: false } });
    const next = makeProps({ message: { complete: true } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when isLast changes", () => {
    const prev = makeProps({ isLast: false });
    const next = makeProps({ isLast: true });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when isStreaming changes", () => {
    const prev = makeProps({ isStreaming: false });
    const next = makeProps({ isStreaming: true });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns false when onRetry reference changes", () => {
    const prev = makeProps({ onRetry: () => {} });
    const next = makeProps({ onRetry: () => {} });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns true when onRetry is the same function reference", () => {
    const fn = () => {};
    const prev = makeProps({ onRetry: fn });
    const next = makeProps({ onRetry: fn });
    expect(messagePropsAreEqual(prev, next)).toBe(true);
  });

  test("returns false when images reference changes (new array)", () => {
    const prev = makeProps({ message: { images: [] } });
    const next = makeProps({ message: { images: [] } });
    expect(messagePropsAreEqual(prev, next)).toBe(false);
  });

  test("returns true when images is the same array reference", () => {
    const imgs = [];
    const prev = makeProps({ message: { images: imgs } });
    const next = makeProps({ message: { images: imgs } });
    expect(messagePropsAreEqual(prev, next)).toBe(true);
  });

  test("streaming message: always returns false when html changes each chunk", () => {
    // Simulates successive streaming chunks — memo must not block re-renders
    const chunks = ["<p>h</p>", "<p>he</p>", "<p>hel</p>", "<p>hell</p>"];
    for (let i = 0; i < chunks.length - 1; i++) {
      const prev = makeProps({ message: { html: chunks[i], complete: false }, isStreaming: true, isLast: true });
      const next = makeProps({ message: { html: chunks[i + 1], complete: false }, isStreaming: true, isLast: true });
      expect(messagePropsAreEqual(prev, next)).toBe(false);
    }
  });
});

// =============================================================================
// sessionChangeText Tests
// =============================================================================

/**
 * Mirror of sessionChangeText from Message.js for isolated unit testing.
 * (Component file imports window.preact globals unavailable in Jest.)
 */
function sessionChangeText(m) {
  const value = m.value || "";
  const previousValue = m.previousValue || "";
  const items = Array.isArray(m.items) ? m.items : [];
  switch (m.kind) {
    case "model":
      return `Model changed to ${value}`;
    case "model_override":
      return previousValue
        ? `⚡ Running this prompt on ${value} — conversation stays on ${previousValue}`
        : `⚡ Running this prompt on ${value}`;
    case "mode":
      return `Mode changed to ${value}`;
    case "prompt_arguments":
      return `Prompt arguments: ${items.join(", ")}`;
    default: {
      const what = m.label || m.kind || "Session";
      if (value) return `${what} changed to ${value}`;
      if (items.length) return `${what}: ${items.join(", ")}`;
      return `${what} changed`;
    }
  }
}

describe("sessionChangeText", () => {
  test("renders model kind as 'Model changed to <value>'", () => {
    expect(
      sessionChangeText({ kind: "model", value: "claude-x" }),
    ).toBe("Model changed to claude-x");
  });

  test("unknown kind with label falls back to generic label text", () => {
    expect(
      sessionChangeText({ kind: "future_thing", label: "Foo" }),
    ).toBe("Foo changed");
  });

  test("unknown kind with label and value uses generic 'changed to' text", () => {
    expect(
      sessionChangeText({ kind: "future_thing", label: "Foo", value: "bar" }),
    ).toBe("Foo changed to bar");
  });

  test("unknown kind without label falls back to kind name", () => {
    expect(sessionChangeText({ kind: "future_thing" })).toBe(
      "future_thing changed",
    );
  });

  test("model_override renders the transient-override pill with baseline", () => {
    expect(
      sessionChangeText({
        kind: "model_override",
        value: "Sonnet 4.5",
        previousValue: "Opus",
      }),
    ).toBe(
      "⚡ Running this prompt on Sonnet 4.5 — conversation stays on Opus",
    );
  });

  test("model_override without baseline omits the 'conversation stays on' clause", () => {
    expect(
      sessionChangeText({ kind: "model_override", value: "Sonnet 4.5" }),
    ).toBe("⚡ Running this prompt on Sonnet 4.5");
  });
});
