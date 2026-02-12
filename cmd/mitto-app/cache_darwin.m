// cache_darwin.m - Objective-C implementation for WKWebView cache management
//
// Clears WKWebView's disk and memory cache on startup to ensure fresh content
// is always loaded. This is important because WKWebView can cache aggressively
// even when the server sends no-cache headers.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "cache_darwin.h"

void clearWebViewCache(void) {
    @autoreleasepool {
        // Define the data types to clear: disk cache and memory cache only
        // We preserve localStorage, cookies, and other data to maintain user state
        NSSet<NSString *> *dataTypes = [NSSet setWithObjects:
            WKWebsiteDataTypeDiskCache,
            WKWebsiteDataTypeMemoryCache,
            nil];
        
        // Get the default data store (same one used by WKWebView)
        WKWebsiteDataStore *dataStore = [WKWebsiteDataStore defaultDataStore];
        
        // Use a semaphore to make this synchronous
        // This ensures cache is cleared before the webview starts loading
        dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
        
        // Remove all cached data since the beginning of time
        [dataStore removeDataOfTypes:dataTypes
                       modifiedSince:[NSDate distantPast]
                   completionHandler:^{
            dispatch_semaphore_signal(semaphore);
        }];
        
        // Wait for completion (max 5 seconds to avoid hanging)
        dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC);
        dispatch_semaphore_wait(semaphore, timeout);
        
        NSLog(@"[Mitto] WKWebView cache cleared");
    }
}

