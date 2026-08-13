package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestFilePendingDispatchStore_ConcurrentAppendAcrossInstances verifies
// mitto-gfr1: independently constructed stores targeting one workspace must
// serialize the whole read-modify-write transaction and retain every entry.
func TestFilePendingDispatchStore_ConcurrentAppendAcrossInstances(t *testing.T) {
	spoolDir := t.TempDir()
	const wsUUID = "ws-concurrent-append"
	stores := []*FilePendingDispatchStore{
		{BaseDir: spoolDir},
		{BaseDir: spoolDir},
	}

	start := make(chan struct{})
	errCh := make(chan error, pendingDispatchMaxEntries)
	var wg sync.WaitGroup
	for i := 0; i < pendingDispatchMaxEntries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := stores[i%len(stores)].Append(PendingDispatchEntry{
				WorkspaceUUID: wsUUID,
				Name:          fmt.Sprintf("batch-%02d", i),
				Prompt:        fmt.Sprintf("prompt-%02d", i),
				SavedAt:       time.Now(),
			})
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Append() error = %v", err)
	}

	entries, err := stores[0].Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != pendingDispatchMaxEntries {
		t.Fatalf("Load() returned %d entries, want %d", len(entries), pendingDispatchMaxEntries)
	}
	prompts := make(map[string]bool, len(entries))
	IDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			t.Fatal("persisted entry has empty audit ID")
		}
		if IDs[entry.ID] {
			t.Fatalf("duplicate audit ID %q", entry.ID)
		}
		IDs[entry.ID] = true
		prompts[entry.Prompt] = true
	}
	for i := 0; i < pendingDispatchMaxEntries; i++ {
		if prompt := fmt.Sprintf("prompt-%02d", i); !prompts[prompt] {
			t.Errorf("missing %s after concurrent appends", prompt)
		}
	}
}

// TestFlushPendingDispatches_AppendDuringFlushPreservesNewEntry reproduces
// mitto-gfr1: a second store appending after a flush claims the spool must not
// have its entry overwritten when the flush writes a failed entry back.
func TestFlushPendingDispatches_AppendDuringFlushPreservesNewEntry(t *testing.T) {
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })

	spoolDir := t.TempDir()
	const wsUUID = "ws-append-during-flush"
	flushStore := &FilePendingDispatchStore{BaseDir: spoolDir}
	appendStore := &FilePendingDispatchStore{BaseDir: spoolDir}
	if _, err := flushStore.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: "batch-a", Prompt: "prompt-a",
		TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1,
	}); err != nil {
		t.Fatalf("Append(batch-a) error = %v", err)
	}

	promptEntered := make(chan struct{})
	releasePrompt := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(releasePrompt)
		}
	})

	m := NewManager("", nil)
	m.SetPendingDispatchStore(flushStore)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		close(promptEntered)
		<-releasePrompt
		return fmt.Errorf("forced dispatch failure")
	})

	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		m.FlushPendingDispatches(context.Background(), wsUUID)
	}()

	select {
	case <-promptEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not reach prompt dispatch after claiming the spool")
	}
	if _, err := appendStore.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: "batch-b", Prompt: "prompt-b",
		TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1,
	}); err != nil {
		t.Fatalf("Append(batch-b) error = %v", err)
	}
	close(releasePrompt)
	released = true

	select {
	case <-flushDone:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not finish after prompt release")
	}

	remaining, err := appendStore.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	counts := make(map[string]int, len(remaining))
	IDs := make(map[string]bool, len(remaining))
	for _, entry := range remaining {
		if entry.ID == "" {
			t.Fatal("persisted entry has empty audit ID")
		}
		if IDs[entry.ID] {
			t.Fatalf("duplicate audit ID %q", entry.ID)
		}
		IDs[entry.ID] = true
		counts[entry.Prompt]++
	}
	if counts["prompt-a"] != 1 || counts["prompt-b"] != 1 || len(remaining) != 2 {
		t.Fatalf("remaining prompts = %v, want prompt-a and prompt-b exactly once", counts)
	}
}

// TestFlushPendingDispatches_TerminalLogsIncludeAuditID verifies mitto-gfr1's
// reconciliation key is carried into both delivery and bounded-drop outcomes.
func TestFlushPendingDispatches_TerminalLogsIncludeAuditID(t *testing.T) {
	tests := []struct {
		name       string
		attempts   int
		wantLogMsg string
	}{
		{name: "delivery", attempts: 1, wantLogMsg: "pending-dispatch flush: delivered spooled batch"},
		{name: "max-attempt drop", attempts: pendingDispatchMaxAttempts, wantLogMsg: "pending-dispatch flush: dropping entry after exceeding max attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const wsUUID = "ws-audit-log"
			const dispatchID = "audit-id-123"
			store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
			if err := store.Replace(wsUUID, []PendingDispatchEntry{{
				ID: dispatchID, WorkspaceUUID: wsUUID, Name: "batch",
				Prompt: "prompt", SavedAt: time.Now(), Attempts: tt.attempts,
			}}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}

			handler := &recordingLogHandler{}
			m := NewManager("", slog.New(handler))
			m.SetPendingDispatchStore(store)
			m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })
			m.FlushPendingDispatches(context.Background(), wsUUID)

			for _, record := range handler.snapshot() {
				if record.Message == tt.wantLogMsg {
					if got := record.Attrs["dispatch_id"]; got != dispatchID {
						t.Fatalf("dispatch_id = %v, want %q", got, dispatchID)
					}
					return
				}
			}
			t.Fatalf("missing log message %q", tt.wantLogMsg)
		})
	}
}
