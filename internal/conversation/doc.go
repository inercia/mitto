// Package conversation provides the runtime conversation domain for Mitto.
//
// It owns the live lifecycle of a conversation: process orchestration, prompt
// dispatch, observer fan-out, queue processing, and follow-up analysis.
// This is distinct from:
//
//   - [internal/session] — persistence only (Store, Recorder, Player, Queue, Flags)
//   - [internal/web] — HTTP and WebSocket transport layer
//
// The central types are BackgroundSession (a single running conversation that
// outlives any individual WebSocket connection), SessionManager (lifecycle
// owner for all conversations in a server process), and SessionObserver (the
// interface that transport-layer clients implement to receive real-time events).
package conversation
