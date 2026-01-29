// Mitto Web Interface - Swipe Navigation Hook
// Handles touch-based swipe gestures for mobile navigation

const { useEffect, useRef } = window.preact;

/**
 * Hook for handling swipe navigation gestures
 * 
 * @param {React.RefObject} ref - Reference to the element to attach touch listeners to
 * @param {Function} onSwipeLeft - Callback when user swipes left (e.g., next session)
 * @param {Function} onSwipeRight - Callback when user swipes right (e.g., previous session)
 * @param {Object} options - Configuration options
 * @param {number} options.threshold - Minimum distance to trigger swipe (default: 50)
 * @param {number} options.maxVertical - Maximum vertical movement allowed (default: 100)
 * @param {number} options.edgeWidth - Width of edge zone where swipe starts (default: 30)
 * @param {Function} options.onEdgeSwipeRight - Callback for right swipe from left edge (e.g., open sidebar)
 * @param {Function} options.onEdgeSwipeLeft - Callback for left swipe from right edge
 */
export function useSwipeNavigation(ref, onSwipeLeft, onSwipeRight, options = {}) {
    const {
        threshold = 50,           // Minimum distance to trigger swipe
        maxVertical = 100,        // Maximum vertical movement allowed
        edgeWidth = 30,           // Width of edge zone where swipe starts (mobile)
        onEdgeSwipeRight = null,  // Callback for right swipe from left edge (e.g., open sidebar)
        onEdgeSwipeLeft = null    // Callback for left swipe from right edge
    } = options;

    const touchStartRef = useRef(null);

    useEffect(() => {
        const element = ref.current;
        if (!element) return;

        const handleTouchStart = (e) => {
            const touch = e.touches[0];
            // Track if touch begins near the edge (for mobile)
            const isNearLeftEdge = touch.clientX < edgeWidth;
            const isNearRightEdge = touch.clientX > window.innerWidth - edgeWidth;

            touchStartRef.current = {
                x: touch.clientX,
                y: touch.clientY,
                time: Date.now(),
                isLeftEdge: isNearLeftEdge,
                isRightEdge: isNearRightEdge
            };
        };

        const handleTouchEnd = (e) => {
            if (!touchStartRef.current) return;

            const touch = e.changedTouches[0];
            const deltaX = touch.clientX - touchStartRef.current.x;
            const deltaY = touch.clientY - touchStartRef.current.y;
            const duration = Date.now() - touchStartRef.current.time;
            const { isLeftEdge, isRightEdge } = touchStartRef.current;

            // Only trigger if:
            // - Horizontal movement exceeds threshold
            // - Vertical movement is within limit (not scrolling)
            // - Gesture was reasonably quick (under 500ms)
            if (Math.abs(deltaX) >= threshold &&
                Math.abs(deltaY) <= maxVertical &&
                duration < 500) {

                if (deltaX > 0) {
                    // Swiped right
                    if (isLeftEdge && onEdgeSwipeRight) {
                        // Edge swipe from left -> open sidebar
                        onEdgeSwipeRight();
                    } else {
                        // Regular swipe right -> go to previous session
                        onSwipeRight?.();
                    }
                } else {
                    // Swiped left
                    if (isRightEdge && onEdgeSwipeLeft) {
                        // Edge swipe from right -> custom action
                        onEdgeSwipeLeft();
                    } else {
                        // Regular swipe left -> go to next session
                        onSwipeLeft?.();
                    }
                }
            }

            touchStartRef.current = null;
        };

        const handleTouchCancel = () => {
            touchStartRef.current = null;
        };

        element.addEventListener('touchstart', handleTouchStart, { passive: true });
        element.addEventListener('touchend', handleTouchEnd, { passive: true });
        element.addEventListener('touchcancel', handleTouchCancel, { passive: true });

        return () => {
            element.removeEventListener('touchstart', handleTouchStart);
            element.removeEventListener('touchend', handleTouchEnd);
            element.removeEventListener('touchcancel', handleTouchCancel);
        };
    }, [ref, onSwipeLeft, onSwipeRight, onEdgeSwipeRight, onEdgeSwipeLeft, threshold, maxVertical, edgeWidth]);
}

