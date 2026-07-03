package auxiliary

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/agents"
	"github.com/inercia/mitto/internal/mcpdiscovery"
)

func noopWatchToolHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{}, nil, nil
}

// newWatchableMCPServer starts an in-memory MCP server exposing toolNames and
// returns the server (so tests can AddTool/RemoveTools on it), a
// mcpdiscovery.TransportFactory that connects to it, and a stop func.
func newWatchableMCPServer(t *testing.T, toolNames ...string) (*mcp.Server, mcpdiscovery.TransportFactory, func()) {
	t.Helper()
	serverCtx, stop := context.WithCancel(context.Background())

	srv := mcp.NewServer(&mcp.Implementation{Name: "mock", Version: "0"}, nil)
	for _, name := range toolNames {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "mock tool"}, noopWatchToolHandler)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	go srv.Run(serverCtx, serverT)

	factory := func(_ context.Context, _ agents.MCPServer) (mcp.Transport, func(), error) {
		return clientT, stop, nil
	}
	return srv, factory, stop
}

func TestEnsureMCPWatchers_InitialAndChangeRebroadcast(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	srv, factory, stop := newWatchableMCPServer(t, "jira_search")
	defer stop()

	mgr.MCPWatchTransportFactory = factory
	mgr.MCPServerLister = func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error) {
		return []agents.MCPServer{{Name: "jira", Command: "unused"}}, nil
	}

	updates := make(chan []MCPToolInfo, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.EnsureMCPWatchers(ctx, "ws", func(tools []MCPToolInfo) {
		updates <- tools
	})

	select {
	case tools := <-updates:
		assertToolNames(t, tools, "jira_search")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial onUpdate")
	}

	cached, ok := mgr.GetCachedMCPTools("ws")
	if !ok {
		t.Fatalf("expected cache entry after initial watcher startup")
	}
	assertToolNames(t, cached, "jira_search")

	mcp.AddTool(srv, &mcp.Tool{Name: "jira_create_issue", Description: "mock tool"}, noopWatchToolHandler)

	select {
	case tools := <-updates:
		assertToolNames(t, tools, "jira_create_issue", "jira_search")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onUpdate after AddTool")
	}

	cached, ok = mgr.GetCachedMCPTools("ws")
	if !ok {
		t.Fatalf("expected cache entry after change notification")
	}
	assertToolNames(t, cached, "jira_create_issue", "jira_search")

	mgr.StopMCPWatchers("ws")
}

func TestEnsureMCPWatchers_Dedup(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	_, factory, stop := newWatchableMCPServer(t, "jira_search")
	defer stop()

	mgr.MCPWatchTransportFactory = factory

	var listerCalls int32
	mgr.MCPServerLister = func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error) {
		atomic.AddInt32(&listerCalls, 1)
		return []agents.MCPServer{{Name: "jira", Command: "unused"}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{}, 4)
	onUpdate := func(tools []MCPToolInfo) { done <- struct{}{} }

	mgr.EnsureMCPWatchers(ctx, "ws", onUpdate)
	mgr.EnsureMCPWatchers(ctx, "ws", onUpdate) // must be a no-op: pool already active/starting

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial onUpdate")
	}

	if got := atomic.LoadInt32(&listerCalls); got != 1 {
		t.Errorf("MCPServerLister called %d times, want 1 (dedup)", got)
	}

	mgr.StopMCPWatchers("ws")
}

func TestStopAndCloseAllMCPWatchers(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	_, factory, stop := newWatchableMCPServer(t, "jira_search")
	defer stop()

	mgr.MCPWatchTransportFactory = factory
	mgr.MCPServerLister = func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error) {
		return []agents.MCPServer{{Name: "jira", Command: "unused"}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	got := false
	done := make(chan struct{}, 4)
	mgr.EnsureMCPWatchers(ctx, "ws", func(tools []MCPToolInfo) {
		mu.Lock()
		got = true
		mu.Unlock()
		done <- struct{}{}
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onUpdate")
	}
	mu.Lock()
	if !got {
		t.Fatalf("onUpdate never called")
	}
	mu.Unlock()

	mgr.StopMCPWatchers("ws")
	mgr.mcpWatchersMu.Lock()
	_, exists := mgr.mcpWatchers["ws"]
	mgr.mcpWatchersMu.Unlock()
	if exists {
		t.Errorf("expected workspace key removed after StopMCPWatchers")
	}

	// Idempotent: calling again must not panic.
	mgr.StopMCPWatchers("ws")

	// CloseAllMCPWatchers must also be safe when empty.
	mgr.CloseAllMCPWatchers()
	mgr.CloseAllMCPWatchers()
}

func TestEnsureMCPWatchers_NilLister_NoOp(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil) // MCPServerLister left nil

	called := false
	mgr.EnsureMCPWatchers(context.Background(), "ws", func(tools []MCPToolInfo) {
		called = true
	})

	time.Sleep(10 * time.Millisecond)
	if called {
		t.Errorf("onUpdate called, want no-op for nil MCPServerLister")
	}
	mgr.mcpWatchersMu.Lock()
	_, exists := mgr.mcpWatchers["ws"]
	mgr.mcpWatchersMu.Unlock()
	if exists {
		t.Errorf("expected no workspace entry created for nil MCPServerLister")
	}
}

func TestEnsureMCPWatchers_ListerErrorReleasesReservation(t *testing.T) {
	mgr := NewWorkspaceAuxiliaryManager(&mockProcessProvider{}, nil)
	_, factory, stop := newWatchableMCPServer(t, "jira_search")
	defer stop()

	mgr.MCPWatchTransportFactory = factory
	mgr.MCPServerLister = func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error) {
		return nil, errors.New("lister boom")
	}

	mgr.EnsureMCPWatchers(context.Background(), "ws", nil)

	// Poll until the failed attempt has released its reservation.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mcpWatchersMu.Lock()
		_, exists := mgr.mcpWatchers["ws"]
		mgr.mcpWatchersMu.Unlock()
		if !exists {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mgr.mcpWatchersMu.Lock()
	_, stillReserved := mgr.mcpWatchers["ws"]
	mgr.mcpWatchersMu.Unlock()
	if stillReserved {
		t.Fatalf("expected reservation released after lister error, workspace still present in mcpWatchers")
	}

	var workingListerCalls int32
	mgr.MCPServerLister = func(ctx context.Context, workspaceUUID string) ([]agents.MCPServer, error) {
		atomic.AddInt32(&workingListerCalls, 1)
		return []agents.MCPServer{{Name: "jira", Command: "unused"}}, nil
	}

	done := make(chan struct{}, 4)
	mgr.EnsureMCPWatchers(context.Background(), "ws", func(tools []MCPToolInfo) {
		done <- struct{}{}
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onUpdate after retrying with a working lister")
	}

	if got := atomic.LoadInt32(&workingListerCalls); got != 1 {
		t.Errorf("working MCPServerLister called %d times, want 1 (retry must not be blocked by the earlier failure)", got)
	}

	mgr.StopMCPWatchers("ws")
}
