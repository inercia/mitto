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
  CopyIcon,
  BroomIcon,
} from "../components/Icons.js";
import { buildPromptGroupMenuItems } from "../components/ContextMenu.js";

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
  onCopyConversation, // optional: (session) => void — shows "Copy as Markdown" item
  flushCommand = "", // optional: when non-empty, shows "Flush context" item
  onFlushContext, // optional: (session) => void — invoked when "Flush context" is clicked
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
    const promptGroupItems =
      onSendPromptToConversation && menuPrompts && menuPrompts.length > 0
        ? buildPromptGroupMenuItems(
            menuPrompts,
            (p) => onSendPromptToConversation(session, p),
            html`<${LightningIcon} />`,
          )
        : [];

    return [
      // Prompt group submenus (menus:conversation prompts), e.g. "Workflow"
      ...promptGroupItems,
      {
        label: "Properties",
        icon: html`<${EditIcon} />`,
        onClick: () => onRename && onRename(session),
      },
      // "Copy as Markdown" — only shown when caller provides the callback
      ...(onCopyConversation
        ? [
            {
              label: "Copy as Markdown",
              icon: html`<${CopyIcon} />`,
              onClick: () => onCopyConversation(session),
            },
          ]
        : []),
      // "Flush context" — only shown when the conversation's ACP server has a
      // context-flush command configured and the caller provides the callback.
      ...(flushCommand && onFlushContext
        ? [
            {
              label: "Flush context",
              icon: html`<${BroomIcon} />`,
              title: `Send ${flushCommand} to clear the agent's context`,
              onClick: () => onFlushContext(session),
            },
          ]
        : []),
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
    onCopyConversation,
    flushCommand,
    onFlushContext,
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
