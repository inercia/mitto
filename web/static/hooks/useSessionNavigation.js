// web/static/hooks/useSessionNavigation.js
// Owns the conversation-navigation cluster: cycling mode, expanded-groups/
// filter-tab/grouping-mode state (synced from sidebar events), the
// navigableSessions memo, all navigate callbacks (prev/next/above/below/by-index),
// the edge-swipe openSidebar handler, and the useSwipeNavigation wiring.
// The global keydown effect and native-function registrations stay in app.js
// and call the returned navigate functions.
const { useState, useEffect, useCallback, useMemo } = window.preact;

import { useSwipeNavigation } from "./useSwipeNavigation.js";
import { getExpandedGroups, getCategoryFilter } from "../utils/index.js";
import {
  computeUnifiedTree,
  filterUnifiedTree,
  flattenUnifiedTreeForNav,
  scopeNavEntriesToCurrentFolder,
} from "../utils/sessionGrouping.js";
import { CYCLING_MODE } from "../constants.js";

/**
 * Conversation-navigation hook.
 *
 * @param {Object} deps
 * @param {Array}    deps.allSessions       - All sessions (active + stored), newest first.
 * @param {Array}    deps.workspaces        - Workspace configs (for group label display names).
 * @param {string|null} deps.activeSessionId - Currently focused session.
 * @param {Function} deps.switchSession     - Switches the active session.
 * @param {Function} deps.setShowSidebar    - Opens/closes sidebar overlay.
 * @param {Function} deps.setSwipeDirection - Triggers swipe-in animation (in app.js state).
 * @param {Function} deps.setSwipeArrow     - Shows directional arrow overlay (in app.js state).
 * @param {Object}   deps.mainContentRef    - Ref to the main content area (for swipe gesture target).
 */
export function useSessionNavigation({
  allSessions,
  workspaces,
  activeSessionId,
  switchSession,
  setShowSidebar,
  setSwipeDirection,
  setSwipeArrow,
  mainContentRef,
}) {
  // Conversation cycling mode setting (web UI, default: "all" - cycle through all non-archived)
  const [conversationCyclingMode, setConversationCyclingMode] = useState(
    CYCLING_MODE.ALL,
  );

  // Track expanded groups state for re-computing navigableSessions in "visible_groups" mode
  // We store the actual groups map in state rather than just a version counter, because
  // on mobile/WKWebView, localStorage can become stale and isGroupExpanded() might return
  // incorrect values. By storing the map in React state, we ensure the navigation filtering
  // always uses the correct, current expanded/collapsed state.
  const [expandedGroupsForNav, setExpandedGroupsForNav] = useState(() =>
    getExpandedGroups(),
  );

  // Category filter for navigation (mitto-1er.8): nav traverses the same
  // filtered unified tree the sidebar renders. Seeded from sessionStorage and
  // kept in sync via the 'mitto-category-filter-changed' event (SessionList).
  const [categoryFilterForNav, setCategoryFilterForNav] = useState(() =>
    getCategoryFilter(),
  );

  // Sessions available for keyboard/swipe navigation, in the exact unified-tree
  // visual order (folders alphabetical). Cycling is restricted to top-level
  // (parent), non-archived conversations in the active conversation's folder
  // only: child conversations spawned by agents (e.g. "Coder") are never cycling
  // targets, archived conversations are never cycling targets, and cycling never
  // crosses into another folder. Children and archived conversations remain
  // visible in the sidebar; this only affects swipe/keyboard navigation.
  // Static nodes (Dashboard, Tasks) are excluded by the flattener.
  // In VISIBLE_GROUPS cycling mode, also skip entries whose folder is collapsed
  // — defaults mirror the sidebar: folders expanded.
  const navigableSessions = useMemo(() => {
    const tree = filterUnifiedTree(
      computeUnifiedTree(allSessions, workspaces),
      categoryFilterForNav,
    );
    const entries = flattenUnifiedTreeForNav(tree);

    // Folder of the active conversation, used when it is not present in the
    // (category-filtered) entries. folderKey equals the root parent's
    // working_dir; the active conversation shares its root's folder.
    const activeSession = (allSessions || []).find(
      (s) => s.session_id === activeSessionId,
    );
    const folderFallback = activeSession
      ? activeSession.working_dir || "Unknown"
      : null;

    const scoped = scopeNavEntriesToCurrentFolder(
      entries,
      activeSessionId,
      folderFallback,
    );

    if (conversationCyclingMode !== CYCLING_MODE.VISIBLE_GROUPS) {
      return scoped.map((e) => e.session);
    }

    return scoped
      .filter((e) => {
        if (expandedGroupsForNav[e.folderKey] === false) return false;
        return true;
      })
      .map((e) => e.session);
  }, [
    allSessions,
    workspaces,
    categoryFilterForNav,
    conversationCyclingMode,
    expandedGroupsForNav,
    activeSessionId,
  ]);

  // Navigate to previous/next session with animation direction (wraps around for swipe gestures)
  // Skips archived sessions
  const navigateToPreviousSession = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the last navigable session
    const prevIndex =
      currentIndex === -1
        ? navigableSessions.length - 1
        : currentIndex === 0
          ? navigableSessions.length - 1
          : currentIndex - 1;
    setSwipeDirection("right"); // Content slides in from left
    setSwipeArrow("right"); // Show right arrow (user swiped right)
    switchSession(navigableSessions[prevIndex].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  const navigateToNextSession = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the first navigable session
    const nextIndex =
      currentIndex === -1
        ? 0
        : currentIndex === navigableSessions.length - 1
          ? 0
          : currentIndex + 1;
    setSwipeDirection("left"); // Content slides in from right
    setSwipeArrow("left"); // Show left arrow (user swiped left)
    switchSession(navigableSessions[nextIndex].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Navigate to session above in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  // Skips archived sessions
  const navigateToSessionAbove = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the last navigable session (conceptually "above" since list goes down)
    if (currentIndex === -1) {
      switchSession(navigableSessions[navigableSessions.length - 1].session_id);
      return;
    }
    if (currentIndex === 0) return; // Already at top
    switchSession(navigableSessions[currentIndex - 1].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Navigate to session below in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  // Skips archived sessions
  const navigateToSessionBelow = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the first navigable session (conceptually "below" since list goes down)
    if (currentIndex === -1) {
      switchSession(navigableSessions[0].session_id);
      return;
    }
    if (currentIndex === navigableSessions.length - 1) return; // Already at bottom
    switchSession(navigableSessions[currentIndex + 1].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Open sidebar handler for edge swipe
  const openSidebar = useCallback(() => {
    setShowSidebar(true);
  }, []);

  // Enable swipe navigation on mobile
  // - Swipe left/right anywhere: switch sessions
  // - Swipe right from left edge: open sidebar
  useSwipeNavigation(
    mainContentRef,
    navigateToNextSession,
    navigateToPreviousSession,
    {
      threshold: 80, // Require a decent swipe distance
      maxVertical: 80, // Allow some vertical movement
      edgeWidth: 40, // Start from edge zone
      onEdgeSwipeRight: openSidebar, // Swipe right from left edge opens sidebar
    },
  );

  // Navigate to session by index (0-based) for keyboard shortcuts
  // Uses navigableSessions to skip archived conversations
  const navigateToSessionByIndex = useCallback(
    (index) => {
      if (index >= 0 && index < navigableSessions.length) {
        const targetSession = navigableSessions[index];
        if (targetSession.session_id !== activeSessionId) {
          switchSession(targetSession.session_id);
        }
      }
    },
    [navigableSessions, activeSessionId, switchSession],
  );

  // Listen for expanded groups and category filter changes for navigation
  useEffect(() => {
    const handleExpandedGroupsChanged = (e) => {
      // Update React state with the new expanded groups state
      // This uses the event detail (groupKey, expanded) to update state directly,
      // avoiding a read from localStorage which can be stale on mobile/WKWebView
      setExpandedGroupsForNav((prev) => {
        const { groupKey, expanded } = e.detail || {};
        if (groupKey !== undefined) {
          return { ...prev, [groupKey]: expanded };
        }
        // If no detail provided, fall back to reading from localStorage
        // (this handles the case where the event is dispatched without detail)
        return getExpandedGroups();
      });
    };
    const handleCategoryFilterChanged = (e) => {
      if (e.detail && e.detail.filter) {
        setCategoryFilterForNav(e.detail.filter);
      }
    };
    window.addEventListener(
      "mitto-expanded-groups-changed",
      handleExpandedGroupsChanged,
    );
    window.addEventListener(
      "mitto-category-filter-changed",
      handleCategoryFilterChanged,
    );
    return () => {
      window.removeEventListener(
        "mitto-expanded-groups-changed",
        handleExpandedGroupsChanged,
      );
      window.removeEventListener(
        "mitto-category-filter-changed",
        handleCategoryFilterChanged,
      );
    };
  }, []);

  return {
    conversationCyclingMode,
    setConversationCyclingMode,
    navigableSessions,
    navigateToPreviousSession,
    navigateToNextSession,
    navigateToSessionAbove,
    navigateToSessionBelow,
    navigateToSessionByIndex,
    openSidebar,
  };
}
