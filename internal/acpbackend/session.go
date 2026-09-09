package acpbackend

import (
	"sync"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// acpSession is the agentbackend.Session implementation returned by
// Connection's SessionOps methods. It wraps the conversation.SessionHandle
// obtained from the underlying SharedProcess plus the SessionRef the adapter
// assigned this session.
type acpSession struct {
	ref    agentbackend.SessionRef
	handle *conversation.SessionHandle

	mu   sync.Mutex
	caps *sessionCapabilities
}

// Ref implements agentbackend.Session.
func (s *acpSession) Ref() agentbackend.SessionRef { return s.ref }

// Capabilities implements agentbackend.Session.
func (s *acpSession) Capabilities() agentbackend.Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

// setCapabilities updates the session's capability snapshot (e.g. after a
// reload). Safe for concurrent use.
func (s *acpSession) setCapabilities(c *sessionCapabilities) {
	s.mu.Lock()
	s.caps = c
	s.mu.Unlock()
}

var _ agentbackend.Session = (*acpSession)(nil)
