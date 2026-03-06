package web

import (
	"context"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestMultiplexClient_RoutesSessionUpdate(t *testing.T) {
	mc := NewMultiplexClient()

	var received1, received2 bool
	mc.RegisterSession("session-1", &SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			received1 = true
			return nil
		},
	})
	mc.RegisterSession("session-2", &SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			received2 = true
			return nil
		},
	})

	// Send update to session-1
	err := mc.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "session-1",
	})
	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}

	if !received1 {
		t.Error("session-1 callback was not called")
	}
	if received2 {
		t.Error("session-2 callback should not have been called")
	}
}

func TestMultiplexClient_UnknownSessionIgnored(t *testing.T) {
	mc := NewMultiplexClient()

	// Sending to an unknown session should not error
	err := mc.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "unknown-session",
	})
	if err != nil {
		t.Fatalf("SessionUpdate for unknown session should not error: %v", err)
	}
}

func TestMultiplexClient_UnregisterSession(t *testing.T) {
	mc := NewMultiplexClient()

	called := false
	mc.RegisterSession("session-1", &SessionCallbacks{
		OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
			called = true
			return nil
		},
	})

	mc.UnregisterSession("session-1")

	err := mc.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "session-1",
	})
	if err != nil {
		t.Fatalf("SessionUpdate failed: %v", err)
	}
	if called {
		t.Error("callback should not be called after unregister")
	}
}

func TestMultiplexClient_RoutesPermission(t *testing.T) {
	mc := NewMultiplexClient()

	called := false
	mc.RegisterSession("session-1", &SessionCallbacks{
		OnRequestPermission: func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			called = true
			return acp.RequestPermissionResponse{}, nil
		},
	})

	_, err := mc.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "session-1",
		Options:   []acp.PermissionOption{},
	})
	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}
	if !called {
		t.Error("permission callback was not called")
	}
}

func TestMultiplexClient_PermissionCancelledForUnknownSession(t *testing.T) {
	mc := NewMultiplexClient()

	resp, err := mc.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "unknown",
		Options:   []acp.PermissionOption{},
	})
	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Error("expected cancelled outcome for unknown session")
	}
}

func TestMultiplexClient_ConcurrentAccess(t *testing.T) {
	mc := NewMultiplexClient()

	var mu sync.Mutex
	counts := make(map[string]int)

	for i := 0; i < 10; i++ {
		sid := acp.SessionId("session-" + string(rune('a'+i)))
		mc.RegisterSession(sid, &SessionCallbacks{
			OnSessionUpdate: func(ctx context.Context, params acp.SessionNotification) error {
				mu.Lock()
				counts[string(params.SessionId)]++
				mu.Unlock()
				return nil
			},
		})
	}

	// Concurrent updates
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := acp.SessionId("session-" + string(rune('a'+(n%10))))
			mc.SessionUpdate(context.Background(), acp.SessionNotification{
				SessionId: sid,
			})
		}(i)
	}
	wg.Wait()

	mu.Lock()
	total := 0
	for _, c := range counts {
		total += c
	}
	mu.Unlock()

	if total != 100 {
		t.Errorf("expected 100 total updates, got %d", total)
	}
}

func TestMultiplexClient_RoutesFileOperations(t *testing.T) {
	mc := NewMultiplexClient()

	var readCalled, writeCalled bool
	mc.RegisterSession("session-1", &SessionCallbacks{
		OnReadTextFile: func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
			readCalled = true
			return acp.ReadTextFileResponse{Content: "test content"}, nil
		},
		OnWriteTextFile: func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
			writeCalled = true
			return acp.WriteTextFileResponse{}, nil
		},
	})

	// Read
	resp, err := mc.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		SessionId: "session-1",
		Path:      "/test/file.txt",
	})
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}
	if !readCalled {
		t.Error("read callback was not called")
	}
	if resp.Content != "test content" {
		t.Errorf("expected 'test content', got %q", resp.Content)
	}

	// Write
	_, err = mc.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		SessionId: "session-1",
		Path:      "/test/file.txt",
		Content:   "new content",
	})
	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}
	if !writeCalled {
		t.Error("write callback was not called")
	}
}
