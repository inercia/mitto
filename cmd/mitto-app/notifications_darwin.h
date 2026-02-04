// notifications_darwin.h - Header for macOS native notifications
// Uses UNUserNotificationCenter (macOS 10.14+)

#ifndef NOTIFICATIONS_DARWIN_H
#define NOTIFICATIONS_DARWIN_H

// Permission status values
#define NOTIFICATION_PERMISSION_NOT_DETERMINED 0
#define NOTIFICATION_PERMISSION_DENIED 1
#define NOTIFICATION_PERMISSION_AUTHORIZED 2

// initNotificationCenter initializes the notification center and sets up the delegate.
// Must be called once before using other notification functions.
// This should be called after the app is running.
void initNotificationCenter(void);

// requestNotificationPermission requests permission to show notifications.
// This is asynchronous - the result is returned via goNotificationPermissionCallback.
void requestNotificationPermission(void);

// getNotificationPermissionStatus returns the current permission status synchronously.
// Returns one of the NOTIFICATION_PERMISSION_* values.
// Note: This blocks briefly while querying the system.
int getNotificationPermissionStatus(void);

// showNativeNotification posts a notification to the macOS Notification Center.
// title: The notification title (e.g., session name)
// body: The notification body text (e.g., "Agent completed")
// sessionId: Identifier used for grouping and click handling
// Returns 0 on success, non-zero on failure.
int showNativeNotification(const char* title, const char* body, const char* sessionId);

// removeNotificationsForSession removes all delivered notifications for a session.
// sessionId: The session identifier
void removeNotificationsForSession(const char* sessionId);

#endif // NOTIFICATIONS_DARWIN_H

