package acpproc

import (
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestSharedACPClientCapabilities_OptsIntoOpenCodeChildUpdatesOnly(t *testing.T) {
	for _, tc := range []struct {
		server string
		want   bool
	}{
		{server: "opencode", want: true},
		{server: "custom-opencode", want: false},
		{server: "claude", want: false},
		{server: "", want: false},
	} {
		t.Run(tc.server, func(t *testing.T) {
			got := sharedACPClientCapabilities(tc.server)
			if got.Fs.ReadTextFile != true || got.Fs.WriteTextFile != true {
				t.Fatalf("filesystem capabilities changed: %+v", got.Fs)
			}
			if enabled, _ := got.Meta[agentbackend.OpenCodeChildSessionUpdatesMetaKey].(bool); enabled != tc.want {
				t.Fatalf("child update opt-in = %v, want %v (meta=%v)", enabled, tc.want, got.Meta)
			}
		})
	}
}
