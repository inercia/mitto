package agentbackend

import "context"

// Prompt delivers content as a local agent-message event, then completes the
// turn. If ctx is already cancelled (or is cancelled before the check runs),
// it publishes a Stopped lifecycle event and returns StopReasonCancelled
// alongside ctx.Err(), exercising explicit context-cancellation propagation.
func (h *fakeHost) Prompt(ctx context.Context, ref SessionRef, content []ContentBlock) (PromptOutcome, error) {
	if _, err := h.lookupSession(ref); err != nil {
		return PromptOutcome{}, err
	}

	select {
	case <-ctx.Done():
		h.publish(Event{Kind: EventLifecycle, Session: ref, Origin: OriginLocal, Lifecycle: LifecycleStopped})
		return PromptOutcome{StopReason: StopReasonCancelled}, ctx.Err()
	default:
	}

	h.publish(Event{Kind: EventAgentMessage, Session: ref, Origin: OriginLocal, Content: content})
	return PromptOutcome{StopReason: StopReasonEndTurn, Content: content}, nil
}

// Cancel requests cancellation of ref's in-flight turn, if any, and publishes
// a Stopped lifecycle event.
func (h *fakeHost) Cancel(ctx context.Context, ref SessionRef) error {
	if _, err := h.lookupSession(ref); err != nil {
		return err
	}
	h.publish(Event{Kind: EventLifecycle, Session: ref, Origin: OriginLocal, Lifecycle: LifecycleStopped})
	return nil
}

// SetModel switches ref's active model, returning a typed *UnsupportedError
// rather than faking success when the session doesn't support model
// selection.
func (h *fakeHost) SetModel(ctx context.Context, ref SessionRef, modelID string) error {
	s, err := h.lookupSession(ref)
	if err != nil {
		return err
	}
	if s.caps.Query(FeatureModelSelection) == CapabilityUnsupported {
		return &UnsupportedError{Feature: FeatureModelSelection}
	}
	s.mu.Lock()
	s.models.CurrentModelID = modelID
	models := s.models
	s.mu.Unlock()
	h.publish(Event{Kind: EventModelChange, Session: ref, Origin: OriginLocal, Models: &models})
	return nil
}

// SetMode switches ref's active mode, returning a typed *UnsupportedError
// rather than faking success when the session doesn't support mode
// selection.
func (h *fakeHost) SetMode(ctx context.Context, ref SessionRef, modeID string) error {
	s, err := h.lookupSession(ref)
	if err != nil {
		return err
	}
	if s.caps.Query(FeatureModeSelection) == CapabilityUnsupported {
		return &UnsupportedError{Feature: FeatureModeSelection}
	}
	s.mu.Lock()
	s.modes.CurrentModeID = modeID
	modes := s.modes
	s.mu.Unlock()
	h.publish(Event{Kind: EventModeChange, Session: ref, Origin: OriginLocal, Modes: &modes})
	return nil
}

// Subscribe registers fn for events on ref.ConversationID (or host-wide when
// ref is the zero value). Delivery is synchronous within publish, so
// Subscription.Close deterministically halts further delivery with no
// background goroutines to leak.
func (h *fakeHost) Subscribe(ctx context.Context, ref SessionRef, fn func(Event)) (Subscription, error) {
	key := ref.ConversationID
	sub := &fakeSubscription{host: h, key: key, fn: fn}
	h.mu.Lock()
	h.subs[key] = append(h.subs[key], sub)
	h.mu.Unlock()
	return sub, nil
}

// publish delivers ev, in order, to subscribers registered for
// ev.Session.ConversationID plus any host-wide subscribers (key ""). Locking
// is only held while copying the subscriber slice; fn is invoked outside the
// lock so subscriber callbacks may safely call back into the host.
func (h *fakeHost) publish(ev Event) {
	h.mu.Lock()
	scoped := h.subs[ev.Session.ConversationID]
	wide := h.subs[""]
	subs := make([]*fakeSubscription, 0, len(scoped)+len(wide))
	subs = append(subs, scoped...)
	subs = append(subs, wide...)
	h.mu.Unlock()
	for _, s := range subs {
		s.fn(ev)
	}
}

// InjectRemoteUpdate lets tests simulate an externally-originated update
// (e.g. another client mutated the session) that this host did not cause
// locally. Exposed to exercise the Origin de-dup requirement from
// contracts.go / ADR §3.
func (h *fakeHost) InjectRemoteUpdate(ref SessionRef, content []ContentBlock) {
	h.publish(Event{Kind: EventAgentMessage, Session: ref, Origin: OriginRemote, Content: content})
}

// SetCapability lets tests simulate a runtime capability change on ref's
// session and publishes the corresponding EventCapabilityChange.
func (h *fakeHost) SetCapability(ref SessionRef, feature Feature, state CapabilityState) {
	s, err := h.lookupSession(ref)
	if err != nil {
		return
	}
	s.caps.set(feature, state)
	h.publish(Event{Kind: EventCapabilityChange, Session: ref, Origin: OriginRemote, Capabilities: s.caps})
}
