package mcpdiscovery

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/agents"
)

// newWatchableServer starts an in-memory MCP server exposing toolNames and
// returns the server (so tests can AddTool/RemoveTools on it), a
// TransportFactory that connects to it, and a stop func.
func newWatchableServer(t *testing.T, toolNames ...string) (*mcp.Server, TransportFactory, func()) {
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
	return srv, factory, stop
}

func sortedNames(names []string) []string {
	got := append([]string(nil), names...)
	sort.Strings(got)
	return got
}

func TestWatchServer_InitialList(t *testing.T) {
	_, factory, stop := newWatchableServer(t, "jira_search")
	defer stop()

	watcher, initial, err := WatchServer(context.Background(), agents.MCPServer{Name: "jira"}, factory, func(ServerToolsResult) {
		t.Error("onChange must not fire for the initial list")
	})
	if err != nil {
		t.Fatalf("WatchServer error = %v", err)
	}
	defer watcher.Close()

	if !initial.Reachable {
		t.Fatalf("initial.Reachable = false, want true (err=%v)", initial.Err)
	}
	if got := sortedNames(initial.Tools); len(got) != 1 || got[0] != "jira_search" {
		t.Errorf("initial.Tools = %v, want [jira_search]", got)
	}
}

func TestWatchServer_ChangeEventFiresCallback(t *testing.T) {
	srv, factory, stop := newWatchableServer(t, "jira_search")
	defer stop()

	changes := make(chan ServerToolsResult, 4)
	watcher, initial, err := WatchServer(context.Background(), agents.MCPServer{Name: "jira"}, factory, func(res ServerToolsResult) {
		changes <- res
	})
	if err != nil {
		t.Fatalf("WatchServer error = %v", err)
	}
	defer watcher.Close()
	if !initial.Reachable {
		t.Fatalf("initial.Reachable = false, want true")
	}

	mcp.AddTool(srv, &mcp.Tool{Name: "jira_create_issue", Description: "mock tool"}, noopToolHandler)

	select {
	case res := <-changes:
		if !res.Reachable {
			t.Fatalf("onChange result.Reachable = false, want true (err=%v)", res.Err)
		}
		got := sortedNames(res.Tools)
		want := []string{"jira_create_issue", "jira_search"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("onChange Tools = %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onChange after AddTool")
	}

	srv.RemoveTools("jira_search")

	select {
	case res := <-changes:
		if !res.Reachable {
			t.Fatalf("onChange result.Reachable = false, want true (err=%v)", res.Err)
		}
		for _, name := range res.Tools {
			if name == "jira_search" {
				t.Errorf("onChange Tools = %v, want jira_search removed", res.Tools)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onChange after RemoveTools")
	}
}

func TestWatchServer_CloseIsCleanAndIdempotent(t *testing.T) {
	_, factory, stop := newWatchableServer(t, "jira_search")
	defer stop()

	watcher, _, err := WatchServer(context.Background(), agents.MCPServer{Name: "jira"}, factory, nil)
	if err != nil {
		t.Fatalf("WatchServer error = %v", err)
	}

	if err := watcher.Close(); err != nil {
		t.Errorf("first Close() error = %v, want nil", err)
	}
	if err := watcher.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil (idempotent)", err)
	}
}

func TestWatchServer_NilOnChangeDoesNotPanic(t *testing.T) {
	srv, factory, stop := newWatchableServer(t, "jira_search")
	defer stop()

	watcher, _, err := WatchServer(context.Background(), agents.MCPServer{Name: "jira"}, factory, nil)
	if err != nil {
		t.Fatalf("WatchServer error = %v", err)
	}
	defer watcher.Close()

	mcp.AddTool(srv, &mcp.Tool{Name: "jira_create_issue", Description: "mock tool"}, noopToolHandler)

	// Give the notification a moment to arrive and be handled; the only
	// assertion here is that nothing panics (a panic would fail the test).
	time.Sleep(200 * time.Millisecond)
}
