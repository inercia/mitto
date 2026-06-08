// web/static/hooks/useSessionNavigation.js
// Owns the conversation-navigation cluster: cycling mode, expanded-groups/
// filter-tab/grouping-mode state (synced from sidebar events), the
// navigableSessions memo, all navigate callbacks (prev/next/above/below/by-index),
// the edge-swipe openSidebar handler, and the useSwipeNavigation wiring.
// The global keydown effect and native-function registrations stay in app.js
// and call the returned navigate functions.
const { useState, useEffect, useCallback, useMemo } = window.preact;

import { useSwipeNavigation } from "./useSwipeNavigation.js";
import {
  getExpandedGroups,
  FILTER_TAB,
  getFilterTab,
  getFilterTabGrouping,
} from "../utils/index.js";
import { getGlobalWorkingDir, getBasename } from "../lib.js";
import { CYCLING_MODE } from "../constants.js";

/**
 * Conversation-navigation hook.
 *
 * @param {Object} deps
 * @param {Array}    deps.allSessions       - All sessions (active + stored), newest first.
 * @param {Array}    deps.storedSessions    - Stored-session metadata (for group key/label).
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
  storedSessions,
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

  // Track filter tab for navigation (needed for filtering navigable sessions)
  const [filterTabForNav, setFilterTabForNav] = useState(() => getFilterTab());

  // Track grouping mode for navigation (needed for "visible_groups" cycling mode)
  // Uses per-tab grouping based on the current filter tab
  const [groupingModeForNav, setGroupingModeForNav] = useState(() =>
    getFilterTabGrouping(getFilterTab()),
  );

  // Helper to get group key for a session (same logic as sidebar grouping)
  const getSessionGroupKey = useCallback(
    (session) => {
      if (groupingModeForNav === "server") {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        return session.acp_server || storedSession?.acp_server || "Unknown";
      } else if (
        groupingModeForNav === "workspace" ||
        groupingModeForNav === "folder"
      ) {
        // workspace and folder modes - group by working_dir|acp_server
        // In folder mode, this returns the subgroup key (sessions are in subgroups, not folders directly)
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        const acpServer = session.acp_server || storedSession?.acp_server || "";
        return `${workingDir}|${acpServer}`;
      }
      return null; // no grouping
    },
    [groupingModeForNav, storedSessions],
  );

  // Helper to get group label for sorting (same as sidebar)
  const getSessionGroupLabel = useCallback(
    (session) => {
      if (groupingModeForNav === "server") {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        return session.acp_server || storedSession?.acp_server || "Unknown";
      } else if (
        groupingModeForNav === "workspace" ||
        groupingModeForNav === "folder"
      ) {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        // Label is the workspace display name if available, otherwise basename
        const acpServer = session.acp_server || storedSession?.acp_server || "";
        const ws = workspaces.find(w => w.working_dir === workingDir && (!acpServer || w.acp_server === acpServer));
        return ws?.name || (workingDir ? getBasename(workingDir) : "Unknown");
      }
      return "";
    },
    [groupingModeForNav, storedSessions, workspaces],
  );

  // Sessions available for navigation based on active filter tab
  // Navigation via keyboard shortcuts and swipe gestures should only cycle within the active tab
  // In "visible_groups" cycling mode, also skip sessions in collapsed groups
  // Sessions are ordered to match the visual order in the sidebar:
  // - When grouped: groups sorted alphabetically, sessions within groups by created_at (newest first)
  // - When not grouped: sessions sorted by created_at (newest first)
  const navigableSessions = useMemo(() => {
    // First filter sessions based on the active filter tab
    // Also exclude child sessions (those with parent_session_id) — navigation
    // should only cycle through top-level conversations
    let tabFilteredSessions;
    switch (filterTabForNav) {
      case FILTER_TAB.PERIODIC:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && s.periodic_enabled && !s.parent_session_id,
        );
        break;
      case FILTER_TAB.ARCHIVED:
        tabFilteredSessions = allSessions.filter(
          (s) => s.archived && !s.parent_session_id,
        );
        break;
      case FILTER_TAB.CONVERSATIONS:
      default:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && !s.periodic_enabled && !s.parent_session_id,
        );
        break;
    }

    // If no grouping mode, sessions are already sorted by created_at from allSessions
    if (groupingModeForNav === "none") {
      return tabFilteredSessions;
    }

    // When grouping is enabled, we need to sort sessions to match the sidebar visual order:
    // 1. Groups sorted alphabetically by label
    // 2. Sessions within each group sorted by created_at (newest first)
    //
    // We do this by sorting all sessions with a composite sort key:
    // primary: group label (alphabetical)
    // secondary: created_at (newest first)
    const sortedSessions = [...tabFilteredSessions].sort((a, b) => {
      const labelA = getSessionGroupLabel(a);
      const labelB = getSessionGroupLabel(b);

      // Primary sort: group label (alphabetical)
      const labelCompare = labelA.localeCompare(labelB);
      if (labelCompare !== 0) return labelCompare;

      // Secondary sort: created_at (newest first)
      return new Date(b.created_at) - new Date(a.created_at);
    });

    // In "visible_groups" cycling mode, only include sessions that are in expanded groups
    if (conversationCyclingMode !== CYCLING_MODE.VISIBLE_GROUPS) {
      return sortedSessions;
    }

    // Filter sessions based on their group's expanded state
    // Use expandedGroupsForNav (React state) instead of calling isGroupExpanded()
    // which reads from localStorage. This is critical for mobile/WKWebView where
    // localStorage can become stale or inconsistent.
    return sortedSessions.filter((session) => {
      const groupKey = getSessionGroupKey(session);
      // Check if group is expanded using React state (not localStorage)
      // Default: archived section is collapsed, all others are expanded
      if (groupKey in expandedGroupsForNav) {
        return expandedGroupsForNav[groupKey];
      }
      if (groupKey === "__archived__") {
        return false;
      }
      return true;
    });
  }, [
    allSessions,
    storedSessions,
    conversationCyclingMode,
    groupingModeForNav,
    filterTabForNav,
    expandedGroupsForNav,
    getSessionGroupKey,
    getSessionGroupLabel,
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

  // Listen for grouping mode, expanded groups, and filter tab changes for navigation
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
    const handleGroupingModeChanged = (e) => {
      setGroupingModeForNav(e.detail.mode);
      // Re-read expanded groups when grouping mode changes
      setExpandedGroupsForNav(getExpandedGroups());
    };
    const handleFilterTabChanged = (e) => {
      setFilterTabForNav(e.detail.tab);
      // Also update grouping mode for the new tab
      const tabGroupingMode = getFilterTabGrouping(e.detail.tab);
      setGroupingModeForNav(tabGroupingMode);
    };
    window.addEventListener(
      "mitto-expanded-groups-changed",
      handleExpandedGroupsChanged,
    );
    window.addEventListener(
      "mitto-grouping-mode-changed",
      handleGroupingModeChanged,
    );
    window.addEventListener("mitto-filter-tab-changed", handleFilterTabChanged);
    return () => {
      window.removeEventListener(
        "mitto-expanded-groups-changed",
        handleExpandedGroupsChanged,
      );
      window.removeEventListener(
        "mitto-grouping-mode-changed",
        handleGroupingModeChanged,
      );
      window.removeEventListener(
        "mitto-filter-tab-changed",
        handleFilterTabChanged,
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
