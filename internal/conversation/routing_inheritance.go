package conversation

// routing_inheritance.go defines the pure decision helper for which
// backend+provider an auxiliary, processor, or child dispatch should use
// (mitto-lrt.10 acceptance criteria: "Processor and child routing never
// silently crosses backend/provider boundaries"). The default is always to
// inherit the originating conversation's AgentRef; a cross-backend fallback
// is taken ONLY when a caller explicitly opts in.

import "github.com/inercia/mitto/internal/agentbackend"

// DispatchOverride is an explicit, caller-supplied opt-in to route a
// dispatch to a different backend+provider than the one the originating
// conversation is attached to. The zero value means "no override" — even a
// populated Agent has no effect unless Allow is explicitly true, so a
// caller cannot accidentally opt in by merely constructing this struct.
type DispatchOverride struct {
	// Agent is the backend+provider to route to instead of the originating
	// conversation's AgentRef.
	Agent agentbackend.AgentRef
	// Allow must be explicitly true for Agent to take effect.
	Allow bool
}

// ResolveDispatchAgent decides which backend+provider an auxiliary,
// processor, or child dispatch should use. origin is the originating
// conversation's current AgentRef. override, when non-nil with Allow set,
// is used instead — this is the ONLY path by which a dispatch may cross
// backend or provider boundaries; every other case (nil override, or
// Allow: false) always inherits origin unchanged, so conversation contents
// are never silently sent to a different provider.
func ResolveDispatchAgent(origin agentbackend.AgentRef, override *DispatchOverride) agentbackend.AgentRef {
	if override != nil && override.Allow {
		return override.Agent
	}
	return origin
}
