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
	"os"
	"os/exec"
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

// DiscoverStdioServer connects to a single stdio MCP server and lists its
// tools. It never panics: any transport/connect/list failure or timeout is
// reported via ServerToolsResult.Err with Reachable=false, distinct from a
// genuine reachable-but-empty tool list (Reachable=true, Tools=nil/empty).
// timeout bounds the whole probe; DefaultTimeout is used when timeout <= 0.
// factory builds the underlying transport; pass CommandTransportFactory (or
// nil, which defaults to it) in production, or an injected factory in tests.
func DiscoverStdioServer(ctx context.Context, srv agents.MCPServer, timeout time.Duration, factory TransportFactory) ServerToolsResult {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if factory == nil {
		factory = CommandTransportFactory
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
