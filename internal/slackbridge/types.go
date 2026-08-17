// Package slackbridge pools process-scoped Slack Socket Mode connections by app
// profile and fans normalized events out to canonical onSlack loop subscriptions.
// The legacy environment-configured single-target Bridge remains as a deprecated
// compatibility adapter.
package slackbridge

import "context"

// Event is a normalized, bounded representation of a single Slack message or
// app_mention event. All fields are plain strings so the event can be safely
// copied into template/log contexts without risking accidental exposure of
// SDK-internal structures. Text is UNTRUSTED external content — callers must
// never treat it as instructions from a trusted party (see
// conversation.PromptSlackContext).
type Event struct {
	// EventID is Slack's event_id, used for de-duplication of redelivered events.
	EventID string
	// TeamID is the Slack workspace/team ID the event originated from.
	TeamID string
	// ChannelID is the Slack channel ID the message was posted to.
	ChannelID string
	// AuthorID is the Slack user ID (or bot ID) that authored the message.
	AuthorID string
	// Kind identifies the originating Slack event type: "message" or "app_mention".
	Kind string
	// Timestamp is the Slack message timestamp ("ts"), Slack's de-facto message ID.
	Timestamp string
	// ThreadTimestamp is the parent thread's timestamp ("thread_ts"), empty if
	// the message is not part of a thread.
	ThreadTimestamp string
	// Text is the raw message text. UNTRUSTED external content.
	Text string
}

// Source is the minimal injectable event-source lifecycle seam. Run blocks,
// delivering each accepted Event via emit, until ctx is cancelled or an
// unrecoverable error occurs. Implementations are expected to handle their
// own reconnect logic internally where practical (e.g. slack-go's Socket
// Mode client already reconnects on transient disconnects); Bridge.Run adds
// an outer retry loop as a second line of defense for a Run call that
// returns early with a non-nil, non-context error.
type Source interface {
	Run(ctx context.Context, emit func(Event)) error
}

// DurableSource delays Socket Mode acknowledgement until accept confirms the
// normalized event has reached durable storage. Returning an error leaves the
// envelope unacknowledged so Slack can redeliver it.
type DurableSource interface {
	RunDurable(ctx context.Context, accept func(Event) error) error
}
