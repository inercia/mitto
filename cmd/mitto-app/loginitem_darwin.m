// loginitem_darwin.m - macOS login item (start at login) implementation
//
// This module manages the app's login item status using LaunchAgents.
// A plist file is created in ~/Library/LaunchAgents/ to start the app at login.
// This approach works on all macOS versions.

#import <Foundation/Foundation.h>
#include "loginitem_darwin.h"

// LaunchAgent label and filename
static NSString *const kLaunchAgentLabel = @"io.mitto.app";
static NSString *const kLaunchAgentFilename = @"io.mitto.app.plist";

// getLaunchAgentPath returns the path to the LaunchAgent plist file
static NSString *getLaunchAgentPath(void) {
    NSString *homeDir = NSHomeDirectory();
    return [NSString stringWithFormat:@"%@/Library/LaunchAgents/%@", homeDir, kLaunchAgentFilename];
}

// getExecutablePath returns the path to the current executable
static NSString *getExecutablePath(void) {
    return [[NSBundle mainBundle] executablePath];
}

// isLoginItemSupported always returns true (LaunchAgents work on all macOS versions)
bool isLoginItemSupported(void) {
    return true;
}

// isLoginItemEnabled checks if the LaunchAgent plist file exists
bool isLoginItemEnabled(void) {
    @autoreleasepool {
        NSString *plistPath = getLaunchAgentPath();
        return [[NSFileManager defaultManager] fileExistsAtPath:plistPath];
    }
}

// enableLoginItem creates the LaunchAgent plist file
int enableLoginItem(void) {
    @autoreleasepool {
        NSString *plistPath = getLaunchAgentPath();
        NSString *execPath = getExecutablePath();

        if (execPath == nil) {
            NSLog(@"Failed to get executable path");
            return 1;
        }

        // Create the LaunchAgents directory if it doesn't exist
        NSString *launchAgentsDir = [plistPath stringByDeletingLastPathComponent];
        NSError *dirError = nil;
        if (![[NSFileManager defaultManager] createDirectoryAtPath:launchAgentsDir
                                       withIntermediateDirectories:YES
                                                        attributes:nil
                                                             error:&dirError]) {
            if (dirError) {
                NSLog(@"Failed to create LaunchAgents directory: %@", dirError.localizedDescription);
                return (int)dirError.code;
            }
        }

        // Create the plist dictionary
        NSDictionary *plist = @{
            @"Label": kLaunchAgentLabel,
            @"ProgramArguments": @[execPath],
            @"RunAtLoad": @YES,
            @"KeepAlive": @NO,
            @"ProcessType": @"Interactive"
        };

        // Write the plist file
        NSError *writeError = nil;
        NSData *plistData = [NSPropertyListSerialization dataWithPropertyList:plist
                                                                       format:NSPropertyListXMLFormat_v1_0
                                                                      options:0
                                                                        error:&writeError];
        if (writeError) {
            NSLog(@"Failed to serialize plist: %@", writeError.localizedDescription);
            return (int)writeError.code;
        }

        if (![plistData writeToFile:plistPath options:NSDataWritingAtomic error:&writeError]) {
            NSLog(@"Failed to write LaunchAgent plist: %@", writeError.localizedDescription);
            return (int)writeError.code;
        }

        NSLog(@"Created LaunchAgent at: %@", plistPath);
        return 0;
    }
}

// disableLoginItem removes the LaunchAgent plist file
int disableLoginItem(void) {
    @autoreleasepool {
        NSString *plistPath = getLaunchAgentPath();

        // Check if the file exists
        if (![[NSFileManager defaultManager] fileExistsAtPath:plistPath]) {
            // Already disabled, nothing to do
            return 0;
        }

        // Remove the plist file
        NSError *error = nil;
        if (![[NSFileManager defaultManager] removeItemAtPath:plistPath error:&error]) {
            NSLog(@"Failed to remove LaunchAgent plist: %@", error.localizedDescription);
            return (int)error.code;
        }

        NSLog(@"Removed LaunchAgent at: %@", plistPath);
        return 0;
    }
}

