package agentbackend

import "sync"

// fakeSession is the fakeHost's in-memory Session implementation.
type fakeSession struct {
	ref SessionRef

	mu     sync.Mutex
	caps   *fakeCapabilities
	models ModelState
	modes  ModeState
}

func (s *fakeSession) Ref() SessionRef { return s.ref }

func (s *fakeSession) Capabilities() Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

// fakeCapabilities is a mutable, concurrency-safe Capabilities implementation
// used by fakeSession. Model selection defaults to Supported and mode
// selection to Unsupported, with an unmodeled feature (terminals) left at
// Unknown, so contract tests can exercise all three CapabilityState values
// without extra setup.
type fakeCapabilities struct {
	mu    sync.Mutex
	state map[Feature]CapabilityState
}

func newFakeCapabilities() *fakeCapabilities {
	return &fakeCapabilities{state: map[Feature]CapabilityState{
		FeatureImages:         CapabilitySupported,
		FeatureFiles:          CapabilitySupported,
		FeaturePermissions:    CapabilitySupported,
		FeatureModelSelection: CapabilitySupported,
		FeatureModeSelection:  CapabilityUnsupported,
		// FeatureTerminals is intentionally left unset: Query returns
		// CapabilityUnknown for it, proving Unknown is never conflated with
		// Unsupported.
	}}
}

func (c *fakeCapabilities) Query(feature Feature) CapabilityState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.state[feature]; ok {
		return s
	}
	return CapabilityUnknown
}

func (c *fakeCapabilities) set(feature Feature, state CapabilityState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state[feature] = state
}

// fakeSubscription is the fakeHost's Subscription implementation. Close
// removes fn from the host's subscriber list so no further events are
// delivered to it; there is no background goroutine to leak.
type fakeSubscription struct {
	host   *fakeHost
	key    string
	fn     func(Event)
	closed bool
}

func (s *fakeSubscription) Close() error {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	list := s.host.subs[s.key]
	for i, x := range list {
		if x == s {
			s.host.subs[s.key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	return nil
}

// Compile-time assertions that the fake satisfies the neutral contracts,
// mirroring acpproc's `var _ conversation.SharedProcess = (*SharedACPProcess)(nil)`
// convention so interface drift fails the build here rather than at call sites.
var (
	_ Connection        = (*fakeHost)(nil)
	_ ProviderDiscovery = (*fakeHost)(nil)
	_ SessionOps        = (*fakeHost)(nil)
	_ EventDelivery     = (*fakeHost)(nil)
	_ Session           = (*fakeSession)(nil)
	_ Capabilities      = (*fakeCapabilities)(nil)
	_ Subscription      = (*fakeSubscription)(nil)
)
