// Mitto Web Interface - Conversation Menu Hook
//
// Shared state + logic for the per-conversation actions menu. Used by both the
// sidebar conversation rows (SessionItem) and the chat header three-dot button
// so both surfaces expose an identical menu. Encapsulates the context-menu
// open/close state, lazy-loaded menus:conversation prompts, and the assembled
// ContextMenu items array (prompt submenus, Properties, periodic toggle,
// archive/unarchive, delete).
const { html, useState, useMemo, useCallback } = window.preact;

import {
  LightningIcon,
  EditIcon,
  ClockIcon,
  MittoIcon,
  ArchiveIcon,
  ArchiveFilledIcon,
  TrashIcon,
  SyncIcon,
  PlusIcon,
  getPromptIconOrDefault,
} from "../components/Icons.js";

export function useConversationMenu({
  session,
  workingDir = "",
  isArchived = false,
  isPeriodicEnabled = false,
  isSpawned = false,
  canArchive = true,
  archiveBlockedReason = null,
  ownsWorktree = false, // If true, the "Merge into" submenu is shown (worktree conversations)
  onRename,
  onDelete,
  onArchive,
  onMakePeriodic,
  onMakeNonPeriodic,
  onFetchConversationPrompts, // async (session, workingDir) => menus:conversation prompts
  onSendPromptToConversation, // (session, prompt) when a context-menu prompt is clicked
  onFetchSessionBranches, // async (session) => branchesData for the "Merge into" submenu
  onMergeSession, // (session, targetBranch) => merge the worktree branch into an existing branch
  onMergeSessionToNewBranch, // (session) => merge into a newly created branch (opens a name dialog)
}) {
  const [contextMenu, setContextMenu] = useState(null);
  // menus:conversation prompts evaluated for THIS conversation. Loaded lazily
  // when the menu opens (enabledWhen depends on this conversation's own context,
  // not the active session). Cached between opens; refreshed each open.
  const [menuPrompts, setMenuPrompts] = useState([]);
  // Candidate merge-back branches for the "Merge into" submenu, loaded lazily
  // when the menu opens (worktree conversations only). null = not yet loaded
  // (submenu shows a "Loading…" placeholder).
  const [menuBranches, setMenuBranches] = useState(null);

  // Open the menu at a viewport position. Used by both right-click and the
  // explicit three-dot button. Evaluates menus:conversation prompts against
  // THIS conversation's context so the submenu reflects the clicked conversation.
  const openContextMenuAt = useCallback(
    (x, y) => {
      setContextMenu({ x, y });
      if (onFetchConversationPrompts && session) {
        onFetchConversationPrompts(session, workingDir).then((prompts) => {
          setMenuPrompts(prompts || []);
        });
      }
      // Lazily load candidate merge-back branches for worktree conversations so
      // the "Merge into" submenu reflects the repo's current branches.
      if (ownsWorktree && onFetchSessionBranches && session) {
        setMenuBranches(null);
        onFetchSessionBranches(session).then((data) => {
          setMenuBranches(data || { branches: [] });
        });
      }
    },
    [
      onFetchConversationPrompts,
      session,
      workingDir,
      ownsWorktree,
      onFetchSessionBranches,
    ],
  );

  const handleContextMenu = useCallback(
    (e) => {
      e.preventDefault();
      e.stopPropagation();
      openContextMenuAt(e.clientX, e.clientY);
    },
    [openContextMenuAt],
  );

  // Explicit three-dot button: anchor the menu at the button's bottom-left.
  // ContextMenu is viewport-aware and will flip/shift to stay on-screen.
  const handleMenuButtonClick = useCallback(
    (e) => {
      e.preventDefault();
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      openContextMenuAt(rect.left, rect.bottom);
    },
    [openContextMenuAt],
  );

  const closeContextMenu = useCallback(() => setContextMenu(null), []);

  const contextMenuItems = useMemo(() => {
    // Build group submenus from prompts flagged with menus:conversation.
    // Prompts are grouped by their `group` attribute; ungrouped prompts fall
    // under "Other". Each group becomes a submenu listing its prompts.
    const promptGroupItems = [];
    if (onSendPromptToConversation && menuPrompts && menuPrompts.length > 0) {
      const groups = new Map();
      for (const p of menuPrompts) {
        if (!p || !p.name) continue;
        const groupName = (p.group && p.group.trim()) || "Other";
        if (!groups.has(groupName)) groups.set(groupName, []);
        groups.get(groupName).push(p);
      }
      for (const [groupName, prompts] of groups) {
        promptGroupItems.push({
          label: groupName,
          icon: html`<${LightningIcon} />`,
          submenu: prompts
            .slice()
            .sort((a, b) => a.name.localeCompare(b.name))
            .map((p) => {
              const PromptIcon = getPromptIconOrDefault(p.icon);
              return {
                label: p.name,
                icon: html`<${PromptIcon} className="w-4 h-4" />`,
                onClick: () => onSendPromptToConversation(session, p),
              };
            }),
        });
      }
    }

    // "Merge into" submenu (worktree conversations): lists candidate branches
    // plus a "New branch…" entry. Branches checked out in *other* worktrees are
    // filtered out (per-conversation branches are never useful merge targets);
    // the repo's default branch is always kept.
    let mergeIntoItem = null;
    if (ownsWorktree && (onMergeSession || onMergeSessionToNewBranch)) {
      const submenu = [];
      if (menuBranches === null) {
        submenu.push({ label: "Loading…", disabled: true });
      } else {
        const checkedOut = menuBranches.checked_out || {};
        const defaultBranch = menuBranches.default_branch || "";
        const candidates = (menuBranches.branches || []).filter(
          (b) => !(checkedOut[b] && b !== defaultBranch),
        );
        if (candidates.length === 0) {
          submenu.push({ label: "No other branches", disabled: true });
        } else if (onMergeSession) {
          for (const b of candidates) {
            submenu.push({
              label: b,
              onClick: () => onMergeSession(session, b),
            });
          }
        }
      }
      if (onMergeSessionToNewBranch) {
        submenu.push({
          label: "New branch…",
          icon: html`<${PlusIcon} className="w-4 h-4" />`,
          onClick: () => onMergeSessionToNewBranch(session),
        });
      }
      mergeIntoItem = {
        label: "Merge into",
        icon: html`<${SyncIcon} className="w-4 h-4" />`,
        submenu,
      };
    }

    return [
      // Prompt group submenus (menus:conversation prompts), e.g. "Workflow"
      ...promptGroupItems,
      // "Merge into" submenu — merge this worktree's branch into another branch
      // without deleting the conversation (worktree conversations only).
      ...(mergeIntoItem ? [mergeIntoItem] : []),
      {
        label: "Properties",
        icon: html`<${EditIcon} />`,
        onClick: () => onRename && onRename(session),
      },
      // "Make periodic" — only for non-periodic, non-spawned, non-archived sessions
      ...(!isPeriodicEnabled && !isSpawned && !isArchived
        ? [
            {
              label: "Make periodic",
              icon: html`<${ClockIcon} />`,
              onClick: () => onMakePeriodic && onMakePeriodic(session),
            },
          ]
        : []),
      // "Make non-periodic" — inverse: only for periodic, non-spawned sessions
      ...(isPeriodicEnabled && !isSpawned
        ? [
            {
              label: "Make non-periodic",
              icon: html`<${MittoIcon} />`,
              onClick: () => onMakeNonPeriodic && onMakeNonPeriodic(session),
            },
          ]
        : []),
      // Hide archive option for child (spawned) sessions
      ...(isSpawned
        ? []
        : [
            {
              label: !canArchive
                ? archiveBlockedReason
                : isArchived
                  ? "Unarchive"
                  : "Archive",
              icon: isArchived
                ? html`<${ArchiveFilledIcon} />`
                : html`<${ArchiveIcon} />`,
              onClick: canArchive
                ? () => onArchive && onArchive(session, !isArchived)
                : undefined,
              disabled: !canArchive,
            },
          ]),
      {
        label: "Delete",
        icon: html`<${TrashIcon} />`,
        onClick: () => onDelete && onDelete(session),
        danger: true,
      },
    ];
  }, [
    menuPrompts,
    onSendPromptToConversation,
    session,
    onRename,
    isPeriodicEnabled,
    isSpawned,
    isArchived,
    onMakePeriodic,
    onMakeNonPeriodic,
    canArchive,
    archiveBlockedReason,
    onArchive,
    onDelete,
    ownsWorktree,
    menuBranches,
    onMergeSession,
    onMergeSessionToNewBranch,
  ]);

  return {
    contextMenu,
    contextMenuItems,
    openContextMenuAt,
    closeContextMenu,
    handleContextMenu,
    handleMenuButtonClick,
  };
}
