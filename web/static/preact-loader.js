// Preact loader for Mitto
// Loads Preact and HTM from local vendor files and initializes the app

import { h, render } from "./vendor/preact.js";
import {
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  useMemo,
} from "./vendor/preact-hooks.js";
import htm from "./vendor/htm.js";
import { marked } from "./vendor/marked.js";
import DOMPurify from "./vendor/dompurify.js";

// Configure marked for safe rendering
marked.setOptions({
  gfm: true, // GitHub Flavored Markdown
  breaks: true, // Convert \n to <br>
  headerIds: false, // Don't add IDs to headers (security)
  mangle: false, // Don't mangle email addresses
});

const html = htm.bind(h);
window.preact = {
  h,
  render,
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  useMemo,
  html,
};
window.marked = marked;
window.DOMPurify = DOMPurify;

// =============================================================================
// Mermaid.js Integration
// =============================================================================

// Flag to track if Mermaid.js is loaded and initialized
window.mermaidReady = false;
window.mermaidLoading = false;

// Queue of elements to render once Mermaid is ready
window.mermaidRenderQueue = [];

// Cache for rendered mermaid SVGs, keyed by content hash
// This allows us to preserve rendered diagrams during streaming updates
window.mermaidSvgCache = new Map();

/**
 * Generate a simple hash for mermaid diagram content.
 * Used to identify diagrams across innerHTML updates.
 * @param {string} content - The mermaid diagram definition
 * @returns {string} A hash string
 */
function hashMermaidContent(content) {
  // Simple hash function - good enough for our use case
  let hash = 0;
  const str = content.trim();
  for (let i = 0; i < str.length; i++) {
    const char = str.charCodeAt(i);
    hash = ((hash << 5) - hash) + char;
    hash = hash & hash; // Convert to 32-bit integer
  }
  return 'mermaid-' + Math.abs(hash).toString(36);
}

/**
 * Load Mermaid.js from CDN dynamically.
 * This avoids loading the library if no mermaid diagrams are present.
 * @returns {Promise<void>} Resolves when Mermaid is loaded and initialized
 */
async function loadMermaid() {
  if (window.mermaidReady) {
    return Promise.resolve();
  }
  if (window.mermaidLoading) {
    // Wait for the existing load to complete
    return new Promise((resolve) => {
      const checkReady = setInterval(() => {
        if (window.mermaidReady) {
          clearInterval(checkReady);
          resolve();
        }
      }, 50);
    });
  }

  window.mermaidLoading = true;

  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js";
    script.async = true;
    script.onload = () => {
      // Detect current theme from document classes
      const isLight = document.documentElement.classList.contains("light");
      const mermaidTheme = isLight ? "default" : "dark";

      // Initialize Mermaid with configuration
      window.mermaid.initialize({
        startOnLoad: false, // We'll manually trigger rendering
        theme: mermaidTheme, // Match current Mitto theme
        securityLevel: "strict", // Prevent XSS
        fontFamily: "ui-sans-serif, system-ui, sans-serif",
        logLevel: "error", // Reduce console noise
      });
      window.mermaidReady = true;
      window.mermaidLoading = false;
      window.mermaidCurrentTheme = mermaidTheme;

      // Process any queued render requests
      while (window.mermaidRenderQueue.length > 0) {
        const container = window.mermaidRenderQueue.shift();
        renderMermaidInContainer(container);
      }

      resolve();
    };
    script.onerror = (err) => {
      window.mermaidLoading = false;
      console.error("Failed to load Mermaid.js:", err);
      reject(err);
    };
    document.head.appendChild(script);
  });
}

/**
 * Render all mermaid diagrams within a container element.
 * This finds all <pre class="mermaid"> elements and converts them to SVG.
 *
 * Supports streaming: Uses a content-based cache to preserve rendered diagrams
 * when the container's innerHTML is updated during streaming. If a diagram
 * with the same content was previously rendered, the cached SVG is reused.
 *
 * @param {HTMLElement} container - The container to search for mermaid diagrams
 */
async function renderMermaidInContainer(container) {
  if (!container) return;

  // Find all unprocessed mermaid blocks
  const mermaidBlocks = container.querySelectorAll(
    'pre.mermaid:not([data-mermaid-processed="true"])',
  );
  if (mermaidBlocks.length === 0) return;

  // Load Mermaid.js if not already loaded
  if (!window.mermaidReady) {
    if (!window.mermaidLoading) {
      loadMermaid();
    }
    // Queue this container for processing once Mermaid is ready
    window.mermaidRenderQueue.push(container);
    return;
  }

  // Process each mermaid block
  for (const block of mermaidBlocks) {
    try {
      // Get the diagram definition
      const diagramDef = block.textContent || "";
      if (!diagramDef.trim()) continue;

      // Generate a content-based hash to identify this diagram
      const contentHash = hashMermaidContent(diagramDef);

      let svg;

      // Check if we have a cached SVG for this diagram content
      if (window.mermaidSvgCache.has(contentHash)) {
        svg = window.mermaidSvgCache.get(contentHash);
      } else {
        // Generate a unique ID for mermaid's internal use
        const id = `${contentHash}-${Date.now()}`;

        // Render the diagram
        const result = await window.mermaid.render(id, diagramDef);
        svg = result.svg;

        // Cache the rendered SVG for future use (during streaming updates)
        window.mermaidSvgCache.set(contentHash, svg);

        // Limit cache size to prevent memory leaks (keep last 50 diagrams)
        if (window.mermaidSvgCache.size > 50) {
          const firstKey = window.mermaidSvgCache.keys().next().value;
          window.mermaidSvgCache.delete(firstKey);
        }
      }

      // Create a wrapper div and insert the SVG
      const wrapper = document.createElement("div");
      wrapper.className = "mermaid-diagram";
      wrapper.setAttribute("data-mermaid-hash", contentHash);
      wrapper.innerHTML = svg;

      // Replace the pre element with the rendered diagram
      block.replaceWith(wrapper);
    } catch (err) {
      console.error("Failed to render mermaid diagram:", err);
      // Mark as processed even on error to avoid retry loops
      block.setAttribute("data-mermaid-processed", "true");
      block.classList.add("mermaid-error");
      // Add error indicator
      const errorMsg = document.createElement("div");
      errorMsg.className = "mermaid-error-message";
      errorMsg.textContent = "⚠️ Failed to render diagram";
      block.appendChild(errorMsg);
    }
  }
}

/**
 * Update Mermaid theme when the app theme changes.
 * This re-initializes Mermaid with the new theme.
 * @param {string} theme - Either "light" or "dark"
 */
function updateMermaidTheme(theme) {
  if (!window.mermaidReady || !window.mermaid) return;

  const mermaidTheme = theme === "light" ? "default" : "dark";
  if (window.mermaidCurrentTheme === mermaidTheme) return;

  // Re-initialize with new theme
  window.mermaid.initialize({
    startOnLoad: false,
    theme: mermaidTheme,
    securityLevel: "strict",
    fontFamily: "ui-sans-serif, system-ui, sans-serif",
    logLevel: "error",
  });
  window.mermaidCurrentTheme = mermaidTheme;

  // Note: Already rendered diagrams will keep their original theme.
  // Re-rendering would require storing the original diagram source,
  // which is lost after mermaid.render() converts it to SVG.
  // This is acceptable since theme changes are relatively rare.
}

// Expose the render function globally for use in components
window.renderMermaidDiagrams = renderMermaidInContainer;
window.updateMermaidTheme = updateMermaidTheme;

// Load the app after preact is ready
import("./app.js");
