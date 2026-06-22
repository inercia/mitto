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
