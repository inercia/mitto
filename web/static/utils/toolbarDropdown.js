// Pure geometry helper for Toolbar's opt-in portal dropdowns. Keeping viewport
// math independent from Preact makes edge placement deterministic and directly
// testable without mounting the full application.

export const TOOLBAR_DROPDOWN_MARGIN = 8;
export const TOOLBAR_DROPDOWN_GAP = 4;

export function computePortalDropdownPlacement({
  anchorRect,
  menuRect,
  viewportWidth,
  viewportHeight,
  align = "start",
  margin = TOOLBAR_DROPDOWN_MARGIN,
  gap = TOOLBAR_DROPDOWN_GAP,
}) {
  const maxWidth = Math.max(0, viewportWidth - margin * 2);
  const maxHeight = Math.max(0, viewportHeight - margin * 2);
  const width = Math.min(Math.max(0, menuRect.width), maxWidth);
  const height = Math.min(Math.max(0, menuRect.height), maxHeight);

  let left = align === "end" ? anchorRect.right - width : anchorRect.left;
  const maxLeft = Math.max(margin, viewportWidth - margin - width);
  left = Math.min(Math.max(margin, left), maxLeft);

  const below = anchorRect.bottom + gap;
  const above = anchorRect.top - gap - height;
  let top = below;
  if (below + height > viewportHeight - margin && above >= margin) {
    top = above;
  }
  const maxTop = Math.max(margin, viewportHeight - margin - height);
  top = Math.min(Math.max(margin, top), maxTop);

  return { left, top, maxWidth, maxHeight };
}
