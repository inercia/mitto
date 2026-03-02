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
