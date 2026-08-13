import { describe, expect, test } from "./testing/testGlobals.js";
import { computePortalDropdownPlacement } from "./toolbarDropdown.js";

function placedRect(placement, menuRect) {
  const width = Math.min(menuRect.width, placement.maxWidth);
  const height = Math.min(menuRect.height, placement.maxHeight);
  return {
    left: placement.left,
    top: placement.top,
    right: placement.left + width,
    bottom: placement.top + height,
  };
}

describe("computePortalDropdownPlacement", () => {
  test("clamps an oversized end-aligned menu within a narrow viewport", () => {
    const viewportWidth = 240;
    const viewportHeight = 320;
    const menuRect = { width: 256, height: 400 };
    const placement = computePortalDropdownPlacement({
      anchorRect: { left: 210, right: 238, top: 24, bottom: 52 },
      menuRect,
      viewportWidth,
      viewportHeight,
      align: "end",
    });
    const rect = placedRect(placement, menuRect);

    expect(rect.left).toBeGreaterThanOrEqual(8);
    expect(rect.top).toBeGreaterThanOrEqual(8);
    expect(rect.right).toBeLessThanOrEqual(viewportWidth - 8);
    expect(rect.bottom).toBeLessThanOrEqual(viewportHeight - 8);
    expect(placement.maxWidth).toBe(viewportWidth - 16);
  });

  test("preserves preferred desktop end alignment when it already fits", () => {
    const placement = computePortalDropdownPlacement({
      anchorRect: { left: 1180, right: 1212, top: 60, bottom: 92 },
      menuRect: { width: 256, height: 300 },
      viewportWidth: 1440,
      viewportHeight: 900,
      align: "end",
    });

    expect(placement.left).toBe(956);
    expect(placement.top).toBe(96);
  });
});
