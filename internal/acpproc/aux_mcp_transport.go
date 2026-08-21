package acpproc

import (
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/auxiliary"
)

// auxMCPTransport enumerates the transport choice made for a processor
// auxiliary session's Mitto MCP server entry (mitto-8ip).
type auxMCPTransport int

const (
	// auxMCPTransportNone means no Mitto MCP server was injected — either the
	// session is not a processor-scoped aux session, no MCP URL is configured,
	// or the stdio-proxy fallback could not resolve the mitto CLI binary.
	auxMCPTransportNone auxMCPTransport = iota
	// auxMCPTransportHTTP means a native HTTP McpServer entry was emitted
	// (agent advertised mcp_capabilities.http).
	auxMCPTransportHTTP
	// auxMCPTransportStdio means the stdio `mitto mcp --proxy-to` fallback was
	// emitted (agent did not advertise mcp_capabilities.http, or capabilities
	// were unavailable).
	auxMCPTransportStdio
)

// buildAuxProcessorMCPServers is the pure decision core for selecting the MCP
// transport used by processor auxiliary sessions. Extracted from
// getOrCreateAuxiliarySession for unit-testability without a live ACP process.
//
// Inputs:
//   - purpose:      the aux session purpose string (must start with
//     auxiliary.PurposeProcessorPrefix to receive a Mitto MCP entry).
//   - mcpServerURL: the HTTP URL of the workspace-local Mitto MCP endpoint
//     (empty disables MCP injection entirely).
//   - caps:         the agent's advertised capabilities, or nil if unavailable
//     (e.g. mid-restart) — nil falls through to the stdio fallback.
//   - resolveCLI:   resolver for the mitto CLI binary path used by the stdio
//     fallback; injected so tests can stub it.
//
// Returns the McpServers slice to hand to session/new (always non-nil, possibly
// empty — ACP validates the field), the transport branch actually taken, and
// the resolved CLI path (empty except on the stdio branch). The transport /
// path returns exist so the caller can emit the correct debug log line without
// re-deriving the branch.
func buildAuxProcessorMCPServers(
	purpose string,
	mcpServerURL string,
	caps *acp.AgentCapabilities,
	resolveCLI func() (string, error),
) ([]acp.McpServer, auxMCPTransport, string) {
	// Non-nil empty slice is the ACP-safe default.
	mcpServers := []acp.McpServer{}

	if !strings.HasPrefix(purpose, auxiliary.PurposeProcessorPrefix) || mcpServerURL == "" {
		return mcpServers, auxMCPTransportNone, ""
	}

	if caps != nil && caps.McpCapabilities.Http {
		mcpServers = []acp.McpServer{{
			Http: &acp.McpServerHttpInline{
				Type:    "http",
				Name:    "mitto",
				Url:     mcpServerURL,
				Headers: []acp.HttpHeader{}, // Must be empty array, not nil — ACP validates this
			},
		}}
		return mcpServers, auxMCPTransportHTTP, ""
	}

	if resolveCLI == nil {
		return mcpServers, auxMCPTransportNone, ""
	}
	exe, err := resolveCLI()
	if err != nil {
		return mcpServers, auxMCPTransportNone, ""
	}
	mcpServers = []acp.McpServer{{
		Stdio: &acp.McpServerStdio{
			Name:    "mitto",
			Command: exe,
			Args:    []string{"mcp", "--proxy-to", mcpServerURL},
			Env:     []acp.EnvVariable{}, // Must be empty array, not nil — ACP validates this
		},
	}}
	return mcpServers, auxMCPTransportStdio, exe
}
