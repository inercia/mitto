// power_darwin.m - macOS power management and screen sleep/wake monitoring

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <IOKit/pwr_mgt/IOPMLib.h>

// Go callbacks declared here so Objective-C can call them
extern void goScreenDidSleepCallback(void);
extern void goScreenDidWakeCallback(void);

// Holds the active IOPMAssertion ID, or kIOPMNullAssertionID if none is held.
static IOPMAssertionID gPowerAssertionID = kIOPMNullAssertionID;

// startNetworkPowerAssertion creates a kIOPMAssertNetworkClientActive assertion.
// This tells macOS that the app has active network connections and should not
// be suspended when the screen locks or the system goes idle.
int startNetworkPowerAssertion(void) {
    if (gPowerAssertionID != kIOPMNullAssertionID) {
        // Already held — nothing to do.
        return 0;
    }
    IOReturn result = IOPMAssertionCreateWithName(
        kIOPMAssertNetworkClientActive,
        kIOPMAssertionLevelOn,
        CFSTR("Mitto external network listener"),
        &gPowerAssertionID
    );
    if (result != kIOReturnSuccess) {
        NSLog(@"[Mitto] Failed to create IOPMAssertion: %d", result);
        return -1;
    }
    NSLog(@"[Mitto] IOPMAssertion acquired (id=%u)", gPowerAssertionID);
    return 0;
}

// stopNetworkPowerAssertion releases the IOPMAssertion if one is currently held.
void stopNetworkPowerAssertion(void) {
    if (gPowerAssertionID != kIOPMNullAssertionID) {
        NSLog(@"[Mitto] Releasing IOPMAssertion (id=%u)", gPowerAssertionID);
        IOPMAssertionRelease(gPowerAssertionID);
        gPowerAssertionID = kIOPMNullAssertionID;
    }
}

// setupScreenSleepMonitoring registers for NSWorkspace screen and system sleep/wake
// notifications. Fires Go callbacks so the app can log the event and reconnect.
void setupScreenSleepMonitoring(void) {
    NSNotificationCenter *center = [[NSWorkspace sharedWorkspace] notificationCenter];

    // Screen locked / display off
    [center addObserverForName:NSWorkspaceScreensDidSleepNotification
                        object:nil
                         queue:[NSOperationQueue mainQueue]
                    usingBlock:^(NSNotification *note) {
        NSLog(@"[Mitto] NSWorkspaceScreensDidSleepNotification — screen locked/off");
        goScreenDidSleepCallback();
    }];

    // Full system sleep (lid close, etc.) — fires just before the system sleeps
    [center addObserverForName:NSWorkspaceWillSleepNotification
                        object:nil
                         queue:[NSOperationQueue mainQueue]
                    usingBlock:^(NSNotification *note) {
        NSLog(@"[Mitto] NSWorkspaceWillSleepNotification — system going to sleep");
        goScreenDidSleepCallback();
    }];

    // Screen woke (display on again, but system may still be resuming)
    [center addObserverForName:NSWorkspaceScreensDidWakeNotification
                        object:nil
                         queue:[NSOperationQueue mainQueue]
                    usingBlock:^(NSNotification *note) {
        NSLog(@"[Mitto] NSWorkspaceScreensDidWakeNotification — screen woke");
        goScreenDidWakeCallback();
    }];

    // Full system wake
    [center addObserverForName:NSWorkspaceDidWakeNotification
                        object:nil
                         queue:[NSOperationQueue mainQueue]
                    usingBlock:^(NSNotification *note) {
        NSLog(@"[Mitto] NSWorkspaceDidWakeNotification — system woke");
        goScreenDidWakeCallback();
    }];

    NSLog(@"[Mitto] Screen sleep/wake monitoring registered");
}
