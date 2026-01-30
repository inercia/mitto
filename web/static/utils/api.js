// Mitto Web Interface - API Configuration
// Provides the API prefix for all API calls and WebSocket connections

/**
 * Get the API prefix for all API endpoints.
 * This is injected by the server into the HTML page.
 * @returns {string} The API prefix (e.g., "/mitto")
 */
export function getApiPrefix() {
    return window.mittoApiPrefix || '';
}

/**
 * Build an API URL with the configured prefix.
 * @param {string} path - The API path (e.g., "/api/sessions")
 * @returns {string} The full URL with prefix (e.g., "/mitto/api/sessions")
 */
export function apiUrl(path) {
    const prefix = getApiPrefix();
    // Ensure path starts with /
    if (!path.startsWith('/')) {
        path = '/' + path;
    }
    return prefix + path;
}

/**
 * Build a WebSocket URL with the configured prefix.
 * @param {string} path - The WebSocket path (e.g., "/ws" or "/api/events")
 * @returns {string} The full WebSocket URL
 */
export function wsUrl(path) {
    const prefix = getApiPrefix();
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Ensure path starts with /
    if (!path.startsWith('/')) {
        path = '/' + path;
    }
    return `${protocol}//${window.location.host}${prefix}${path}`;
}

