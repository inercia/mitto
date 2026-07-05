package mcpdiscovery

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/agents"
)

// ToolListWatcher holds a persistent MCP client session that has registered
// for notifications/tools/list_changed (mitto-sys.4, event-driven tool
// refresh — the alternative to the bounded-backoff polling primitive in
// backoff.go). Unlike probeServer's one-shot connect/list/close, the session
// is kept open for the watcher's lifetime; callers must Close it when done.
type ToolListWatcher struct {
	sess      *mcp.ClientSession
	cleanup   func()
	closeOnce sync.Once
	closeErr  error
}

// Close closes the underlying persistent session (and runs the transport's
// cleanup, if any). It is idempotent and concurrency-safe: only the first
// call actually closes the session, and its error is the one returned by
// all calls.
func (w *ToolListWatcher) Close() error {
	w.closeOnce.Do(func() {
		w.closeErr = w.sess.Close()
		if w.cleanup != nil {
			w.cleanup()
		}
	})
	return w.closeErr
}

// WatchServer connects to srv and keeps the session open, invoking onChange
// with a freshly re-listed ServerToolsResult every time the server emits a
// notifications/tools/list_changed notification. onChange may be nil (the
// notification is still handled, just not forwarded). factory defaults to
// DefaultTransportFactory when nil, matching DiscoverServers.
//
// The initial tools/list result is returned directly as the second return
// value, NOT via onChange — onChange only fires for subsequent changes. On
// any transport-build/connect/initial-list failure, any partially-opened
// session is closed, and a nil watcher plus an unreachable ServerToolsResult
// and a non-nil error are returned.
//
// The notification handler runs on the SDK's read-loop goroutine; it derives
// a bounded context (DefaultTimeout) from ctx for its own re-list call, so a
// slow/hanging server cannot block the read loop indefinitely.
func WatchServer(ctx context.Context, srv agents.MCPServer, factory TransportFactory, onChange func(ServerToolsResult)) (*ToolListWatcher, ServerToolsResult, error) {
	if factory == nil {
		factory = DefaultTransportFactory
	}

	transport, cleanup, err := factory(ctx, srv)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		wrapped := fmt.Errorf("mcpdiscovery: build transport for %q: %w", srv.Name, err)
		return nil, ServerToolsResult{Server: srv.Name, Err: wrapped}, wrapped
	}

	// cleanup (if any) is deferred to Close, NOT run here: the transport/
	// session must stay open for the watcher's lifetime, unlike probeServer's
	// one-shot connect/list/close.
	watcher := &ToolListWatcher{cleanup: cleanup}

	opts := &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, _ *mcp.ToolListChangedRequest) {
			lctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()

			res, lerr := listToolsResult(lctx, watcher.sess, srv.Name)
			if lerr != nil {
				res = ServerToolsResult{Server: srv.Name, Err: fmt.Errorf("mcpdiscovery: list tools for %q after change notification: %w", srv.Name, lerr)}
			}
			if onChange != nil {
				onChange(res)
			}
		},
	}

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, opts)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		wrapped := fmt.Errorf("mcpdiscovery: connect to %q: %w", srv.Name, err)
		return nil, ServerToolsResult{Server: srv.Name, Err: wrapped}, wrapped
	}
	watcher.sess = sess

	initial, err := listToolsResult(ctx, sess, srv.Name)
	if err != nil {
		wrapped := fmt.Errorf("mcpdiscovery: list tools for %q: %w", srv.Name, err)
		_ = watcher.Close()
		return nil, ServerToolsResult{Server: srv.Name, Err: wrapped}, wrapped
	}

	return watcher, initial, nil
}

// listToolsResult calls ListTools on sess and converts the outcome into a
// ServerToolsResult, mirroring probeServer's ListTools handling.
func listToolsResult(ctx context.Context, sess *mcp.ClientSession, serverName string) (ServerToolsResult, error) {
	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return ServerToolsResult{}, err
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return ServerToolsResult{Server: serverName, Tools: names, Reachable: true}, nil
}
