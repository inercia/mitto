// cache_darwin.h - Header for WKWebView cache management

#ifndef CACHE_DARWIN_H
#define CACHE_DARWIN_H

// clearWebViewCache clears all cached data from WKWebView's default data store.
// This includes disk cache and memory cache, but preserves localStorage and cookies.
// This function blocks until the cache is cleared.
// Should be called before creating the webview to ensure fresh content is loaded.
void clearWebViewCache(void);

#endif // CACHE_DARWIN_H

