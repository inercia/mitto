#ifndef VIEWER_DARWIN_H
#define VIEWER_DARWIN_H

// Opens a new native viewer window with the given URL.
// The window contains a WKWebView that loads the URL (typically viewer.html).
// Multiple viewer windows can be open simultaneously.
// Parameters:
//   url: The full URL to load (e.g., "http://127.0.0.1:PORT/mitto/viewer.html?ws=UUID&path=file.go")
//   title: Window title (e.g., the filename)
//   width: Window width in points (0 for default 1000)
//   height: Window height in points (0 for default 750)
void openViewerWindow(const char* url, const char* title, int width, int height);

#endif
