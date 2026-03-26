# WebSocket Synchronization Testing

## Overview

Mobile clients (iOS Safari) and browser tabs go offline and reconnect regularly. When they
reconnect they must catch up on missed events using `load_events` with an `after_seq` watermark.
At startup a user with 5 open tabs triggers 5 simultaneous `load_events` requests — the
"thundering herd" — which could serialize and cause 30 s timeouts if the handler blocks.

The test infrastructure in `tests/integration/inprocess/` covers both the correctness of the
sync protocol and the concurrency behaviour of the server.

---

## Test Infrastructure

### TestEventInjector

**File:** `tests/integration/inprocess/event_injector.go`

Injects synthetic events directly into the `session.Store` without going through the ACP pipeline.
Use it to build up a deterministic event history in a session before connecting clients.

```go
injector := NewEventInjector(t, ts, sessionID)

// Inject 20 rounds (20 user_prompt + 20 agent_message = 40 events)
firstSeq, lastSeq := injector.InjectMixed(20)

// Also available:
injector.InjectAgentMessages(n)
injector.InjectUserPrompts(n)
maxSeq := injector.CurrentMaxSeq()
```

**Note:** The `BackgroundSession` in-memory sequence counter is NOT updated by these calls.
The `load_events` handler reads directly from the store, so reconnecting clients still receive
the injected events correctly.

### SleepWakeScenario

**File:** `tests/integration/inprocess/sleep_wake_helpers.go`

Orchestrates the full sleep/wake lifecycle in a single call:

1. Connect a client and register as observer
2. Inject events before sleep (`RoundsBeforeSleep`)
3. Disconnect (sleep)
4. Inject events while disconnected (`RoundsDuringSleep`)
5. Reconnect and call `LoadEvents(200, lastSeqBeforeSleep, 0)`
6. Validate that only the missed events are returned

```go
result := (&SleepWakeScenario{
    Server:            ts,
    SessionID:         sess.SessionID,
    RoundsBeforeSleep: 5,
    RoundsDuringSleep: 10,
}).Run(t)

if !result.ReceivedCorrectEvents {
    t.Error("client did not receive exactly the missed events")
}
```

**SleepWakeResult fields:**

| Field | Meaning |
|-------|---------|
| `LastSeqBeforeSleep` | Highest seq in store after pre-sleep injection |
| `LastSeqAfterSleep`  | Highest seq after during-sleep injection |
| `EventsAfterReconnect` | Events returned by `load_events` on reconnect |
| `ReceivedCorrectEvents` | `true` iff count == RoundsDuringSleep×2 and all seq > LastSeqBeforeSleep |

### client.Session: EnableMessageRecording / LastLoadEventsAfterSeq

**File:** `internal/client/session.go`

Records all outgoing WebSocket messages so you can assert the exact `after_seq` value the
client sent on reconnect:

```go
sess.EnableMessageRecording()
sess.LoadEvents(200, lastKnownSeq, 0)

afterSeq := sess.LastLoadEventsAfterSeq() // returns lastKnownSeq
assert.Equal(t, lastKnownSeq, afterSeq)
```

### window.__debug (Playwright)

The frontend exposes runtime state for Playwright assertions:

```javascript
// In tests/ui/
const debug = await page.evaluate(() => window.__debug);
expect(debug.lastKnownSeq).toBeGreaterThan(0);
expect(debug.afterSeq).toEqual(debug.lastKnownSeq); // correct watermark on reconnect
```

---

## Test Gap Coverage

| Gap | Test(s) | Status |
|-----|---------|--------|
| Long offline period with many events | `TestSleepWake_Basic` | ✅ Covered |
| Precise watermark verification (after_seq) | `client.Session.LastLoadEventsAfterSeq` helper | ✅ Covered |
| Thundering herd at startup | `TestThunderingHerd_MultipleSessionsSync` | ✅ Covered |
| Multiple simultaneous cold-start loads | `TestThunderingHerd_MultipleSessionsSync` | ✅ Covered |
| Late joiner sees full history | `TestMultiClientSync_LateJoinerSeesHistory` | ✅ Covered |
| No duplicate messages on reconnect | `TestMultiClientSync_NoDuplicateMessages` | ✅ Covered |
| Stale after_seq recovery (too-high watermark) | `TestMultiClientSync_StaleSyncRecovery` | ✅ Covered |
| lastKnownSeq persistence (browser) | `tests/ui/specs/sleep-wake-sync.spec.ts` | ✅ Covered |
| after_seq wire assertion (browser) | `tests/ui/specs/sleep-wake-sync.spec.ts` | ✅ Covered |
| True zombie connection (conn open, no load_events) | Not yet covered | ⚠️ Planned |
| has_more pagination (> limit events) | Partially via large injection counts | ⚠️ Partial |

---

## Running the Tests

### Go Integration Tests

```bash
# Build mock ACP server first (required once per build)
make build-mock-acp

# Run all sync-related tests
go test -v -tags integration -run "TestSleepWake|TestThunderingHerd|TestMultiClientSync" \
    ./tests/integration/inprocess/

# Run only the thundering herd test
go test -v -tags integration -run "TestThunderingHerd" \
    ./tests/integration/inprocess/
```

### Playwright Tests

```bash
cd tests/ui
npx playwright test specs/sleep-wake-sync.spec.ts
```

---

## Design Notes

- **Why inject directly into the store?** The `BackgroundSession` ACP subprocess is not running
  during these tests (it would be a race to create real ACP events). Direct store injection lets
  us create arbitrarily large, deterministic event histories in milliseconds.

- **Why measure wall-clock time in the thundering herd test?** The original bug was all 5 sessions
  timing out at 30 s each. Even with 5 goroutines, serialized handler locking would push total
  time past 15 s. The test uses a 15 s hard timeout and a 10 s soft assertion.

- **Session initialization event (+1 offset):** Every newly created session receives one
  initialization event before any injection. `InjectMixed(20)` therefore starts at seq 2.
  Assertions use `>= roundsPerSess*2` rather than exact equality to tolerate this offset.

