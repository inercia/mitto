// notifications_darwin.m - Objective-C implementation for macOS native notifications
// Uses UNUserNotificationCenter (macOS 10.14+)

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include "notifications_darwin.h"

// Forward declaration of Go callback functions
extern void goNotificationPermissionCallback(int granted);
extern void goNotificationTappedCallback(const char* sessionId);

// Notification center delegate to handle user interactions
@interface MittoNotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
+ (instancetype)sharedDelegate;
@end

@implementation MittoNotificationDelegate

+ (instancetype)sharedDelegate {
    static MittoNotificationDelegate *delegate = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        delegate = [[MittoNotificationDelegate alloc] init];
    });
    return delegate;
}

// Called when notification is tapped
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void(^)(void))completionHandler {
    // Extract session ID from notification userInfo
    NSString *sessionId = response.notification.request.content.userInfo[@"sessionId"];
    if (sessionId) {
        goNotificationTappedCallback([sessionId UTF8String]);
    }
    completionHandler();
}

// Called when notification arrives while app is in foreground.
// Always show notifications with banner, sound, and list (Notification Center) so that
// sticky notifications persist in Notification Center even when the app is active.
// Previously we suppressed all notifications when active, but that prevented MCP
// notifications from being seen at all if the user was looking at Mitto.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void(^)(UNNotificationPresentationOptions))completionHandler {
    // UNNotificationPresentationOptionBanner and List were introduced in macOS 11.0.
    // Banner shows the temporary slide-in notification.
    // List adds it to Notification Center so sticky notifications persist.
    // For older systems, use the deprecated Alert option (suppressing warning).
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
    if (@available(macOS 11.0, *)) {
        completionHandler(UNNotificationPresentationOptionBanner |
                          UNNotificationPresentationOptionList |
                          UNNotificationPresentationOptionSound);
    } else {
        completionHandler(UNNotificationPresentationOptionAlert | UNNotificationPresentationOptionSound);
    }
#pragma clang diagnostic pop
}

@end

// Initialize the notification center with our delegate
void initNotificationCenter(void) {
    @autoreleasepool {
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
        center.delegate = [MittoNotificationDelegate sharedDelegate];
    }
}

// Request notification permission asynchronously
void requestNotificationPermission(void) {
    @autoreleasepool {
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
        [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
                              completionHandler:^(BOOL granted, NSError * _Nullable error) {
            if (error) {
                NSLog(@"Notification permission error: %@", error);
            }
            goNotificationPermissionCallback(granted ? 1 : 0);
        }];
    }
}

// Get current permission status synchronously
int getNotificationPermissionStatus(void) {
    __block int status = NOTIFICATION_PERMISSION_NOT_DETERMINED;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    
    @autoreleasepool {
        [[UNUserNotificationCenter currentNotificationCenter]
            getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
                switch (settings.authorizationStatus) {
                    case UNAuthorizationStatusAuthorized:
                    case UNAuthorizationStatusProvisional:
                        status = NOTIFICATION_PERMISSION_AUTHORIZED;
                        break;
                    case UNAuthorizationStatusDenied:
                        status = NOTIFICATION_PERMISSION_DENIED;
                        break;
                    default:
                        status = NOTIFICATION_PERMISSION_NOT_DETERMINED;
                        break;
                }
                dispatch_semaphore_signal(sem);
            }];
    }
    
    // Wait up to 2 seconds for the result
    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
    return status;
}

// Show a native notification
// When sticky is 0, the notification auto-dismisses after 5 seconds.
// When sticky is 1, the notification persists in Notification Center until the user dismisses it.
int showNativeNotification(const char* title, const char* body, const char* sessionId, int sticky) {
    @autoreleasepool {
        UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
        content.title = [NSString stringWithUTF8String:title];
        content.body = [NSString stringWithUTF8String:body];
        content.sound = [UNNotificationSound defaultSound];

        // Store session ID for click handling
        NSString *sessionIdStr = [NSString stringWithUTF8String:sessionId];
        content.userInfo = @{@"sessionId": sessionIdStr};

        // Group notifications by session
        content.threadIdentifier = sessionIdStr;

        // Create a unique identifier for this notification
        NSString *identifier = [NSString stringWithFormat:@"mitto-%@-%f",
                               sessionIdStr, [[NSDate date] timeIntervalSince1970]];

        // Create request with no trigger (show immediately)
        UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:identifier
                                                                              content:content
                                                                              trigger:nil];

        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];

        // Capture sticky as a local BOOL so the block can use it safely
        BOOL isSticky = (sticky != 0);
        NSLog(@"[Mitto] showNativeNotification: title=%@ sticky=%d isSticky=%d", content.title, sticky, isSticky);

        // Add to notification center
        [center addNotificationRequest:request
            withCompletionHandler:^(NSError * _Nullable error) {
                if (error) {
                    NSLog(@"Failed to show notification: %@", error);
                    return;
                }

                // Auto-remove the notification after 5 seconds to keep Notification Center clean,
                // unless sticky is requested — in that case, leave it until the user dismisses it.
                if (!isSticky) {
                    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC),
                                  dispatch_get_main_queue(), ^{
                        [center removeDeliveredNotificationsWithIdentifiers:@[identifier]];
                    });
                }
            }];

        return 0;
    }
}

// Set the dock badge count. Pass 0 to clear the badge.
void setDockBadgeCount(int count) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSDockTile *tile = [NSApp dockTile];
        if (count > 0) {
            tile.badgeLabel = [NSString stringWithFormat:@"%d", count];
        } else {
            tile.badgeLabel = nil;
        }
        // Force the dock tile to redraw immediately
        [tile display];
        NSLog(@"[Mitto] setDockBadgeCount: %d (badgeLabel=%@)", count, tile.badgeLabel);
    });
}

// Clear the dock badge (convenience wrapper).
void clearDockBadge(void) {
    setDockBadgeCount(0);
}

// Remove all notifications for a specific session
void removeNotificationsForSession(const char* sessionId) {
    @autoreleasepool {
        NSString *sessionIdStr = [NSString stringWithUTF8String:sessionId];
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
        
        // Get all delivered notifications and remove those matching the session
        [center getDeliveredNotificationsWithCompletionHandler:^(NSArray<UNNotification *> *notifications) {
            NSMutableArray *identifiersToRemove = [NSMutableArray array];
            for (UNNotification *notification in notifications) {
                NSString *notifSessionId = notification.request.content.userInfo[@"sessionId"];
                if ([notifSessionId isEqualToString:sessionIdStr]) {
                    [identifiersToRemove addObject:notification.request.identifier];
                }
            }
            if (identifiersToRemove.count > 0) {
                [center removeDeliveredNotificationsWithIdentifiers:identifiersToRemove];
            }
        }];
    }
}

