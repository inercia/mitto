/**
 * Unit tests for Message component logic
 *
 * Tests cover:
 * - isModelErrorThought detection patterns
 * - False positive avoidance for normal thinking text
 * - Tool-title viewer URL workspace resolution
 */

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { describeProvenance } from "../utils/promptProvenance.js";

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
      expect(isModelErrorThought("The model is overloaded right now")).toBe(
        true,
      );
    });

    test("detects 'service unavailable'", () => {
      expect(isModelErrorThought("Got a service unavailable response")).toBe(
        true,
      );
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
      expect(isModelErrorThought("Request failed due to an api timeout")).toBe(
        true,
      );
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
      expect(isModelErrorThought("I think the error is in line 42")).toBe(
        false,
      );
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
        isModelErrorThought(
          "This function returns an error when the input is invalid",
        ),
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
      expect(isModelErrorThought("The build failed due to a timeout")).toBe(
        false,
      );
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
    expect(buildArgTip({ argumentCount: 4, meta: { arguments: [] } })).toBe(
      "4 argument(s)",
    );
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
// NamedPromptPill provenance indicator (mitto-rg79)
// =============================================================================

describe("NamedPromptPill provenance indicator", () => {
  test("describeProvenance returns null for absent/falsy provenance", () => {
    expect(describeProvenance(undefined)).toBeNull();
    expect(describeProvenance(null)).toBeNull();
  });

  test("prefers startup over forced when both flags are true", () => {
    const info = describeProvenance({
      is_loop_run_on_start: true,
      is_loop_forced: true,
      loop_trigger: "schedule",
    });
    expect(info.label).toBe("Startup");
  });

  test("manual Run now maps to a distinct label from scheduled delivery", () => {
    const info = describeProvenance({ is_loop_forced: true });
    expect(info.label).toBe("Manual run");
  });

  test.each([
    ["schedule", "Schedule"],
    ["onCompletion", "On completion"],
    ["onTasks", "On tasks"],
    ["onChild", "On child"],
  ])("maps loop_trigger=%s to label %s", (trigger, label) => {
    expect(describeProvenance({ loop_trigger: trigger }).label).toBe(label);
  });

  test("onSlack includes channel/event-count detail when present", () => {
    const info = describeProvenance({
      loop_trigger: "onSlack",
      slack: { installation_id: "I1", channel_id: "C123", event_count: 3 },
    });
    expect(info.label).toBe("Slack");
    expect(info.detail).toContain("channel C123");
    expect(info.detail).toContain("3 events");
  });

  test("onSlack without slack detail still renders a generic label", () => {
    const info = describeProvenance({ loop_trigger: "onSlack" });
    expect(info.label).toBe("Slack");
    expect(info.detail).not.toContain("#");
  });

  test("unknown future trigger names still render an informative label instead of being dropped", () => {
    const info = describeProvenance({ loop_trigger: "onSomethingNew" });
    expect(info.label).toBe("onSomethingNew");
    expect(info.detail).toContain("onSomethingNew");
  });

  test("Message.js defines a shared ProvenanceFooter that renders nothing when provenance is absent", () => {
    const messageJs = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "Message.js"),
      "utf8",
    );
    expect(messageJs).toMatch(
      /import \{ describeProvenance \} from "\.\.\/utils\/promptProvenance\.js";/,
    );
    const idx = messageJs.indexOf("function ProvenanceFooter(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = messageJs.slice(idx, idx + 700);
    // Gated: bails out before rendering anything for ad-hoc prompts.
    expect(snippet).toMatch(/if \(!provenanceInfo\) return null;/);
    expect(snippet).toMatch(/data-testid=\$\{testId\}/);
    expect(snippet).toMatch(/aria-label=\$\{`Trigger: \$\{provenanceInfo\.label\}`\}/);
    // Wrapped in a Tooltip exposing describeProvenance(...).detail.
    expect(snippet).toMatch(/<\$\{Tooltip\} tip=\$\{provenanceInfo\.detail\}>/);
  });

  test("NamedPromptPill and the free-text user bubble both mount ProvenanceFooter with distinct testids", () => {
    const messageJs = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "Message.js"),
      "utf8",
    );
    expect(messageJs).toMatch(
      /<\$\{ProvenanceFooter\}\s*\n\s*provenanceInfo=\$\{provenanceInfo\}\s*\n\s*testId="named-prompt-provenance"\s*\n\s*\/>/,
    );
    expect(messageJs).toMatch(
      /<\$\{ProvenanceFooter\}\s*\n\s*provenanceInfo=\$\{provenanceInfo\}\s*\n\s*testId="user-message-provenance"\s*\n\s*\/>/,
    );
  });

  test("free-text user message branch computes provenanceInfo from message.provenance", () => {
    const messageJs = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "Message.js"),
      "utf8",
    );
    const idx = messageJs.indexOf("const userTimeStr = formatMessageTime");
    expect(idx).toBeGreaterThan(-1);
    const snippet = messageJs.slice(idx, idx + 500);
    expect(snippet).toMatch(
      /const provenanceInfo = describeProvenance\(message\.provenance\);/,
    );
  });

  test("Message.js pill tooltip combines the trigger description with existing argument info", () => {
    const messageJs = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "Message.js"),
      "utf8",
    );
    const idx = messageJs.indexOf("const pillTip = ");
    expect(idx).toBeGreaterThan(-1);
    const snippet = messageJs.slice(idx, idx + 200);
    expect(snippet).toMatch(/provenanceInfo\?\.detail/);
    expect(snippet).toMatch(/argumentCount > 0 && argTip/);
  });
});

// =============================================================================
// mitto-rg79: ProvenanceFooter — mounted DOM behavior
//
// Message.js reads window.preact at module load time (like every other
// component in this codebase — see the note atop SessionPanel.test.js), so it
// cannot be imported directly under the default test setup. This section
// mounts it for real by setting window.preact before a dynamic import, then
// runs in an isolated child process (own happy-dom via bunfig preload) so
// that setup never leaks into the source-scan tests above — mirroring the
// pattern in LoopSettingsTab.test.js / SlackSubscriptionEditor.test.js.
// =============================================================================

const isMountedChildRun = process.env.MITTO_MESSAGE_COMPONENT_TEST_CHILD === "1";

// Minimal memo() shim mirroring preact-loader.js's vendored-Preact-compatible
// implementation (no preact/compat available here) — Message.js calls
// window.preact.memo(MessageImpl, messagePropsAreEqual) at module load time.
function makeMemo(preact) {
  return function memo(component, propsAreEqual) {
    class MemoComponent extends preact.Component {
      shouldComponentUpdate(nextProps) {
        if (propsAreEqual) return !propsAreEqual(this.props, nextProps);
        const a = this.props,
          b = nextProps;
        for (const k in a) if (a[k] !== b[k]) return true;
        for (const k in b) if (!(k in a)) return true;
        return false;
      }
      render() {
        return preact.h(component, this.props);
      }
    }
    return MemoComponent;
  };
}

if (isMountedChildRun) {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const previousPreact = window.preact;
  window.preact = {
    ...preact,
    ...hooks,
    html: htm.bind(preact.h),
    memo: makeMemo(preact),
  };
  const { Message } = await import("./Message.js?mitto-rg79-mounted-tests");
  window.preact = previousPreact;

  const html = htm.bind(preact.h);

  function mount(message) {
    const container = document.createElement("div");
    document.body.appendChild(container);
    preact.render(html`<${Message} message=${message} isLast=${false} isStreaming=${false} />`, container);
    return container;
  }

  function unmount(container) {
    preact.render(null, container);
    container.remove();
  }

  describe("ProvenanceFooter mounted: free-text user message bubble", () => {
    test("renders no provenance footer when message.provenance is absent", () => {
      const container = mount({
        role: "user",
        text: "Hello there",
        timestamp: 0,
      });
      try {
        expect(
          container.querySelector('[data-testid="user-message-provenance"]'),
        ).toBeNull();
        // Ordinary bubble content is unaffected.
        expect(container.textContent).toContain("Hello there");
      } finally {
        unmount(container);
      }
    });

    test("renders the icon+label footer with a tooltip carrying describeProvenance(...).detail when provenance exists", () => {
      const container = mount({
        role: "user",
        text: "Run the deploy",
        timestamp: 0,
        provenance: { loop_trigger: "onTasks" },
      });
      try {
        const footer = container.querySelector(
          '[data-testid="user-message-provenance"]',
        );
        expect(footer).not.toBeNull();
        expect(footer.textContent).toContain("On tasks");
        expect(footer.querySelector("svg")).not.toBeNull();
        expect(footer.getAttribute("aria-label")).toBe("Trigger: On tasks");
        // Tooltip wrapper exposes describeProvenance(...).detail as data-tip.
        const tooltipWrapper = footer.closest("[data-tip]");
        expect(tooltipWrapper).not.toBeNull();
        expect(tooltipWrapper.getAttribute("data-tip")).toBe(
          describeProvenance({ loop_trigger: "onTasks" }).detail,
        );
      } finally {
        unmount(container);
      }
    });
  });

  describe("ProvenanceFooter mounted: named-prompt pill", () => {
    test("renders no provenance footer when message.provenance is absent", () => {
      const container = mount({
        role: "user",
        promptName: "deploy",
        argumentCount: 0,
        timestamp: 0,
      });
      try {
        expect(
          container.querySelector('[data-testid="named-prompt-provenance"]'),
        ).toBeNull();
        expect(
          container.querySelector('[data-testid="named-prompt-pill"]'),
        ).not.toBeNull();
      } finally {
        unmount(container);
      }
    });

    test("renders the icon+label footer beneath the pill when provenance exists", () => {
      const container = mount({
        role: "user",
        promptName: "deploy",
        argumentCount: 0,
        timestamp: 0,
        provenance: { loop_trigger: "schedule" },
      });
      try {
        expect(
          container.querySelector('[data-testid="named-prompt-pill"]'),
        ).not.toBeNull();
        const footer = container.querySelector(
          '[data-testid="named-prompt-provenance"]',
        );
        expect(footer).not.toBeNull();
        expect(footer.textContent).toContain("Schedule");
        expect(footer.getAttribute("aria-label")).toBe("Trigger: Schedule");
      } finally {
        unmount(container);
      }
    });
  });
} else {
  describe("ProvenanceFooter mounted behavior (mitto-rg79)", () => {
    test("passes mounted behavior tests in an isolated happy-dom process", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_MESSAGE_COMPONENT_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Message mounted tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}

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
    prev.message.provenance === next.message.provenance &&
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
      provenance: null,
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

  test("returns false when message.provenance changes", () => {
    const prev = makeProps();
    const next = makeProps({
      message: { provenance: { loop_trigger: "schedule" } },
    });
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
      const prev = makeProps({
        message: { html: chunks[i], complete: false },
        isStreaming: true,
        isLast: true,
      });
      const next = makeProps({
        message: { html: chunks[i + 1], complete: false },
        isStreaming: true,
        isLast: true,
      });
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
    case "context_cleared":
      return value === "flush"
        ? "🧹 Context cleared for fresh loop iteration"
        : value === "new_session"
          ? "🧹 New agent session started for fresh loop iteration"
          : "🧹 Context cleared";
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
    expect(sessionChangeText({ kind: "model", value: "claude-x" })).toBe(
      "Model changed to claude-x",
    );
  });

  test("unknown kind with label falls back to generic label text", () => {
    expect(sessionChangeText({ kind: "future_thing", label: "Foo" })).toBe(
      "Foo changed",
    );
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
    ).toBe("⚡ Running this prompt on Sonnet 4.5 — conversation stays on Opus");
  });

  test("model_override without baseline omits the 'conversation stays on' clause", () => {
    expect(
      sessionChangeText({ kind: "model_override", value: "Sonnet 4.5" }),
    ).toBe("⚡ Running this prompt on Sonnet 4.5");
  });

  test("context_cleared with value 'flush' renders the in-place-flush pill (mitto-so19)", () => {
    expect(sessionChangeText({ kind: "context_cleared", value: "flush" })).toBe(
      "🧹 Context cleared for fresh loop iteration",
    );
  });

  test("context_cleared with value 'new_session' renders the fresh-session pill (mitto-so19)", () => {
    expect(
      sessionChangeText({ kind: "context_cleared", value: "new_session" }),
    ).toBe("🧹 New agent session started for fresh loop iteration");
  });

  test("context_cleared without value falls back to the generic 'Context cleared' pill (mitto-so19)", () => {
    expect(sessionChangeText({ kind: "context_cleared" })).toBe(
      "🧹 Context cleared",
    );
  });
});

// =============================================================================
// Tool-title file link URL Tests
// =============================================================================

/**
 * Build the viewer URL for a file path found in a tool-call title.
 * Duplicated from Message.js `renderTitle` for testing (the component imports
 * window.preact globals). The source-scan guard below asserts the production
 * code still prefers the props over the window globals.
 */
function buildToolTitleViewerURL(
  pathValue,
  { apiPrefix, workspaceUUID, workspacePath, globals },
) {
  const wsUUID = workspaceUUID || globals.mittoCurrentWorkspaceUUID || "";
  const wsPath = workspacePath || globals.mittoCurrentWorkspace || "";
  const relativePath = pathValue.replace(/^\.\//, "");
  let viewerUrl = null;
  if (wsUUID) {
    viewerUrl = `${apiPrefix}/viewer.html?ws=${encodeURIComponent(wsUUID)}&path=${encodeURIComponent(relativePath)}`;
    if (wsPath) {
      viewerUrl += `&ws_path=${encodeURIComponent(wsPath)}`;
    }
  }
  return viewerUrl;
}

describe("tool-title viewer URL workspace resolution", () => {
  // The window globals track the most recently activated conversation, so they
  // point at the wrong workspace for messages rendered from another one.
  const globals = {
    mittoCurrentWorkspaceUUID: "4abb5d84-global-mitto",
    mittoCurrentWorkspace: "/Users/x/mitto",
  };

  test("uses the passed-in workspaceUUID/workspacePath instead of the globals", () => {
    expect(
      buildToolTitleViewerURL("docs/IMS/GLB-testing.md", {
        apiPrefix: "/mitto",
        workspaceUUID: "f8a510bc-go-adobe-apis",
        workspacePath: "/Users/x/go-adobe-apis",
        globals,
      }),
    ).toBe(
      "/mitto/viewer.html?ws=f8a510bc-go-adobe-apis&path=docs%2FIMS%2FGLB-testing.md&ws_path=%2FUsers%2Fx%2Fgo-adobe-apis",
    );
  });

  test("falls back to the globals when no workspace props are passed", () => {
    expect(
      buildToolTitleViewerURL("docs/IMS/GLB-testing.md", {
        apiPrefix: "/mitto",
        workspaceUUID: undefined,
        workspacePath: undefined,
        globals,
      }),
    ).toBe(
      "/mitto/viewer.html?ws=4abb5d84-global-mitto&path=docs%2FIMS%2FGLB-testing.md&ws_path=%2FUsers%2Fx%2Fmitto",
    );
  });

  test("strips a leading ./ from the path", () => {
    expect(
      buildToolTitleViewerURL("./README.md", {
        apiPrefix: "",
        workspaceUUID: "ws-1",
        workspacePath: "",
        globals: {},
      }),
    ).toBe("/viewer.html?ws=ws-1&path=README.md");
  });

  test("returns null when no workspace UUID is resolvable", () => {
    expect(
      buildToolTitleViewerURL("README.md", {
        apiPrefix: "/mitto",
        workspaceUUID: undefined,
        workspacePath: "/Users/x/mitto",
        globals: {},
      }),
    ).toBe(null);
  });
});

describe("source-scan guard — Message.js prefers workspace props over globals", () => {
  test("renderTitle resolves wsUUID/wsPath from props first", () => {
    const src = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "Message.js"),
      "utf8",
    );
    expect(src).toMatch(
      /const wsUUID =\s*workspaceUUID \|\| window\.mittoCurrentWorkspaceUUID \|\| "";/,
    );
    expect(src).toMatch(
      /const wsPath =\s*workspacePath \|\| window\.mittoCurrentWorkspace \|\| "";/,
    );
  });

  test("MessageList.js passes the conversation's workspace into Message", () => {
    const src = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "MessageList.js"),
      "utf8",
    );
    expect(src).toMatch(/workspaceUUID=\$\{sessionInfo\?\.workspace_uuid\}/);
    expect(src).toMatch(/workspacePath=\$\{sessionInfo\?\.working_dir\}/);
  });
});
