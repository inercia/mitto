/**
 * UI responsiveness benchmark (mitto-sus.1): background/foreground stream
 * chunk-apply cost.
 *
 * Drives the deterministic `perf-plain-short` / `perf-plain-long` mock-ACP
 * fixtures (tests/fixtures/responses/) and validates the
 * `mitto.ws.chunk.applied` perf marks recorded by
 * sessionUpdateScheduler.js's `applyUpdates` seam (see
 * web/static/utils/perfMarks.js).
 *
 * Smoke test of the instrumentation + fixtures, not a hard performance gate —
 * see docs/devel/ui-responsiveness-benchmarks.md for proposed (not yet
 * enforced) budgets.
 */
import { test, expect } from "../../fixtures/test-fixtures";
import {
  enablePerf,
  getPerfEntries,
  percentile,
  writePerfSample,
} from "../../utils/perf";

test.describe("Perf: background chunk apply cost", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(60_000);

  test("records ws.chunk.applied marks for the short deterministic stream", async ({
    page,
    helpers,
  }) => {
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    await helpers.sendMessageAndWait(page, "perf plain short");
    await helpers.waitForStreamingSettled(page);

    const marks = await getPerfEntries(page, "mitto.ws.chunk.applied");
    expect(marks.length).toBeGreaterThan(0);
    for (const m of marks) {
      expect(Number.isFinite(m.startTime)).toBe(true);
      expect(Number.isFinite(m.duration)).toBe(true);
      expect(m.duration).toBeGreaterThanOrEqual(0);
    }
    // eslint-disable-next-line no-console
    console.log(`[perf] ws.chunk.applied (short): n=${marks.length}`);
    // Record-only (mitto-sus.1.3): not one of the 8 budgeted rows itself —
    // this scenario measures apply cadence/count, not received->applied
    // latency (see ws-chunk-received-applied.spec.ts for the gated pair).
    writePerfSample("ws.chunk.applied-short", "count", marks.length);
  });

  test("records ws.chunk.applied marks for the long (200-chunk) deterministic stream", async ({
    page,
    helpers,
  }) => {
    // Independent page/session (default per-test isolation).
    await enablePerf(page);
    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);
    await helpers.createFreshSession(page);

    await helpers.sendMessageAndWait(page, "perf plain long");
    await helpers.waitForStreamingSettled(page);
    const marks = await getPerfEntries(page, "mitto.ws.chunk.applied");

    // NOTE: this does NOT assert marks.length scales with the fixture's raw
    // 200-chunk count. The backend's own StreamBuffer soft-flush window
    // coalesces rapid chunks into far fewer WebSocket deliveries before the
    // frontend ever sees them (see waitForStreamingSettled's doc comment),
    // so `mitto.ws.chunk.applied` — one mark per *scheduler* apply, i.e. per
    // WS delivery for the active session — legitimately fires a similar,
    // small number of times for both the short and long fixtures at this
    // total streaming duration. That coalescing behavior is itself useful
    // signal for the eventual budget work, not a bug in this seam.
    expect(marks.length).toBeGreaterThan(0);
    for (const m of marks) {
      expect(Number.isFinite(m.startTime)).toBe(true);
      expect(Number.isFinite(m.duration)).toBe(true);
      expect(m.duration).toBeGreaterThanOrEqual(0);
    }

    const durations = marks.map((m) => m.duration);
    const p95 = percentile(durations, 95);
    // eslint-disable-next-line no-console
    console.log(
      `[perf] ws.chunk.applied (long): n=${marks.length} p95=${p95.toFixed(2)}ms`,
    );
    writePerfSample("ws.chunk.applied-long", "p95", p95, { n: marks.length });

    // mitto-sus.3 (record-only, like the rest of this scenario — see the
    // NOTE above): each mark's `detail.count` is the number of queued
    // chunks a single apply drained (sessionUpdateScheduler.js's
    // frame-paced active queue coalesces same-frame chunks into one
    // setSessions commit). This deterministic fixture's own WS-delivery
    // count is already tiny (n above), because the backend's StreamBuffer
    // coalesces before the frontend ever sees a chunk — so this scenario
    // can't reliably *exercise* frontend-side coalescing (there's rarely
    // more than one chunk per animation frame to coalesce). Recording
    // maxCoalesced anyway gives `make bench-ui` a signal for scenarios
    // where a slower/backed-up client does see multi-chunk frames, without
    // asserting a hard floor this fixture can't guarantee.
    const maxCoalesced = Math.max(...marks.map((m) => m.detail?.count ?? 1));
    // eslint-disable-next-line no-console
    console.log(`[perf] ws.chunk.applied (long): maxCoalesced=${maxCoalesced}`);
    writePerfSample("ws.chunk.applied-long", "maxCoalesced", maxCoalesced);
  });
});
