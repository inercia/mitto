// Unit tests for phaseState.derivePhaseState (mitto-66r).
//
// Follows the pattern from utils/prompts.test.js: pure ESM import, no jsdom /
// preact bootstrap needed because the helper is framework-free.

import { derivePhaseState, PHASE_TIER_CLASSES } from "./phaseState.js";

describe("derivePhaseState — non-feature/bug types", () => {
  test.each([
    ["task"],
    ["epic"],
    ["chore"],
    ["unknown"],
    [""],
    [undefined],
    [null],
  ])("returns null for issue_type=%p", (t) => {
    expect(derivePhaseState(t, [])).toBeNull();
    expect(derivePhaseState(t, ["planned", "implemented"])).toBeNull();
  });
});

describe("derivePhaseState — feature", () => {
  test("no phase labels: current=plan (reasoning), all upcoming", () => {
    const s = derivePhaseState("feature", []);
    expect(s.isTerminal).toBe(false);
    expect(s.currentIndex).toBe(0);
    expect(s.currentLabel).toBe("plan");
    expect(s.currentDisplayName).toBe("Plan");
    expect(s.currentTier).toBe("reasoning");
    expect(s.kindLabel).toBe("Feature");
    expect(s.phases).toHaveLength(4);
    expect(s.phases[0].status).toBe("current");
    expect(s.phases.slice(1).every((p) => p.status === "upcoming")).toBe(true);
  });

  test("planned only: current=implement (coding), first phase done", () => {
    const s = derivePhaseState("feature", ["planned"]);
    expect(s.currentIndex).toBe(1);
    expect(s.currentLabel).toBe("implement");
    expect(s.currentTier).toBe("coding");
    expect(s.phases[0].status).toBe("done");
    expect(s.phases[0].tier).toBe("terminal");
    expect(s.phases[1].status).toBe("current");
    expect(s.phases[2].status).toBe("upcoming");
    expect(s.phases[3].status).toBe("upcoming");
  });

  test("planned+implemented: current=test", () => {
    const s = derivePhaseState("feature", ["implemented", "planned"]);
    expect(s.currentIndex).toBe(2);
    expect(s.currentLabel).toBe("test");
    expect(s.currentTier).toBe("coding");
    expect(s.phases.slice(0, 2).every((p) => p.status === "done")).toBe(true);
    expect(s.phases[2].status).toBe("current");
    expect(s.phases[3].status).toBe("upcoming");
  });

  test("planned+implemented+tested: current=review (reasoning)", () => {
    const s = derivePhaseState("feature", [
      "planned",
      "implemented",
      "tested",
    ]);
    expect(s.currentIndex).toBe(3);
    expect(s.currentLabel).toBe("review");
    expect(s.currentTier).toBe("reasoning");
    expect(s.phases[3].status).toBe("current");
  });

  test("all four labels: terminal / done", () => {
    const s = derivePhaseState("feature", [
      "planned",
      "implemented",
      "tested",
      "verified",
    ]);
    expect(s.isTerminal).toBe(true);
    expect(s.currentIndex).toBe(4);
    expect(s.currentLabel).toBe("done");
    expect(s.currentTier).toBe("terminal");
    expect(s.phases.every((p) => p.status === "done")).toBe(true);
  });

  test("out-of-order gap: implemented without planned => current=plan (strict order)", () => {
    // The driver early-exit check walks phases in reverse and requires each
    // prior label to be present; if `planned` is missing, `plan` is still the
    // next step regardless of whether `implemented` snuck in.
    const s = derivePhaseState("feature", ["implemented", "tested"]);
    expect(s.currentIndex).toBe(0);
    expect(s.currentLabel).toBe("plan");
    expect(s.phases[0].status).toBe("current");
    // Later phases whose labels *are* present but come after the gap: still
    // upcoming, not done — we only credit contiguous progress from the start.
    expect(s.phases[1].status).toBe("upcoming");
    expect(s.phases[2].status).toBe("upcoming");
    expect(s.phases[3].status).toBe("upcoming");
  });

  test("unknown labels are ignored", () => {
    const s = derivePhaseState("feature", [
      "planned",
      "wip",
      "needs-review",
    ]);
    expect(s.currentLabel).toBe("implement");
    expect(s.phases[0].status).toBe("done");
  });
});

describe("derivePhaseState — bug", () => {
  test("no phase labels: current=investigate (reasoning)", () => {
    const s = derivePhaseState("bug", []);
    expect(s.isTerminal).toBe(false);
    expect(s.currentIndex).toBe(0);
    expect(s.currentLabel).toBe("investigate");
    expect(s.currentTier).toBe("reasoning");
    expect(s.kindLabel).toBe("Bug");
    expect(s.phases).toHaveLength(3);
  });

  test("researched: current=reproduce (coding)", () => {
    const s = derivePhaseState("bug", ["researched"]);
    expect(s.currentIndex).toBe(1);
    expect(s.currentLabel).toBe("reproduce");
    expect(s.currentTier).toBe("coding");
  });

  test("researched+reproduced: current=fix (coding)", () => {
    const s = derivePhaseState("bug", ["reproduced", "researched"]);
    expect(s.currentIndex).toBe(2);
    expect(s.currentLabel).toBe("fix");
    expect(s.currentTier).toBe("coding");
  });

  test("all three labels: terminal / done", () => {
    const s = derivePhaseState("bug", [
      "researched",
      "reproduced",
      "fixed",
    ]);
    expect(s.isTerminal).toBe(true);
    expect(s.currentLabel).toBe("done");
    expect(s.currentTier).toBe("terminal");
    expect(s.phases.every((p) => p.status === "done")).toBe(true);
  });
});

describe("derivePhaseState — tier class hints", () => {
  test("each phase carries tierClasses matching PHASE_TIER_CLASSES", () => {
    const s = derivePhaseState("feature", ["planned"]);
    expect(s.phases[0].tierClasses).toBe(PHASE_TIER_CLASSES.terminal);
    expect(s.phases[1].tierClasses).toBe(PHASE_TIER_CLASSES.coding);
    expect(s.phases[3].tierClasses).toBe(PHASE_TIER_CLASSES.reasoning);
  });
});
