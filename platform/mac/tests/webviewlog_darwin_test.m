// webviewlog_darwin_test.m - Unit tests for WebView console logging
//
// This file is kept separate from cmd/mitto-app/ to avoid CGO picking up the
// main() function during Go compilation.
//
// Compile and run with:
//   make test-webviewlog
//
// Or manually:
//   clang -framework Foundation -o webviewlog_test \
//     cmd/mitto-app/webviewlog_darwin.m \
//     platform/mac/tests/webviewlog_darwin_test.m && ./webviewlog_test

#import <Foundation/Foundation.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include "../../../cmd/mitto-app/webviewlog_darwin.h"

// Test counters
static int gTestsPassed = 0;
static int gTestsFailed = 0;

#define ASSERT_EQ(a, b, msg) do { \
    if ((a) == (b)) { gTestsPassed++; } \
    else { gTestsFailed++; printf("FAIL: %s (expected %ld, got %ld)\n", msg, (long)(b), (long)(a)); } \
} while(0)

#define ASSERT_NE(a, b, msg) do { \
    if ((a) != (b)) { gTestsPassed++; } \
    else { gTestsFailed++; printf("FAIL: %s (values should not be equal: %ld)\n", msg, (long)(a)); } \
} while(0)

#define ASSERT_TRUE(cond, msg) do { \
    if (cond) { gTestsPassed++; } \
    else { gTestsFailed++; printf("FAIL: %s\n", msg); } \
} while(0)

#define ASSERT_FALSE(cond, msg) do { \
    if (!(cond)) { gTestsPassed++; } \
    else { gTestsFailed++; printf("FAIL: %s (should be false)\n", msg); } \
} while(0)

// Helper to create a temporary directory for tests
static NSString* createTempDir(void) {
    NSString* template = [NSTemporaryDirectory() stringByAppendingPathComponent:@"mitto-test-XXXXXX"];
    char* templateCStr = strdup([template fileSystemRepresentation]);
    char* result = mkdtemp(templateCStr);
    NSString* path = nil;
    if (result) {
        path = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:result length:strlen(result)];
    }
    free(templateCStr);
    return path;
}

// Helper to read file contents
static NSString* readFile(NSString* path) {
    return [NSString stringWithContentsOfFile:path encoding:NSUTF8StringEncoding error:nil];
}

// Helper to get file size
static long getFileSize(NSString* path) {
    NSDictionary* attrs = [[NSFileManager defaultManager] attributesOfItemAtPath:path error:nil];
    return [[attrs objectForKey:NSFileSize] longValue];
}

// Test: getConsoleHookScript returns valid JavaScript
static void testConsoleHookScript(void) {
    printf("Testing getConsoleHookScript...\n");
    
    const char* script = getConsoleHookScript();
    ASSERT_TRUE(script != NULL, "getConsoleHookScript should return non-NULL");
    ASSERT_TRUE(strlen(script) > 100, "Console hook script should be substantial");
    
    // Check for expected content
    ASSERT_TRUE(strstr(script, "__mittoConsoleHooked") != NULL, "Script should contain __mittoConsoleHooked");
    ASSERT_TRUE(strstr(script, "console[level]") != NULL, "Script should hook console methods");
    ASSERT_TRUE(strstr(script, "mittoLogConsole") != NULL, "Script should call mittoLogConsole");
}

// Test: initWebViewLog creates directory and file
static void testInitCreatesLogFile(void) {
    printf("Testing initWebViewLog creates log file...\n");
    
    NSString* tmpDir = createTempDir();
    ASSERT_TRUE(tmpDir != nil, "Should create temp directory");
    
    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 1024 * 1024, // 1MB
        .maxBackups = 3
    };
    
    int result = initWebViewLog(config);
    ASSERT_EQ(result, 0, "initWebViewLog should return 0 on success");
    
    // Check log file exists
    NSString* logPath = [tmpDir stringByAppendingPathComponent:@"webview.log"];
    BOOL exists = [[NSFileManager defaultManager] fileExistsAtPath:logPath];
    ASSERT_TRUE(exists, "Log file should exist after init");
    
    // Check startup marker is written
    NSString* contents = readFile(logPath);
    ASSERT_TRUE([contents containsString:@"Mitto WebView Log Started"], "Log should contain startup marker");
    
    closeWebViewLog();
    
    // Verify close marker was written
    contents = readFile(logPath);
    ASSERT_TRUE([contents containsString:@"Mitto WebView Log Closed"], "Log should contain close marker");
    
    // Cleanup
    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: logWebViewConsole writes formatted messages
static void testLogWritesFormattedMessages(void) {
    printf("Testing logWebViewConsole writes formatted messages...\n");
    
    NSString* tmpDir = createTempDir();
    
    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 1024 * 1024,
        .maxBackups = 3
    };
    
    initWebViewLog(config);
    
    // Log some messages
    logWebViewConsole("LOG", "Test message 1");
    logWebViewConsole("WARN", "Warning message");
    logWebViewConsole("ERROR", "Error message");
    logWebViewConsole("DEBUG", "Debug info");
    logWebViewConsole("INFO", "Info message");
    
    closeWebViewLog();
    
    // Read and verify
    NSString* logPath = [tmpDir stringByAppendingPathComponent:@"webview.log"];
    NSString* contents = readFile(logPath);
    
    ASSERT_TRUE([contents containsString:@"[LOG] Test message 1"], "Should contain LOG message");
    ASSERT_TRUE([contents containsString:@"[WARN] Warning message"], "Should contain WARN message");
    ASSERT_TRUE([contents containsString:@"[ERROR] Error message"], "Should contain ERROR message");
    ASSERT_TRUE([contents containsString:@"[DEBUG] Debug info"], "Should contain DEBUG message");
    ASSERT_TRUE([contents containsString:@"[INFO] Info message"], "Should contain INFO message");
    
    // Verify timestamp format (ISO 8601)
    // Look for pattern like [2024-01-15T10:30:45
    NSRegularExpression* regex = [NSRegularExpression 
        regularExpressionWithPattern:@"\\[\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}"
        options:0 error:nil];
    NSUInteger matches = [regex numberOfMatchesInString:contents options:0 range:NSMakeRange(0, contents.length)];
    ASSERT_TRUE(matches >= 5, "Should have timestamp for each message");
    
    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: newlines are sanitized in log messages
static void testNewlineSanitization(void) {
    printf("Testing newline sanitization...\n");

    NSString* tmpDir = createTempDir();

    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 1024 * 1024,
        .maxBackups = 3
    };

    initWebViewLog(config);

    // Log message with newlines
    logWebViewConsole("LOG", "Line 1\nLine 2\nLine 3");

    closeWebViewLog();

    // Read and verify - newlines should be escaped
    NSString* logPath = [tmpDir stringByAppendingPathComponent:@"webview.log"];
    NSString* contents = readFile(logPath);

    ASSERT_TRUE([contents containsString:@"Line 1\\nLine 2\\nLine 3"], "Newlines should be escaped as \\n");

    // Count actual newlines - should not have extra newlines from the message
    NSUInteger newlineCount = [[contents componentsSeparatedByString:@"\n"] count] - 1;
    // Expecting: startup marker (2 lines), log message (1 line), close marker (2 lines)
    ASSERT_TRUE(newlineCount <= 6, "Should not have extra newlines from message content");

    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: log rotation when size limit is reached
static void testLogRotation(void) {
    printf("Testing log rotation...\n");

    NSString* tmpDir = createTempDir();

    // Use a very small size limit to trigger rotation
    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 500, // 500 bytes - very small to trigger rotation quickly
        .maxBackups = 3
    };

    initWebViewLog(config);

    // Write enough messages to trigger rotation
    for (int i = 0; i < 50; i++) {
        char msg[256];
        snprintf(msg, sizeof(msg), "Log message number %d with some extra text to make it longer", i);
        logWebViewConsole("LOG", msg);
    }

    closeWebViewLog();

    // Check that rotation happened - should have backup files
    NSString* logPath = [tmpDir stringByAppendingPathComponent:@"webview.log"];
    NSString* backup1 = [tmpDir stringByAppendingPathComponent:@"webview.log.1"];

    BOOL mainExists = [[NSFileManager defaultManager] fileExistsAtPath:logPath];
    BOOL backup1Exists = [[NSFileManager defaultManager] fileExistsAtPath:backup1];

    ASSERT_TRUE(mainExists, "Main log file should exist");
    ASSERT_TRUE(backup1Exists, "At least one backup should exist after rotation");

    // Verify main log file is not too large
    long mainSize = getFileSize(logPath);
    ASSERT_TRUE(mainSize < 1000, "Main log should be reasonably sized after rotation");

    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: multiple backups are maintained correctly
static void testMultipleBackups(void) {
    printf("Testing multiple backup files...\n");

    NSString* tmpDir = createTempDir();

    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 200, // Very small
        .maxBackups = 3
    };

    initWebViewLog(config);

    // Write many messages to trigger multiple rotations
    for (int i = 0; i < 100; i++) {
        char msg[256];
        snprintf(msg, sizeof(msg), "Rotation test message %d padding padding padding", i);
        logWebViewConsole("LOG", msg);
    }

    closeWebViewLog();

    // Count backup files
    NSString* backup1 = [tmpDir stringByAppendingPathComponent:@"webview.log.1"];
    NSString* backup2 = [tmpDir stringByAppendingPathComponent:@"webview.log.2"];
    NSString* backup3 = [tmpDir stringByAppendingPathComponent:@"webview.log.3"];
    NSString* backup4 = [tmpDir stringByAppendingPathComponent:@"webview.log.4"];

    BOOL b1 = [[NSFileManager defaultManager] fileExistsAtPath:backup1];
    BOOL b2 = [[NSFileManager defaultManager] fileExistsAtPath:backup2];
    BOOL b3 = [[NSFileManager defaultManager] fileExistsAtPath:backup3];
    BOOL b4 = [[NSFileManager defaultManager] fileExistsAtPath:backup4];

    // With maxBackups=3, we should have .1, .2, .3 but not .4
    ASSERT_TRUE(b1, "Backup .1 should exist");
    ASSERT_TRUE(b2 || b3, "At least one of .2 or .3 should exist");
    ASSERT_FALSE(b4, "Backup .4 should NOT exist (maxBackups=3)");

    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: NULL and empty messages are handled
static void testEdgeCases(void) {
    printf("Testing edge cases...\n");

    NSString* tmpDir = createTempDir();

    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 1024 * 1024,
        .maxBackups = 3
    };

    initWebViewLog(config);

    // These should not crash
    logWebViewConsole(NULL, "Message with NULL level");
    logWebViewConsole("LOG", NULL);
    logWebViewConsole("LOG", "");
    logWebViewConsole("", "Empty level");

    closeWebViewLog();

    NSString* logPath = [tmpDir stringByAppendingPathComponent:@"webview.log"];
    NSString* contents = readFile(logPath);

    // NULL level should be replaced with "LOG"
    ASSERT_TRUE([contents containsString:@"[LOG] Message with NULL level"], "NULL level should become LOG");

    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: logging before init is a no-op (should not crash)
static void testLoggingBeforeInit(void) {
    printf("Testing logging before init...\n");

    // This should not crash - just a no-op
    logWebViewConsole("LOG", "This should be ignored");

    gTestsPassed++; // If we get here without crashing, it passed
}

// Test: double init returns success without reinitializing
static void testDoubleInit(void) {
    printf("Testing double initialization...\n");

    NSString* tmpDir = createTempDir();

    WebViewLogConfig config = {
        .logDir = [tmpDir UTF8String],
        .maxSizeBytes = 1024 * 1024,
        .maxBackups = 3
    };

    int result1 = initWebViewLog(config);
    ASSERT_EQ(result1, 0, "First init should succeed");

    // Second init should return 0 (already initialized)
    int result2 = initWebViewLog(config);
    ASSERT_EQ(result2, 0, "Second init should return 0");

    closeWebViewLog();

    [[NSFileManager defaultManager] removeItemAtPath:tmpDir error:nil];
}

// Test: default directory is used when not specified
static void testDefaultDirectory(void) {
    printf("Testing default log directory...\n");

    WebViewLogConfig config = {
        .logDir = NULL,  // Use default
        .maxSizeBytes = 0,  // Use default
        .maxBackups = 0  // Use default
    };

    int result = initWebViewLog(config);
    ASSERT_EQ(result, 0, "Init with defaults should succeed");

    // Check that default directory exists
    NSString* defaultPath = [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Logs/Mitto/webview.log"];
    BOOL exists = [[NSFileManager defaultManager] fileExistsAtPath:defaultPath];
    ASSERT_TRUE(exists, "Default log file should exist");

    closeWebViewLog();

    // Clean up default log (but keep directory for real app use)
    [[NSFileManager defaultManager] removeItemAtPath:defaultPath error:nil];
}

int main(int argc, char* argv[]) {
    @autoreleasepool {
        printf("\n========================================\n");
        printf("WebView Console Logger Tests\n");
        printf("========================================\n\n");

        testConsoleHookScript();
        testLoggingBeforeInit();
        testInitCreatesLogFile();
        testLogWritesFormattedMessages();
        testNewlineSanitization();
        testEdgeCases();
        testDoubleInit();
        testLogRotation();
        testMultipleBackups();
        testDefaultDirectory();

        printf("\n========================================\n");
        printf("Results: %d passed, %d failed\n", gTestsPassed, gTestsFailed);
        printf("========================================\n\n");

        return gTestsFailed > 0 ? 1 : 0;
    }
}
