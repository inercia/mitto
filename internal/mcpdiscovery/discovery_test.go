package mcpdiscovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/agents"
)

// noopToolHandler is a tool handler that does nothing; only the tool's
// presence (its Name) matters for these tests.
func noopToolHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{}, nil, nil
}

// newMockStdioServer starts an in-memory MCP server exposing toolNames and
// returns a TransportFactory that connects to it, plus a stop func to release
// the server goroutine. The server keeps running until stop is called (or the
// factory's own cleanup calls it after a probe).
func newMockStdioServer(t *testing.T, toolNames ...string) (TransportFactory, func()) {
	t.Helper()
	serverCtx, stop := context.WithCancel(context.Background())

	srv := mcp.NewServer(&mcp.Implementation{Name: "mock", Version: "0"}, nil)
	for _, name := range toolNames {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "mock tool"}, noopToolHandler)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	go srv.Run(serverCtx, serverT)

	factory := func(_ context.Context, _ agents.MCPServer) (mcp.Transport, func(), error) {
		return clientT, stop, nil
	}
	return factory, stop
}

func TestDiscoverStdioServer_ReachableWithTools(t *testing.T) {
	factory, stop := newMockStdioServer(t, "jira_create_issue", "jira_search")
	defer stop()

	result := DiscoverStdioServer(context.Background(), agents.MCPServer{Name: "jira", Command: "unused"}, time.Second, factory)

	if !result.Reachable {
		t.Fatalf("Reachable = false, want true (err=%v)", result.Err)
	}
	if result.Err != nil {
		t.Errorf("Err = %v, want nil", result.Err)
	}
	if result.Server != "jira" {
		t.Errorf("Server = %q, want %q", result.Server, "jira")
	}
	got := append([]string(nil), result.Tools...)
	sort.Strings(got)
	want := []string{"jira_create_issue", "jira_search"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Tools = %v, want %v", result.Tools, want)
	}
}

func TestDiscoverStdioServer_ReachableEmpty(t *testing.T) {
	factory, stop := newMockStdioServer(t) // no tools registered
	defer stop()

	result := DiscoverStdioServer(context.Background(), agents.MCPServer{Name: "empty", Command: "unused"}, time.Second, factory)

	if !result.Reachable {
		t.Fatalf("Reachable = false, want true (err=%v)", result.Err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("Tools = %v, want empty", result.Tools)
	}
}

func TestDiscoverStdioServer_Unreachable_TransportBuildError(t *testing.T) {
	factory := func(_ context.Context, _ agents.MCPServer) (mcp.Transport, func(), error) {
		return nil, nil, errors.New("boom")
	}

	result := DiscoverStdioServer(context.Background(), agents.MCPServer{Name: "broken", Command: "unused"}, time.Second, factory)

	if result.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if result.Err == nil {
		t.Fatalf("Err = nil, want non-nil")
	}
	if len(result.Tools) != 0 {
		t.Errorf("Tools = %v, want empty", result.Tools)
	}
}

func TestDiscoverStdioServer_Unreachable_ConnectFailure(t *testing.T) {
	// Uses the real production factory (nil -> CommandTransportFactory)
	// against a command that cannot possibly exist, so Connect fails
	// deterministically (exec: file not found) without any timeout wait.
	srv := agents.MCPServer{Name: "nope", Command: "mitto-test-nonexistent-binary-xyz"}

	result := DiscoverStdioServer(context.Background(), srv, 2*time.Second, nil)

	if result.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if result.Err == nil {
		t.Fatalf("Err = nil, want non-nil")
	}
	if len(result.Tools) != 0 {
		t.Errorf("Tools = %v, want empty", result.Tools)
	}
}

func TestDiscoverStdioServers_MixedIsolatesFailuresAndSkipsNonStdio(t *testing.T) {
	factory, stop := newMockStdioServer(t, "jira_create_issue")
	defer stop()

	servers := []agents.MCPServer{
		{Name: "jira", Command: "unused"},
		{Name: "broken", Command: "mitto-test-nonexistent-binary-xyz"},
		{Name: "http-only", URL: "https://example.com/mcp"}, // not stdio: must be skipped
	}

	mixedFactory := func(ctx context.Context, s agents.MCPServer) (mcp.Transport, func(), error) {
		if s.Name == "jira" {
			return factory(ctx, s)
		}
		return CommandTransportFactory(ctx, s)
	}

	results := DiscoverStdioServers(context.Background(), servers, time.Second, mixedFactory)

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (http-only must be skipped): %+v", len(results), results)
	}

	byName := map[string]ServerToolsResult{}
	for _, r := range results {
		byName[r.Server] = r
	}

	jira, ok := byName["jira"]
	if !ok || !jira.Reachable || len(jira.Tools) != 1 || jira.Tools[0] != "jira_create_issue" {
		t.Errorf("jira result = %+v, want Reachable=true Tools=[jira_create_issue]", jira)
	}

	broken, ok := byName["broken"]
	if !ok || broken.Reachable || broken.Err == nil {
		t.Errorf("broken result = %+v, want Reachable=false with Err set", broken)
	}
}

// stubLister is a mock ServerLister that returns a fixed output/error and
// records the arguments it was called with.
type stubLister struct {
	out       *agents.MCPListOutput
	err       error
	gotAgent  string
	gotInput  *agents.MCPListInput
	callCount int
}

func (s *stubLister) ListMCPServers(_ context.Context, agentName string, input *agents.MCPListInput) (*agents.MCPListOutput, error) {
	s.callCount++
	s.gotAgent = agentName
	s.gotInput = input
	return s.out, s.err
}

func TestDiscoverWorkspaceStdioTools_ListsThenProbes(t *testing.T) {
	factory, stop := newMockStdioServer(t, "jira_create_issue", "jira_search")
	defer stop()

	lister := &stubLister{out: &agents.MCPListOutput{Servers: []agents.MCPServer{
		{Name: "jira", Command: "unused"},
		{Name: "http-only", URL: "https://example.com/mcp"}, // skipped: not stdio
	}}}

	results, err := DiscoverWorkspaceStdioTools(context.Background(), lister, "auggie", "/ws/path", time.Second, factory)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if lister.callCount != 1 || lister.gotAgent != "auggie" {
		t.Errorf("lister called %d times with agent %q, want 1 with %q", lister.callCount, lister.gotAgent, "auggie")
	}
	if lister.gotInput == nil || lister.gotInput.Path != "/ws/path" {
		t.Errorf("lister input = %+v, want Path=/ws/path", lister.gotInput)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1 (http-only skipped): %+v", len(results), results)
	}
	if !results[0].Reachable || results[0].Server != "jira" {
		t.Errorf("result = %+v, want Reachable=true Server=jira", results[0])
	}
}

func TestDiscoverWorkspaceStdioTools_ListError(t *testing.T) {
	lister := &stubLister{err: errors.New("mcp-list.sh failed")}

	results, err := DiscoverWorkspaceStdioTools(context.Background(), lister, "auggie", "/ws/path", time.Second, nil)
	if err == nil {
		t.Fatalf("err = nil, want non-nil (list failure must be a top-level error)")
	}
	if results != nil {
		t.Errorf("results = %+v, want nil on list error", results)
	}
}

func TestDiscoverWorkspaceStdioTools_NilLister(t *testing.T) {
	if _, err := DiscoverWorkspaceStdioTools(context.Background(), nil, "auggie", "/ws", time.Second, nil); err == nil {
		t.Fatalf("err = nil, want non-nil for nil lister")
	}
}

// =============================================================================
// http/sse transport discovery (mitto-sys.3)
// =============================================================================

func TestNetworkTransportFactory_SelectsSSEBySuffix(t *testing.T) {
	transport, cleanup, err := NetworkTransportFactory(context.Background(), agents.MCPServer{Name: "s", URL: "https://example.com/sse"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	defer cleanup()

	sseT, ok := transport.(*mcp.SSEClientTransport)
	if !ok {
		t.Fatalf("transport = %T, want *mcp.SSEClientTransport", transport)
	}
	if sseT.Endpoint != "https://example.com/sse" {
		t.Errorf("Endpoint = %q, want %q", sseT.Endpoint, "https://example.com/sse")
	}
}

func TestNetworkTransportFactory_SelectsStreamableByDefault(t *testing.T) {
	transport, cleanup, err := NetworkTransportFactory(context.Background(), agents.MCPServer{Name: "s", URL: "https://example.com/mcp"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	defer cleanup()

	streamT, ok := transport.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport = %T, want *mcp.StreamableClientTransport", transport)
	}
	if streamT.Endpoint != "https://example.com/mcp" {
		t.Errorf("Endpoint = %q, want %q", streamT.Endpoint, "https://example.com/mcp")
	}
}

func TestNetworkTransportFactory_EmptyURL(t *testing.T) {
	_, _, err := NetworkTransportFactory(context.Background(), agents.MCPServer{Name: "s"})
	if err == nil {
		t.Fatalf("err = nil, want non-nil for empty URL")
	}
}

func TestDefaultTransportFactory_Dispatch(t *testing.T) {
	t.Run("stdio", func(t *testing.T) {
		transport, cleanup, err := DefaultTransportFactory(context.Background(), agents.MCPServer{Name: "s", Command: "echo"})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		defer cleanup()
		if _, ok := transport.(*mcp.CommandTransport); !ok {
			t.Fatalf("transport = %T, want *mcp.CommandTransport", transport)
		}
	})

	t.Run("network", func(t *testing.T) {
		transport, cleanup, err := DefaultTransportFactory(context.Background(), agents.MCPServer{Name: "s", URL: "https://example.com/mcp"})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		defer cleanup()
		switch transport.(type) {
		case *mcp.StreamableClientTransport, *mcp.SSEClientTransport:
			// ok
		default:
			t.Fatalf("transport = %T, want a network transport", transport)
		}
	})

	t.Run("neither", func(t *testing.T) {
		_, _, err := DefaultTransportFactory(context.Background(), agents.MCPServer{Name: "s"})
		if err == nil {
			t.Fatalf("err = nil, want non-nil when server has neither Command nor URL")
		}
	})
}

func TestDiscoverNetworkServer_ReachableWithTools(t *testing.T) {
	factory, stop := newMockStdioServer(t, "web_search")
	defer stop()

	result := DiscoverNetworkServer(context.Background(), agents.MCPServer{Name: "web", URL: "https://example.com/mcp"}, time.Second, factory)

	if !result.Reachable {
		t.Fatalf("Reachable = false, want true (err=%v)", result.Err)
	}
	if len(result.Tools) != 1 || result.Tools[0] != "web_search" {
		t.Errorf("Tools = %v, want [web_search]", result.Tools)
	}
}

func TestDiscoverNetworkServer_RealStreamableHTTP(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mock", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "http_tool", Description: "mock http tool"}, noopToolHandler)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	result := DiscoverNetworkServer(context.Background(), agents.MCPServer{Name: "http", URL: ts.URL}, 5*time.Second, nil)

	if !result.Reachable {
		t.Fatalf("Reachable = false, want true (err=%v)", result.Err)
	}
	if len(result.Tools) != 1 || result.Tools[0] != "http_tool" {
		t.Errorf("Tools = %v, want [http_tool]", result.Tools)
	}
}

// TestDiscoverNetworkServer_RealSSE exercises a genuine SSE round-trip
// against mcp.NewSSEHandler. SSEHandler computes its session endpoint
// relative to the incoming request's own path (see mcp/sse.go ServeHTTP:
// it builds "?sessionid=..." off req.URL, and POSTs are routed by mux to
// whatever path the handler is mounted on), so mounting it at "/sse" and
// probing ts.URL+"/sse" makes the client hit the real code path for both
// endpoint-suffix detection (NetworkTransportFactory picks SSEClientTransport)
// and the SSE round-trip itself — no need to fall back to an in-memory-only
// test for SSE.
func TestDiscoverNetworkServer_RealSSE(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mock", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "sse_tool", Description: "mock sse tool"}, noopToolHandler)

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/sse", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	result := DiscoverNetworkServer(context.Background(), agents.MCPServer{Name: "sse", URL: ts.URL + "/sse"}, 5*time.Second, nil)

	if !result.Reachable {
		t.Fatalf("Reachable = false, want true (err=%v)", result.Err)
	}
	if len(result.Tools) != 1 || result.Tools[0] != "sse_tool" {
		t.Errorf("Tools = %v, want [sse_tool]", result.Tools)
	}
}

func TestDiscoverNetworkServer_AuthRequiredGracefulFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	result := DiscoverNetworkServer(context.Background(), agents.MCPServer{Name: "secured", URL: ts.URL}, 5*time.Second, nil)

	if result.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if result.Err == nil {
		t.Fatalf("Err = nil, want non-nil")
	}
	if len(result.Tools) != 0 {
		t.Errorf("Tools = %v, want empty", result.Tools)
	}
}

func TestDiscoverNetworkServer_HeaderProtected(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mock", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "secured_tool", Description: "mock http tool"}, noopToolHandler)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)

	const wantAuth = "Bearer test-token" // dummy test token, not a real secret
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(gated)
	defer ts.Close()

	// Without headers -> gate returns 401 -> unreachable.
	noHdr := DiscoverNetworkServer(context.Background(), agents.MCPServer{Name: "secured", URL: ts.URL}, 5*time.Second, nil)
	if noHdr.Reachable {
		t.Fatalf("without headers: Reachable = true, want false (err=%v)", noHdr.Err)
	}

	// With correct headers -> gate passes -> reachable, tool listed.
	withHdr := DiscoverNetworkServer(context.Background(), agents.MCPServer{
		Name:    "secured",
		URL:     ts.URL,
		Headers: map[string]string{"Authorization": wantAuth},
	}, 5*time.Second, nil)
	if !withHdr.Reachable {
		t.Fatalf("with headers: Reachable = false, want true (err=%v)", withHdr.Err)
	}
	if len(withHdr.Tools) != 1 || withHdr.Tools[0] != "secured_tool" {
		t.Errorf("Tools = %v, want [secured_tool]", withHdr.Tools)
	}
}

func TestDiscoverServers_MixedTransportsSkipsNeither(t *testing.T) {
	stdioFactory, stopStdio := newMockStdioServer(t, "stdio_tool")
	defer stopStdio()
	netFactory, stopNet := newMockStdioServer(t, "net_tool")
	defer stopNet()

	servers := []agents.MCPServer{
		{Name: "stdio-srv", Command: "unused"},
		{Name: "net-srv", URL: "https://example.com/mcp"},
		{Name: "neither-srv"}, // no Command, no URL: must be skipped
	}

	mixedFactory := func(ctx context.Context, s agents.MCPServer) (mcp.Transport, func(), error) {
		if s.Name == "stdio-srv" {
			return stdioFactory(ctx, s)
		}
		return netFactory(ctx, s)
	}

	results := DiscoverServers(context.Background(), servers, time.Second, mixedFactory)

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (neither-srv must be skipped): %+v", len(results), results)
	}
	for _, r := range results {
		if !r.Reachable {
			t.Errorf("result for %q: Reachable = false, want true (err=%v)", r.Server, r.Err)
		}
	}
}

func TestDiscoverWorkspaceTools_ProbesBothStdioAndNetwork(t *testing.T) {
	stdioFactory, stopStdio := newMockStdioServer(t, "stdio_tool")
	defer stopStdio()
	netFactory, stopNet := newMockStdioServer(t, "net_tool")
	defer stopNet()

	lister := &stubLister{out: &agents.MCPListOutput{Servers: []agents.MCPServer{
		{Name: "jira", Command: "unused"},
		{Name: "http-only", URL: "https://example.com/mcp"},
	}}}

	mixedFactory := func(ctx context.Context, s agents.MCPServer) (mcp.Transport, func(), error) {
		if s.Name == "jira" {
			return stdioFactory(ctx, s)
		}
		return netFactory(ctx, s)
	}

	results, err := DiscoverWorkspaceTools(context.Background(), lister, "auggie", "/ws/path", time.Second, mixedFactory)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (both stdio and network probed): %+v", len(results), results)
	}

	byName := map[string]ServerToolsResult{}
	for _, r := range results {
		byName[r.Server] = r
	}
	if jira, ok := byName["jira"]; !ok || !jira.Reachable {
		t.Errorf("jira result = %+v, want Reachable=true", jira)
	}
	if httpOnly, ok := byName["http-only"]; !ok || !httpOnly.Reachable {
		t.Errorf("http-only result = %+v, want Reachable=true", httpOnly)
	}
}
