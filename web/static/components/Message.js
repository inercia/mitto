// Mitto Web Interface - Message Component
// Renders different types of messages (user, agent, thought, tool, error, system)

const { html } = window.preact;

import {
    ROLE_USER,
    ROLE_AGENT,
    ROLE_THOUGHT,
    ROLE_TOOL,
    ROLE_ERROR,
    ROLE_SYSTEM
} from '../lib.js';

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
                <div class="text-xs text-gray-500 bg-slate-800/50 px-3 py-1 rounded-full">
                    ${message.text}
                </div>
            </div>
        `;
    }

    // Tool call display
    if (isTool) {
        const isRunning = message.status === 'running';
        const isCompleted = message.status === 'completed';
        const isFailed = message.status === 'failed';

        const renderStatus = () => {
            if (isCompleted) {
                // Green checkmark icon for completed
                return html`
                    <svg class="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
                    </svg>
                `;
            }
            if (isFailed) {
                // Red X icon for failed
                return html`
                    <svg class="w-4 h-4 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                `;
            }
            if (isRunning) {
                // Spinning indicator for running
                return html`
                    <svg class="w-4 h-4 text-yellow-400 animate-spin" fill="none" viewBox="0 0 24 24">
                        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                `;
            }
            // Default: gray text for unknown status
            return html`<span class="text-xs text-gray-400">${message.status}</span>`;
        };

        return html`
            <div class="message-enter flex justify-center mb-1">
                <div class="text-sm text-gray-400 flex items-center gap-2 bg-slate-800/50 dark:bg-slate-800/50 px-3 py-1.5 rounded-lg">
                    <span class="text-yellow-500">🔧</span>
                    <span class="font-medium">${message.title}</span>
                    ${renderStatus()}
                </div>
            </div>
        `;
    }

    // Thought display (plain text)
    if (isThought) {
        const showCursor = isLast && isStreaming && !message.complete;
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-slate-800/50 text-gray-400 rounded-bl-sm border border-slate-700">
                    <div class="flex items-start gap-2">
                        <span class="text-purple-400 mt-0.5">💭</span>
                        <span class="italic ${showCursor ? 'streaming-cursor' : ''}">${message.text}</span>
                    </div>
                </div>
            </div>
        `;
    }

    // Error message
    if (isError) {
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-red-900/30 text-red-200 rounded-bl-sm border border-red-800">
                    <div class="flex items-start gap-2">
                        <span>❌</span>
                        <span>${message.text}</span>
                    </div>
                </div>
            </div>
        `;
    }

    // User message (plain text with optional images)
    if (isUser) {
        const hasImages = message.images && message.images.length > 0;
        return html`
            <div class="message-enter flex justify-end mb-3">
                <div class="max-w-[95%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-mitto-user text-mitto-user-text border border-mitto-user-border rounded-br-sm">
                    ${hasImages && html`
                        <div class="flex flex-wrap gap-2 mb-2">
                            ${message.images.map(img => html`
                                <div key=${img.id} class="relative group">
                                    <img
                                        src=${img.url}
                                        alt=${img.name || 'Attached image'}
                                        class="max-w-[200px] max-h-[150px] rounded-lg object-cover cursor-pointer hover:opacity-90 transition-opacity"
                                        onClick=${() => window.open(img.url, '_blank')}
                                    />
                                </div>
                            `)}
                        </div>
                    `}
                    <pre class="whitespace-pre-wrap font-sans text-sm m-0">${message.text}</pre>
                </div>
            </div>
        `;
    }

    // Agent message (HTML content)
    if (isAgent) {
        const showCursor = isLast && isStreaming && !message.complete;
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[95%] md:max-w-[75%] px-4 py-3 rounded-2xl bg-mitto-agent text-gray-100 rounded-bl-sm">
                    <div
                        class="markdown-content text-sm ${showCursor ? 'streaming-cursor' : ''}"
                        dangerouslySetInnerHTML=${{ __html: message.html || '' }}
                    />
                </div>
            </div>
        `;
    }

    return null;
}

