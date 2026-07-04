// Package mcpdiscovery provides deterministic MCP tool discovery by
// connecting directly to configured MCP servers over the vendored
// modelcontextprotocol/go-sdk client, replacing the LLM-introspection
// fallback for servers that support it. See docs/devel/mcp-tool-discovery.md
// (Q1) for background. This package does not import internal/config or
// internal/web; callers map its results onto config types themselves.
package mcpdiscovery

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/agents"
)

// DefaultTimeout bounds a single server's tools/list probe (transport
// build + Connect + ListTools) when the caller passes timeout <= 0. Chosen
// within the 5-10s range from docs/devel/mcp-tool-discovery.md (Q1): long
// enough for a cold npx-based server, short enough to keep callers responsive.
const DefaultTimeout = 8 * time.Second

// clientName/clientVersion identify Mitto to the MCP servers it probes.
const (
	clientName    = "mitto"
	clientVersion = "0.0.0"
)

// ServerToolsResult is the outcome of probing a single MCP server for its
// tool list.
type ServerToolsResult struct {
	// Server is the probed server's name (agents.MCPServer.Name).
	Server string
	// Tools contains the tool names returned by tools/list. Only meaningful
	// when Reachable is true; a reachable server may legitimately have zero
	// tools.
	Tools []string
	// Reachable is true iff Connect and ListTools both succeeded.
	Reachable bool
	// Err is non-nil on transport, connect, or list failure, or on timeout.
	// Callers must treat this as configured-but-unreachable, never as a
	// confirmed-empty result.
	Err error
}

// TransportFactory builds a transport for connecting to srv, plus a cleanup
// function the caller must invoke once done with the transport (may be a
// no-op, never nil). It exists as a seam so tests can inject in-memory
// transports instead of spawning real subprocesses.
type TransportFactory func(ctx context.Context, srv agents.MCPServer) (mcp.Transport, func(), error)

// CommandTransportFactory is the production TransportFactory for stdio MCP
// servers: it builds an *mcp.CommandTransport wrapping srv.Command/srv.Args,
// with the environment inherited from the current process plus srv.Env
// merged in. Its cleanup is a no-op; the ClientSession.Close performed by
// DiscoverStdioServer tears down the subprocess.
func CommandTransportFactory(ctx context.Context, srv agents.MCPServer) (mcp.Transport, func(), error) {
	cmd := exec.CommandContext(ctx, srv.Command, srv.Args...)
	env := os.Environ()
	for k, v := range srv.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	return &mcp.CommandTransport{Command: cmd}, func() {}, nil
}

// probeServer connects to a single MCP server (any transport, as built by
// factory) and lists its tools. It never panics: any transport/connect/list
// failure or timeout is reported via ServerToolsResult.Err with
// Reachable=false, distinct from a genuine reachable-but-empty tool list
// (Reachable=true, Tools=nil/empty). timeout bounds the whole probe;
// DefaultTimeout is used when timeout <= 0. factory must be non-nil; callers
// (DiscoverStdioServer, DiscoverNetworkServer) apply their own default.
func probeServer(ctx context.Context, srv agents.MCPServer, timeout time.Duration, factory TransportFactory) ServerToolsResult {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	transport, cleanup, err := factory(ctx, srv)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return ServerToolsResult{Server: srv.Name, Err: fmt.Errorf("mcpdiscovery: build transport for %q: %w", srv.Name, err)}
	}

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return ServerToolsResult{Server: srv.Name, Err: fmt.Errorf("mcpdiscovery: connect to %q: %w", srv.Name, err)}
	}
	defer sess.Close()

	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return ServerToolsResult{Server: srv.Name, Err: fmt.Errorf("mcpdiscovery: list tools for %q: %w", srv.Name, err)}
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return ServerToolsResult{Server: srv.Name, Tools: names, Reachable: true}
}

// DiscoverStdioServer connects to a single stdio MCP server and lists its
// tools. See probeServer for behavior details. factory defaults to
// CommandTransportFactory when nil.
func DiscoverStdioServer(ctx context.Context, srv agents.MCPServer, timeout time.Duration, factory TransportFactory) ServerToolsResult {
	if factory == nil {
		factory = CommandTransportFactory
	}
	return probeServer(ctx, srv, timeout, factory)
}

// NetworkTransportFactory is the production TransportFactory for http/sse MCP
// servers. It builds an *mcp.SSEClientTransport when srv.URL's path ends in
// "/sse" (case-insensitive) — the conventional SSE endpoint suffix — and an
// *mcp.StreamableClientTransport (the modern default) otherwise. Cleanup is a
// no-op; ClientSession.Close (in probeServer) tears down the connection.
// NOTE: agents.MCPServer now has a Headers field (mitto-sys.9 part A/B), but
// this factory does not yet apply srv.Env or srv.Headers to the transport
// (that wiring is mitto-sys.9 part C, still pending); auth-required endpoints
// therefore still fail at Connect/ListTools and surface as Reachable=false,
// letting callers fall back to the LLM path.
func NetworkTransportFactory(_ context.Context, srv agents.MCPServer) (mcp.Transport, func(), error) {
	if srv.URL == "" {
		return nil, nil, fmt.Errorf("mcpdiscovery: server %q has no URL", srv.Name)
	}

	isSSE := strings.HasSuffix(strings.ToLower(srv.URL), "/sse")
	if parsed, err := url.Parse(srv.URL); err == nil {
		isSSE = strings.HasSuffix(strings.ToLower(parsed.Path), "/sse")
	}

	if isSSE {
		return &mcp.SSEClientTransport{Endpoint: srv.URL}, func() {}, nil
	}
	return &mcp.StreamableClientTransport{Endpoint: srv.URL}, func() {}, nil
}

// DefaultTransportFactory dispatches to CommandTransportFactory for stdio
// servers (Command != "") and NetworkTransportFactory for network servers
// (URL != ""), erroring when a server has neither. It is the production
// factory for DiscoverServers, which probes mixed-transport server lists.
func DefaultTransportFactory(ctx context.Context, srv agents.MCPServer) (mcp.Transport, func(), error) {
	switch {
	case srv.Command != "":
		return CommandTransportFactory(ctx, srv)
	case srv.URL != "":
		return NetworkTransportFactory(ctx, srv)
	default:
		return nil, nil, fmt.Errorf("mcpdiscovery: server %q has neither Command nor URL", srv.Name)
	}
}

// DiscoverNetworkServer connects to a single http/sse MCP server and lists
// its tools. See probeServer for behavior details. factory defaults to
// NetworkTransportFactory when nil.
func DiscoverNetworkServer(ctx context.Context, srv agents.MCPServer, timeout time.Duration, factory TransportFactory) ServerToolsResult {
	if factory == nil {
		factory = NetworkTransportFactory
	}
	return probeServer(ctx, srv, timeout, factory)
}

// DiscoverStdioServers probes every stdio server in servers (srv.Command !=
// "" — http/sse-only servers are skipped, see mitto-sys.3) and returns one
// ServerToolsResult per stdio server, in input order. Servers are probed
// sequentially, each isolated: one server's failure or timeout never aborts
// the others. factory defaults to CommandTransportFactory when nil.
func DiscoverStdioServers(ctx context.Context, servers []agents.MCPServer, timeout time.Duration, factory TransportFactory) []ServerToolsResult {
	if factory == nil {
		factory = CommandTransportFactory
	}
	var results []ServerToolsResult
	for _, srv := range servers {
		if srv.Command == "" {
			continue
		}
		results = append(results, DiscoverStdioServer(ctx, srv, timeout, factory))
	}
	return results
}

// DiscoverNetworkServers probes every network server in servers (srv.URL !=
// "" && srv.Command == "" — stdio servers are skipped) and returns one
// ServerToolsResult per network server, in input order. Servers are probed
// sequentially, each isolated: one server's failure or timeout never aborts
// the others. factory defaults to NetworkTransportFactory when nil.
func DiscoverNetworkServers(ctx context.Context, servers []agents.MCPServer, timeout time.Duration, factory TransportFactory) []ServerToolsResult {
	if factory == nil {
		factory = NetworkTransportFactory
	}
	var results []ServerToolsResult
	for _, srv := range servers {
		if srv.URL == "" || srv.Command != "" {
			continue
		}
		results = append(results, DiscoverNetworkServer(ctx, srv, timeout, factory))
	}
	return results
}

// DiscoverServers probes every server in servers regardless of transport,
// skipping only servers with neither Command nor URL set. Servers are probed
// sequentially, each isolated: one server's failure or timeout never aborts
// the others. factory defaults to DefaultTransportFactory when nil, which
// dispatches each server to the appropriate stdio/network transport.
func DiscoverServers(ctx context.Context, servers []agents.MCPServer, timeout time.Duration, factory TransportFactory) []ServerToolsResult {
	if factory == nil {
		factory = DefaultTransportFactory
	}
	var results []ServerToolsResult
	for _, srv := range servers {
		if srv.Command == "" && srv.URL == "" {
			continue
		}
		results = append(results, probeServer(ctx, srv, timeout, factory))
	}
	return results
}

// ServerLister abstracts agents.Manager.ListMCPServers so the workspace-level
// discovery bridge can be tested without a real agent or mcp-list.sh script.
// *agents.Manager satisfies it.
type ServerLister interface {
	ListMCPServers(ctx context.Context, agentName string, input *agents.MCPListInput) (*agents.MCPListOutput, error)
}

// Compile-time assertion that the production *agents.Manager satisfies
// ServerLister, so DiscoverWorkspaceStdioTools can be wired to it directly.
var _ ServerLister = (*agents.Manager)(nil)

// DiscoverWorkspaceStdioTools resolves a workspace's configured MCP servers via
// lister.ListMCPServers(agentName, {Path: workspacePath}) — the same
// deterministic mcp-list.sh discovery the MCP handler uses — then probes the
// stdio ones with DiscoverStdioServers. The returned results cover only stdio
// servers (http/sse are skipped, see mitto-sys.3). An error is returned only
// when the server list itself cannot be obtained; per-server probe failures are
// reported in-band via ServerToolsResult (Reachable=false, Err set), never as a
// top-level error.
func DiscoverWorkspaceStdioTools(ctx context.Context, lister ServerLister, agentName, workspacePath string, timeout time.Duration, factory TransportFactory) ([]ServerToolsResult, error) {
	if lister == nil {
		return nil, fmt.Errorf("mcpdiscovery: nil server lister")
	}
	out, err := lister.ListMCPServers(ctx, agentName, &agents.MCPListInput{Path: workspacePath})
	if err != nil {
		return nil, fmt.Errorf("mcpdiscovery: list mcp servers for agent %q: %w", agentName, err)
	}
	if out == nil {
		return nil, nil
	}
	return DiscoverStdioServers(ctx, out.Servers, timeout, factory), nil
}

// DiscoverWorkspaceTools resolves a workspace's configured MCP servers via
// lister.ListMCPServers(agentName, {Path: workspacePath}) — the same
// deterministic mcp-list.sh discovery the MCP handler uses — then probes ALL
// of them (stdio + http/sse) with DiscoverServers. An error is returned only
// when the server list itself cannot be obtained; per-server probe failures
// are reported in-band via ServerToolsResult (Reachable=false, Err set),
// never as a top-level error.
func DiscoverWorkspaceTools(ctx context.Context, lister ServerLister, agentName, workspacePath string, timeout time.Duration, factory TransportFactory) ([]ServerToolsResult, error) {
	if lister == nil {
		return nil, fmt.Errorf("mcpdiscovery: nil server lister")
	}
	out, err := lister.ListMCPServers(ctx, agentName, &agents.MCPListInput{Path: workspacePath})
	if err != nil {
		return nil, fmt.Errorf("mcpdiscovery: list mcp servers for agent %q: %w", agentName, err)
	}
	if out == nil {
		return nil, nil
	}
	return DiscoverServers(ctx, out.Servers, timeout, factory), nil
}
