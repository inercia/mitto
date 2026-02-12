// Mitto Web Interface - Infinite Scroll Hook
// Uses IntersectionObserver to detect when user scrolls near the top
// and triggers loading of earlier messages

const { useRef, useEffect, useCallback } = window.preact;

/**
 * Hook for infinite scroll functionality to load earlier messages.
 *
 * Uses IntersectionObserver to detect when a sentinel element becomes visible,
 * indicating the user has scrolled near the top of the messages container.
 *
 * @param {Object} options - Configuration options
 * @param {boolean} options.hasMoreMessages - Whether there are more messages to load
 * @param {boolean} options.isLoading - Whether a load is currently in progress
 * @param {Function} options.onLoadMore - Callback to trigger loading more messages
 * @param {React.RefObject} options.containerRef - Reference to the scrollable container
 * @param {string} options.rootMargin - IntersectionObserver rootMargin (default: "200px")
 * @param {number} options.debounceMs - Debounce time in ms to prevent rapid-fire loading (default: 300)
 * @returns {Object} { sentinelRef } - Ref to attach to the sentinel element
 */
export function useInfiniteScroll(options = {}) {
  const {
    hasMoreMessages = false,
    isLoading = false,
    onLoadMore = null,
    containerRef = null,
    rootMargin = "200px",
    debounceMs = 300,
  } = options;

  const sentinelRef = useRef(null);
  const lastLoadTimeRef = useRef(0);
  const observerRef = useRef(null);

  // Stable callback ref to avoid recreating observer on every render
  const onLoadMoreRef = useRef(onLoadMore);
  onLoadMoreRef.current = onLoadMore;

  const hasMoreRef = useRef(hasMoreMessages);
  hasMoreRef.current = hasMoreMessages;

  const isLoadingRef = useRef(isLoading);
  isLoadingRef.current = isLoading;

  // Handle intersection - called when sentinel becomes visible
  const handleIntersection = useCallback(
    (entries) => {
      const [entry] = entries;
      if (!entry.isIntersecting) return;

      // Check if we should load more
      if (!hasMoreRef.current || isLoadingRef.current) {
        return;
      }

      // Debounce to prevent rapid-fire loading
      const now = Date.now();
      if (now - lastLoadTimeRef.current < debounceMs) {
        return;
      }
      lastLoadTimeRef.current = now;

      // Trigger load
      if (onLoadMoreRef.current) {
        onLoadMoreRef.current();
      }
    },
    [debounceMs],
  );

  // Set up IntersectionObserver
  useEffect(() => {
    const sentinel = sentinelRef.current;
    const container = containerRef?.current;

    if (!sentinel) return;

    // Clean up previous observer
    if (observerRef.current) {
      observerRef.current.disconnect();
    }

    // Create new observer
    // Use the container as root if provided, otherwise use viewport
    const observerOptions = {
      root: container || null,
      rootMargin: rootMargin,
      threshold: 0,
    };

    observerRef.current = new IntersectionObserver(
      handleIntersection,
      observerOptions,
    );
    observerRef.current.observe(sentinel);

    return () => {
      if (observerRef.current) {
        observerRef.current.disconnect();
        observerRef.current = null;
      }
    };
  }, [containerRef, rootMargin, handleIntersection]);

  return { sentinelRef };
}

