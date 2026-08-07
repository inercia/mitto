// Mitto Web Interface - Conversation Menu Hook
//
// Shared state + logic for the per-conversation actions menu. Used by both the
// sidebar conversation rows (SessionItem) and the chat header three-dot button
// so both surfaces expose an identical menu. Encapsulates the context-menu
// open/close state, lazy-loaded menus:conversation prompts, and the assembled
// ContextMenu items array (prompt submenus, Properties, loop toggle,
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
  CheckIcon,
  PaletteIcon,
  CircleIcon,
} from "../components/Icons.js";
import { buildPromptGroupMenuItems } from "../components/ContextMenu.js";
import { CONVERSATION_COLORS } from "../constants.js";

export function useConversationMenu({
  session,
  workingDir = "",
  isArchived = false,
  isLoopConfigured = false,
  isSpawned = false,
  canArchive = true,
  archiveBlockedReason = null,
  onRename,
  onDelete,
  onArchive,
  onMakeLoop,
  onMakeNonLoop,
  onFetchConversationPrompts, // async (session, workingDir) => menus:conversation prompts
  onSendPromptToConversation, // (session, prompt) when a context-menu prompt is clicked
  onCopyConversation, // optional: (session) => void — shows "Copy as Markdown" item
  onCopyConversationName, // optional: (session) => void — adds "Copy conversation name" to the Copy submenu
  onCopyConversationId, // optional: (session) => void — adds "Copy conversation ID" to the Copy submenu
  onCopyLastResponse, // optional: (session) => void — adds "Copy last response as Markdown" to the Copy submenu
  hasConversationMarkdown = true, // whether the conversation has copyable content (disables the full-Markdown submenu entry when false)
  hasLastResponseMarkdown = true, // whether there is a copyable last agent response (disables that submenu entry when false)
  flushCommand = "", // optional: when non-empty, shows "Flush context" item
  onFlushContext, // optional: (session) => void — invoked when "Flush context" is clicked
  onSetColor, // optional: (session, hexColor) => void — shows "Change color" submenu
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

  // Prompt group submenus (menus:conversation prompts), e.g. "Workflow".
  // Exposed separately so surfaces like the conversation Toolbar can render
  // these hierarchical groups inside a dedicated dropdown while promoting the
  // fixed actions (Copy, Flush, Loop, Archive, Delete) to top-level buttons.
  const promptGroupItems = useMemo(
    () =>
      onSendPromptToConversation && menuPrompts && menuPrompts.length > 0
        ? buildPromptGroupMenuItems(
            menuPrompts,
            (p, opts) => onSendPromptToConversation(session, p, opts),
            html`<${LightningIcon} />`,
          )
        : [],
    [menuPrompts, onSendPromptToConversation, session],
  );

  const contextMenuItems = useMemo(() => {
    return [
      // Submenu-bearing entries are grouped at the top: first the prompt
      // group submenus (menus:conversation prompts), e.g. "Workflow", then
      // "Change color". Flat actions follow below.
      ...promptGroupItems,
      // "Change color" — only shown when caller provides the callback
      ...(onSetColor
        ? [
            {
              label: "Change color",
              icon: html`<${PaletteIcon} />`,
              submenu: [
                ...CONVERSATION_COLORS.map((c) => ({
                  label: c.name,
                  icon: html`<span
                    class="w-4 h-4 rounded-full block border border-mitto-border-2"
                    style="background-color: ${c.hex}"
                  ></span>`,
                  trailing:
                    (session?.background_color || "").toLowerCase() ===
                    c.hex.toLowerCase()
                      ? html`<${CheckIcon} />`
                      : null,
                  onClick: () => onSetColor(session, c.hex),
                })),
                {
                  label: "No color",
                  icon: html`<${CircleIcon} />`,
                  trailing: !session?.background_color
                    ? html`<${CheckIcon} />`
                    : null,
                  onClick: () => onSetColor(session, ""),
                },
              ],
            },
          ]
        : []),
      {
        label: "Properties",
        icon: html`<${EditIcon} />`,
        onClick: () => onRename && onRename(session),
      },
      // "Copy" — only shown when caller provides at least one copy callback.
      // When any of the three sibling callbacks are supplied (mitto-a6v1),
      // this becomes a "Copy" entry with a 4-action submenu (same shape as
      // "Change color" above); otherwise it stays the original flat
      // "Copy as Markdown" entry so existing callers (none pass siblings
      // today) are unaffected.
      ...(onCopyConversation
        ? onCopyConversationName || onCopyConversationId || onCopyLastResponse
          ? [
              {
                label: "Copy",
                icon: html`<${CopyIcon} />`,
                submenu: [
                  {
                    label: "Copy conversation name",
                    disabled: !session?.name,
                    onClick: () =>
                      onCopyConversationName &&
                      onCopyConversationName(session),
                  },
                  {
                    label: "Copy conversation ID",
                    disabled: !session?.session_id,
                    onClick: () =>
                      onCopyConversationId && onCopyConversationId(session),
                  },
                  {
                    label: "Copy full contents as Markdown",
                    disabled: !hasConversationMarkdown,
                    onClick: () => onCopyConversation(session),
                  },
                  {
                    label: "Copy last response as Markdown",
                    disabled: !hasLastResponseMarkdown,
                    onClick: () =>
                      onCopyLastResponse && onCopyLastResponse(session),
                  },
                ],
              },
            ]
          : [
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
      // "Make loop" — only for conversations without a loop config yet,
      // non-spawned, non-archived. Gated on loop_configured (not
      // loop_enabled) so a paused/draft loop conversation is still
      // treated as already loop and does not offer "Make loop" again.
      ...(!isLoopConfigured && !isSpawned && !isArchived
        ? [
            {
              label: "Loop",
              icon: html`<${ClockIcon} />`,
              onClick: () => onMakeLoop && onMakeLoop(session),
            },
          ]
        : []),
      // "Make non-loop" — inverse: any conversation that has a loop
      // config (enabled OR paused/draft), non-spawned, can remove it.
      ...(isLoopConfigured && !isSpawned
        ? [
            {
              label: "Make non-loop",
              icon: html`<${MittoIcon} />`,
              onClick: () => onMakeNonLoop && onMakeNonLoop(session),
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
    promptGroupItems,
    session,
    onRename,
    isLoopConfigured,
    isSpawned,
    isArchived,
    onMakeLoop,
    onMakeNonLoop,
    canArchive,
    archiveBlockedReason,
    onArchive,
    onDelete,
    onCopyConversation,
    onCopyConversationName,
    onCopyConversationId,
    onCopyLastResponse,
    hasConversationMarkdown,
    hasLastResponseMarkdown,
    flushCommand,
    onFlushContext,
    onSetColor,
  ]);

  return {
    contextMenu,
    contextMenuItems,
    promptGroupItems,
    openContextMenuAt,
    closeContextMenu,
    handleContextMenu,
    handleMenuButtonClick,
  };
}
