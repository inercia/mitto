/**
 * Reproduction test for mitto-d2h: Modal's Escape-key handling races
 * Preact's effect-flush scheduling.
 *
 * Modal registers the window `keydown` (Escape) listener inside
 * `useEffect(..., [isOpen])` (Modal.js ~91-162, addEventListener at ~144).
 * Preact only flushes that effect asynchronously via `requestAnimationFrame`
 * (see vendor/preact-hooks.js `n.diffed`, which pushes the component into a
 * queue flushed by `((i = n.requestAnimationFrame) || j)(b)`) -- strictly
 * AFTER the initial render has already committed the modal's DOM.
 *
 * A caller that dispatches Escape as soon as the modal element exists (e.g.
 * SlackSubscriptionEditor.test.js's "closing the picker modal via checkmark,
 * backdrop, or Escape..." test, whose openModal() helper waits only for the
 * DOM node via waitFor()) can race: the listener isn't attached yet, so the
 * keydown is dropped and onClose never fires. Under CI CPU contention this
 * surfaced as an intermittent ~12.5% failure rate on that one assertion
 * (investigation notes on the bead).
 *
 * This test reproduces the race deterministically -- no CPU contention
 * needed -- by dispatching Escape in the SAME tick as the initial render,
 * before Preact has had any chance to flush the effect.
 *
 * Follows the isolated-happy-dom-child-process pattern used by
 * SlackSubscriptionEditor.test.js / LoopSettingsTab.test.js: bunfig.toml
 * registers happy-dom globally for the whole `bun test` process, so mounted
 * DOM tests that render real components isolate themselves in a child
 * process to avoid interfering with (or being interfered by) sibling test
 * files sharing that same global DOM/window state.
 */
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

const childRun = process.env.MITTO_MODAL_TEST_CHILD === "1";

if (childRun) {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const previousPreact = window.preact;
  window.preact = { ...preact, ...hooks, html: htm.bind(preact.h) };
  const { Modal } = await import("./Modal.js?mitto-d2h-tests");
  window.preact = previousPreact;

  const html = htm.bind(preact.h);

  describe("Modal Escape key handling (mitto-d2h)", () => {
    test("Escape dispatched in the same tick the modal DOM commits still closes it", () => {
      const onClose = jest.fn();
      const container = document.createElement("div");
      document.body.appendChild(container);
      try {
        preact.render(
          html`<${Modal} isOpen=${true} onClose=${onClose} title="Test modal"
            >body</${Modal}
          >`,
          container,
        );

        // The modal box commits synchronously as part of the initial
        // render -- the same DOM-presence signal
        // SlackSubscriptionEditor.test.js's waitFor() polls for before
        // dispatching Escape.
        expect(container.querySelector(".modal.modal-open")).toBeTruthy();

        // Dispatch Escape in the SAME tick, before Preact has flushed the
        // useEffect(..., [isOpen]) that attaches the window keydown
        // listener (deferred via requestAnimationFrame; see
        // vendor/preact-hooks.js n.diffed). A user who presses Escape the
        // instant the dialog appears should still have it close.
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

        expect(onClose).toHaveBeenCalledTimes(1);
      } finally {
        preact.render(null, container);
        container.remove();
      }
    });
  });

  describe("Modal initial focus placement", () => {
    // Regression pin: a titled Modal's initial focus must land inside the
    // body (typically the first form field the user came to interact with)
    // rather than on the header's ✕ Close button, which was previously
    // winning as the first DOM-order focusable inside the modal-box and
    // forced users to click into the field manually.
    test("initial focus prefers a body input over the header ✕ close button", () => {
      const onClose = jest.fn();
      const container = document.createElement("div");
      document.body.appendChild(container);
      try {
        preact.render(
          html`<${Modal}
            isOpen=${true}
            onClose=${onClose}
            title="Test modal"
            closeTestid="modal-close"
          >
            <input data-testid="body-input" type="text" />
          </${Modal}>`,
          container,
        );

        // Both the ✕ button and the body input must exist so we know we
        // are actually exercising the header-vs-body preference.
        const closeBtn = container.querySelector(
          '[data-testid="modal-close"]',
        );
        const bodyInput = container.querySelector(
          '[data-testid="body-input"]',
        );
        expect(closeBtn).toBeTruthy();
        expect(bodyInput).toBeTruthy();

        // Focus placement happens inside useLayoutEffect, which fires
        // synchronously with the initial render commit -- so activeElement
        // is already correct without any waitFor().
        expect(document.activeElement).toBe(bodyInput);
        expect(document.activeElement).not.toBe(closeBtn);
      } finally {
        preact.render(null, container);
        container.remove();
      }
    });

    // Fallback branch: a titled Modal with NO body focusables (e.g. a
    // confirmation dialog whose only interactive elements live in the
    // footer) should focus a footer button rather than the header ✕.
    test("initial focus falls through to the footer when the body has no focusables", () => {
      const onClose = jest.fn();
      const container = document.createElement("div");
      document.body.appendChild(container);
      try {
        preact.render(
          html`<${Modal}
            isOpen=${true}
            onClose=${onClose}
            title="Confirm"
            closeTestid="modal-close"
            footer=${html`<button data-testid="footer-ok">OK</button>`}
          >
            <p>Are you sure?</p>
          </${Modal}>`,
          container,
        );

        const closeBtn = container.querySelector(
          '[data-testid="modal-close"]',
        );
        const footerBtn = container.querySelector(
          '[data-testid="footer-ok"]',
        );
        expect(closeBtn).toBeTruthy();
        expect(footerBtn).toBeTruthy();

        expect(document.activeElement).toBe(footerBtn);
        expect(document.activeElement).not.toBe(closeBtn);
      } finally {
        preact.render(null, container);
        container.remove();
      }
    });
  });
} else {
  describe("Modal", () => {
    test("passes mounted Escape-key tests in an isolated happy-dom process (mitto-d2h)", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: { ...process.env, MITTO_MODAL_TEST_CHILD: "1" },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated Modal mounted tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
