// Preact loader for Mitto
// Loads Preact and HTM from local vendor files and initializes the app

import { h, render } from './vendor/preact.js';
import { useState, useEffect, useLayoutEffect, useRef, useCallback, useMemo } from './vendor/preact-hooks.js';
import htm from './vendor/htm.js';
import { marked } from './vendor/marked.js';
import DOMPurify from './vendor/dompurify.js';

// Configure marked for safe rendering
marked.setOptions({
    gfm: true,           // GitHub Flavored Markdown
    breaks: true,        // Convert \n to <br>
    headerIds: false,    // Don't add IDs to headers (security)
    mangle: false,       // Don't mangle email addresses
});

const html = htm.bind(h);
window.preact = { h, render, useState, useEffect, useLayoutEffect, useRef, useCallback, useMemo, html };
window.marked = marked;
window.DOMPurify = DOMPurify;

// Load the app after preact is ready
import('./app.js');

