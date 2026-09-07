package agentbackend

import (
	"context"
	"fmt"
	"sync"
)

// fakeHost is a non-process, in-memory backend used to prove out the
// contracts in contracts.go without spawning any OS process or depending on
// a protocol SDK. It models: multiple providers on one host, remote-owned
// sessions (no subprocess, no exec), reconnect (with a sequence-gap signal),
// runtime capability changes, and externally-originated (Origin=Remote)
// updates. All delivery is synchronous and goroutine-free, so there is
// nothing to leak on Subscription.Close.
type fakeHost struct {
	mu        sync.Mutex
	state     LifecycleState
	providers []ProviderID
	sessions  map[SessionRef]*fakeSession
	nextSeq   int
	subs      map[string][]*fakeSubscription // key: ConversationID, "" = host-wide
}

// NewFakeHost creates a fake backend host advertising the given providers
// (defaulting to a single "fake-provider" when none are given). Intended for
// contract tests and validation only — not a production backend.
func NewFakeHost(providers ...ProviderID) *fakeHost {
	if len(providers) == 0 {
		providers = []ProviderID{"fake-provider"}
	}
	return &fakeHost{
		state:     LifecycleDisconnected,
		providers: providers,
		sessions:  make(map[SessionRef]*fakeSession),
		subs:      make(map[string][]*fakeSubscription),
	}
}

func (h *fakeHost) Connect(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = LifecycleConnected
	return nil
}

func (h *fakeHost) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = LifecycleStopped
	return nil
}

func (h *fakeHost) State() LifecycleState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *fakeHost) Providers(ctx context.Context) ([]ProviderID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ProviderID, len(h.providers))
	copy(out, h.providers)
	return out, nil
}

func (h *fakeHost) hasProviderLocked(p ProviderID) bool {
	for _, x := range h.providers {
		if x == p {
			return true
		}
	}
	return false
}

func (h *fakeHost) NewSession(ctx context.Context, provider ProviderID) (Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.hasProviderLocked(provider) {
		return nil, fmt.Errorf("agentbackend: unknown provider %q: %w", provider, ErrSessionNotFound)
	}
	h.nextSeq++
	ref := SessionRef{
		ConversationID:  fmt.Sprintf("conv-%d", h.nextSeq),
		Provider:        provider,
		ProviderSession: ProviderSessionID(fmt.Sprintf("sess-%d", h.nextSeq)),
	}
	s := &fakeSession{ref: ref, caps: newFakeCapabilities()}
	h.sessions[ref] = s
	return s, nil
}

func (h *fakeHost) LoadSession(ctx context.Context, ref SessionRef) (Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[ref]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

// ResumeSession simulates a reconnect: it publishes a lifecycle event
// carrying a non-empty UpstreamCursor as a sequence-gap signal before
// returning the resumed session, so subscribers can detect they may have
// missed events while disconnected.
func (h *fakeHost) ResumeSession(ctx context.Context, ref SessionRef) (Session, error) {
	h.mu.Lock()
	s, ok := h.sessions[ref]
	seq := h.nextSeq
	h.mu.Unlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	h.publish(Event{
		Kind:           EventLifecycle,
		Session:        ref,
		Origin:         OriginRemote,
		Lifecycle:      LifecycleReconnected,
		UpstreamCursor: fmt.Sprintf("gap-%d", seq),
	})
	return s, nil
}

func (h *fakeHost) lookupSession(ref SessionRef) (*fakeSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[ref]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return s, nil
}
