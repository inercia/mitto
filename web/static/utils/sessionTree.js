/**
 * Session Tree Utilities
 * 
 * Builds hierarchical conversation trees from flat session lists.
 * Handles parent-child relationships created via mitto_conversation_new MCP tool.
 */

/**
 * Build a conversation tree from a flat list of sessions.
 * 
 * @param {Array} sessions - Flat array of session objects
 * @returns {Object} Tree structure with rootSessions and childrenMap
 * 
 * @example
 * const { rootSessions, childrenMap } = buildSessionTree(sessions);
 * // rootSessions: sessions with no parent
 * // childrenMap: Map<parentId, Array<childSession>>
 */
export function buildSessionTree(sessions) {
  if (!sessions || !Array.isArray(sessions)) {
    return { rootSessions: [], childrenMap: new Map(), orphans: [] };
  }

  const childrenMap = new Map();
  const rootSessions = [];
  const sessionById = new Map();

  // First pass: index all sessions by ID
  sessions.forEach(session => {
    sessionById.set(session.session_id, session);
  });

  // Second pass: build parent-child relationships
  sessions.forEach(session => {
    if (session.parent_session_id) {
      // This is a child session
      if (!childrenMap.has(session.parent_session_id)) {
        childrenMap.set(session.parent_session_id, []);
      }
      childrenMap.get(session.parent_session_id).push(session);
    } else {
      // This is a root session (no parent)
      rootSessions.push(session);
    }
  });

  // Third pass: identify orphaned children (parent not in session list)
  const orphans = [];
  childrenMap.forEach((children, parentId) => {
    if (!sessionById.has(parentId)) {
      // Parent doesn't exist in current session list
      console.warn('buildSessionTree: Found orphaned children for missing parent:', parentId.substring(0, 8));
      children.forEach(child => {
        child._isOrphan = true;
        orphans.push(child);
      });
      // Remove from childrenMap since parent doesn't exist
      childrenMap.delete(parentId);
    }
  });

  return { rootSessions, childrenMap, orphans };
}

/**
 * Get all children for a session (recursively).
 * 
 * @param {string} sessionId - Parent session ID
 * @param {Map} childrenMap - Map of parent ID to children array
 * @returns {Array} All descendant sessions (children, grandchildren, etc.)
 */
export function getAllDescendants(sessionId, childrenMap) {
  const descendants = [];
  const children = childrenMap.get(sessionId) || [];

  children.forEach(child => {
    descendants.push(child);
    // Recursively get grandchildren
    const grandchildren = getAllDescendants(child.session_id, childrenMap);
    descendants.push(...grandchildren);
  });

  return descendants;
}

/**
 * Check if a session has any children.
 *
 * @param {string} sessionId - Session ID to check
 * @param {Map} childrenMap - Map of parent ID to children array
 * @returns {boolean} True if session has children
 */
export function hasChildren(sessionId, childrenMap) {
  const children = childrenMap.get(sessionId);
  return !!(children && children.length > 0);
}

/**
 * Get the number of direct children for a session.
 * 
 * @param {string} sessionId - Session ID
 * @param {Map} childrenMap - Map of parent ID to children array
 * @returns {number} Number of direct children
 */
export function getChildCount(sessionId, childrenMap) {
  const children = childrenMap.get(sessionId);
  return children ? children.length : 0;
}

/**
 * Detect circular references in parent-child relationships.
 * This should never happen (backend prevents it), but we check defensively.
 * 
 * @param {string} sessionId - Session ID to check
 * @param {string} parentId - Proposed parent ID
 * @param {Array} sessions - All sessions
 * @returns {boolean} True if adding this relationship would create a cycle
 */
export function detectCircularReference(sessionId, parentId, sessions) {
  if (!parentId) return false;
  if (sessionId === parentId) return true;

  const visited = new Set();
  let current = parentId;

  // Walk up the parent chain
  while (current) {
    if (visited.has(current)) {
      // Found a cycle in the existing tree
      return true;
    }
    if (current === sessionId) {
      // Would create a cycle
      return true;
    }

    visited.add(current);

    // Find the parent of current
    const session = sessions.find(s => s.session_id === current);
    current = session?.parent_session_id;
  }

  return false;
}

/**
 * Get the depth level of a session in the tree (0 = root, 1 = child, 2 = grandchild, etc.)
 * 
 * @param {string} sessionId - Session ID
 * @param {Array} sessions - All sessions
 * @returns {number} Depth level (0 for root sessions)
 */
export function getSessionDepth(sessionId, sessions) {
  let depth = 0;
  let current = sessionId;

  const sessionById = new Map(sessions.map(s => [s.session_id, s]));

  while (current) {
    const session = sessionById.get(current);
    if (!session || !session.parent_session_id) {
      break;
    }
    depth++;
    current = session.parent_session_id;

    // Safety check: prevent infinite loops
    if (depth > 100) {
      console.error('Detected deep nesting or circular reference', sessionId);
      break;
    }
  }

  return depth;
}
