/**
 * UI responsiveness benchmark (mitto-sus.1.2): fixture determinism.
 *
 * The bead's acceptance criteria explicitly requires "determinism holds:
 * repeated runs of any spec produce the same mark counts and same-order
 * stream chunks". None of the other perf specs assert this directly — they
 * only sanity-check that a single run's marks are well-formed. This spec
 * closes that gap by driving the deterministic `perf-plain-short` fixture
 * (tests/fixtures/responses/perf-plain-short.json) twice, in two independent
 * sessions, and proving both runs:
 *
 * 1. Assemble byte-identical final message text (same-order stream chunks —
 *    if chunks arrived out of order or were dropped, the concatenated text
 *    would differ from the fixture's fixed chunk sequence).
 * 2. Record the same number of `mitto.ws.chunk.applied` marks (same mark
 *    counts — proves the backend's StreamBuffer flush cadence for this fixed
 *    fixture is not flaky run-to-run).
 *
 * Smoke test of the instrumentation + fixtures, not a hard performance gate —
 * see docs/devel/ui-responsiveness-benchmarks.md.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import { enablePerf, getPerfEntries, writePerfSample } from "../../utils/perf";

// Exact concatenation of perf-plain-short.json's fixed chunk sequence.
const EXPECTED_TEXT =
  "This is a short, deterministic streaming response used to benchmark " +
  "plain-text chunk apply cost and frame stability during a brief agent turn.";

async function runOnce(
  page: import("@playwright/test").Page,
  helpers: typeof import("../../utils/helpers"),
): Promise<{ text: string; appliedMarks: number }> {
  await enablePerf(page);
  await helpers.navigateAndWait(page);
  await helpers.clearLocalStorage(page);
  await helpers.createFreshSession(page);

  await helpers.sendMessageAndWait(page, "perf plain short");
  await helpers.waitForStreamingSettled(page);

  // Scope to the agent bubble's markdown body specifically: both user and
  // agent messages share the "markdown-content" class (user adds a second
  // "markdown-content-user" class), and this nested selector excludes the
  // sibling .message-timestamp footer that .bg-mitto-agent's innerText would
  // otherwise include.
  const text = await page
    .locator(".bg-mitto-agent .markdown-content")
    .last()
    .innerText();
  const marks = await getPerfEntries(page, "mitto.ws.chunk.applied");
  return { text: text.trim(), appliedMarks: marks.length };
}

test.describe("Perf: fixture determinism across repeated runs", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("perf-plain-short assembles identical text and mark counts on two independent runs", async ({
    page,
    helpers,
  }) => {
    const first = await runOnce(page, helpers);
    expect(first.text).toBe(EXPECTED_TEXT);
    expect(first.appliedMarks).toBeGreaterThan(0);

    // Independent second run on a fresh session within the same page —
    // exercises the same trigger/fixture path a second time.
    const second = await runOnce(page, helpers);
    expect(second.text).toBe(EXPECTED_TEXT);

    expect(second.text).toBe(first.text);
    expect(second.appliedMarks).toBe(first.appliedMarks);

    // eslint-disable-next-line no-console
    console.log(
      `[perf] determinism: run1 marks=${first.appliedMarks} run2 marks=${second.appliedMarks} (equal, text matches fixture)`,
    );
    // Record-only (mitto-sus.1.3): not one of the 8 budgeted rows — this
    // scenario validates determinism, not a latency budget.
    writePerfSample("determinism.applied-marks", "count", first.appliedMarks);
  });
});
