package acpproc

import (
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
)

// stubResolveCLI returns a resolveCLI stub yielding the given path and error.
func stubResolveCLI(path string, err error) func() (string, error) {
	return func() (string, error) { return path, err }
}

// processorPurpose builds a valid processor purpose string that satisfies
// strings.HasPrefix(purpose, auxiliary.PurposeProcessorPrefix).
func processorPurpose(name string) string {
	return auxiliary.PurposeProcessorPrefix + name
}

// TestBuildAuxProcessorMCPServers_HTTPWhenAgentAdvertisesHTTPCap covers the
// mitto-8ip acceptance criterion: "Aux processor session on an http-capable
// agent starts with an HTTP McpServer entry ... no `mitto mcp --proxy-to`
// child process".
func TestBuildAuxProcessorMCPServers_HTTPWhenAgentAdvertisesHTTPCap(t *testing.T) {
	caps := &acp.AgentCapabilities{
		McpCapabilities: acp.McpCapabilities{Http: true},
	}
	// resolveCLI must NOT be called on the HTTP branch — pass a stub that
	// fails loudly if invoked to guard against a regression that silently
	// falls through to stdio.
	failResolve := func() (string, error) {
		t.Fatalf("resolveCLI should not be called when agent advertises mcp_capabilities.http")
		return "", nil
	}

	servers, transport, exe := buildAuxProcessorMCPServers(
		processorPurpose("test"),
		"http://127.0.0.1:5757/mcp",
		caps,
		failResolve,
	)

	if transport != auxMCPTransportHTTP {
		t.Fatalf("transport = %d, want auxMCPTransportHTTP (%d)", transport, auxMCPTransportHTTP)
	}
	if exe != "" {
		t.Fatalf("exe = %q, want empty on HTTP branch", exe)
	}
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	s := servers[0]
	if s.Http == nil {
		t.Fatalf("servers[0].Http is nil, want non-nil McpServerHttpInline")
	}
	if s.Stdio != nil {
		t.Fatalf("servers[0].Stdio = %+v, want nil (no stdio subprocess on HTTP branch)", s.Stdio)
	}
	if s.Http.Name != "mitto" {
		t.Errorf("servers[0].Http.Name = %q, want %q", s.Http.Name, "mitto")
	}
	if s.Http.Type != "http" {
		t.Errorf("servers[0].Http.Type = %q, want %q", s.Http.Type, "http")
	}
	if s.Http.Url != "http://127.0.0.1:5757/mcp" {
		t.Errorf("servers[0].Http.Url = %q, want the passed mcpServerURL", s.Http.Url)
	}
	if s.Http.Headers == nil {
		t.Errorf("servers[0].Http.Headers is nil, want non-nil (empty) slice — ACP validates this")
	}
}

// TestBuildAuxProcessorMCPServers_StdioFallbackWhenNoHTTPCap covers the
// mitto-8ip regression criterion: "Aux processor session on a non-http agent
// still gets the stdio proxy".
func TestBuildAuxProcessorMCPServers_StdioFallbackWhenNoHTTPCap(t *testing.T) {
	cases := []struct {
		name string
		caps *acp.AgentCapabilities
	}{
		{"nil caps (mid-restart)", nil},
		{"caps present, Http=false", &acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: false}}},
		{"caps present, other transport (Sse) only", &acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Sse: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			servers, transport, exe := buildAuxProcessorMCPServers(
				processorPurpose("test"),
				"http://127.0.0.1:5757/mcp",
				tc.caps,
				stubResolveCLI("/opt/mitto/bin/mitto", nil),
			)

			if transport != auxMCPTransportStdio {
				t.Fatalf("transport = %d, want auxMCPTransportStdio (%d)", transport, auxMCPTransportStdio)
			}
			if exe != "/opt/mitto/bin/mitto" {
				t.Errorf("exe = %q, want the resolved CLI path", exe)
			}
			if len(servers) != 1 {
				t.Fatalf("len(servers) = %d, want 1", len(servers))
			}
			s := servers[0]
			if s.Stdio == nil {
				t.Fatalf("servers[0].Stdio is nil, want non-nil McpServerStdio (regression: HTTP branch chosen without http cap)")
			}
			if s.Http != nil {
				t.Errorf("servers[0].Http = %+v, want nil on stdio branch", s.Http)
			}
			if s.Stdio.Name != "mitto" {
				t.Errorf("servers[0].Stdio.Name = %q, want %q", s.Stdio.Name, "mitto")
			}
			if s.Stdio.Command != "/opt/mitto/bin/mitto" {
				t.Errorf("servers[0].Stdio.Command = %q, want CLI path", s.Stdio.Command)
			}
			wantArgs := []string{"mcp", "--proxy-to", "http://127.0.0.1:5757/mcp"}
			if len(s.Stdio.Args) != len(wantArgs) {
				t.Fatalf("servers[0].Stdio.Args = %v, want %v", s.Stdio.Args, wantArgs)
			}
			for i, a := range s.Stdio.Args {
				if a != wantArgs[i] {
					t.Errorf("servers[0].Stdio.Args[%d] = %q, want %q", i, a, wantArgs[i])
				}
			}
			if s.Stdio.Env == nil {
				t.Errorf("servers[0].Stdio.Env is nil, want non-nil (empty) slice — ACP validates this")
			}
		})
	}
}

// TestBuildAuxProcessorMCPServers_NoInjectionForNonProcessorPurpose ensures
// non-processor auxiliary sessions (title generation, follow-up, etc.) never
// receive a Mitto MCP entry regardless of capability advertisement.
func TestBuildAuxProcessorMCPServers_NoInjectionForNonProcessorPurpose(t *testing.T) {
	caps := &acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}}

	servers, transport, exe := buildAuxProcessorMCPServers(
		"title-generation",
		"http://127.0.0.1:5757/mcp",
		caps,
		stubResolveCLI("/opt/mitto/bin/mitto", nil),
	)

	if transport != auxMCPTransportNone {
		t.Fatalf("transport = %d, want auxMCPTransportNone (%d) for non-processor purpose", transport, auxMCPTransportNone)
	}
	if exe != "" {
		t.Errorf("exe = %q, want empty for non-processor purpose", exe)
	}
	if servers == nil {
		t.Fatalf("servers is nil; ACP requires an empty non-nil slice")
	}
	if len(servers) != 0 {
		t.Errorf("len(servers) = %d, want 0 for non-processor purpose", len(servers))
	}
}

// TestBuildAuxProcessorMCPServers_NoInjectionWhenMCPURLUnset covers the
// existing guard: no MCP URL means no injection even for processor sessions.
func TestBuildAuxProcessorMCPServers_NoInjectionWhenMCPURLUnset(t *testing.T) {
	caps := &acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}}

	servers, transport, _ := buildAuxProcessorMCPServers(
		processorPurpose("test"),
		"", // no MCP URL configured
		caps,
		stubResolveCLI("/opt/mitto/bin/mitto", nil),
	)

	if transport != auxMCPTransportNone {
		t.Fatalf("transport = %d, want auxMCPTransportNone when mcpServerURL is empty", transport)
	}
	if servers == nil {
		t.Fatalf("servers is nil; ACP requires an empty non-nil slice")
	}
	if len(servers) != 0 {
		t.Errorf("len(servers) = %d, want 0 when mcpServerURL is empty", len(servers))
	}
}

// TestBuildAuxProcessorMCPServers_StdioFallbackDegradesGracefullyWhenCLIUnresolvable
// covers the edge case where the stdio fallback path is chosen but the mitto
// CLI binary cannot be resolved (e.g. missing sibling in an unusual install).
// The function must return a non-nil empty McpServers slice rather than a
// half-built entry — matching the pre-refactor behavior of skipping the block.
func TestBuildAuxProcessorMCPServers_StdioFallbackDegradesGracefullyWhenCLIUnresolvable(t *testing.T) {
	servers, transport, exe := buildAuxProcessorMCPServers(
		processorPurpose("test"),
		"http://127.0.0.1:5757/mcp",
		&acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: false}},
		stubResolveCLI("", errors.New("no mitto sibling binary")),
	)

	if transport != auxMCPTransportNone {
		t.Fatalf("transport = %d, want auxMCPTransportNone when CLI resolution fails", transport)
	}
	if exe != "" {
		t.Errorf("exe = %q, want empty when CLI resolution fails", exe)
	}
	if servers == nil {
		t.Fatalf("servers is nil; ACP requires an empty non-nil slice")
	}
	if len(servers) != 0 {
		t.Errorf("len(servers) = %d, want 0 when CLI resolution fails", len(servers))
	}
}
