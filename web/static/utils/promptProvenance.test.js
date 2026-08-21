/**
 * Unit tests for web/static/utils/promptProvenance.js (mitto-rg79).
 * Pure module — no window.preact import gate, so it can be imported directly.
 */

import {
  describeProvenance,
  provenanceTooltip,
  PROVENANCE_ICON_SCHEDULE,
  PROVENANCE_ICON_COMPLETION,
  PROVENANCE_ICON_TASKS,
  PROVENANCE_ICON_CHILD,
  PROVENANCE_ICON_SLACK,
  PROVENANCE_ICON_MANUAL,
  PROVENANCE_ICON_STARTUP,
  PROVENANCE_ICON_UNKNOWN,
} from "./promptProvenance.js";

describe("describeProvenance", () => {
  test("returns null for null/undefined/falsy provenance", () => {
    expect(describeProvenance(null)).toBeNull();
    expect(describeProvenance(undefined)).toBeNull();
    expect(describeProvenance(false)).toBeNull();
  });

  test("returns null for an empty object with no trigger/forced/startup flags", () => {
    expect(describeProvenance({})).toBeNull();
  });

  test("startup pulse takes precedence over forced when both are true", () => {
    const info = describeProvenance({
      is_loop_run_on_start: true,
      is_loop_forced: true,
    });
    expect(info).toEqual({
      label: "Startup",
      detail: expect.stringContaining("started"),
      iconKey: PROVENANCE_ICON_STARTUP,
    });
  });

  test("startup pulse alone", () => {
    const info = describeProvenance({ is_loop_run_on_start: true });
    expect(info.iconKey).toBe(PROVENANCE_ICON_STARTUP);
  });

  test("manual Run now (forced, not startup)", () => {
    const info = describeProvenance({ is_loop_forced: true });
    expect(info.label).toBe("Manual run");
    expect(info.iconKey).toBe(PROVENANCE_ICON_MANUAL);
  });

  test("schedule trigger", () => {
    const info = describeProvenance({ loop_trigger: "schedule" });
    expect(info.label).toBe("Schedule");
    expect(info.iconKey).toBe(PROVENANCE_ICON_SCHEDULE);
  });

  test("onCompletion trigger", () => {
    const info = describeProvenance({ loop_trigger: "onCompletion" });
    expect(info.label).toBe("On completion");
    expect(info.iconKey).toBe(PROVENANCE_ICON_COMPLETION);
  });

  test("onTasks trigger", () => {
    const info = describeProvenance({ loop_trigger: "onTasks" });
    expect(info.label).toBe("On tasks");
    expect(info.iconKey).toBe(PROVENANCE_ICON_TASKS);
  });

  test("onChild trigger", () => {
    const info = describeProvenance({ loop_trigger: "onChild" });
    expect(info.label).toBe("On child");
    expect(info.iconKey).toBe(PROVENANCE_ICON_CHILD);
  });

  describe("onSlack trigger", () => {
    test("with full slack detail (channel + event count)", () => {
      const info = describeProvenance({
        loop_trigger: "onSlack",
        slack: { installation_id: "I1", channel_id: "C42", event_count: 2 },
      });
      expect(info.label).toBe("Slack");
      expect(info.iconKey).toBe(PROVENANCE_ICON_SLACK);
      expect(info.detail).toBe(
        "Fired by a Slack event (installation I1 · channel C42 · 2 events)",
      );
    });

    test("singular event count uses 'event' not 'events'", () => {
      const info = describeProvenance({
        loop_trigger: "onSlack",
        slack: { channel_id: "C1", event_count: 1 },
      });
      expect(info.detail).toContain("1 event)");
      expect(info.detail).not.toContain("1 events");
    });

    test("with no slack sub-object falls back to a generic label", () => {
      const info = describeProvenance({ loop_trigger: "onSlack" });
      expect(info.label).toBe("Slack");
      expect(info.detail).toBe("Fired by a Slack event");
    });

    test("supports the legacy slack trigger spelling", () => {
      const info = describeProvenance({ loop_trigger: "slack" });
      expect(info.label).toBe("Slack");
      expect(info.iconKey).toBe(PROVENANCE_ICON_SLACK);
    });

    test("with only an installation ID still identifies the source", () => {
      const info = describeProvenance({
        loop_trigger: "onSlack",
        slack: { installation_id: "I1" },
      });
      expect(info.detail).toBe("Fired by a Slack event (installation I1)");
    });
  });

  test("unknown/future trigger name still renders informative label+detail", () => {
    const info = describeProvenance({ loop_trigger: "onFutureThing" });
    expect(info.label).toBe("onFutureThing");
    expect(info.detail).toContain("onFutureThing");
    expect(info.iconKey).toBe(PROVENANCE_ICON_UNKNOWN);
  });

  test("empty-string loop_trigger with no forced/startup flags returns null", () => {
    expect(describeProvenance({ loop_trigger: "" })).toBeNull();
  });
});

describe("provenanceTooltip", () => {
  test("returns the detail string for valid provenance", () => {
    expect(provenanceTooltip({ loop_trigger: "onTasks" })).toBe(
      "Fired by a beads/task change",
    );
  });

  test("returns empty string for absent provenance", () => {
    expect(provenanceTooltip(null)).toBe("");
    expect(provenanceTooltip(undefined)).toBe("");
  });
});
