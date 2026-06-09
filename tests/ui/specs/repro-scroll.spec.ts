import { testWithCleanup as test, expect } from "../fixtures/test-fixtures";

/**
 * TEMPORARY reproduction spec for the scroll-to-bottom regression.
 * Hypothesis: during ACTIVE streaming, scrolling up gets yanked back to the
 * bottom because each streamed chunk calls scrollToBottom() (which force-sets
 * isUserAtBottom=true), and the scroll-to-bottom button never stabilizes.
 * Delete after diagnosis.
 */
test.describe("REPRO: streaming scroll race", () => {
  test.setTimeout(120000);

  test("scrolling up during streaming should stay up and show button", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await page.setViewportSize({ width: 1024, height: 560 });

    await helpers.navigateAndWait(page);
    await helpers.clearLocalStorage(page);

    await page.locator(selectors.newSessionButton).click();
    await expect(page.locator(selectors.chatInput)).toBeEnabled({
      timeout: timeouts.appReady,
    });
    await page.waitForTimeout(300);

    await page.evaluate(() => {
      (window as any).__debug = { ...(window as any).__debug, scroll: true };
    });

    // Build a tall conversation so there is plenty to scroll.
    for (let i = 0; i < 8; i++) {
      await helpers.sendMessage(page, `seed message ${i}`);
      await helpers.waitForUserMessage(page, `seed message ${i}`);
      await helpers.waitForAgentResponse(page);
      await page.waitForTimeout(60);
    }
    await helpers.waitForStreamingComplete(page);

    const container = page.locator(selectors.messagesContainer);
    const btn = page.locator(selectors.scrollToBottomButton);

    // Trigger a SLOW streaming response (~4s of streaming).
    await helpers.sendMessage(page, "slow response please");

    // Wait until streaming has begun (stop button visible).
    await expect(page.locator(selectors.stopButton)).toBeVisible({
      timeout: timeouts.shortAction,
    });

    // Move the mouse over the messages container so wheel events target it.
    const box = await container.boundingBox();
    if (box) {
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    }

    // Scroll up using REAL wheel input WHILE streaming is in progress.
    // This faithfully reproduces a user trying to scroll up during streaming,
    // fighting the continuous smooth scrollToBottom() animation.
    await page.waitForTimeout(500);

    // Sample scrollTop repeatedly during the rest of the stream, wheeling up
    // each iteration like a user would.
    const samples: number[] = [];
    const btnVisible: boolean[] = [];
    for (let i = 0; i < 12; i++) {
      await page.mouse.wheel(0, -400); // wheel up
      await page.waitForTimeout(250);
      const st = await container.evaluate((el) => el.scrollTop);
      samples.push(st);
      btnVisible.push(await btn.isVisible().catch(() => false));
    }

    console.log("REPRO streaming scrollTop samples:", JSON.stringify(samples));
    console.log("REPRO streaming btnVisible samples:", JSON.stringify(btnVisible));

    const maxSample = Math.max(...samples);
    const yankedBack = maxSample > 200; // got pulled away from the top
    const buttonEverHidden = btnVisible.some((v) => v === false);
    console.log("REPRO yankedBack:", yankedBack, "maxSample:", maxSample);
    console.log("REPRO buttonEverHidden:", buttonEverHidden);

    await helpers.waitForStreamingComplete(page);

    // EXPECTED correct behavior: once the user scrolls up during streaming,
    // they should STAY up (not be yanked to the bottom) and the button should
    // remain visible. These assertions FAIL while the bug is present.
    expect(yankedBack).toBe(false);
    expect(buttonEverHidden).toBe(false);
  });
});
