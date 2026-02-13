// webviewlog_darwin.h - Header for WebView console logging
//
// This module captures JavaScript console output from WKWebView and writes
// it to a log file with automatic rotation.

#ifndef WEBVIEWLOG_DARWIN_H
#define WEBVIEWLOG_DARWIN_H

// WebViewLogConfig holds configuration for WebView console logging.
typedef struct {
    // logDir is the directory for log files (e.g., ~/Library/Logs/Mitto)
    const char* logDir;
    // maxSizeBytes is the maximum size of a single log file before rotation (default: 10MB)
    long maxSizeBytes;
    // maxBackups is the number of rotated files to keep (default: 3)
    int maxBackups;
} WebViewLogConfig;

// initWebViewLog initializes the WebView console logger.
// Must be called once before any logging occurs.
// Returns 0 on success, non-zero on failure.
int initWebViewLog(WebViewLogConfig config);

// closeWebViewLog closes the log file and cleans up resources.
// Should be called during app shutdown.
void closeWebViewLog(void);

// logWebViewConsole writes a console message to the log file.
// level: log level string (e.g., "log", "warn", "error", "debug", "info")
// message: the console message content
// This function handles rotation internally when size limit is reached.
void logWebViewConsole(const char* level, const char* message);

// getConsoleHookScript returns the JavaScript code that should be injected
// into the WKWebView to capture console output.
// The returned string is static and should not be freed.
const char* getConsoleHookScript(void);

#endif // WEBVIEWLOG_DARWIN_H

