// Mitto Web Interface - Swipe to Delete Hook
// Handles swipe-to-delete gesture for list items with mouse and touch support

const { useState, useEffect, useRef, useCallback } = window.preact;

// Dead zone in pixels - movement below this is considered a tap, not a swipe
const DEAD_ZONE = 10;

/**
 * Hook for handling swipe-to-delete gestures
 *
 * @param {Object} options - Configuration options
 * @param {Function} options.onDelete - Callback when delete is triggered
 * @param {number} options.threshold - Percentage of width to trigger auto-delete (default: 0.5 = 50%)
 * @param {number} options.revealWidth - Width in pixels to reveal delete button (default: 80)
 * @param {boolean} options.disabled - Whether swipe is disabled (default: false)
 * @returns {Object} { swipeOffset, isSwiping, isSwipingRef, isRevealed, containerProps, reset, triggerDelete }
 */
export function useSwipeToDelete(options = {}) {
  const {
    onDelete = null,
    threshold = 0.5,
    revealWidth = 80,
    disabled = false,
  } = options;

  const [swipeOffset, setSwipeOffset] = useState(0);
  const [isSwiping, setIsSwiping] = useState(false);
  const [isRevealed, setIsRevealed] = useState(false);
  const containerRef = useRef(null);
  const dragStartRef = useRef(null);
  // Track if swipe has been "confirmed" (passed dead zone with horizontal intent)
  const swipeConfirmedRef = useRef(false);
  // Track isSwiping state in a ref for synchronous access in click handlers
  const isSwipingRef = useRef(false);

  // Reset the swipe state
  const reset = useCallback(() => {
    setSwipeOffset(0);
    setIsRevealed(false);
    setIsSwiping(false);
    isSwipingRef.current = false;
    dragStartRef.current = null;
    swipeConfirmedRef.current = false;
  }, []);

  // Handle delete action
  const triggerDelete = useCallback(() => {
    if (onDelete) {
      onDelete();
    }
    reset();
  }, [onDelete, reset]);

  // Calculate swipe offset from movement
  const calculateOffset = useCallback(
    (clientX) => {
      if (!dragStartRef.current || !containerRef.current) return 0;

      const deltaX = clientX - dragStartRef.current.startX;
      // Only allow swiping left (negative values)
      if (deltaX > 0) return 0;

      return deltaX;
    },
    [],
  );

  // Handle start of drag (mouse or touch)
  const handleDragStart = useCallback(
    (clientX, clientY) => {
      if (disabled) return;

      // If already revealed, clicking anywhere should reset
      if (isRevealed) {
        reset();
        return;
      }

      setIsSwiping(true);
      isSwipingRef.current = true;
      swipeConfirmedRef.current = false;
      dragStartRef.current = {
        startX: clientX,
        startY: clientY,
        containerWidth: containerRef.current?.offsetWidth || 300,
      };
    },
    [disabled, isRevealed, reset],
  );

  // Handle drag movement - returns true if event should be stopped
  const handleDragMove = useCallback(
    (clientX, clientY) => {
      if (!isSwiping || !dragStartRef.current) return false;

      const deltaX = clientX - dragStartRef.current.startX;
      const deltaY = clientY - dragStartRef.current.startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      // If not yet confirmed, check if we should confirm or cancel
      if (!swipeConfirmedRef.current) {
        // If vertical movement exceeds horizontal, user is scrolling - cancel swipe
        if (absY > absX && absY > DEAD_ZONE) {
          reset();
          return false;
        }
        // If horizontal movement exceeds dead zone and is leftward, confirm swipe
        if (absX > DEAD_ZONE && deltaX < 0) {
          swipeConfirmedRef.current = true;
        } else {
          // Still in dead zone, don't update offset yet
          return false;
        }
      }

      // Swipe is confirmed, update offset
      const offset = calculateOffset(clientX);
      setSwipeOffset(offset);
      return true; // Signal that we're handling this as a swipe
    },
    [isSwiping, calculateOffset, reset],
  );

  // Handle end of drag
  const handleDragEnd = useCallback(() => {
    if (!isSwiping || !dragStartRef.current) {
      reset();
      return;
    }

    // Only process if swipe was confirmed (passed dead zone)
    if (!swipeConfirmedRef.current) {
      reset();
      return;
    }

    const containerWidth = dragStartRef.current.containerWidth;
    const absOffset = Math.abs(swipeOffset);

    // If swiped past threshold, trigger delete
    if (absOffset > containerWidth * threshold) {
      triggerDelete();
    }
    // If swiped past reveal width but not threshold, leave revealed
    else if (absOffset > revealWidth * 0.5) {
      setSwipeOffset(-revealWidth);
      setIsRevealed(true);
      setIsSwiping(false);
      isSwipingRef.current = false;
      dragStartRef.current = null;
      swipeConfirmedRef.current = false;
    }
    // Otherwise, snap back
    else {
      reset();
    }
  }, [isSwiping, swipeOffset, threshold, revealWidth, triggerDelete, reset]);

  // Mouse event handlers
  const handleMouseDown = useCallback(
    (e) => {
      // Only handle left mouse button
      if (e.button !== 0) return;
      handleDragStart(e.clientX, e.clientY);
    },
    [handleDragStart],
  );

  const handleMouseMove = useCallback(
    (e) => {
      if (!isSwiping) return;
      const handled = handleDragMove(e.clientX, e.clientY);
      if (handled) {
        e.preventDefault();
        e.stopPropagation();
      }
    },
    [isSwiping, handleDragMove],
  );

  const handleMouseUp = useCallback(() => {
    handleDragEnd();
  }, [handleDragEnd]);

  // Touch event handlers
  const handleTouchStart = useCallback(
    (e) => {
      if (!e.touches[0]) return;
      const touch = e.touches[0];
      handleDragStart(touch.clientX, touch.clientY);
    },
    [handleDragStart],
  );

  const handleTouchMove = useCallback(
    (e) => {
      if (!isSwiping || !e.touches[0]) return;
      const touch = e.touches[0];
      const handled = handleDragMove(touch.clientX, touch.clientY);
      // If we're handling this as a confirmed swipe, stop propagation
      // to prevent other gesture handlers from interfering
      if (handled && swipeConfirmedRef.current) {
        e.stopPropagation();
      }
    },
    [isSwiping, handleDragMove],
  );

  const handleTouchEnd = useCallback(
    (e) => {
      // If swipe was confirmed, stop propagation to prevent click events
      if (swipeConfirmedRef.current) {
        e.stopPropagation();
      }
      handleDragEnd();
    },
    [handleDragEnd],
  );

  // Add/remove document listeners for mouse/touch move and end
  // Use capture phase for touch events to intercept before other handlers
  useEffect(() => {
    if (isSwiping) {
      document.addEventListener("mousemove", handleMouseMove);
      document.addEventListener("mouseup", handleMouseUp);
      // Use passive: false for touchmove so we can call stopPropagation
      document.addEventListener("touchmove", handleTouchMove, { passive: true, capture: true });
      document.addEventListener("touchend", handleTouchEnd, { capture: true });
      document.addEventListener("touchcancel", handleTouchEnd, { capture: true });
      // Prevent text selection while dragging
      document.body.style.userSelect = "none";
    }

    return () => {
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("mouseup", handleMouseUp);
      document.removeEventListener("touchmove", handleTouchMove, { capture: true });
      document.removeEventListener("touchend", handleTouchEnd, { capture: true });
      document.removeEventListener("touchcancel", handleTouchEnd, { capture: true });
      document.body.style.userSelect = "";
    };
  }, [isSwiping, handleMouseMove, handleMouseUp, handleTouchMove, handleTouchEnd]);

  // Click outside to reset revealed state
  // Use a ref to store the listener so we can remove it even if the component unmounts
  const clickListenerRef = useRef(null);

  useEffect(() => {
    // Clean up any existing listener first
    if (clickListenerRef.current) {
      document.removeEventListener("click", clickListenerRef.current, true);
      clickListenerRef.current = null;
    }

    if (!isRevealed) return;

    let timeoutId = null;

    const handleClickOutside = (e) => {
      // If component is unmounted, remove listener and bail
      if (!containerRef.current) {
        if (clickListenerRef.current) {
          document.removeEventListener("click", clickListenerRef.current, true);
          clickListenerRef.current = null;
        }
        return;
      }

      if (!containerRef.current.contains(e.target)) {
        reset();
      }
    };

    // Delay to avoid catching the mouseup that revealed it
    timeoutId = setTimeout(() => {
      // Use capture phase to ensure this runs before other click handlers
      document.addEventListener("click", handleClickOutside, true);
      clickListenerRef.current = handleClickOutside;
    }, 10);

    return () => {
      if (timeoutId) {
        clearTimeout(timeoutId);
      }
      if (clickListenerRef.current) {
        document.removeEventListener("click", clickListenerRef.current, true);
        clickListenerRef.current = null;
      }
    };
  }, [isRevealed, reset]);

  // Props to spread on the swipeable container element
  const containerProps = {
    ref: containerRef,
    onMouseDown: handleMouseDown,
    onTouchStart: handleTouchStart,
  };

  return {
    swipeOffset,
    isSwiping,
    isSwipingRef,
    isRevealed,
    containerProps,
    reset,
    triggerDelete,
  };
}

