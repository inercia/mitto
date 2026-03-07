// power_darwin.h - Header for macOS power management and screen sleep monitoring

#ifndef POWER_DARWIN_H
#define POWER_DARWIN_H

// startNetworkPowerAssertion creates a kIOPMAssertNetworkClientActive assertion
// so macOS does not suspend network activity when the screen locks or the system
// goes idle. This keeps the external listener reachable via Tailscale.
// Returns 0 on success, -1 on failure. Calling when already held is a no-op (returns 0).
int startNetworkPowerAssertion(void);

// stopNetworkPowerAssertion releases the IOPMAssertion if one is held.
// Safe to call even if no assertion is currently held.
void stopNetworkPowerAssertion(void);

// setupScreenSleepMonitoring registers NSWorkspace observers for screen sleep/wake events.
// On screen sleep/lock it calls goScreenDidSleepCallback().
// On screen/system wake it calls goScreenDidWakeCallback().
// Must be called from the main thread after the app is running.
void setupScreenSleepMonitoring(void);

#endif // POWER_DARWIN_H
