// Mitto Web Interface - Shared Library Functions
// This file contains pure functions and utilities that can be tested independently

// Maximum number of messages to keep in browser memory per session.
// This prevents memory issues in very long sessions while keeping enough context.
// Set high enough to allow meaningful history loading while still protecting memory.
// Modern browsers can handle 1000+ DOM nodes efficiently with modern virtual scrolling techniques.
export const MAX_MESSAGES = 1000;

// Number of events to load initially when switching to a session.
// This provides a faster initial load while allowing users to load more history.
export const INITIAL_EVENTS_LIMIT = 50;

// Number of events to load in the fast initial phase (two-phase loading).
// This small batch loads quickly to show the user the latest messages immediately,
// then the remaining history is loaded in the background.
export const FAST_INITIAL_LOAD_LIMIT = 10;

// Message roles
export const ROLE_USER = "user";
export const ROLE_AGENT = "agent";
export const ROLE_THOUGHT = "thought";
export const ROLE_TOOL = "tool";
export const ROLE_ERROR = "error";
export const ROLE_SYSTEM = "system";

// =============================================================================
// User Message Markdown Rendering
// =============================================================================

/**
 * Maximum message length for Markdown rendering.
 * Messages longer than this will be displayed as plain text for performance.
 */
export const MAX_MARKDOWN_LENGTH = 10000;

/**
 * Checks if a text string likely contains Markdown formatting.
 * This is a heuristic check to avoid unnecessary Markdown processing for plain text.
 *
 * @param {string} text - The text to check
 * @returns {boolean} True if the text likely contains Markdown
 */
export function hasMarkdownContent(text) {
  if (!text || typeof text !== "string") {
    return false;
  }

  // Check for common Markdown patterns
  // Headers: # Header, ## Header, etc.
  if (/^#{1,6}\s+\S/m.test(text)) {
    return true;
  }

  // Bold: **text** or __text__
  if (/\*\*[^*]+\*\*/.test(text) || /__[^_]+__/.test(text)) {
    return true;
  }

  // Italic: *text* or _text_ (single word or short phrase without internal asterisks/underscores)
  // Match patterns like *word* or *some text* but not * spaced * asterisks
  if (/\*[^*\s][^*]*\*/.test(text) || /_[^_\s][^_]*_/.test(text)) {
    return true;
  }

  // Code: `code` or ```code blocks```
  if (/`[^`]+`/.test(text) || /```[\s\S]*?```/.test(text)) {
    return true;
  }

  // Links: [text](url) or [text][ref]
  if (/\[[^\]]+\]\([^)]+\)/.test(text) || /\[[^\]]+\]\[[^\]]*\]/.test(text)) {
    return true;
  }

  // Lists: - item, * item, + item, or 1. item
  if (/^[\s]*[-*+]\s+\S/m.test(text) || /^[\s]*\d+\.\s+\S/m.test(text)) {
    return true;
  }

  // Blockquotes: > text
  if (/^>\s+\S/m.test(text)) {
    return true;
  }

  // Horizontal rules: --- or *** or ___
  if (/^[-*_]{3,}\s*$/m.test(text)) {
    return true;
  }

  // Tables: | header | header |
  if (/\|[^|]+\|/.test(text) && /^[\s]*\|/.test(text)) {
    return true;
  }

  // Strikethrough: ~~text~~
  if (/~~[^~]+~~/.test(text)) {
    return true;
  }

  return false;
}

/**
 * Renders user message text as Markdown HTML.
 * Returns null if rendering should be skipped (plain text display).
 *
 * @param {string} text - The text to render
 * @returns {string|null} The rendered HTML, or null if plain text should be used
 */
export function renderUserMarkdown(text) {
  if (!text || typeof text !== "string") {
    return null;
  }

  // Skip rendering for very long messages (performance)
  if (text.length > MAX_MARKDOWN_LENGTH) {
    return null;
  }

  // Skip rendering if text doesn't appear to contain Markdown
  if (!hasMarkdownContent(text)) {
    return null;
  }

  // Check if marked and DOMPurify are available
  if (typeof window === "undefined" || !window.marked || !window.DOMPurify) {
    return null;
  }

  try {
    // Render Markdown to HTML
    const rawHtml = window.marked.parse(text);

    // Sanitize HTML to prevent XSS
    // See: https://github.com/inercia/mitto/issues/20 for data URL image support
    const cleanHtml = window.DOMPurify.sanitize(rawHtml, {
      USE_PROFILES: { html: true },
      ALLOWED_TAGS: [
        "p",
        "br",
        "strong",
        "em",
        "code",
        "pre",
        "blockquote",
        "ul",
        "ol",
        "li",
        "h1",
        "h2",
        "h3",
        "h4",
        "h5",
        "h6",
        "a",
        "img", // Allow img tags for inline/data URL images
        "table",
        "thead",
        "tbody",
        "tr",
        "th",
        "td",
        "hr",
        "del",
        "span",
      ],
      ALLOWED_ATTR: ["href", "title", "target", "rel", "class", "src", "alt"],
      ALLOW_DATA_ATTR: false,
      // Allow data: URIs only on img elements for specific image types
      // Note: SVG (image/svg+xml) is NOT allowed as it can contain scripts
      DATA_URI_TAGS: ["img"],
      ALLOWED_URI_REGEXP:
        /^(?:(?:https?|mailto|file):|data:image\/(?:png|jpeg|gif|webp);base64,|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
    });

    return cleanHtml;
  } catch (error) {
    // On any error, fall back to plain text
    console.warn("Failed to render user message Markdown:", error);
    return null;
  }
}

// =============================================================================
// Tool Title File Path Parsing
// =============================================================================

/**
 * Regular expression to match file paths in tool titles.
 * Matches patterns like:
 * - "Edit src/main.js" -> "src/main.js"
 * - "Read /home/user/file.txt" -> "/home/user/file.txt"
 * - "Write ./config.yaml" -> "./config.yaml"
 * - "View internal/web/server.go" -> "internal/web/server.go"
 */
const TOOL_TITLE_PATH_REGEX =
  /\b((?:\.{0,2}\/)?(?:[\w.-]+\/)*[\w.-]+\.[a-zA-Z0-9]+)\b/g;

/**
 * Common tool action prefixes that precede file paths.
 * Used to improve path detection accuracy.
 */
const TOOL_ACTION_PREFIXES = [
  "Edit",
  "Read",
  "Write",
  "View",
  "Create",
  "Delete",
  "Update",
  "Open",
  "Save",
  "Load",
  "Modify",
  "Remove",
  "Rename",
  "Copy",
  "Move",
];

/**
 * Parses a tool title and returns segments with file paths identified.
 * Each segment is either plain text or a file path that can be linked.
 *
 * @param {string} title - The tool title (e.g., "Edit src/main.js")
 * @returns {Array<{type: 'text'|'path', value: string}>} Array of segments
 */
export function parseToolTitlePaths(title) {
  if (!title || typeof title !== "string") {
    return [{ type: "text", value: title || "" }];
  }

  const segments = [];
  let lastIndex = 0;

  // Reset regex state
  TOOL_TITLE_PATH_REGEX.lastIndex = 0;

  let match;
  while ((match = TOOL_TITLE_PATH_REGEX.exec(title)) !== null) {
    const path = match[1];
    const matchStart = match.index;

    // Add text before this match
    if (matchStart > lastIndex) {
      segments.push({
        type: "text",
        value: title.slice(lastIndex, matchStart),
      });
    }

    // Check if this looks like a real file path:
    // 1. Has a file extension
    // 2. Contains at least one path separator OR starts with ./ or ../
    // 3. Doesn't look like a version number (e.g., "v1.0")
    const hasExtension = /\.[a-zA-Z0-9]+$/.test(path);
    const hasPathSeparator = path.includes("/");
    const looksLikeVersion = /^v?\d+\.\d+/.test(path);

    if (
      hasExtension &&
      (hasPathSeparator || path.startsWith("./") || path.startsWith("../")) &&
      !looksLikeVersion
    ) {
      segments.push({ type: "path", value: path });
    } else {
      // Not a file path, treat as text
      segments.push({ type: "text", value: path });
    }

    lastIndex = matchStart + match[0].length;
  }

  // Add remaining text after last match
  if (lastIndex < title.length) {
    segments.push({ type: "text", value: title.slice(lastIndex) });
  }

  // If no segments were created, return the whole title as text
  if (segments.length === 0) {
    return [{ type: "text", value: title }];
  }

  return segments;
}

// =============================================================================
// Session State Management
// =============================================================================

/**
 * Combines active and stored sessions, avoiding duplicates, and sorts by creation time (most recent first).
 * @param {Array} activeSessions - Currently active sessions in memory
 * @param {Array} storedSessions - Sessions loaded from storage
 * @returns {Array} Combined and sorted sessions
 */
// Global map to store working_dir values from API responses
// This is used as a fallback when React state updates haven't propagated yet
const globalWorkingDirMap = new Map();

// Function to update the global working_dir map
export function updateGlobalWorkingDir(sessionId, workingDir) {
  if (sessionId && workingDir) {
    globalWorkingDirMap.set(sessionId, workingDir);
  }
}

// Function to get working_dir from the global map
export function getGlobalWorkingDir(sessionId) {
  return globalWorkingDirMap.get(sessionId) || "";
}

export function computeAllSessions(activeSessions, storedSessions) {
  // Update global map from storedSessions
  storedSessions.forEach((s) => {
    if (s.session_id && s.working_dir) {
      globalWorkingDirMap.set(s.session_id, s.working_dir);
    }
  });

  // Create a map of stored sessions for quick lookup
  const storedMap = new Map(storedSessions.map((s) => [s.session_id, s]));

  // Merge properties from storedSessions into activeSessions
  // Properties like archived, name, pinned, isStreaming are shared between active and stored sessions
  // Flatten acp_server and working_dir from active session's info so grouping uses the correct ACP server
  const mergedActive = activeSessions.map((s) => {
    const stored = storedMap.get(s.session_id);
    const globalWd = globalWorkingDirMap.get(s.session_id);
    // Get working_dir from: stored session, global map, or existing value (including info)
    const workingDir =
      stored?.working_dir ||
      globalWd ||
      s.working_dir ||
      s.info?.working_dir ||
      "";

    // Flatten acp_server from info so session.acp_server is set for grouping/tooltips
    const acpServer = s.acp_server || s.info?.acp_server || stored?.acp_server || "";

    // Always merge stored properties (archived, name, pinned, isStreaming, periodic_enabled, next_scheduled_at, periodic_frequency) if stored session exists
    if (stored) {
      return {
        ...s,
        working_dir: workingDir || s.working_dir || s.info?.working_dir,
        acp_server: acpServer || stored.acp_server,
        // Merge these properties from stored session (they don't exist in active sessions)
        // For name: active session takes precedence if it has one, otherwise use stored
        archived: stored.archived,
        name: s.name || stored.name,
        pinned: stored.pinned,
        // For isStreaming: active session takes precedence (it has real-time state),
        // but also consider stored session's value from global events
        isStreaming: s.isStreaming || stored.isStreaming || false,
        // Periodic enabled state (from stored session, updated via WebSocket)
        periodic_enabled: stored.periodic_enabled || false,
        // Progress bar: next run time and frequency (from API list or WebSocket periodic_updated)
        next_scheduled_at: s.next_scheduled_at ?? stored.next_scheduled_at ?? null,
        periodic_frequency: s.periodic_frequency ?? stored.periodic_frequency ?? null,
      };
    }

    // No stored session (e.g. newly created): always flatten so grouping uses correct workspace
    return { ...s, working_dir: workingDir || s.working_dir, acp_server: acpServer || s.acp_server };
  });

  const activeIds = new Set(mergedActive.map((s) => s.session_id));
  const filteredStored = storedSessions.filter(
    (s) => !activeIds.has(s.session_id),
  );
  const combined = [...mergedActive, ...filteredStored];
  // Sort by creation time (most recent first)
  combined.sort((a, b) => {
    return new Date(b.created_at) - new Date(a.created_at);
  });
  return combined;
}

/**
 * Helper function to convert stored events to messages for display.
 * @param {Array} events - Array of stored session events
 * @param {Object} options - Optional configuration
 * @param {boolean} options.reverseInput - If true, events are in reverse order (newest first) and will be reversed to chronological
 * @returns {Array} Array of message objects for rendering (always in chronological order, oldest first)
 */
export function convertEventsToMessages(events, options = {}) {
  const { reverseInput = false, sessionId = null, apiPrefix = "" } = options;

  // If events are in reverse order (newest first), reverse them to chronological order
  const orderedEvents = reverseInput ? [...events].reverse() : events;

  const messages = [];
  for (const event of orderedEvents) {
    const seq = event.seq || 0; // Include sequence number for tracking
    switch (event.type) {
      case "user_prompt": {
        const message = {
          role: ROLE_USER,
          text: event.data?.message || event.data?.text || "",
          timestamp: new Date(event.timestamp).getTime(),
          seq,
        };
        // Convert stored image references to full image objects with URLs
        // Image refs are stored as: [{id, name?, mime_type}]
        // UI expects: [{id, url, name, mimeType}]
        if (event.data?.images && event.data.images.length > 0 && sessionId) {
          message.images = event.data.images.map((img) => ({
            id: img.id,
            url: `${apiPrefix}/api/sessions/${sessionId}/images/${img.id}`,
            name: img.name || img.id,
            mimeType: img.mime_type,
          }));
        }
        messages.push(message);
        break;
      }
      case "agent_message":
        messages.push({
          role: ROLE_AGENT,
          html: event.data?.html || event.data?.text || "",
          complete: true,
          timestamp: new Date(event.timestamp).getTime(),
          seq,
        });
        break;
      case "agent_thought":
        messages.push({
          role: ROLE_THOUGHT,
          text: event.data?.text || "",
          complete: true,
          timestamp: new Date(event.timestamp).getTime(),
          seq,
        });
        break;
      case "tool_call":
        messages.push({
          role: ROLE_TOOL,
          id: event.data?.tool_call_id || event.data?.id,
          title: event.data?.title,
          status: event.data?.status || "completed",
          timestamp: new Date(event.timestamp).getTime(),
          seq,
        });
        break;
      case "error":
        messages.push({
          role: ROLE_ERROR,
          text: event.data?.message || "",
          timestamp: new Date(event.timestamp).getTime(),
          seq,
        });
        break;
    }
  }
  return messages;
}

/**
 * Default options for coalesceAgentMessages.
 * These can be overridden by passing an options object.
 */
export const COALESCE_DEFAULTS = {
  /**
   * EXPERIMENTAL: When true, horizontal rules (<hr/>) act as coalescing breaks.
   * This creates visual separation between sections, improving readability
   * when the agent sends multiple logical sections in rapid succession.
   *
   * Set to true to enable this experiment.
   */
  hrBreaksCoalescing: true, // EXPERIMENT ENABLED
};

/**
 * Check if a message contains only an <hr/> element.
 * Used to detect horizontal rules that should act as visual breaks.
 *
 * @param {string} html - The HTML content to check
 * @returns {boolean} True if the content is only an <hr/> element
 */
function isHrOnlyMessage(html) {
  if (!html) return false;
  // Match <hr>, <hr/>, <hr /> with optional whitespace
  return /^\s*<hr\s*\/?>\s*$/i.test(html.trim());
}

/**
 * Coalesce consecutive agent messages into single messages for display.
 *
 * The backend's MarkdownBuffer flushes content at semantic boundaries (paragraphs,
 * headers, horizontal rules, etc.), creating separate events with different sequence
 * numbers. This is correct for tracking and sync, but creates a poor visual experience
 * where each flush appears as a separate message bubble.
 *
 * This function combines consecutive agent messages into single messages for rendering,
 * while preserving the original messages for internal tracking.
 *
 * @param {Array} messages - Array of message objects (in chronological order)
 * @param {Object} options - Optional configuration
 * @param {boolean} options.hrBreaksCoalescing - When true, <hr/> elements break coalescing
 * @returns {Array} Array of messages with consecutive agent messages coalesced
 *
 * @example
 * // Default: Input: [agent(seq:1, "Hello"), agent(seq:2, "<hr/>"), agent(seq:3, "World")]
 * // Output: [agent(seq:1, "Hello<hr/>World", coalescedSeqs:[1,2,3])]
 *
 * @example
 * // With hrBreaksCoalescing: Input: [agent(seq:1, "Hello"), agent(seq:2, "<hr/>"), agent(seq:3, "World")]
 * // Output: [agent(seq:1, "Hello"), agent(seq:3, "World")]
 * // Note: The <hr/> message is dropped as a visual separator
 */
export function coalesceAgentMessages(messages, options = {}) {
  if (!messages || messages.length === 0) {
    return messages;
  }

  const { hrBreaksCoalescing = COALESCE_DEFAULTS.hrBreaksCoalescing } = options;

  const result = [];
  let currentCoalesced = null;

  for (const msg of messages) {
    // Check if this is an HR-only message that should break coalescing
    const isHrBreak =
      hrBreaksCoalescing &&
      msg.role === ROLE_AGENT &&
      isHrOnlyMessage(msg.html);

    // Non-agent messages always break coalescing
    // HR-only messages break coalescing when hrBreaksCoalescing is enabled
    if (msg.role !== ROLE_AGENT || isHrBreak) {
      // Flush any pending coalesced message
      if (currentCoalesced) {
        result.push(currentCoalesced);
        currentCoalesced = null;
      }

      // HR-only messages are dropped (they served as visual separators)
      // Non-agent messages are always included
      if (!isHrBreak) {
        result.push(msg);
      }
      continue;
    }

    // Agent message - check if we should coalesce
    if (currentCoalesced) {
      // Append to existing coalesced message
      currentCoalesced = {
        ...currentCoalesced,
        html: (currentCoalesced.html || "") + (msg.html || ""),
        // Keep the latest timestamp and complete status
        timestamp: msg.timestamp,
        complete: msg.complete,
        // Track all coalesced sequence numbers (for debugging)
        coalescedSeqs: [...(currentCoalesced.coalescedSeqs || []), msg.seq],
        // Use the highest seq for deduplication purposes
        maxSeq: Math.max(
          currentCoalesced.maxSeq || currentCoalesced.seq,
          msg.seq,
        ),
      };
    } else {
      // Start a new coalesced message
      currentCoalesced = {
        ...msg,
        coalescedSeqs: [msg.seq],
        maxSeq: msg.seq,
      };
    }
  }

  // Flush any remaining coalesced message
  if (currentCoalesced) {
    result.push(currentCoalesced);
  }

  return result;
}

/**
 * Get the minimum sequence number from an array of events.
 * @param {Array} events - Array of events with seq property
 * @returns {number} The minimum sequence number, or 0 if no events
 */
export function getMinSeq(events) {
  if (!events || events.length === 0) return 0;
  return Math.min(...events.map((e) => e.seq || 0));
}

/**
 * Get the maximum sequence number from an array of events.
 * @param {Array} events - Array of events with seq property
 * @returns {number} The maximum sequence number, or 0 if no events
 */
export function getMaxSeq(events) {
  if (!events || events.length === 0) return 0;
  return Math.max(...events.map((e) => e.seq || 0));
}

/**
 * Detects if the client has stale state compared to the server.
 *
 * This happens when the client's lastLoadedSeq is higher than the server's lastSeq,
 * indicating the client has cached state from a different server instance or after
 * a server restart. In this case, the server is always right and the client should
 * discard its state and use the server's data.
 *
 * Common scenarios where this occurs:
 * - Mobile client reconnects after phone was sleeping
 * - Server restarted while client was offline
 * - Browser tab restored with cached state
 * - Network disconnection and reconnection
 *
 * @param {number} clientLastSeq - The client's last loaded sequence number
 * @param {number} serverLastSeq - The server's last sequence number from events_loaded
 * @returns {boolean} True if client has stale state and should defer to server
 */
export function isStaleClientState(clientLastSeq, serverLastSeq) {
  // Both must be positive numbers for a valid comparison
  if (!clientLastSeq || clientLastSeq <= 0) return false;
  if (!serverLastSeq || serverLastSeq <= 0) return false;

  // Client is stale if it thinks it has seen more than the server has
  return clientLastSeq > serverLastSeq;
}

/**
 * Create a content hash for a message for deduplication.
 * Handles different message types appropriately:
 * - user/agent/thought/error: use text or html content
 * - tool: use id and title (since they don't have text/html)
 *
 * @param {Object} message - The message to hash
 * @returns {string} A hash string for deduplication
 */
export function getMessageHash(message) {
  const role = message.role || "unknown";

  if (role === ROLE_TOOL) {
    // Tool messages use id and title, not text/html
    // Include both to handle multiple tool calls with the same title
    const id = message.id || "";
    const title = message.title || "";
    return `${role}:${id}:${title}`;
  }

  // For other message types, use text or html content (first 200 chars)
  const content = (message.text || message.html || "").substring(0, 200);
  return `${role}:${content}`;
}

/**
 * Merge new messages from sync into existing messages, maintaining correct order.
 * This handles the case where:
 * 1. Existing messages may have been received via streaming (with seq from backend)
 * 2. New messages come from sync (with seq and server timestamp)
 *
 * The merge strategy:
 * 1. Deduplicate by seq (if both have seq) or by content hash (fallback)
 * 2. Combine all messages and sort by seq for correct ordering
 * 3. Messages without seq are kept in their relative position
 *
 * Sorting by seq is now safe because:
 * - All events (including tool calls) are buffered and persisted together
 * - Seq is assigned when the event is received from ACP, not at persistence time
 * - Streaming messages now include seq, so they match their persisted counterparts
 *
 * The sync messages themselves are already in chronological order from the backend
 * (they're read from events.jsonl which is append-only).
 *
 * @param {Array} existingMessages - Messages currently in UI
 * @param {Array} newMessages - Messages from session_sync
 * @returns {Array} Merged and ordered messages
 */
export function mergeMessagesWithSync(existingMessages, newMessages) {
  if (!existingMessages || existingMessages.length === 0) {
    return newMessages || [];
  }
  if (!newMessages || newMessages.length === 0) {
    return existingMessages;
  }

  // Create a map of existing messages by seq for fast lookup
  const existingBySeq = new Map();
  const existingHashes = new Set();
  for (const m of existingMessages) {
    if (m.seq) {
      existingBySeq.set(m.seq, m);
    }
    existingHashes.add(getMessageHash(m));
  }

  // M2 fix: Filter out duplicates from new messages, preferring complete messages
  // When a message with matching seq is found, compare content to keep the longer/complete version
  const filteredNewMessages = [];
  const seqsToUpdate = new Map(); // seq -> newMessage (for messages that should replace existing)

  for (const m of newMessages) {
    // If both have seq, check if we should prefer the new message
    if (m.seq && existingBySeq.has(m.seq)) {
      const existing = existingBySeq.get(m.seq);

      // Prefer the more complete message:
      // For agent messages, compare html length
      // For thoughts, compare text length
      // For other types, prefer the new one if existing is marked incomplete
      let shouldReplace = false;
      if (m.role === ROLE_AGENT || existing.role === ROLE_AGENT) {
        const newLen = (m.html || "").length;
        const existingLen = (existing.html || "").length;
        // Prefer complete message, or longer message if both have same complete status
        shouldReplace =
          (m.complete && !existing.complete) ||
          (m.complete === existing.complete && newLen > existingLen);
      } else if (m.role === ROLE_THOUGHT || existing.role === ROLE_THOUGHT) {
        const newLen = (m.text || "").length;
        const existingLen = (existing.text || "").length;
        shouldReplace =
          (m.complete && !existing.complete) ||
          (m.complete === existing.complete && newLen > existingLen);
      } else {
        // For tools and other types, prefer complete over incomplete
        shouldReplace = m.complete && !existing.complete;
      }

      if (shouldReplace) {
        seqsToUpdate.set(m.seq, m);
      }
      // Either way, don't add to filteredNewMessages (we'll handle updates separately)
      continue;
    }

    // Fall back to content hash for messages without seq
    const hash = getMessageHash(m);
    if (!existingHashes.has(hash)) {
      filteredNewMessages.push(m);
    }
  }

  // Apply replacements for messages where new version is more complete
  let allMessages = existingMessages.map((m) => {
    if (m.seq && seqsToUpdate.has(m.seq)) {
      return seqsToUpdate.get(m.seq);
    }
    return m;
  });

  // Add filtered new messages
  if (filteredNewMessages.length === 0 && seqsToUpdate.size === 0) {
    return existingMessages;
  }

  if (filteredNewMessages.length > 0) {
    allMessages = [...allMessages, ...filteredNewMessages];
  }

  // Sort by seq if available
  // Messages with seq are sorted by seq
  // Messages without seq maintain their relative position
  allMessages.sort((a, b) => {
    // Both have seq - sort by seq
    if (a.seq && b.seq) {
      return a.seq - b.seq;
    }
    // Only one has seq - the one with seq comes in its correct position
    // The one without seq stays where it was (use timestamp as fallback)
    if (a.seq && !b.seq) {
      // a has seq, b doesn't - compare a.seq with b's approximate position
      return 0; // Keep relative order
    }
    if (!a.seq && b.seq) {
      // b has seq, a doesn't
      return 0; // Keep relative order
    }
    // Neither has seq - use timestamp
    return (a.timestamp || 0) - (b.timestamp || 0);
  });

  return allMessages;
}

/**
 * Safely parse JSON with error handling.
 * @param {string} jsonString - The JSON string to parse
 * @returns {{ data: any, error: Error|null }} Parsed data or error
 */
export function safeJsonParse(jsonString) {
  try {
    return { data: JSON.parse(jsonString), error: null };
  } catch (error) {
    return { data: null, error };
  }
}

/**
 * Create a new session state object.
 * @param {string} sessionId - The session ID
 * @param {Object} options - Session options
 * @returns {Object} New session state
 */
export function createSessionState(sessionId, options = {}) {
  const {
    name,
    acpServer,
    createdAt,
    messages = [],
    status = "active",
  } = options;
  return {
    messages,
    info: {
      session_id: sessionId,
      name: name || "New conversation",
      acp_server: acpServer || "",
      created_at: createdAt || new Date().toISOString(),
      status,
    },
  };
}

/**
 * Limit an array to the last N items.
 * @param {Array} arr - Array to limit
 * @param {number} maxItems - Maximum number of items to keep (default: MAX_MESSAGES)
 * @returns {Array} Array with at most maxItems elements (the last ones)
 */
export function limitMessages(arr, maxItems = MAX_MESSAGES) {
  if (!arr || arr.length <= maxItems) {
    return arr;
  }
  return arr.slice(-maxItems);
}

/**
 * Add a message to a session's message list immutably.
 * Automatically limits messages to MAX_MESSAGES to prevent memory issues.
 * @param {Object} session - Current session state
 * @param {Object} message - Message to add
 * @returns {Object} New session state with message added
 */
export function addMessageToSessionState(session, message) {
  if (!session) {
    session = { messages: [], info: {} };
  }
  const newMessages = limitMessages([...session.messages, message]);
  return {
    ...session,
    messages: newMessages,
  };
}

/**
 * Update the last message in a session immutably.
 * @param {Object} session - Current session state
 * @param {Function} updater - Function to update the last message
 * @returns {Object} New session state with updated last message
 */
export function updateLastMessageInSession(session, updater) {
  if (!session || session.messages.length === 0) {
    return session;
  }
  const messages = [...session.messages];
  const lastIdx = messages.length - 1;
  messages[lastIdx] = updater(messages[lastIdx]);
  return { ...session, messages };
}

/**
 * Remove a session from sessions state and determine next active session.
 * @param {Object} sessions - Current sessions state { sessionId: sessionData }
 * @param {string} sessionIdToRemove - Session ID to remove
 * @param {string} currentActiveSessionId - Currently active session ID
 * @returns {{ newSessions: Object, nextActiveSessionId: string|null, needsNewSession: boolean }}
 */
export function removeSessionFromState(
  sessions,
  sessionIdToRemove,
  currentActiveSessionId,
) {
  const { [sessionIdToRemove]: removed, ...rest } = sessions;

  let nextActiveSessionId = currentActiveSessionId;
  let needsNewSession = false;

  if (sessionIdToRemove === currentActiveSessionId) {
    const remainingIds = Object.keys(rest);
    if (remainingIds.length > 0) {
      nextActiveSessionId = remainingIds[0];
    } else {
      nextActiveSessionId = null;
      needsNewSession = true;
    }
  }

  return { newSessions: rest, nextActiveSessionId, needsNewSession };
}

// =============================================================================
// Workspace Visual Identification
// =============================================================================

/**
 * Extracts the basename from a directory path.
 * @param {string} path - Full directory path
 * @returns {string} The basename (last component) of the path
 */
export function getBasename(path) {
  if (!path) return "";
  // Handle both Unix and Windows paths
  const parts = path
    .replace(/\\/g, "/")
    .split("/")
    .filter((p) => p);
  return parts[parts.length - 1] || "";
}

/**
 * Generates a three-letter abbreviation from a directory basename.
 * Algorithm:
 * 1. If name has hyphens/underscores, take first letter of each word (up to 3)
 * 2. If name is camelCase, take first letter of each word (up to 3)
 * 3. Otherwise, take first 3 consonants, or first 3 characters
 *
 * @param {string} path - Full directory path or basename
 * @returns {string} Three-letter uppercase abbreviation
 */
export function getWorkspaceAbbreviation(path) {
  const basename = getBasename(path);
  if (!basename) return "???";

  // Split by common separators (hyphen, underscore, space)
  const separatorParts = basename.split(/[-_\s]+/).filter((p) => p);

  if (separatorParts.length >= 2) {
    // Take first letter of each part (up to 3)
    const abbr = separatorParts
      .slice(0, 3)
      .map((p) => p[0])
      .join("")
      .toUpperCase();
    // Pad with last part's letters if needed
    if (abbr.length < 3 && separatorParts.length > 0) {
      const lastPart = separatorParts[separatorParts.length - 1];
      return (abbr + lastPart.slice(1, 4 - abbr.length))
        .toUpperCase()
        .slice(0, 3);
    }
    return abbr.slice(0, 3);
  }

  // Check for camelCase
  const camelParts = basename.split(/(?=[A-Z])/).filter((p) => p);
  if (camelParts.length >= 2) {
    const abbr = camelParts
      .slice(0, 3)
      .map((p) => p[0])
      .join("")
      .toUpperCase();
    if (abbr.length < 3 && camelParts.length > 0) {
      const lastPart = camelParts[camelParts.length - 1];
      return (abbr + lastPart.slice(1, 4 - abbr.length))
        .toUpperCase()
        .slice(0, 3);
    }
    return abbr.slice(0, 3);
  }

  // Single word: take first 3 consonants, or first 3 characters
  const consonants = basename.replace(/[aeiouAEIOU]/g, "");
  if (consonants.length >= 3) {
    return consonants.slice(0, 3).toUpperCase();
  }

  // Fall back to first 3 characters
  return basename.slice(0, 3).toUpperCase();
}

/**
 * Simple hash function for strings.
 * @param {string} str - String to hash
 * @returns {number} Hash value (positive integer)
 */
function hashString(str) {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    const char = str.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32-bit integer
  }
  return Math.abs(hash);
}

/**
 * Converts a hex color to RGB components.
 * @param {string} hex - Hex color (e.g., "#ff5500" or "ff5500")
 * @returns {object|null} { r, g, b } or null if invalid
 */
export function hexToRgb(hex) {
  if (!hex) return null;
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  if (!result) return null;
  return {
    r: parseInt(result[1], 16),
    g: parseInt(result[2], 16),
    b: parseInt(result[3], 16),
  };
}

/**
 * Calculates relative luminance of a color for accessibility.
 * @param {number} r - Red (0-255)
 * @param {number} g - Green (0-255)
 * @param {number} b - Blue (0-255)
 * @returns {number} Relative luminance (0-1)
 */
export function getLuminance(r, g, b) {
  const [rs, gs, bs] = [r, g, b].map((c) => {
    c = c / 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
}

/**
 * Generates color info from a hex color string.
 * @param {string} hexColor - Hex color (e.g., "#ff5500")
 * @returns {object|null} Color object with { background, text, border } or null if invalid
 */
export function getColorFromHex(hexColor) {
  const rgb = hexToRgb(hexColor);
  if (!rgb) return null;

  const luminance = getLuminance(rgb.r, rgb.g, rgb.b);
  // Use white text for dark backgrounds (luminance < 0.4), dark text otherwise
  const text = luminance < 0.4 ? "white" : "rgb(30, 30, 30)";

  // Border is a darker version of the background
  const darkenFactor = 0.7;
  const borderR = Math.round(rgb.r * darkenFactor);
  const borderG = Math.round(rgb.g * darkenFactor);
  const borderB = Math.round(rgb.b * darkenFactor);
  const border = `rgb(${borderR}, ${borderG}, ${borderB})`;

  return {
    background: hexColor,
    text,
    border,
  };
}

/**
 * Converts HSL values to a hex color string.
 * @param {number} h - Hue (0-360)
 * @param {number} s - Saturation (0-100)
 * @param {number} l - Lightness (0-100)
 * @returns {string} Hex color (e.g., "#ff5500")
 */
export function hslToHex(h, s, l) {
  s /= 100;
  l /= 100;
  const a = s * Math.min(l, 1 - l);
  const f = (n) => {
    const k = (n + h / 30) % 12;
    const color = l - a * Math.max(Math.min(k - 3, 9 - k, 1), -1);
    return Math.round(255 * color)
      .toString(16)
      .padStart(2, "0");
  };
  return `#${f(0)}${f(8)}${f(4)}`;
}

/**
 * Generates a deterministic color for a workspace based on its path.
 * Uses HSL color space for better control over saturation and lightness.
 *
 * @param {string} path - Full directory path
 * @returns {object} Color object with { hue, background, backgroundHex, text, border }
 */
export function getWorkspaceColor(path) {
  const basename = getBasename(path);
  if (!basename) {
    return {
      hue: 0,
      background: "rgb(100, 100, 100)",
      backgroundHex: "#646464",
      text: "white",
      border: "rgb(120, 120, 120)",
    };
  }

  // Generate hue from hash (0-360)
  const hash = hashString(basename);
  const hue = hash % 360;

  // Use fixed saturation and lightness for consistent appearance
  // Saturation: 65% for vibrant but not overwhelming colors
  // Lightness: 45% for good contrast with white text
  const saturation = 65;
  const lightness = 45;

  // Generate the background color
  const background = `hsl(${hue}, ${saturation}%, ${lightness}%)`;
  const backgroundHex = hslToHex(hue, saturation, lightness);

  // For text, use white for dark backgrounds (lightness < 55%)
  // and dark for light backgrounds
  const text = lightness < 55 ? "white" : "rgb(30, 30, 30)";

  // Border is slightly darker version
  const border = `hsl(${hue}, ${saturation}%, ${Math.max(lightness - 10, 20)}%)`;

  return { hue, background, backgroundHex, text, border };
}

/**
 * Gets complete workspace visual info (abbreviation and color).
 * @param {string} path - Full directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} customCode - Optional custom three-letter code
 * @param {string} customName - Optional custom friendly name
 * @returns {object} { abbreviation, color: { background, text, border }, basename, displayName }
 */
export function getWorkspaceVisualInfo(
  path,
  customColor = null,
  customCode = null,
  customName = null,
) {
  // If a custom color is provided and valid, use it
  const color = customColor ? getColorFromHex(customColor) : null;
  const basename = getBasename(path);

  return {
    // Use custom code if provided and valid (3 characters), otherwise calculate
    abbreviation:
      customCode && customCode.length === 3
        ? customCode.toUpperCase()
        : getWorkspaceAbbreviation(path),
    color: color || getWorkspaceColor(path),
    basename,
    // Use custom name if provided, otherwise fall back to basename
    displayName: customName || basename,
  };
}

// Credential validation constants
export const MIN_USERNAME_LENGTH = 3;
export const MAX_USERNAME_LENGTH = 64;
export const MIN_PASSWORD_LENGTH = 8;
export const MAX_PASSWORD_LENGTH = 128;

// Common weak passwords that should be rejected
const COMMON_WEAK_PASSWORDS = new Set([
  "password",
  "password1",
  "password12",
  "12345678",
  "123456789",
  "qwerty123",
  "admin123",
  "letmein",
  "welcome",
  "monkey123",
  "dragon123",
  "master123",
  "changeme",
]);

/**
 * Validates a username for external access authentication.
 * @param {string} username - Username to validate
 * @returns {string} Error message if invalid, empty string if valid
 */
export function validateUsername(username) {
  const trimmed = (username || "").trim();

  if (!trimmed) {
    return "Username is required";
  }

  if (trimmed.length < MIN_USERNAME_LENGTH) {
    return "Username must be at least 3 characters";
  }

  if (trimmed.length > MAX_USERNAME_LENGTH) {
    return "Username must be at most 64 characters";
  }

  // Username should start with a letter or number
  if (!/^[a-zA-Z0-9]/.test(trimmed)) {
    return "Username must start with a letter or number";
  }

  // Check for valid characters (alphanumeric, underscore, hyphen, dot)
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(trimmed)) {
    return "Username can only contain letters, numbers, underscore, hyphen, and dot";
  }

  return "";
}

/**
 * Validates a password for external access authentication.
 * @param {string} password - Password to validate
 * @returns {string} Error message if invalid, empty string if valid
 */
export function validatePassword(password) {
  if (!password) {
    return "Password is required";
  }

  if (password.length < MIN_PASSWORD_LENGTH) {
    return "Password must be at least 8 characters";
  }

  if (password.length > MAX_PASSWORD_LENGTH) {
    return "Password must be at most 128 characters";
  }

  // Check against common weak passwords (case-insensitive)
  if (COMMON_WEAK_PASSWORDS.has(password.toLowerCase())) {
    return "Password is too common. Please choose a stronger password";
  }

  // Check for minimum complexity: at least one letter and one number or special char
  const hasLetter = /[a-zA-Z]/.test(password);
  const hasNonLetter = /[^a-zA-Z\s]/.test(password);

  if (!hasLetter || !hasNonLetter) {
    return "Password must contain at least one letter and one number or special character";
  }

  return "";
}

/**
 * Validates both username and password.
 * @param {string} username - Username to validate
 * @param {string} password - Password to validate
 * @returns {string} First error message found, or empty string if both valid
 */
export function validateCredentials(username, password) {
  const usernameError = validateUsername(username);
  if (usernameError) return usernameError;
  return validatePassword(password);
}

// =============================================================================
// Pending Prompts Queue (for reliable message delivery on mobile)
// =============================================================================

const PENDING_PROMPTS_KEY = "mitto_pending_prompts";
const PROMPT_EXPIRY_MS = 5 * 60 * 1000; // 5 minutes - prompts older than this are considered stale

/**
 * Generates a unique prompt ID for tracking delivery.
 * @returns {string} A unique prompt ID
 */
export function generatePromptId() {
  return `prompt_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
}

/**
 * Saves a pending prompt to localStorage before sending.
 * @param {string} sessionId - The session ID
 * @param {string} promptId - The unique prompt ID
 * @param {string} message - The message text
 * @param {Array} imageIds - Optional array of image IDs
 * @param {Array} fileIds - Optional array of file IDs
 */
export function savePendingPrompt(
  sessionId,
  promptId,
  message,
  imageIds = [],
  fileIds = [],
) {
  try {
    const pending = getPendingPrompts();
    pending[promptId] = {
      sessionId,
      message,
      imageIds,
      fileIds,
      timestamp: Date.now(),
    };
    localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
  } catch (err) {
    console.warn("Failed to save pending prompt:", err);
  }
}

/**
 * Removes a pending prompt after receiving acknowledgment.
 * @param {string} promptId - The prompt ID to remove
 */
export function removePendingPrompt(promptId) {
  try {
    const pending = getPendingPrompts();
    delete pending[promptId];
    localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
  } catch (err) {
    console.warn("Failed to remove pending prompt:", err);
  }
}

/**
 * Gets all pending prompts from localStorage.
 * @returns {Object} Map of promptId -> { sessionId, message, imageIds, timestamp }
 */
export function getPendingPrompts() {
  try {
    const data = localStorage.getItem(PENDING_PROMPTS_KEY);
    if (!data) return {};
    return JSON.parse(data) || {};
  } catch (err) {
    console.warn("Failed to get pending prompts:", err);
    return {};
  }
}

/**
 * Gets pending prompts for a specific session that haven't expired.
 * @param {string} sessionId - The session ID
 * @returns {Array} Array of { promptId, message, imageIds, timestamp }
 */
export function getPendingPromptsForSession(sessionId) {
  const pending = getPendingPrompts();
  const now = Date.now();
  const results = [];

  for (const [promptId, data] of Object.entries(pending)) {
    if (
      data.sessionId === sessionId &&
      now - data.timestamp < PROMPT_EXPIRY_MS
    ) {
      results.push({ promptId, ...data });
    }
  }

  // Sort by timestamp (oldest first for retry order)
  results.sort((a, b) => a.timestamp - b.timestamp);
  return results;
}

/**
 * Cleans up expired pending prompts.
 */
export function cleanupExpiredPrompts() {
  try {
    const pending = getPendingPrompts();
    const now = Date.now();
    let changed = false;

    for (const [promptId, data] of Object.entries(pending)) {
      if (now - data.timestamp >= PROMPT_EXPIRY_MS) {
        delete pending[promptId];
        changed = true;
      }
    }

    if (changed) {
      localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
    }
  } catch (err) {
    console.warn("Failed to cleanup expired prompts:", err);
  }
}

/**
 * Clears pending prompts that have been persisted in loaded events.
 * This is called when events are loaded on reconnect to prevent duplicate sends.
 * @param {Array} events - Array of loaded events from the server
 */
export function clearPendingPromptsFromEvents(events) {
  if (!events || events.length === 0) return;

  try {
    const pending = getPendingPrompts();
    if (Object.keys(pending).length === 0) return;

    let changed = false;
    for (const event of events) {
      if (event.type === "user_prompt" && event.data?.prompt_id) {
        const promptId = event.data.prompt_id;
        if (pending[promptId]) {
          console.log(
            `Clearing pending prompt ${promptId} - found in loaded events`,
          );
          delete pending[promptId];
          changed = true;
        }
      }
    }

    if (changed) {
      localStorage.setItem(PENDING_PROMPTS_KEY, JSON.stringify(pending));
    }
  } catch (err) {
    console.warn("Failed to clear pending prompts from events:", err);
  }
}

// =============================================================================
// URL Detection and Linkification
// =============================================================================

/**
 * Regular expression to detect URLs in text.
 * Matches:
 * - http:// and https:// URLs
 * - ftp:// URLs
 * - mailto: links
 * - file:// URLs (for compatibility)
 *
 * The regex captures the full URL including path, query params, and fragments.
 * It stops at whitespace, quotes, parentheses (unless balanced), and common punctuation at the end.
 */
const URL_PATTERN =
  /\b((?:https?:\/\/|ftp:\/\/|file:\/\/|mailto:)[^\s<>"\[\]{}|\\^`]+?)(?=[.,;:!?)]*(?:\s|$)|$)/gi;

/**
 * Escapes HTML special characters in a string.
 * @param {string} text - The text to escape
 * @returns {string} The escaped text
 */
function escapeHtmlForLinkify(text) {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

/**
 * Converts URLs in plain text to clickable anchor tags.
 * This function is designed for plain text content (not HTML).
 *
 * @param {string} text - Plain text that may contain URLs
 * @returns {string} HTML string with URLs converted to anchor tags
 *
 * @example
 * linkifyUrls("Check out https://example.com for more info")
 * // Returns: 'Check out <a href="https://example.com" target="_blank" rel="noopener noreferrer" class="url-link">https://example.com</a> for more info'
 */
export function linkifyUrls(text) {
  if (!text || typeof text !== "string") {
    return text || "";
  }

  // Split text by URL matches and rebuild with links
  const parts = [];
  let lastIndex = 0;
  let match;

  // Reset regex state
  URL_PATTERN.lastIndex = 0;

  while ((match = URL_PATTERN.exec(text)) !== null) {
    const url = match[1];
    const matchStart = match.index;

    // Add text before the URL (escaped for HTML)
    if (matchStart > lastIndex) {
      parts.push(escapeHtmlForLinkify(text.slice(lastIndex, matchStart)));
    }

    // Clean up trailing punctuation that might have been captured
    let cleanUrl = url;
    // Remove trailing punctuation that's unlikely to be part of the URL
    while (cleanUrl.length > 0 && /[.,;:!?)\]}>]$/.test(cleanUrl)) {
      const lastChar = cleanUrl.slice(-1);
      // Keep if it's a balanced closing bracket/paren
      if (
        lastChar === ")" &&
        (cleanUrl.match(/\(/g) || []).length >
          (cleanUrl.match(/\)/g) || []).length - 1
      ) {
        break;
      }
      if (
        lastChar === "]" &&
        (cleanUrl.match(/\[/g) || []).length >
          (cleanUrl.match(/]/g) || []).length - 1
      ) {
        break;
      }
      cleanUrl = cleanUrl.slice(0, -1);
    }

    // Calculate any trailing chars that were removed
    const trailingChars = url.slice(cleanUrl.length);

    // Determine link attributes based on scheme
    const isMailto = cleanUrl.toLowerCase().startsWith("mailto:");
    const attrs = isMailto
      ? `href="${escapeHtmlForLinkify(cleanUrl)}" class="url-link mailto-link"`
      : `href="${escapeHtmlForLinkify(cleanUrl)}" target="_blank" rel="noopener noreferrer" class="url-link"`;

    // Add the link
    parts.push(`<a ${attrs}>${escapeHtmlForLinkify(cleanUrl)}</a>`);

    // Add back any trailing punctuation as plain text
    if (trailingChars) {
      parts.push(escapeHtmlForLinkify(trailingChars));
    }

    lastIndex = match.index + url.length;
  }

  // Add remaining text after the last URL
  if (lastIndex < text.length) {
    parts.push(escapeHtmlForLinkify(text.slice(lastIndex)));
  }

  return parts.join("");
}

// =============================================================================
// Date/Time Formatting
// =============================================================================

/**
 * Format a date as a relative time ago (e.g., "3m ago", "2h ago", "1d ago").
 * @param {Date|string|number} date - The date to format
 * @returns {string} Human-readable relative time
 */
export function formatTimeAgo(date) {
  if (!date) return "";

  const target = date instanceof Date ? date : new Date(date);

  // Guard against invalid dates (e.g., non-ISO strings that produce NaN)
  if (Number.isNaN(target.getTime())) {
    return "";
  }

  const now = new Date();
  const diffMs = now.getTime() - target.getTime();

  // If in the future, show "now"
  if (diffMs <= 0) {
    return "now";
  }

  const diffSeconds = Math.floor(diffMs / 1000);
  const diffMinutes = Math.floor(diffSeconds / 60);
  const diffHours = Math.floor(diffMinutes / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffSeconds < 60) {
    return "just now";
  } else if (diffMinutes < 60) {
    return `${diffMinutes}m ago`;
  } else if (diffHours < 24) {
    return `${diffHours}h ago`;
  } else if (diffDays < 7) {
    return `${diffDays}d ago`;
  } else if (diffDays < 30) {
    const weeks = Math.floor(diffDays / 7);
    return `${weeks}w ago`;
  } else if (diffDays < 365) {
    const months = Math.floor(diffDays / 30);
    return `${months}mo ago`;
  } else {
    const years = Math.floor(diffDays / 365);
    return `${years}y ago`;
  }
}
