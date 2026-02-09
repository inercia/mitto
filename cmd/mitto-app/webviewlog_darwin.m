// webviewlog_darwin.m - Implementation for WebView console logging
//
// Captures JavaScript console output and writes to ~/Library/Logs/Mitto/webview.log
// with automatic file rotation based on size.

#import <Foundation/Foundation.h>
#include <pthread.h>
#include <sys/stat.h>
#include "webviewlog_darwin.h"

// Global state for the logger
static FILE* gLogFile = NULL;
static NSString* gLogDir = nil;
static NSString* gLogPath = nil;
static long gMaxSizeBytes = 10 * 1024 * 1024; // 10MB default
static int gMaxBackups = 3;
static pthread_mutex_t gLogMutex = PTHREAD_MUTEX_INITIALIZER;
static BOOL gInitialized = NO;

// Forward declarations
static void rotateLogIfNeeded(void);
static void performRotation(void);
static BOOL createLogDirectory(void);
static BOOL openLogFile(void);

// JavaScript to inject into WKWebView to capture console output.
// This hooks console.log/warn/error/debug/info and sends messages to the
// native Go function bound as window.mittoLogConsole.
static const char* kConsoleHookScript =
    "(function() {\n"
    "  if (window.__mittoConsoleHooked) return;\n"
    "  window.__mittoConsoleHooked = true;\n"
    "  const levels = ['log', 'warn', 'error', 'debug', 'info'];\n"
    "  levels.forEach(function(level) {\n"
    "    const original = console[level];\n"
    "    console[level] = function() {\n"
    "      const args = Array.from(arguments);\n"
    "      const message = args.map(function(arg) {\n"
    "        if (arg === null) return 'null';\n"
    "        if (arg === undefined) return 'undefined';\n"
    "        if (arg instanceof Error) return arg.stack || arg.toString();\n"
    "        if (typeof arg === 'object') {\n"
    "          try { return JSON.stringify(arg, null, 0); } catch(e) { return String(arg); }\n"
    "        }\n"
    "        return String(arg);\n"
    "      }).join(' ');\n"
    "      if (typeof window.mittoLogConsole === 'function') {\n"
    "        try {\n"
    "          window.mittoLogConsole(level.toUpperCase(), message);\n"
    "        } catch(e) {\n"
    "          // Ignore errors from logging\n"
    "        }\n"
    "      }\n"
    "      original.apply(console, arguments);\n"
    "    };\n"
    "  });\n"
    "  // Log that console hooking is complete\n"
    "  if (typeof window.mittoLogConsole === 'function') {\n"
    "    window.mittoLogConsole('INFO', 'Console logging initialized');\n"
    "  }\n"
    "})();\n";

const char* getConsoleHookScript(void) {
    return kConsoleHookScript;
}

static BOOL createLogDirectory(void) {
    NSFileManager* fm = [NSFileManager defaultManager];
    NSError* error = nil;
    
    if (![fm fileExistsAtPath:gLogDir]) {
        if (![fm createDirectoryAtPath:gLogDir withIntermediateDirectories:YES attributes:nil error:&error]) {
            NSLog(@"WebViewLog: Failed to create log directory %@: %@", gLogDir, error);
            return NO;
        }
    }
    return YES;
}

static BOOL openLogFile(void) {
    if (gLogFile != NULL) {
        fclose(gLogFile);
        gLogFile = NULL;
    }
    
    const char* path = [gLogPath fileSystemRepresentation];
    gLogFile = fopen(path, "a");
    if (gLogFile == NULL) {
        NSLog(@"WebViewLog: Failed to open log file %s: %s", path, strerror(errno));
        return NO;
    }
    return YES;
}

static void performRotation(void) {
    // Close current log file
    if (gLogFile != NULL) {
        fclose(gLogFile);
        gLogFile = NULL;
    }
    
    NSFileManager* fm = [NSFileManager defaultManager];
    
    // Delete oldest backup if it exists
    NSString* oldest = [NSString stringWithFormat:@"%@.%d", gLogPath, gMaxBackups];
    [fm removeItemAtPath:oldest error:nil];
    
    // Shift existing backups
    for (int i = gMaxBackups - 1; i >= 1; i--) {
        NSString* src = [NSString stringWithFormat:@"%@.%d", gLogPath, i];
        NSString* dst = [NSString stringWithFormat:@"%@.%d", gLogPath, i + 1];
        if ([fm fileExistsAtPath:src]) {
            [fm moveItemAtPath:src toPath:dst error:nil];
        }
    }
    
    // Rename current log to .1
    if ([fm fileExistsAtPath:gLogPath]) {
        NSString* firstBackup = [NSString stringWithFormat:@"%@.1", gLogPath];
        [fm moveItemAtPath:gLogPath toPath:firstBackup error:nil];
    }
    
    // Open new log file
    openLogFile();
}

static void rotateLogIfNeeded(void) {
    if (gLogFile == NULL) return;
    
    // Get current file size
    struct stat st;
    if (fstat(fileno(gLogFile), &st) == 0) {
        if (st.st_size >= gMaxSizeBytes) {
            performRotation();
        }
    }
}

int initWebViewLog(WebViewLogConfig config) {
    pthread_mutex_lock(&gLogMutex);
    
    if (gInitialized) {
        pthread_mutex_unlock(&gLogMutex);
        return 0; // Already initialized
    }
    
    @autoreleasepool {
        // Set configuration
        if (config.logDir != NULL) {
            gLogDir = [[NSString alloc] initWithUTF8String:config.logDir];
        } else {
            // Default to ~/Library/Logs/Mitto
            NSArray* paths = NSSearchPathForDirectoriesInDomains(NSLibraryDirectory, NSUserDomainMask, YES);
            NSString* libraryDir = [paths firstObject];
            gLogDir = [[libraryDir stringByAppendingPathComponent:@"Logs/Mitto"] retain];
        }
        
        gLogPath = [[gLogDir stringByAppendingPathComponent:@"webview.log"] retain];
        
        if (config.maxSizeBytes > 0) {
            gMaxSizeBytes = config.maxSizeBytes;
        }
        if (config.maxBackups > 0) {
            gMaxBackups = config.maxBackups;
        }
        
        // Create directory and open file
        if (!createLogDirectory() || !openLogFile()) {
            pthread_mutex_unlock(&gLogMutex);
            return -1;
        }
        
        gInitialized = YES;

        // Log rotation on startup if file is too large
        rotateLogIfNeeded();

        // Write startup marker
        NSDateFormatter* formatter = [[NSDateFormatter alloc] init];
        [formatter setDateFormat:@"yyyy-MM-dd'T'HH:mm:ssZZZZZ"];
        NSString* timestamp = [formatter stringFromDate:[NSDate date]];
        [formatter release];

        fprintf(gLogFile, "\n=== Mitto WebView Log Started at %s ===\n", [timestamp UTF8String]);
        fflush(gLogFile);
    }

    pthread_mutex_unlock(&gLogMutex);
    return 0;
}

void closeWebViewLog(void) {
    pthread_mutex_lock(&gLogMutex);

    if (gLogFile != NULL) {
        // Write shutdown marker
        @autoreleasepool {
            NSDateFormatter* formatter = [[NSDateFormatter alloc] init];
            [formatter setDateFormat:@"yyyy-MM-dd'T'HH:mm:ssZZZZZ"];
            NSString* timestamp = [formatter stringFromDate:[NSDate date]];
            [formatter release];

            fprintf(gLogFile, "=== Mitto WebView Log Closed at %s ===\n\n", [timestamp UTF8String]);
        }

        fclose(gLogFile);
        gLogFile = NULL;
    }

    gInitialized = NO;

    pthread_mutex_unlock(&gLogMutex);
}

void logWebViewConsole(const char* level, const char* message) {
    if (!gInitialized || gLogFile == NULL) return;

    pthread_mutex_lock(&gLogMutex);

    @autoreleasepool {
        // Check if rotation is needed before writing
        rotateLogIfNeeded();

        if (gLogFile == NULL) {
            pthread_mutex_unlock(&gLogMutex);
            return;
        }

        // Format: [timestamp] [LEVEL] message
        NSDateFormatter* formatter = [[NSDateFormatter alloc] init];
        [formatter setDateFormat:@"yyyy-MM-dd'T'HH:mm:ss.SSSZZZZZ"];
        NSString* timestamp = [formatter stringFromDate:[NSDate date]];
        [formatter release];

        // Map level to uppercase string
        const char* levelStr = level;
        if (level == NULL) levelStr = "LOG";

        // Sanitize message - replace newlines with \n literal for single-line log entries
        NSString* msgStr = message ? [NSString stringWithUTF8String:message] : @"";
        NSString* sanitized = [msgStr stringByReplacingOccurrencesOfString:@"\n" withString:@"\\n"];

        fprintf(gLogFile, "[%s] [%s] %s\n",
                [timestamp UTF8String],
                levelStr,
                [sanitized UTF8String]);
        fflush(gLogFile);
    }

    pthread_mutex_unlock(&gLogMutex);
}

