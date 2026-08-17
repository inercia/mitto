package slackbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SlackSource is the production Source implementation (mitto-qewp PoC): a
// single process-scoped Socket Mode listener for the Slack Events API.
// Never logs cfg.AppToken or cfg.BotToken.
type SlackSource struct {
	cfg    Config
	logger *slog.Logger
	// onSelfIdentified, if set, is invoked once (per Run) with the bridge's
	// own bot user ID, resolved via auth.test, before any events are
	// emitted — wired to Bridge.SetSelfUserID in production.
	onSelfIdentified func(userID string)
}

// NewSlackSource constructs a SlackSource. logger and onSelfIdentified may be nil.
func NewSlackSource(cfg Config, logger *slog.Logger, onSelfIdentified func(userID string)) *SlackSource {
	return &SlackSource{cfg: cfg, logger: logger, onSelfIdentified: onSelfIdentified}
}

// Run implements Source. It blocks until ctx is cancelled or the Socket Mode
// connection fails unrecoverably. Reconnects after a transient disconnect
// are handled internally by socketmode.Client.RunContext; Bridge.Run's outer
// retry loop is a second line of defense if RunContext still returns early.
func (s *SlackSource) Run(ctx context.Context, emit func(Event)) error {
	api := slack.New(s.cfg.BotToken, slack.OptionAppLevelToken(s.cfg.AppToken))

	selfUserID := ""
	if s.cfg.BotToken != "" {
		auth, err := api.AuthTestContext(ctx)
		if err != nil {
			return fmt.Errorf("slackbridge: auth.test failed: %w", err)
		}
		selfUserID = auth.UserID
		if s.onSelfIdentified != nil {
			s.onSelfIdentified(selfUserID)
		}
	}

	client := socketmode.New(api)

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- client.RunContext(ctx) }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-runErrCh:
			return err
		case evt, ok := <-client.Events:
			if !ok {
				return nil
			}
			s.handleSocketEvent(evt, client, selfUserID, emit)
		}
	}
}

// handleSocketEvent processes a single socketmode.Event: acknowledges
// events_api requests (required by the Slack protocol) and, for in-scope
// inner event types (message / app_mention), emits a normalized Event.
func (s *SlackSource) handleSocketEvent(evt socketmode.Event, client *socketmode.Client, selfUserID string, emit func(Event)) {
	if evt.Type != socketmode.EventTypeEventsAPI {
		return
	}
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if evt.Request != nil {
		client.Ack(*evt.Request)
	}

	eventID := extractEventID(evt.Request)

	var out Event
	switch inner := eventsAPIEvent.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		// Only plain new messages are in scope: skip subtypes (edits,
		// joins, message_changed, ...) and any bot-authored message,
		// including Mitto's own app (self-filter by user ID as well, in
		// case BotID differs across workspaces).
		if inner.SubType != "" || inner.BotID != "" || inner.User == selfUserID {
			return
		}
		out = Event{
			EventID: eventID, TeamID: eventsAPIEvent.TeamID, ChannelID: inner.Channel,
			AuthorID: inner.User, Kind: "message", Timestamp: inner.TimeStamp,
			ThreadTimestamp: inner.ThreadTimeStamp, Text: inner.Text,
		}
	case *slackevents.AppMentionEvent:
		if inner.BotID != "" || inner.User == selfUserID {
			return
		}
		out = Event{
			EventID: eventID, TeamID: eventsAPIEvent.TeamID, ChannelID: inner.Channel,
			AuthorID: inner.User, Kind: "app_mention", Timestamp: inner.TimeStamp,
			ThreadTimestamp: inner.ThreadTimeStamp, Text: inner.Text,
		}
	default:
		return
	}
	emit(out)
}

// extractEventID reads the outer envelope's "event_id" from the raw Socket
// Mode payload. slackevents.ParseEvent's return type (EventsAPIEvent) drops
// this field, so it must be read from the raw JSON directly.
func extractEventID(req *socketmode.Request) string {
	if req == nil {
		return ""
	}
	var envelope struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(req.Payload, &envelope); err != nil {
		return ""
	}
	return envelope.EventID
}
