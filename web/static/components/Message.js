// Mitto Web Interface - Message Component
// Renders different types of messages (user, agent, thought, tool, error, system)

const { html, useMemo, useEffect, useRef } = window.preact;

import {
  ROLE_USER,
  ROLE_AGENT,
  ROLE_THOUGHT,
  ROLE_TOOL,
  ROLE_ERROR,
  ROLE_SYSTEM,
  renderUserMarkdown,
  parseToolTitlePaths,
  linkifyUrls,
} from "../lib.js";

import { openFileURL, isNativeApp } from "../utils/index.js";

/**
 * Message component - renders a single message in the chat
 * @param {Object} props
 * @param {Object} props.message - The message object
 * @param {boolean} props.isLast - Whether this is the last message
 * @param {boolean} props.isStreaming - Whether the session is currently streaming
 */
export function Message({ message, isLast, isStreaming }) {
  const isUser = message.role === ROLE_USER;
  const isAgent = message.role === ROLE_AGENT;
  const isThought = message.role === ROLE_THOUGHT;
  const isTool = message.role === ROLE_TOOL;
  const isError = message.role === ROLE_ERROR;
  const isSystem = message.role === ROLE_SYSTEM;

  // System messages
  if (isSystem) {
    return html`
      <div class="message-enter flex justify-center mb-3">
        <div
          class="text-xs text-gray-500 bg-slate-800/50 px-3 py-1 rounded-full"
        >
          ${message.text}
        </div>
      </div>
    `;
  }

  // Tool call display
  if (isTool) {
    const isRunning = message.status === "running";
    const isCompleted = message.status === "completed";
    const isFailed = message.status === "failed";

    const renderStatus = () => {
      if (isCompleted) {
        // Green checkmark icon for completed
        return html`
          <svg
            class="w-4 h-4 text-green-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M5 13l4 4L19 7"
            />
          </svg>
        `;
      }
      if (isFailed) {
        // Red X icon for failed
        return html`
          <svg
            class="w-4 h-4 text-red-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M6 18L18 6M6 6l12 12"
            />
          </svg>
        `;
      }
      if (isRunning) {
        // Spinning indicator for running
        return html`
          <svg
            class="w-4 h-4 text-yellow-400 animate-spin"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
        `;
      }
      // Default: gray text for unknown status
      return html`<span class="text-xs text-gray-400">${message.status}</span>`;
    };

    // Parse the title for file paths
    const titleSegments = useMemo(
      () => parseToolTitlePaths(message.title),
      [message.title],
    );

    // Render title with clickable file paths
    const renderTitle = () => {
      return titleSegments.map((segment, index) => {
        if (segment.type === "path") {
          // Render as a clickable link
          return html`<a
            key=${index}
            href="#"
            class="file-link hover:underline"
            onClick=${(e) => {
              e.preventDefault();
              // Get the current workspace from global state
              const workspace = window.mittoCurrentWorkspace || "";
              if (workspace) {
                // Build the file URL
                const absolutePath = segment.value.startsWith("/")
                  ? segment.value
                  : workspace + "/" + segment.value;
                openFileURL("file://" + absolutePath);
              }
            }}
            >${segment.value}</a
          >`;
        }
        // Plain text segment
        return html`<span key=${index}>${segment.value}</span>`;
      });
    };

    return html`
      <div class="message-enter flex justify-center mb-1">
        <div
          class="text-sm text-gray-400 flex items-center gap-2 bg-slate-800/50 dark:bg-slate-800/50 px-3 py-1.5 rounded-lg"
        >
          <span class="text-yellow-500">🔧</span>
          <span class="font-medium">${renderTitle()}</span>
          ${renderStatus()}
        </div>
      </div>
    `;
  }

  // Thought display (plain text with URL linkification)
  if (isThought) {
    const showCursor = isLast && isStreaming && !message.complete;
    // Linkify URLs in thought text
    const linkedText = useMemo(
      () => linkifyUrls(message.text),
      [message.text],
    );
    return html`
      <div class="message-enter flex justify-start mb-3">
        <div
          class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-slate-800/50 text-gray-400 rounded-bl-sm border border-slate-700"
        >
          <div class="flex items-start gap-2">
            <span class="text-purple-400 mt-0.5">💭</span>
            <span
              class="italic ${showCursor ? "streaming-cursor" : ""}"
              dangerouslySetInnerHTML=${{ __html: linkedText }}
            />
          </div>
        </div>
      </div>
    `;
  }

  // Error message (with URL linkification)
  if (isError) {
    // Linkify URLs in error text
    const linkedErrorText = useMemo(
      () => linkifyUrls(message.text),
      [message.text],
    );
    return html`
      <div class="message-enter flex justify-start mb-3">
        <div
          class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-red-900/30 text-red-200 rounded-bl-sm border border-red-800"
        >
          <div class="flex items-start gap-2">
            <span>❌</span>
            <span dangerouslySetInnerHTML=${{ __html: linkedErrorText }} />
          </div>
        </div>
      </div>
    `;
  }

  // User message (Markdown or plain text with optional images)
  if (isUser) {
    const hasImages = message.images && message.images.length > 0;
    // Try to render as Markdown, falling back to plain text if not applicable
    // useMemo ensures we only re-render Markdown when the message text changes
    const renderedHtml = useMemo(
      () => renderUserMarkdown(message.text),
      [message.text],
    );
    const useMarkdown = renderedHtml !== null;

    // For plain text, linkify URLs
    const linkedPlainText = useMemo(
      () => (useMarkdown ? null : linkifyUrls(message.text)),
      [message.text, useMarkdown],
    );

    return html`
      <div class="message-enter flex justify-end mb-3">
        <div
          class="max-w-[95%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-mitto-user text-mitto-user-text border border-mitto-user-border rounded-br-sm"
        >
          ${hasImages &&
          html`
            <div class="flex flex-wrap gap-2 mb-2">
              ${message.images.map(
                (img) => html`
                  <div key=${img.id} class="relative group">
                    <img
                      src=${img.url}
                      alt=${img.name || "Attached image"}
                      class="max-w-[200px] max-h-[150px] rounded-lg object-cover cursor-pointer hover:opacity-90 transition-opacity"
                      onClick=${() => window.open(img.url, "_blank")}
                    />
                  </div>
                `,
              )}
            </div>
          `}
          ${useMarkdown
            ? html`<div
                class="markdown-content markdown-content-user text-sm"
                dangerouslySetInnerHTML=${{ __html: renderedHtml }}
              />`
            : html`<pre
                class="whitespace-pre-wrap font-sans text-sm m-0"
                dangerouslySetInnerHTML=${{ __html: linkedPlainText }}
              />`}
        </div>
      </div>
    `;
  }

  // Agent message (HTML content)
  if (isAgent) {
    const showCursor = isLast && isStreaming && !message.complete;
    const agentMessageRef = useRef(null);

    // Trigger mermaid diagram rendering after the HTML is inserted.
    //
    // We render on every HTML update (not just when complete) because:
    // 1. The backend's MarkdownBuffer ensures mermaid blocks are only flushed
    //    when complete (opening and closing fences detected)
    // 2. The renderMermaidDiagrams function uses a content-based cache, so
    //    previously rendered diagrams are instantly restored from cache
    // 3. This provides better UX by showing diagrams as soon as they're ready
    //
    // The cache prevents re-rendering: when innerHTML is replaced during streaming,
    // the same diagram content will hit the cache and reuse the existing SVG.
    useEffect(() => {
      if (
        agentMessageRef.current &&
        typeof window.renderMermaidDiagrams === "function"
      ) {
        window.renderMermaidDiagrams(agentMessageRef.current);
      }
    }, [message.html]);

    return html`
      <div class="message-enter flex justify-start mb-3">
        <div
          class="max-w-[95%] md:max-w-[75%] px-4 py-3 rounded-2xl bg-mitto-agent text-gray-100 rounded-bl-sm"
        >
          <div
            ref=${agentMessageRef}
            class="markdown-content text-sm ${showCursor
              ? "streaming-cursor"
              : ""}"
            dangerouslySetInnerHTML=${{ __html: message.html || "" }}
          />
        </div>
      </div>
    `;
  }

  return null;
}
