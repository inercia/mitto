// Mitto Web Interface - Resize Handle Hook
// Handles drag-to-resize functionality with mouse and touch support

const { useState, useEffect, useRef, useCallback } = window.preact;

/**
 * Hook for handling drag-to-resize interactions
 *
 * @param {Object} options - Configuration options
 * @param {number} options.initialHeight - Initial height in pixels
 * @param {number} options.minHeight - Minimum height constraint
 * @param {number} options.maxHeight - Maximum height constraint
 * @param {Function} options.onHeightChange - Callback when height changes (height) => void
 * @param {Function} options.onDragStart - Callback when drag starts
 * @param {Function} options.onDragEnd - Callback when drag ends (finalHeight) => void
 * @returns {Object} { height, isDragging, handleRef, handleProps }
 */
export function useResizeHandle(options = {}) {
  const {
    initialHeight = 256,
    minHeight = 100,
    maxHeight = 500,
    onHeightChange = null,
    onDragStart = null,
    onDragEnd = null,
  } = options;

  const [height, setHeight] = useState(initialHeight);
  const [isDragging, setIsDragging] = useState(false);
  const handleRef = useRef(null);
  const dragStartRef = useRef(null);

  // Update height when initialHeight changes (e.g., from localStorage)
  useEffect(() => {
    setHeight(initialHeight);
  }, [initialHeight]);

  // Calculate new height from drag movement
  // Since the dropdown expands upward, dragging up (negative deltaY) increases height
  const calculateHeight = useCallback(
    (clientY) => {
      if (!dragStartRef.current) return height;

      const deltaY = dragStartRef.current.startY - clientY;
      const newHeight = dragStartRef.current.startHeight + deltaY;
      return Math.max(minHeight, Math.min(maxHeight, newHeight));
    },
    [height, minHeight, maxHeight],
  );

  // Mouse move handler
  const handleMouseMove = useCallback(
    (e) => {
      if (!isDragging) return;
      e.preventDefault();
      const newHeight = calculateHeight(e.clientY);
      setHeight(newHeight);
      onHeightChange?.(newHeight);
    },
    [isDragging, calculateHeight, onHeightChange],
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
      const newHeight = calculateHeight(e.touches[0].clientY);
      setHeight(newHeight);
      onHeightChange?.(newHeight);
    },
    [isDragging, calculateHeight, onHeightChange],
  );

  // Touch end handler
  const handleTouchEnd = useCallback(() => {
    if (!isDragging) return;
    setIsDragging(false);
    dragStartRef.current = null;
    onDragEnd?.(height);
  }, [isDragging, height, onDragEnd]);

  // Add/remove document listeners for mouse/touch move and end
  useEffect(() => {
    if (isDragging) {
      document.addEventListener("mousemove", handleMouseMove);
      document.addEventListener("mouseup", handleMouseUp);
      document.addEventListener("touchmove", handleTouchMove, { passive: false });
      document.addEventListener("touchend", handleTouchEnd);
      document.addEventListener("touchcancel", handleTouchEnd);
      // Prevent text selection while dragging
      document.body.style.userSelect = "none";
      document.body.style.cursor = "ns-resize";
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
  }, [isDragging, handleMouseMove, handleMouseUp, handleTouchMove, handleTouchEnd]);

  // Handle props to spread on the resize handle element
  const handleProps = {
    onMouseDown: (e) => {
      e.preventDefault();
      setIsDragging(true);
      dragStartRef.current = { startY: e.clientY, startHeight: height };
      onDragStart?.();
    },
    onTouchStart: (e) => {
      if (!e.touches[0]) return;
      setIsDragging(true);
      dragStartRef.current = { startY: e.touches[0].clientY, startHeight: height };
      onDragStart?.();
    },
  };

  return { height, setHeight, isDragging, handleRef, handleProps };
}

