/**
 * Unit tests for the header archive/unarchive toolbar button (bead mitto-a5p).
 *
 * The button descriptor is built inline in web/static/app.js as an entry of
 * `conversationToolbarItems`; because that module pulls in the full app
 * (preact/htm globals, WebSocket client, DOM), we mirror the two pure pieces
 * of behaviour under test here — the archive-gate predicate + the button
 * descriptor — following the pattern established by
 * HeaderChildrenDropdown.test.js / Message.test.js.
 *
 * Acceptance criteria pinned by this file (from the bead description):
 *
 *   - archived + no queue + not streaming        → enabled, tip=Unarchive
 *   - archived + queued  (leftover from before)  → STILL enabled (unarchive is safe)
 *   - archived + isStreaming stale-true          → STILL enabled (unarchive is safe)
 *   - not archived + queued                      → disabled, tip="Clear queue before archiving"
 *   - not archived + isStreaming                 → disabled, tip="Wait for response to complete"
 *   - archive-direction behavior preserved       (existing tooltip / disabled semantics unchanged)
 *
 * The bug: `canArchive = !hasQueued && !isStreaming` is direction-blind — it
 * was written to guard the ARCHIVE flow (both preconditions would be disrupted
 * by archiving) but is reused unchanged as the `disabled` gate + tooltip
 * source for the UNARCHIVE flow, where those preconditions are meaningless.
 *
 * Kept as a plain-JS unit test (jsdom) — no Playwright / server dependencies —
 * so it runs in `make test-js` alongside the other component tests.
 */

// -----------------------------------------------------------------------------
// Pure mirror of the app.js header-archive button descriptor (see
// web/static/app.js around L2577-2583 for the predicate and L2971-2986 for the
// button descriptor). This function reflects the CURRENT production code
// verbatim so the failing assertions below pinpoint the bug — not our mirror.
// When the fix lands, this mirror will be updated to match, and the tests
// (which encode the acceptance criteria) will go green.
// -----------------------------------------------------------------------------
function buildHeaderArchiveButton({
  isArchived,
  isSpawned = false,
  hasQueued = false,
  isStreaming = false,
}) {
  // Header archive button is omitted for spawned (child) conversations —
  // matches the `...(headerIsSpawned ? [] : [{...}])` guard in app.js.
  if (isSpawned) return null;

  // Direction-aware archive gate (mitto-a5p fix): an archived session can
  // always be unarchived — the queue/streaming preconditions only apply to
  // the archive direction. Mirrors app.js:2578-2586 verbatim.
  const canArchive = isArchived || (!hasQueued && !isStreaming);
  const archiveBlockedReason =
    !isArchived && hasQueued
      ? "Clear queue before archiving"
      : !isArchived && isStreaming
        ? "Wait for response to complete"
        : null;

  return {
    kind: "button",
    testId: "header-archive",
    tip: !canArchive
      ? archiveBlockedReason
      : isArchived
        ? "Unarchive"
        : "Archive",
    ariaLabel: isArchived ? "Unarchive" : "Archive",
    disabled: !canArchive,
  };
}

// -----------------------------------------------------------------------------
// Pure mirror of the SessionItem row's archive-gate props (see
// web/static/components/SessionItem.js L206-215 for the predicate and
// L354-361 for the propagation into the `...` menu item). The row's Unarchive
// menu item ends up gated by `canArchive` and captions itself with
// `archiveBlockedReason` — exactly the same bug shape as the header button.
// -----------------------------------------------------------------------------
function computeSessionItemArchiveGate({
  isArchived,
  hasQueuedMessages = false,
  isSessionStreaming = false,
}) {
  // Direction-aware archive gate (mitto-a5p fix): mirrors SessionItem.js:208-220
  // verbatim. Unarchive has no blocking preconditions.
  const canArchive =
    isArchived || (!hasQueuedMessages && !isSessionStreaming);
  const archiveBlockedReason =
    !isArchived && hasQueuedMessages
      ? "Clear queue before archiving"
      : !isArchived && isSessionStreaming
        ? "Wait for response to complete"
        : null;

  // What the ... menu's Unarchive item ends up with (encoded so tests can
  // assert against the final surface, not the intermediate props).
  const unarchiveMenuItem = isArchived
    ? {
        label: "Unarchive",
        disabled: !canArchive,
        blockedReason: !canArchive ? archiveBlockedReason : null,
      }
    : null;

  return { canArchive, archiveBlockedReason, unarchiveMenuItem };
}

// -----------------------------------------------------------------------------
// Header archive/unarchive button — mitto-a5p acceptance matrix.
// -----------------------------------------------------------------------------
describe("header archive button — direction-aware gate (mitto-a5p)", () => {
  // --- Non-archived direction: existing archive-blocking behavior preserved ---

  test("not archived + no queue + not streaming → enabled, tip=Archive", () => {
    const btn = buildHeaderArchiveButton({ isArchived: false });
    expect(btn.disabled).toBe(false);
    expect(btn.tip).toBe("Archive");
    expect(btn.ariaLabel).toBe("Archive");
  });

  test("not archived + queued → disabled, tip='Clear queue before archiving'", () => {
    const btn = buildHeaderArchiveButton({
      isArchived: false,
      hasQueued: true,
    });
    expect(btn.disabled).toBe(true);
    expect(btn.tip).toBe("Clear queue before archiving");
  });

  test("not archived + streaming → disabled, tip='Wait for response to complete'", () => {
    const btn = buildHeaderArchiveButton({
      isArchived: false,
      isStreaming: true,
    });
    expect(btn.disabled).toBe(true);
    expect(btn.tip).toBe("Wait for response to complete");
  });

  // --- Archived direction: unarchive must not be blocked (the bug) ---

  test("archived + no queue + not streaming → enabled, tip=Unarchive", () => {
    const btn = buildHeaderArchiveButton({ isArchived: true });
    expect(btn.disabled).toBe(false);
    expect(btn.tip).toBe("Unarchive");
    expect(btn.ariaLabel).toBe("Unarchive");
  });

  test("archived + stale isStreaming flag → STILL enabled (currently fails: unarchive greyed-out)", () => {
    // The screenshot-reported symptom: cgw-managed-tools-m'i had a stale
    // isStreaming flag left over from before archiving; the header archive
    // icon (which becomes Unarchive in this state) renders disabled.
    const btn = buildHeaderArchiveButton({
      isArchived: true,
      isStreaming: true,
    });
    expect(btn.disabled).toBe(false);
    expect(btn.tip).toBe("Unarchive");
    expect(btn.ariaLabel).toBe("Unarchive");
  });

  test("archived + queued (leftover) → STILL enabled (currently fails: unarchive greyed-out)", () => {
    // Queued messages on an archived session are moot in the unarchive
    // direction — the point of unarchiving is to restore the row so those
    // queued messages can be handled.
    const btn = buildHeaderArchiveButton({
      isArchived: true,
      hasQueued: true,
    });
    expect(btn.disabled).toBe(false);
    expect(btn.tip).toBe("Unarchive");
    expect(btn.ariaLabel).toBe("Unarchive");
  });

  test("archived + queued + streaming (both stale) → STILL enabled", () => {
    // Belt-and-braces: even with both stale flags, unarchive stays live.
    const btn = buildHeaderArchiveButton({
      isArchived: true,
      hasQueued: true,
      isStreaming: true,
    });
    expect(btn.disabled).toBe(false);
    expect(btn.tip).toBe("Unarchive");
  });

  test("spawned → button omitted entirely (unchanged)", () => {
    // The header archive button is not rendered for child/spawned rows;
    // pin that this behavior is preserved by the fix.
    expect(
      buildHeaderArchiveButton({ isArchived: false, isSpawned: true }),
    ).toBeNull();
    expect(
      buildHeaderArchiveButton({ isArchived: true, isSpawned: true }),
    ).toBeNull();
  });
});

// -----------------------------------------------------------------------------
// SessionItem `...` menu Unarchive item — same acceptance matrix from the
// row-menu side. Same bug (same predicate) in a parallel spot.
// -----------------------------------------------------------------------------
describe("SessionItem archive gate — direction-aware (mitto-a5p)", () => {
  // Archive direction — unchanged.

  test("not archived + queued → gate blocked with 'Clear queue…'", () => {
    const gate = computeSessionItemArchiveGate({
      isArchived: false,
      hasQueuedMessages: true,
    });
    expect(gate.canArchive).toBe(false);
    expect(gate.archiveBlockedReason).toBe("Clear queue before archiving");
    expect(gate.unarchiveMenuItem).toBeNull();
  });

  test("not archived + streaming → gate blocked with 'Wait for response…'", () => {
    const gate = computeSessionItemArchiveGate({
      isArchived: false,
      isSessionStreaming: true,
    });
    expect(gate.canArchive).toBe(false);
    expect(gate.archiveBlockedReason).toBe("Wait for response to complete");
    expect(gate.unarchiveMenuItem).toBeNull();
  });

  // Unarchive direction — the bug.

  test("archived + clean flags → Unarchive menu item enabled", () => {
    const gate = computeSessionItemArchiveGate({ isArchived: true });
    expect(gate.unarchiveMenuItem).not.toBeNull();
    expect(gate.unarchiveMenuItem.disabled).toBe(false);
    expect(gate.unarchiveMenuItem.blockedReason).toBeNull();
  });

  test("archived + stale isSessionStreaming → Unarchive item STILL enabled (currently fails)", () => {
    const gate = computeSessionItemArchiveGate({
      isArchived: true,
      isSessionStreaming: true,
    });
    expect(gate.unarchiveMenuItem).not.toBeNull();
    expect(gate.unarchiveMenuItem.disabled).toBe(false);
    expect(gate.unarchiveMenuItem.blockedReason).toBeNull();
  });

  test("archived + queued (leftover) → Unarchive item STILL enabled (currently fails)", () => {
    const gate = computeSessionItemArchiveGate({
      isArchived: true,
      hasQueuedMessages: true,
    });
    expect(gate.unarchiveMenuItem).not.toBeNull();
    expect(gate.unarchiveMenuItem.disabled).toBe(false);
    expect(gate.unarchiveMenuItem.blockedReason).toBeNull();
  });

  test("archived + queued + streaming → Unarchive item STILL enabled", () => {
    const gate = computeSessionItemArchiveGate({
      isArchived: true,
      hasQueuedMessages: true,
      isSessionStreaming: true,
    });
    expect(gate.unarchiveMenuItem).not.toBeNull();
    expect(gate.unarchiveMenuItem.disabled).toBe(false);
  });
});
