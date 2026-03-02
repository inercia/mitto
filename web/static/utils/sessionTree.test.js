/**
 * @jest-environment jsdom
 */

import {
  buildSessionTree,
  getAllDescendants,
  hasChildren,
  getChildCount,
  detectCircularReference,
  getSessionDepth,
} from './sessionTree.js';

describe('sessionTree', () => {
  describe('buildSessionTree', () => {
    test('handles empty array', () => {
      const result = buildSessionTree([]);
      expect(result.rootSessions).toEqual([]);
      expect(result.childrenMap.size).toBe(0);
      expect(result.orphans).toEqual([]);
    });

    test('handles null/undefined input', () => {
      expect(buildSessionTree(null).rootSessions).toEqual([]);
      expect(buildSessionTree(undefined).rootSessions).toEqual([]);
    });

    test('builds tree with parent and children', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
        { session_id: 'child-2', parent_session_id: 'parent-1' },
      ];

      const { rootSessions, childrenMap, orphans } = buildSessionTree(sessions);

      expect(rootSessions).toHaveLength(1);
      expect(rootSessions[0].session_id).toBe('parent-1');
      expect(childrenMap.get('parent-1')).toHaveLength(2);
      expect(orphans).toHaveLength(0);
    });

    test('identifies orphaned children', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
        { session_id: 'orphan-1', parent_session_id: 'missing-parent' },
      ];

      const { rootSessions, childrenMap, orphans } = buildSessionTree(sessions);

      expect(rootSessions).toHaveLength(1);
      expect(orphans).toHaveLength(1);
      expect(orphans[0].session_id).toBe('orphan-1');
      expect(orphans[0]._isOrphan).toBe(true);
      expect(childrenMap.has('missing-parent')).toBe(false);
    });

    test('handles multiple root sessions', () => {
      const sessions = [
        { session_id: 'root-1', parent_session_id: '' },
        { session_id: 'root-2', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'root-1' },
      ];

      const { rootSessions, childrenMap } = buildSessionTree(sessions);

      expect(rootSessions).toHaveLength(2);
      expect(childrenMap.get('root-1')).toHaveLength(1);
      expect(childrenMap.get('root-2')).toBeUndefined();
    });
  });

  describe('getAllDescendants', () => {
    test('returns empty array for session with no children', () => {
      const childrenMap = new Map();
      const descendants = getAllDescendants('session-1', childrenMap);
      expect(descendants).toEqual([]);
    });

    test('returns direct children', () => {
      const childrenMap = new Map([
        ['parent-1', [
          { session_id: 'child-1' },
          { session_id: 'child-2' },
        ]],
      ]);

      const descendants = getAllDescendants('parent-1', childrenMap);
      expect(descendants).toHaveLength(2);
    });

    test('returns children and grandchildren recursively', () => {
      const childrenMap = new Map([
        ['parent-1', [{ session_id: 'child-1' }]],
        ['child-1', [{ session_id: 'grandchild-1' }]],
      ]);

      const descendants = getAllDescendants('parent-1', childrenMap);
      expect(descendants).toHaveLength(2);
      expect(descendants[0].session_id).toBe('child-1');
      expect(descendants[1].session_id).toBe('grandchild-1');
    });
  });

  describe('hasChildren', () => {
    test('returns false for session with no children', () => {
      const childrenMap = new Map();
      expect(hasChildren('session-1', childrenMap)).toBe(false);
    });

    test('returns true for session with children', () => {
      const childrenMap = new Map([
        ['parent-1', [{ session_id: 'child-1' }]],
      ]);
      expect(hasChildren('parent-1', childrenMap)).toBe(true);
    });
  });

  describe('getChildCount', () => {
    test('returns 0 for session with no children', () => {
      const childrenMap = new Map();
      expect(getChildCount('session-1', childrenMap)).toBe(0);
    });

    test('returns correct count', () => {
      const childrenMap = new Map([
        ['parent-1', [
          { session_id: 'child-1' },
          { session_id: 'child-2' },
          { session_id: 'child-3' },
        ]],
      ]);
      expect(getChildCount('parent-1', childrenMap)).toBe(3);
    });
  });

  describe('detectCircularReference', () => {
    test('returns false for valid parent-child relationship', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
      ];
      expect(detectCircularReference('child-2', 'parent-1', sessions)).toBe(false);
    });

    test('detects direct self-reference', () => {
      const sessions = [];
      expect(detectCircularReference('session-1', 'session-1', sessions)).toBe(true);
    });

    test('detects circular reference in chain', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: 'child-1' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
      ];
      expect(detectCircularReference('child-1', 'parent-1', sessions)).toBe(true);
    });
  });

  describe('getSessionDepth', () => {
    test('returns 0 for root session', () => {
      const sessions = [
        { session_id: 'root-1', parent_session_id: '' },
      ];
      expect(getSessionDepth('root-1', sessions)).toBe(0);
    });

    test('returns 1 for direct child', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
      ];
      expect(getSessionDepth('child-1', sessions)).toBe(1);
    });

    test('returns correct depth for grandchild', () => {
      const sessions = [
        { session_id: 'parent-1', parent_session_id: '' },
        { session_id: 'child-1', parent_session_id: 'parent-1' },
        { session_id: 'grandchild-1', parent_session_id: 'child-1' },
      ];
      expect(getSessionDepth('grandchild-1', sessions)).toBe(2);
    });
  });
});

