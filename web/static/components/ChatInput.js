// Mitto Web Interface - Chat Input Component
// Handles message composition, image uploads, and predefined prompts

const { useState, useEffect, useRef, useCallback, html } = window.preact;

import { hasNativeImagePicker, pickImages } from '../utils/native.js';

/**
 * ChatInput component - message composition with image support
 * @param {Object} props
 * @param {Function} props.onSend - Callback when message is sent (text, images)
 * @param {Function} props.onCancel - Callback to cancel streaming
 * @param {boolean} props.disabled - Whether input is disabled
 * @param {boolean} props.isStreaming - Whether agent is currently streaming
 * @param {boolean} props.isReadOnly - Whether session is read-only
 * @param {Array} props.predefinedPrompts - Array of predefined prompts
 * @param {Object} props.inputRef - Ref for external focus control
 * @param {boolean} props.noSession - Whether there's no active session
 * @param {string} props.sessionId - Current session ID
 * @param {string} props.draft - Current draft text
 * @param {Function} props.onDraftChange - Callback when draft changes
 */
export function ChatInput({ onSend, onCancel, disabled, isStreaming, isReadOnly, predefinedPrompts = [], inputRef, noSession = false, sessionId, draft = '', onDraftChange }) {
    // Use the draft from parent state instead of local state
    const text = draft;
    const setText = useCallback((newText) => {
        if (onDraftChange) {
            onDraftChange(sessionId, newText);
        }
    }, [onDraftChange, sessionId]);

    const [showDropup, setShowDropup] = useState(false);
    // Track ongoing prompt improvements: { targetSessionId, abortController }
    const [improvingState, setImprovingState] = useState(null);
    const [improveError, setImproveError] = useState(null);
    const textareaRef = useRef(null);
    const dropupRef = useRef(null);

    // Image upload state
    const [pendingImages, setPendingImages] = useState([]); // Array of { id, url, name, mimeType, uploading }
    const [isDragOver, setIsDragOver] = useState(false);
    const [uploadError, setUploadError] = useState(null);
    const fileInputRef = useRef(null);

    // Track window width for responsive placeholder
    const [isSmallWindow, setIsSmallWindow] = useState(window.innerWidth < 640);
    useEffect(() => {
        const handleResize = () => setIsSmallWindow(window.innerWidth < 640);
        window.addEventListener('resize', handleResize);
        return () => window.removeEventListener('resize', handleResize);
    }, []);

    // Clear pending images when session changes
    useEffect(() => {
        setPendingImages([]);
        setUploadError(null);
    }, [sessionId]);

    // For backwards compatibility
    const isImproving = !!improvingState;

    // Determine if input should be fully disabled (no session or explicitly disabled)
    const isFullyDisabled = disabled || noSession;

    // Expose focus method via inputRef for native menu integration
    useEffect(() => {
        if (inputRef) {
            inputRef.current = {
                focus: () => {
                    if (textareaRef.current) {
                        textareaRef.current.focus();
                    }
                }
            };
        }
    }, [inputRef]);

    // Close dropup when clicking outside
    useEffect(() => {
        const handleClickOutside = (e) => {
            if (dropupRef.current && !dropupRef.current.contains(e.target)) {
                setShowDropup(false);
            }
        };
        if (showDropup) {
            document.addEventListener('mousedown', handleClickOutside);
            return () => document.removeEventListener('mousedown', handleClickOutside);
        }
    }, [showDropup]);

    // Adjust textarea height when draft changes (e.g., switching sessions)
    useEffect(() => {
        const textarea = textareaRef.current;
        if (textarea) {
            textarea.style.height = 'auto';
            textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
        }
    }, [text]);

    const handleSubmit = (e) => {
        e.preventDefault();
        // Allow sending if there's text OR images (or both)
        const hasContent = text.trim() || pendingImages.some(img => !img.uploading);
        if (hasContent && !disabled && !isReadOnly && !isStreaming) {
            // Filter out images that are still uploading
            const readyImages = pendingImages.filter(img => !img.uploading);
            onSend(text.trim(), readyImages);
            setText('');
            setPendingImages([]);
            if (textareaRef.current) {
                textareaRef.current.style.height = 'auto';
            }
        }
    };

    const handleKeyDown = (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSubmit(e);
        }
        // Close dropup on Escape
        if (e.key === 'Escape') {
            setShowDropup(false);
        }
        // Ctrl+P to improve prompt (magic wand)
        if (e.ctrlKey && e.key === 'p') {
            e.preventDefault();
            handleImprovePrompt();
        }
    };

    const handleInput = (e) => {
        setText(e.target.value);
        const textarea = e.target;
        textarea.style.height = 'auto';
        textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
    };

    const handlePredefinedPrompt = (prompt) => {
        const textarea = textareaRef.current;
        if (textarea) {
            // Get cursor position and insert prompt text at that position
            const start = textarea.selectionStart;
            const end = textarea.selectionEnd;
            const newText = text.substring(0, start) + prompt.prompt + text.substring(end);
            setText(newText);

            // Close dropdown and focus textarea
            setShowDropup(false);

            // Set cursor position after inserted text and adjust textarea height
            requestAnimationFrame(() => {
                const newCursorPos = start + prompt.prompt.length;
                textarea.selectionStart = newCursorPos;
                textarea.selectionEnd = newCursorPos;
                textarea.focus();
                // Adjust height to fit content
                textarea.style.height = 'auto';
                textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
            });
        } else {
            // Fallback: just set the text
            setText(prompt.prompt);
            setShowDropup(false);
        }
    };

    const handleImprovePrompt = async () => {
        if (!text.trim() || isImproving) return;

        // Capture the current sessionId - this is the session the improvement is for
        const targetSessionId = sessionId;
        const controller = new AbortController();

        setImprovingState({ targetSessionId, abortController: controller });
        setImproveError(null);

        try {
            const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout

            const response = await fetch('/api/aux/improve-prompt', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ prompt: text }),
                signal: controller.signal,
            });

            clearTimeout(timeoutId);

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(errorText || 'Failed to improve prompt');
            }

            const data = await response.json();
            if (data.improved_prompt && onDraftChange) {
                onDraftChange(targetSessionId, data.improved_prompt);
                if (targetSessionId === sessionId) {
                    requestAnimationFrame(() => {
                        const textarea = textareaRef.current;
                        if (textarea) {
                            textarea.style.height = 'auto';
                            textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
                            textarea.focus();
                        }
                    });
                }
            }
        } catch (err) {
            console.error('Failed to improve prompt:', err);
            if (err.name === 'AbortError') {
                setImproveError('Request timed out. Please try again.');
            } else {
                setImproveError(err.message || 'Failed to improve prompt');
            }
            setTimeout(() => setImproveError(null), 5000);
        } finally {
            setImprovingState(null);
        }
    };

    const getPlaceholder = () => {
        if (noSession) return "Create a new conversation to start chatting...";
        if (isReadOnly) return "This is a read-only session. Create a new session to chat.";
        if (isStreaming) return "Agent is responding...";
        if (isImproving) return "Improving prompt...";
        if (isDragOver) return "Drop image here...";
        return isSmallWindow ? "Type your message..." : "Type your message... (drop or paste images)";
    };

    // Upload an image file to the session
    const uploadImage = async (file) => {
        if (!sessionId) return null;

        const validTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
        if (!validTypes.includes(file.type)) {
            setUploadError('Only PNG, JPEG, GIF, and WebP images are supported');
            setTimeout(() => setUploadError(null), 5000);
            return null;
        }

        if (file.size > 10 * 1024 * 1024) {
            setUploadError('Image exceeds 10MB limit');
            setTimeout(() => setUploadError(null), 5000);
            return null;
        }

        const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
        const previewUrl = URL.createObjectURL(file);
        const tempImage = { id: tempId, url: previewUrl, name: file.name, mimeType: file.type, uploading: true };
        setPendingImages(prev => [...prev, tempImage]);

        try {
            const formData = new FormData();
            formData.append('image', file);

            const response = await fetch(`/api/sessions/${sessionId}/images`, {
                method: 'POST',
                body: formData,
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.message || 'Failed to upload image');
            }

            const data = await response.json();
            setPendingImages(prev => prev.map(img =>
                img.id === tempId
                    ? { id: data.id, url: data.url, name: data.name, mimeType: data.mime_type, uploading: false }
                    : img
            ));
            URL.revokeObjectURL(previewUrl);
            return data;
        } catch (err) {
            console.error('Failed to upload image:', err);
            setUploadError(err.message || 'Failed to upload image');
            setTimeout(() => setUploadError(null), 5000);
            setPendingImages(prev => prev.filter(img => img.id !== tempId));
            URL.revokeObjectURL(previewUrl);
            return null;
        }
    };

    // Upload images from file paths (for native macOS app)
    const uploadImagesFromPaths = async (paths) => {
        if (!sessionId || !paths || paths.length === 0) return [];

        const tempImages = paths.map(path => {
            const filename = path.split('/').pop() || 'image';
            const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
            return { id: tempId, filename, path };
        });

        tempImages.forEach(({ id, filename }) => {
            setPendingImages(prev => [...prev, { id, url: '', name: filename, mimeType: '', uploading: true }]);
        });

        try {
            const response = await fetch(`/api/sessions/${sessionId}/images/from-path`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ paths }),
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.message || 'Failed to upload images');
            }

            const results = await response.json();
            const tempIds = tempImages.map(t => t.id);
            setPendingImages(prev => prev.filter(img => !tempIds.includes(img.id)));

            for (const data of results) {
                setPendingImages(prev => [...prev, {
                    id: data.id, url: data.url, name: data.name, mimeType: data.mime_type, uploading: false
                }]);
            }
            return results;
        } catch (err) {
            console.error('Failed to upload images from paths:', err);
            setUploadError(err.message || 'Failed to upload images');
            setTimeout(() => setUploadError(null), 5000);
            const tempIds = tempImages.map(t => t.id);
            setPendingImages(prev => prev.filter(img => !tempIds.includes(img.id)));
            return [];
        }
    };


    // Handle attach button click - uses native picker on macOS, file input otherwise
    const handleAttachClick = async () => {
        if (hasNativeImagePicker()) {
            const paths = await pickImages();
            if (paths && paths.length > 0) {
                await uploadImagesFromPaths(paths);
            }
        } else {
            if (fileInputRef.current) {
                fileInputRef.current.click();
            }
        }
    };

    // Handle file drop
    const handleDrop = async (e) => {
        e.preventDefault();
        setIsDragOver(false);
        if (isFullyDisabled || isReadOnly || !sessionId) return;

        const files = Array.from(e.dataTransfer.files);
        const imageFiles = files.filter(f => f.type.startsWith('image/'));
        for (const file of imageFiles) {
            await uploadImage(file);
        }
    };

    const handleDragOver = (e) => {
        e.preventDefault();
        if (!isFullyDisabled && !isReadOnly && sessionId) {
            setIsDragOver(true);
        }
    };

    const handleDragLeave = (e) => {
        e.preventDefault();
        setIsDragOver(false);
    };

    // Handle paste (for clipboard images)
    const handlePaste = async (e) => {
        if (isFullyDisabled || isReadOnly || !sessionId) return;

        const items = Array.from(e.clipboardData.items);
        const imageItems = items.filter(item => item.type.startsWith('image/'));

        if (imageItems.length > 0) {
            e.preventDefault();
            for (const item of imageItems) {
                const file = item.getAsFile();
                if (file) {
                    await uploadImage(file);
                }
            }
        }
    };

    // Remove a pending image
    const removeImage = (imageId) => {
        setPendingImages(prev => {
            const img = prev.find(i => i.id === imageId);
            if (img && img.url.startsWith('blob:')) {
                URL.revokeObjectURL(img.url);
            }
            return prev.filter(i => i.id !== imageId);
        });
    };

    // Handle file input change
    const handleFileInputChange = async (e) => {
        const files = Array.from(e.target.files);
        for (const file of files) {
            await uploadImage(file);
        }
        e.target.value = '';
    };

    const hasPrompts = predefinedPrompts && predefinedPrompts.length > 0;
    const hasPendingImages = pendingImages.length > 0;

    return html`
        <form
            onSubmit=${handleSubmit}
            onDrop=${handleDrop}
            onDragOver=${handleDragOver}
            onDragLeave=${handleDragLeave}
            class="p-4 bg-mitto-input border-t border-slate-700 ${isDragOver ? 'ring-2 ring-blue-500 ring-inset' : ''}"
        >
            <input ref=${fileInputRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple class="hidden" onChange=${handleFileInputChange} />

            ${hasPendingImages && html`
                <div class="max-w-4xl mx-auto mb-3">
                    <div class="flex flex-wrap gap-2">
                        ${pendingImages.map(img => html`
                            <div key=${img.id} class="relative group">
                                <img src=${img.url} alt=${img.name || 'Pending image'} class="w-16 h-16 rounded-lg object-cover border border-slate-600 ${img.uploading ? 'opacity-50' : ''}" />
                                ${img.uploading ? html`
                                    <div class="absolute inset-0 flex items-center justify-center">
                                        <svg class="w-5 h-5 text-white animate-spin" fill="none" viewBox="0 0 24 24">
                                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                                        </svg>
                                    </div>
                                ` : html`
                                    <button type="button" onClick=${() => removeImage(img.id)} class="absolute -top-1 -right-1 w-5 h-5 bg-red-600 hover:bg-red-700 rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity" title="Remove image">
                                        <svg class="w-3 h-3 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
                                    </button>
                                `}
                            </div>
                        `)}
                    </div>
                </div>
            `}

            <div class="flex gap-2 max-w-4xl mx-auto">
                <textarea ref=${textareaRef} value=${text} onInput=${handleInput} onKeyDown=${handleKeyDown} onPaste=${handlePaste} placeholder=${getPlaceholder()} rows="2"
                    class="flex-1 bg-mitto-input-box text-white rounded-xl px-4 py-3 resize-none focus:outline-none focus:ring-2 focus:ring-blue-500 max-h-[200px] placeholder-gray-400 placeholder:text-sm border border-slate-600 ${isFullyDisabled || isReadOnly || isImproving ? 'opacity-50 cursor-not-allowed' : ''}"
                    disabled=${isFullyDisabled || isReadOnly || isImproving}
                />
                <div class="relative flex flex-col gap-1.5" ref=${dropupRef}>
                    ${showDropup && hasPrompts && html`
                        <div class="absolute bottom-full right-0 mb-2 w-56 bg-slate-800 border border-slate-600 rounded-xl shadow-lg overflow-hidden z-50">
                            <div class="py-1">
                                ${predefinedPrompts.map((prompt, idx) => html`
                                    <button key=${idx} type="button" onClick=${() => handlePredefinedPrompt(prompt)} class="w-full text-left px-4 py-2.5 text-sm text-gray-200 hover:bg-slate-700 transition-colors flex items-center gap-2">
                                        <svg class="w-4 h-4 text-blue-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>
                                        <span class="truncate">${prompt.name}</span>
                                    </button>
                                `)}
                            </div>
                        </div>
                    `}

                    <div class="flex rounded-xl overflow-hidden flex-1">
                        ${isStreaming ? html`
                            <button type="button" onClick=${onCancel} class="w-full bg-red-600 hover:bg-red-700 text-white px-4 py-2 font-medium transition-colors flex items-center justify-center gap-2">
                                <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" rx="2" /></svg>
                                <span>Stop</span>
                            </button>
                        ` : html`
                            <button type="submit" disabled=${isFullyDisabled || (!text.trim() && !hasPendingImages) || isReadOnly}
                                class="flex-1 bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:opacity-50 disabled:cursor-not-allowed text-white px-4 py-2 font-medium transition-colors flex items-center justify-center gap-1.5">
                                <span>Send</span><span class="text-blue-300 text-xs hidden sm:inline">⌘↵</span>
                            </button>
                        `}
                        ${hasPrompts && !isStreaming && html`
                            <button type="button" onClick=${() => setShowDropup(!showDropup)} disabled=${isFullyDisabled || isReadOnly}
                                class="bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:cursor-not-allowed text-white px-2 py-2 border-l border-blue-500 transition-colors" title="Insert predefined prompt">
                                <svg class="w-4 h-4 transition-transform ${showDropup ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" /></svg>
                            </button>
                        `}
                        ${hasPrompts && isStreaming && html`
                            <button type="button" onClick=${() => setShowDropup(!showDropup)} disabled=${isFullyDisabled || isReadOnly}
                                class="bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:cursor-not-allowed text-white px-2 py-2 border-l border-slate-600 transition-colors" title="Insert predefined prompt">
                                <svg class="w-4 h-4 transition-transform ${showDropup ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" /></svg>
                            </button>
                        `}
                    </div>

                    <div class="flex gap-1.5 flex-1">
                        <button type="button" onClick=${handleAttachClick} disabled=${isFullyDisabled || isReadOnly}
                            class="flex-1 bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:cursor-not-allowed text-white px-3 py-2 rounded-xl transition-colors flex items-center justify-center" title="Attach image">
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" /></svg>
                        </button>
                        <button type="button" onClick=${handleImprovePrompt} disabled=${isFullyDisabled || !text.trim() || isReadOnly || isStreaming || isImproving}
                            class="flex-1 bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:opacity-50 disabled:cursor-not-allowed text-white px-3 py-2 rounded-xl transition-colors flex items-center justify-center" title="Improve prompt with AI">
                            ${isImproving ? html`
                                <svg class="w-5 h-5 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg>
                            ` : html`
                                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" /></svg>
                            `}
                        </button>
                    </div>
                </div>
            </div>

            ${improveError && html`
                <div class="max-w-4xl mx-auto mt-2">
                    <div class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2">
                        <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                        <span>${improveError}</span>
                        <button type="button" onClick=${() => setImproveError(null)} class="ml-auto text-red-300 hover:text-red-100">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
                        </button>
                    </div>
                </div>
            `}
            ${uploadError && html`
                <div class="max-w-4xl mx-auto mt-2">
                    <div class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2">
                        <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" /></svg>
                        <span>${uploadError}</span>
                        <button type="button" onClick=${() => setUploadError(null)} class="ml-auto text-red-300 hover:text-red-100">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
                        </button>
                    </div>
                </div>
            `}
        </form>
    `;
}