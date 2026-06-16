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
  onRename,
  onDelete,
  onArchive,
  onMakePeriodic,
  onMakeNonPeriodic,
  onFetchConversationPrompts, // async (session, workingDir) => menus:conversation prompts
  onSendPromptToConversation, // (session, prompt) when a context-menu prompt is clicked
}) {
  const [contextMenu, setContextMenu] = useState(null);
  // menus:conversation prompts evaluated for THIS conversation. Loaded lazily
  // when the menu opens (enabledWhen depends on this conversation's own context,
  // not the active session). Cached between opens; refreshed each open.
  const [menuPrompts, setMenuPrompts] = useState([]);

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
    },
    [onFetchConversationPrompts, session, workingDir],
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

    return [
      // Prompt group submenus (menus:conversation prompts), e.g. "Workflow"
      ...promptGroupItems,
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
              label: isArchived ? "Unarchive" : "Archive",
              icon: isArchived
                ? html`<${ArchiveFilledIcon} />`
                : html`<${ArchiveIcon} />`,
              onClick: canArchive
                ? () => onArchive && onArchive(session, !isArchived)
                : undefined,
              disabled: !canArchive,
              // When archiving is blocked (agent responding or queued
              // messages) keep the plain "Archive" label greyed out and
              // surface the reason on hover instead of replacing the label.
              title: !canArchive ? archiveBlockedReason : undefined,
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
