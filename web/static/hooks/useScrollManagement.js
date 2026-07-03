// web/static/hooks/useScrollManagement.js
// Manages the messages-area scroll behavior for the App: tracks whether the user
// is at the bottom, shows the "new messages" indicator, auto-scrolls on new content
// during an active conversation, positions instantly at the bottom on session switch
// (before paint), and restores scroll position after "load more" (prepend).
//
// Shared refs (messagesContainerRef, scrollPreservationRef) are owned by App because
// they are also used by the render, useInfiniteScroll, and handleLoadMore; they are
// passed in. All scroll-only state and bookkeeping refs live here.
const { useState, useRef, useEffect, useLayoutEffect, useCallback } =
  window.preact;

/**
 * Scroll management hook for the messages area.
 *
 * @param {Object} deps
 * @param {Array} deps.messages - Current conversation messages.
 * @param {string|null} deps.activeSessionId - Focused conversation id.
 * @param {string} deps.mainView - Active main view ("conversation" | "beads").
 * @param {boolean} deps.isStreaming - Whether the agent is actively streaming.
 * @param {boolean} deps.isLoadingMore - Whether older messages are loading (prepend).
 * @param {Object} deps.messagesContainerRef - Ref to the scrollable container.
 * @param {Object} deps.scrollPreservationRef - Ref holding pre-load scroll metrics.
 * @returns {{ isUserAtBottom: boolean, hasNewMessages: boolean, scrollToBottom: Function }}
 */
export function useScrollManagement({
  messages,
  activeSessionId,
  mainView,
  isStreaming,
  isLoadingMore,
  messagesContainerRef,
  scrollPreservationRef,
}) {
  const [isUserAtBottom, setIsUserAtBottom] = useState(true);
  const [hasNewMessages, setHasNewMessages] = useState(false);

  const prevMessagesLengthRef = useRef(0);

  // Threshold for considering user "at bottom"
  // For large scroll ranges (>200px), use a fixed 50px threshold
  // For smaller ranges, use 25% of maxScroll to ensure the button can appear
  const SCROLL_THRESHOLD_PX = 50;
  const SCROLL_THRESHOLD_PERCENT = 0.25;

  // Check if the user is at the bottom of the messages container
  // With flex-col-reverse on the INNER wrapper (not the scrollable container):
  // - scrollTop=0 means we're at the visual TOP (oldest messages)
  // - scrollTop=scrollHeight-clientHeight means we're at the visual BOTTOM (newest messages)
  const checkIfAtBottom = useCallback(() => {
    const container = messagesContainerRef.current;
    if (!container) return true;
    const maxScroll = container.scrollHeight - container.clientHeight;
    // If there's no scrollable content, consider us at bottom
    if (maxScroll <= 0) return true;
    // Use percentage-based threshold for small scroll ranges,
    // fixed threshold for larger ones
    const threshold = Math.min(
      SCROLL_THRESHOLD_PX,
      maxScroll * SCROLL_THRESHOLD_PERCENT,
    );
    const atBottom = container.scrollTop >= maxScroll - threshold;
    if (window.__debug?.scroll)
      console.log("[scroll] checkIfAtBottom:", {
        scrollTop: container.scrollTop,
        scrollHeight: container.scrollHeight,
        clientHeight: container.clientHeight,
        maxScroll,
        threshold,
        atBottom,
      });
    return atBottom;
  }, []);

  // Scroll to bottom handler
  // With flex-col-reverse on inner wrapper, scrollHeight is the visual bottom
  const scrollToBottom = useCallback((smooth = true) => {
    const container = messagesContainerRef.current;
    if (container) {
      container.scrollTo({
        top: container.scrollHeight,
        behavior: smooth ? "smooth" : "auto",
      });
      setIsUserAtBottom(true);
      setHasNewMessages(false);
    }
  }, []);

  // Position the messages container at the visual bottom instantly (bypassing CSS
  // scroll-behavior: smooth) and mark the user as at-bottom. Shared by the
  // session-switch and conversation-view-reentry effects below, both of which
  // need to land at the bottom BEFORE paint with no animation.
  // With flex-col-reverse on the inner wrapper, scrollHeight is the visual bottom.
  const scrollToBottomInstant = useCallback(() => {
    const container = messagesContainerRef.current;
    if (!container) return;
    // Temporarily disable smooth scrolling to make scroll instant
    const originalBehavior = container.style.scrollBehavior;
    container.style.scrollBehavior = "auto";
    const beforeScrollTop = container.scrollTop;
    container.scrollTop = container.scrollHeight; // scrollHeight = visual bottom
    if (window.__debug?.scroll)
      console.log("[scroll] scrollToBottomInstant:", {
        beforeScrollTop,
        afterScrollTop: container.scrollTop,
        scrollHeight: container.scrollHeight,
        clientHeight: container.clientHeight,
      });
    // Restore original behavior after the scroll completes
    container.style.scrollBehavior = originalBehavior;
    // Explicitly set state since scroll event may not fire if position doesn't change
    setIsUserAtBottom(true);
    setHasNewMessages(false);
  }, []);

  // Handle scroll events to track user's scroll position.
  //
  // The messages container is conditionally rendered (it unmounts when the Beads
  // view is shown, then remounts as a brand-new element when returning to a
  // conversation). messagesContainerRef is a ref object, so Preact sets .current
  // to null on unmount and to a NEW element on remount. A one-time mount effect
  // would either never attach (container absent on mount) or stay bound to a
  // stale/detached element after a remount, leaving isUserAtBottom frozen at
  // true — which hides the scroll-to-bottom button and forces every streamed
  // chunk to auto-scroll. To stay correct across remounts we re-check on every
  // render and (re)attach whenever the container element changes identity.
  const attachedContainerRef = useRef(null);
  const detachScrollRef = useRef(null);

  useEffect(() => {
    const container = messagesContainerRef.current;
    // Unchanged since the last attach — keep the existing listener as-is.
    if (container === attachedContainerRef.current) return;

    // The element changed (mount, unmount, or remount): detach from the previous
    // element before binding to the new one.
    if (detachScrollRef.current) {
      detachScrollRef.current();
      detachScrollRef.current = null;
    }
    attachedContainerRef.current = container;
    if (!container) return;

    const handleScroll = (source = "scroll") => {
      const atBottom = checkIfAtBottom();
      if (window.__debug?.scroll)
        console.log(`[scroll] handleScroll(${source}):`, { atBottom });
      setIsUserAtBottom(atBottom);
      // Clear new messages indicator when user scrolls to bottom
      if (atBottom) {
        setHasNewMessages(false);
      }
    };

    // Check initial scroll position on mount
    // This handles cases where content fits in viewport (no scroll event fires)
    // Use requestAnimationFrame to ensure layout is complete before checking
    requestAnimationFrame(() => {
      handleScroll("initial-raf");
    });

    const onScroll = () => handleScroll("event");
    container.addEventListener("scroll", onScroll, { passive: true });
    detachScrollRef.current = () =>
      container.removeEventListener("scroll", onScroll);
  });

  // Detach the scroll listener when the hook unmounts.
  useEffect(() => {
    return () => {
      if (detachScrollRef.current) {
        detachScrollRef.current();
        detachScrollRef.current = null;
      }
    };
  }, []);

  // Track the active session to detect when we switch sessions
  const prevActiveSessionIdRef = useRef(activeSessionId);
  // Track if we're still in the initial load phase after a session switch
  const sessionJustSwitchedRef = useRef(false);
  // Track previous isLoadingMore state to detect when a "load more" completes
  const prevIsLoadingMoreRef = useRef(false);
  // Track if we just finished loading more (prepend) - skip auto-scroll in this case
  const justLoadedMoreRef = useRef(false);

  // Position at bottom synchronously BEFORE paint when switching sessions
  // This prevents any visible "jump" - the content appears already at the bottom
  useLayoutEffect(() => {
    const currentLength = messages.length;

    // Detect session switch (activeSessionId changed)
    const sessionSwitched = prevActiveSessionIdRef.current !== activeSessionId;
    if (sessionSwitched) {
      prevActiveSessionIdRef.current = activeSessionId;
      prevMessagesLengthRef.current = currentLength;

      // Position at bottom instantly - useLayoutEffect ensures this happens BEFORE paint
      if (currentLength > 0) {
        scrollToBottomInstant();
      } else {
        // No messages yet - set flag so we scroll when messages arrive
        sessionJustSwitchedRef.current = true;
      }
      return;
    }

    // If we just switched sessions and now messages appeared, this is the initial load
    // Position at bottom instantly BEFORE paint
    if (sessionJustSwitchedRef.current && currentLength > 0) {
      sessionJustSwitchedRef.current = false;
      prevMessagesLengthRef.current = currentLength;
      scrollToBottomInstant();
      return;
    }
  }, [messages, activeSessionId, scrollToBottomInstant]);

  // Re-entering the conversation view (e.g. after closing the Beads issue viewer)
  // remounts the messages container as a brand-new element WITHOUT changing
  // activeSessionId, so the session-switch effect above does not fire and the
  // fresh container would otherwise stay at its default top position. Treat a
  // transition back into the conversation view like a focus: position at the
  // bottom instantly BEFORE paint so the user returns to the latest message they
  // were viewing (mirrors the session-switch behavior).
  const prevMainViewRef = useRef(mainView);
  useLayoutEffect(() => {
    const prev = prevMainViewRef.current;
    prevMainViewRef.current = mainView;
    if (mainView !== "conversation" || prev === "conversation") return;
    if (messages.length > 0) {
      scrollToBottomInstant();
    } else {
      // Messages not loaded yet — defer to the session-switch effect, which
      // scrolls once they arrive.
      sessionJustSwitchedRef.current = true;
    }
  }, [mainView, messages, scrollToBottomInstant]);

  // Detect when "load more" (prepend) completes - restore scroll position and skip auto-scroll
  // Uses useLayoutEffect to run BEFORE browser paint, preventing visual jump
  useLayoutEffect(() => {
    // Detect transition from isLoadingMore=true to isLoadingMore=false
    if (prevIsLoadingMoreRef.current && !isLoadingMore) {
      // Load more just completed - set flag to skip auto-scroll for prepended content
      justLoadedMoreRef.current = true;
      if (window.__debug?.scroll)
        console.log("[Scroll] Load more completed, will skip auto-scroll");

      // Restore scroll position to maintain visual position after prepend
      // The new content was added above, so we need to offset scrollTop by the height difference
      const container = messagesContainerRef.current;
      const savedMetrics = scrollPreservationRef.current;
      if (container && savedMetrics) {
        // Temporarily disable smooth scrolling to make scroll position restoration instant
        // Without this, the browser will animate the scroll which causes visual jumping
        const originalBehavior = container.style.scrollBehavior;
        container.style.scrollBehavior = "auto";

        const newScrollHeight = container.scrollHeight;
        const heightDiff = newScrollHeight - savedMetrics.scrollHeight;
        const newScrollTop = savedMetrics.scrollTop + heightDiff;
        container.scrollTop = newScrollTop;
        if (window.__debug?.scroll)
          console.log("[Scroll] Restored scroll position after prepend:", {
            oldScrollHeight: savedMetrics.scrollHeight,
            newScrollHeight,
            heightDiff,
            oldScrollTop: savedMetrics.scrollTop,
            newScrollTop,
          });

        // Restore original scroll behavior after the instant scroll
        container.style.scrollBehavior = originalBehavior;
        scrollPreservationRef.current = null;
      }
    }
    prevIsLoadingMoreRef.current = isLoadingMore;
  }, [isLoadingMore, messages]);

  // Smart auto-scroll for new content during active conversation
  useEffect(() => {
    const currentLength = messages.length;
    const prevLength = prevMessagesLengthRef.current;

    // Skip if this is a session switch (handled by useLayoutEffect above)
    if (prevActiveSessionIdRef.current !== activeSessionId) {
      return;
    }

    // Skip if this is initial load after session switch (handled by useLayoutEffect above)
    if (sessionJustSwitchedRef.current) {
      return;
    }

    // Skip auto-scroll if we just loaded older messages (prepend)
    // The useInfiniteScroll hook handles scroll position restoration for this case
    if (justLoadedMoreRef.current) {
      if (window.__debug?.scroll)
        console.log(
          "[Scroll] Skipping auto-scroll - just loaded older messages",
        );
      justLoadedMoreRef.current = false;
      prevMessagesLengthRef.current = currentLength;
      return;
    }

    const hasNewContent =
      currentLength > prevLength || (isStreaming && currentLength > 0);

    if (hasNewContent) {
      if (isUserAtBottom) {
        // User is at bottom, auto-scroll
        scrollToBottom(true);
      } else {
        // User has scrolled up, show new messages indicator
        setHasNewMessages(true);
      }
    }

    prevMessagesLengthRef.current = currentLength;
  }, [messages, isStreaming, isUserAtBottom, scrollToBottom, activeSessionId]);

  // Reset scroll state when switching sessions
  // The auto-scroll effect above handles the initial positioning after messages load
  useEffect(() => {
    setIsUserAtBottom(true);
    setHasNewMessages(false);
  }, [activeSessionId]);

  return { isUserAtBottom, hasNewMessages, scrollToBottom };
}
