package slackbridge

import (
	"context"
	"encoding/json"
	"errors"
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
	ack    func(*socketmode.Client, *socketmode.Request)
	// onSelfIdentified, if set, is invoked once (per Run) with the bridge's
	// own bot user ID, resolved via auth.test, before any events are
	// emitted — wired to Bridge.SetSelfUserID in production.
	onSelfIdentified func(userID string)
	// listEventAuthorizations resolves every installation authorized for one
	// event_context. Tests replace this seam to remain credential-free.
	listEventAuthorizations func(context.Context, string) ([]slack.EventAuthorization, error)
}

var errEventAuthorizationLookup = errors.New("slackbridge: event authorization lookup failed")

// NewSlackSource constructs a SlackSource. logger and onSelfIdentified may be nil.
func NewSlackSource(cfg Config, logger *slog.Logger, onSelfIdentified func(userID string)) *SlackSource {
	return &SlackSource{cfg: cfg, logger: logger, ack: ackSocketRequest, onSelfIdentified: onSelfIdentified}
}

// Run implements Source. It blocks until ctx is cancelled or the Socket Mode
// connection fails unrecoverably. Reconnects after a transient disconnect
// are handled internally by socketmode.Client.RunContext; Bridge.Run's outer
// retry loop is a second line of defense if RunContext still returns early.
func (s *SlackSource) Run(ctx context.Context, emit func(Event)) error {
	return s.run(ctx, func(event Event) error {
		emit(event)
		return nil
	})
}

// RunDurable acknowledges each relevant Socket Mode envelope only after accept
// confirms that the normalized event was persisted.
func (s *SlackSource) RunDurable(ctx context.Context, accept func(Event) error) error {
	return s.run(ctx, accept)
}

func (s *SlackSource) run(ctx context.Context, accept func(Event) error) error {
	api := slack.New(s.cfg.BotToken, slack.OptionAppLevelToken(s.cfg.AppToken))
	listEventAuthorizations := s.listEventAuthorizations
	if listEventAuthorizations == nil {
		listEventAuthorizations = api.ListEventAuthorizationsContext
	}

	selfUserID := ""
	if s.cfg.BotToken != "" {
		auth, err := api.AuthTestContext(ctx)
		if err != nil {
			return errors.New("slackbridge: auth.test failed")
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
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("slackbridge: socket mode connection failed")
		case evt, ok := <-client.Events:
			if !ok {
				return nil
			}
			if err := s.handleSocketEventDurableWithContext(ctx, evt, client, selfUserID, listEventAuthorizations, accept); err != nil && s.logger != nil {
				errorClass := "journal"
				if errors.Is(err, errEventAuthorizationLookup) {
					errorClass = "authorization"
				}
				s.logger.Warn("slackbridge: event not acknowledged because durable acceptance failed", "error_class", errorClass)
			}
		}
	}
}

// handleSocketEvent processes a single socketmode.Event: acknowledges
// events_api requests (required by the Slack protocol) and, for in-scope
// inner event types (message / app_mention), emits a normalized Event.
func (s *SlackSource) handleSocketEvent(evt socketmode.Event, client *socketmode.Client, selfUserID string, emit func(Event)) {
	_ = s.handleSocketEventDurable(evt, client, selfUserID, func(event Event) error {
		emit(event)
		return nil
	})
}

func (s *SlackSource) handleSocketEventDurable(evt socketmode.Event, client *socketmode.Client, selfUserID string, accept func(Event) error) error {
	return s.handleSocketEventDurableWithContext(context.Background(), evt, client, selfUserID, s.listEventAuthorizations, accept)
}

func (s *SlackSource) handleSocketEventDurableWithContext(ctx context.Context, evt socketmode.Event, client *socketmode.Client, selfUserID string,
	listEventAuthorizations func(context.Context, string) ([]slack.EventAuthorization, error), accept func(Event) error,
) error {
	if evt.Type != socketmode.EventTypeEventsAPI {
		return nil
	}
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return nil
	}

	metadata := extractEventMetadata(evt.Request)

	var out Event
	switch inner := eventsAPIEvent.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		// Only plain new messages are in scope: skip subtypes (edits,
		// joins, message_changed, ...) and any bot-authored message,
		// including Mitto's own app (self-filter by user ID as well, in
		// case BotID differs across workspaces). Slack represents public
		// message.channels and private message.groups events with the same
		// MessageEvent type; direct messages remain out of scope. Empty is
		// accepted for older/synthetic envelopes that omit channel_type.
		if (inner.ChannelType != "" && inner.ChannelType != slackevents.ChannelTypeChannel && inner.ChannelType != slackevents.ChannelTypeGroup) ||
			inner.SubType != "" || inner.BotID != "" || inner.User == selfUserID {
			s.ackRequest(client, evt.Request)
			return nil
		}
		out = Event{
			EventID: metadata.EventID, TeamID: eventsAPIEvent.TeamID, ChannelID: inner.Channel,
			AuthorID: inner.User, Kind: "message", Timestamp: inner.TimeStamp,
			ThreadTimestamp: inner.ThreadTimeStamp, Text: inner.Text,
		}
	case *slackevents.AppMentionEvent:
		if inner.BotID != "" || inner.User == selfUserID {
			s.ackRequest(client, evt.Request)
			return nil
		}
		out = Event{
			EventID: metadata.EventID, TeamID: eventsAPIEvent.TeamID, ChannelID: inner.Channel,
			AuthorID: inner.User, Kind: "app_mention", Timestamp: inner.TimeStamp,
			ThreadTimestamp: inner.ThreadTimeStamp, Text: inner.Text,
		}
	default:
		s.ackRequest(client, evt.Request)
		return nil
	}
	authorizations, known, err := resolveEventAuthorizations(ctx, metadata, listEventAuthorizations)
	if err != nil {
		return err
	}
	out.Authorizations = authorizations
	out.AuthorizationScopeKnown = known
	if err := accept(out); err != nil {
		return err
	}
	s.ackRequest(client, evt.Request)
	return nil
}

type socketEventMetadata struct {
	EventID               string
	EventContext          string
	Authorizations        []EventAuthorization
	AuthorizationsPresent bool
}

func extractEventMetadata(req *socketmode.Request) socketEventMetadata {
	if req == nil {
		return socketEventMetadata{}
	}
	var envelope struct {
		EventID        string          `json:"event_id"`
		EventContext   string          `json:"event_context"`
		Authorizations json.RawMessage `json:"authorizations"`
	}
	if err := json.Unmarshal(req.Payload, &envelope); err != nil {
		return socketEventMetadata{}
	}
	metadata := socketEventMetadata{EventID: envelope.EventID, EventContext: envelope.EventContext,
		AuthorizationsPresent: envelope.Authorizations != nil}
	if !metadata.AuthorizationsPresent {
		return metadata
	}
	var authorizations []EventAuthorization
	if err := json.Unmarshal(envelope.Authorizations, &authorizations); err == nil {
		metadata.Authorizations = authorizations
	}
	return metadata
}

func resolveEventAuthorizations(ctx context.Context, metadata socketEventMetadata,
	listEventAuthorizations func(context.Context, string) ([]slack.EventAuthorization, error),
) ([]EventAuthorization, bool, error) {
	if metadata.EventContext == "" || listEventAuthorizations == nil {
		return metadata.Authorizations, metadata.AuthorizationsPresent, nil
	}
	authorizations, err := listEventAuthorizations(ctx, metadata.EventContext)
	if err != nil {
		return nil, true, errEventAuthorizationLookup
	}
	result := make([]EventAuthorization, 0, len(authorizations))
	for _, authorization := range authorizations {
		result = append(result, EventAuthorization{UserID: authorization.UserID, IsBot: authorization.IsBot})
	}
	return result, true, nil
}

func (s *SlackSource) ackRequest(client *socketmode.Client, request *socketmode.Request) {
	if s.ack != nil {
		s.ack(client, request)
	}
}

func ackSocketRequest(client *socketmode.Client, request *socketmode.Request) {
	if request != nil {
		client.Ack(*request)
	}
}

// extractEventID reads the outer envelope's "event_id" from the raw Socket
// Mode payload. slackevents.ParseEvent's return type (EventsAPIEvent) drops
// this field, so it must be read from the raw JSON directly.
func extractEventID(req *socketmode.Request) string {
	return extractEventMetadata(req).EventID
}
