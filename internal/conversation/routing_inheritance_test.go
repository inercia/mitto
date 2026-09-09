package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestResolveDispatchAgent_DefaultsInheritOrigin(t *testing.T) {
	origin := agentbackend.AgentRef{Backend: "acp", Provider: "auggie"}
	other := agentbackend.AgentRef{Backend: "ahp", Provider: "remote-agent"}

	tests := []struct {
		name     string
		override *DispatchOverride
	}{
		{"nil override", nil},
		{"zero-value override", &DispatchOverride{}},
		{"populated Agent but Allow=false", &DispatchOverride{Agent: other, Allow: false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDispatchAgent(origin, tt.override)
			if got != origin {
				t.Errorf("ResolveDispatchAgent() = %+v, want origin unchanged %+v (must never silently cross backend/provider boundaries)", got, origin)
			}
		})
	}
}

func TestResolveDispatchAgent_ExplicitOptInCrossesBoundary(t *testing.T) {
	origin := agentbackend.AgentRef{Backend: "acp", Provider: "auggie"}
	other := agentbackend.AgentRef{Backend: "ahp", Provider: "remote-agent"}

	got := ResolveDispatchAgent(origin, &DispatchOverride{Agent: other, Allow: true})
	if got != other {
		t.Errorf("ResolveDispatchAgent() with explicit opt-in = %+v, want override %+v", got, other)
	}
}
