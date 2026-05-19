// Mitto Web Interface - Resize Handle Hook
// Handles drag-to-resize functionality with mouse and touch support

const { useState, useEffect, useRef, useCallback } = window.preact;

/**
 * Hook for handling drag-to-resize interactions
 *
 * @param {Object} options - Configuration options
 * @param {number} options.initialHeight - Initial size in pixels (height for vertical, width for horizontal)
 * @param {number} options.minHeight - Minimum size constraint
 * @param {number} options.maxHeight - Maximum size constraint
 * @param {string} options.direction - 'vertical' (default) or 'horizontal'
 * @param {Function} options.onHeightChange - Callback when size changes (size) => void
 * @param {Function} options.onDragStart - Callback when drag starts
 * @param {Function} options.onDragEnd - Callback when drag ends (finalSize) => void
 * @returns {Object} { height, setHeight, isDragging, handleRef, handleProps }
 */
export function useResizeHandle(options = {}) {
  const {
    initialHeight = 256,
    minHeight = 100,
    maxHeight = 500,
    direction = "vertical",
    onHeightChange = null,
    onDragStart = null,
    onDragEnd = null,
  } = options;

  const isHorizontal = direction === "horizontal";

  const [height, setHeight] = useState(initialHeight);
  const [isDragging, setIsDragging] = useState(false);
  const handleRef = useRef(null);
  const dragStartRef = useRef(null);

  // Update height when initialHeight changes (e.g., from localStorage)
  useEffect(() => {
    setHeight(initialHeight);
  }, [initialHeight]);

  // Calculate new size from drag movement
  // Vertical: dragging up (negative deltaY) increases height (dropdown expands upward)
  // Horizontal: dragging right (positive deltaX) increases width
  const calculateHeight = useCallback(
    (clientPos) => {
      if (!dragStartRef.current) return height;

      let delta;
      if (isHorizontal) {
        delta = clientPos - dragStartRef.current.startPos;
      } else {
        delta = dragStartRef.current.startPos - clientPos;
      }
      const newSize = dragStartRef.current.startHeight + delta;
      return Math.max(minHeight, Math.min(maxHeight, newSize));
    },
    [height, minHeight, maxHeight, isHorizontal],
  );

  // Mouse move handler
  const handleMouseMove = useCallback(
    (e) => {
      if (!isDragging) return;
      e.preventDefault();
      const pos = isHorizontal ? e.clientX : e.clientY;
      const newHeight = calculateHeight(pos);
      setHeight(newHeight);
      onHeightChange?.(newHeight);
    },
    [isDragging, calculateHeight, onHeightChange, isHorizontal],
  );

  // Mouse up handler
  const handleMouseUp = useCallback(() => {
    if (!isDragging) return;
    setIsDragging(false);
    dragStartRef.current = null;
    onDragEnd?.(height);
  }, [isDragging, height, onDragEnd]);

  // Touch move handler
  const handleTouchMove = useCallback(
    (e) => {
      if (!isDragging || !e.touches[0]) return;
      // Prevent scrolling while dragging
      e.preventDefault();
      const pos = isHorizontal ? e.touches[0].clientX : e.touches[0].clientY;
      const newHeight = calculateHeight(pos);
      setHeight(newHeight);
      onHeightChange?.(newHeight);
    },
    [isDragging, calculateHeight, onHeightChange, isHorizontal],
  );

  // Touch end handler
  const handleTouchEnd = useCallback(() => {
    if (!isDragging) return;
    setIsDragging(false);
    dragStartRef.current = null;
    onDragEnd?.(height);
  }, [isDragging, height, onDragEnd]);

  const cursorStyle = isHorizontal ? "col-resize" : "ns-resize";

  // Add/remove document listeners for mouse/touch move and end
  useEffect(() => {
    if (isDragging) {
      document.addEventListener("mousemove", handleMouseMove);
      document.addEventListener("mouseup", handleMouseUp);
      document.addEventListener("touchmove", handleTouchMove, {
        passive: false,
      });
      document.addEventListener("touchend", handleTouchEnd);
      document.addEventListener("touchcancel", handleTouchEnd);
      // Prevent text selection while dragging
      document.body.style.userSelect = "none";
      document.body.style.cursor = cursorStyle;
    }

    return () => {
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("mouseup", handleMouseUp);
      document.removeEventListener("touchmove", handleTouchMove);
      document.removeEventListener("touchend", handleTouchEnd);
      document.removeEventListener("touchcancel", handleTouchEnd);
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    };
  }, [
    isDragging,
    handleMouseMove,
    handleMouseUp,
    handleTouchMove,
    handleTouchEnd,
    cursorStyle,
  ]);

  // Handle props to spread on the resize handle element
  const handleProps = {
    onMouseDown: (e) => {
      e.preventDefault();
      setIsDragging(true);
      const pos = isHorizontal ? e.clientX : e.clientY;
      dragStartRef.current = { startPos: pos, startHeight: height };
      onDragStart?.();
    },
    onTouchStart: (e) => {
      if (!e.touches[0]) return;
      setIsDragging(true);
      const pos = isHorizontal ? e.touches[0].clientX : e.touches[0].clientY;
      dragStartRef.current = { startPos: pos, startHeight: height };
      onDragStart?.();
    },
  };

  return { height, setHeight, isDragging, handleRef, handleProps };
}
