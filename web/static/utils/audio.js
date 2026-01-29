// Mitto Web Interface - Audio Utilities
// Functions for playing notification sounds

// =============================================================================
// Notification Sound Helper
// =============================================================================

// Audio context for playing notification sounds (created on first use)
let notificationAudioContext = null;

/**
 * Get or create the Web Audio context
 * @returns {AudioContext}
 */
function getAudioContext() {
    if (!notificationAudioContext) {
        notificationAudioContext = new (window.AudioContext || window.webkitAudioContext)();
    }
    return notificationAudioContext;
}

/**
 * Play the agent completed notification sound.
 * Uses the native macOS function if available, otherwise falls back to Web Audio API.
 */
export function playAgentCompletedSound() {
    // Check if native macOS sound function is available
    if (typeof window.mittoPlayNotificationSound === 'function') {
        window.mittoPlayNotificationSound();
        return;
    }

    // Fall back to Web Audio API - play a pleasant two-tone chime
    try {
        const ctx = getAudioContext();
        const now = ctx.currentTime;

        // First tone (higher pitch)
        const osc1 = ctx.createOscillator();
        const gain1 = ctx.createGain();
        osc1.type = 'sine';
        osc1.frequency.value = 880; // A5
        gain1.gain.setValueAtTime(0.15, now);
        gain1.gain.exponentialRampToValueAtTime(0.01, now + 0.15);
        osc1.connect(gain1);
        gain1.connect(ctx.destination);
        osc1.start(now);
        osc1.stop(now + 0.15);

        // Second tone (slightly lower, played after first)
        const osc2 = ctx.createOscillator();
        const gain2 = ctx.createGain();
        osc2.type = 'sine';
        osc2.frequency.value = 659.25; // E5
        gain2.gain.setValueAtTime(0.15, now + 0.1);
        gain2.gain.exponentialRampToValueAtTime(0.01, now + 0.3);
        osc2.connect(gain2);
        gain2.connect(ctx.destination);
        osc2.start(now + 0.1);
        osc2.stop(now + 0.3);
    } catch (err) {
        console.warn('Failed to play notification sound:', err);
    }
}

// Global ref for agent completed sound setting (used by WebSocket handler)
// This is set by the App component when config is loaded
window.mittoAgentCompletedSoundEnabled = false;

