package processors

import (
	"context"
	"strings"
	"testing"
	"time"
)

// waitForPendingDispatchEntries polls store.Load(workspaceUUID) until at
// least minCount entries are present or a bounded deadline passes.
// ApplyOnClose's prompt-mode dispatch runs asynchronously (dispatchPromptBatch
// wraps dispatchWithRetry in a goroutine), so tests that need to inspect the
// fully-assembled prompt text must synchronize on the durable spool rather
// than asserting immediately after ApplyOnClose returns.
func waitForPendingDispatchEntries(t *testing.T, store *FilePendingDispatchStore, workspaceUUID string, minCount int) []PendingDispatchEntry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := store.Load(workspaceUUID)
		if err != nil {
			t.Fatalf("store.Load: %v", err)
		}
		if len(entries) >= minCount {
			return entries
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d pending-dispatch entries; got %d", minCount, len(entries))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// applyCloseAndCaptureSpooledPrompt runs ApplyOnClose against processors with
// a forced spool deferral (via SetShouldDeferDispatchFunc) so the fully
// assembled prompt text lands in the durable spool without any real RPC,
// then returns the single persisted entry's prompt.
func applyCloseAndCaptureSpooledPrompt(t *testing.T, m *Manager, workspaceUUID string, input CloseProcessorInput) string {
	t.Helper()
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)
	m.SetShouldDeferDispatchFunc(func(ws string) bool { return ws == workspaceUUID })
	// hasPromptExecutor() gates prompt-mode collection on a configured
	// PromptFunc; this one is never actually invoked because the forced
	// defer predicate above sends dispatchWithRetry straight to the spool.
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	input.WorkspaceUUID = workspaceUUID
	m.ApplyOnClose(context.Background(), input)

	entries := waitForPendingDispatchEntries(t, store, workspaceUUID, 1)
	if len(entries) != 1 {
		t.Fatalf("pending-dispatch entries = %d, want exactly 1", len(entries))
	}
	return entries[0].Prompt
}

func snapshotBlockCount(body string) int {
	return strings.Count(body, "<mitto_close_history_snapshot>")
}

// TestApplyOnClose_SharedSnapshot_ZeroProcessors verifies no dispatch (and no
// spool entry) is produced when no prompt-mode close processor is configured.
func TestApplyOnClose_SharedSnapshot_ZeroProcessors(t *testing.T) {
	m := NewManager("", nil)
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)
	m.SetShouldDeferDispatchFunc(func(string) bool { return true })

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "sess-zero", WorkspaceUUID: "ws-zero",
		HistorySnapshot: `{"events":[]}`,
	})

	entries, err := store.Load("ws-zero")
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch entries = %d, want 0 for zero prompt-mode processors", len(entries))
	}
}

// TestApplyOnClose_SharedSnapshot_OneProcessor verifies the dispatched body
// for a single prompt-mode close processor contains exactly one snapshot
// block at the top, with the processor's own body preserved verbatim below.
func TestApplyOnClose_SharedSnapshot_OneProcessor(t *testing.T) {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "solo", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Solo processor body."},
	}

	body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-one", CloseProcessorInput{
		SessionID:       "sess-one",
		HistorySnapshot: `{"events":[{"seq":1}]}`,
	})

	if got := snapshotBlockCount(body); got != 1 {
		t.Fatalf("snapshot block count = %d, want exactly 1; body=%q", got, body)
	}
	if !strings.HasPrefix(body, "<mitto_close_history_snapshot>") {
		t.Fatalf("body does not start with the snapshot block: %q", body)
	}
	if !strings.Contains(body, "Solo processor body.") {
		t.Fatalf("body missing processor's own text verbatim: %q", body)
	}
	if !strings.Contains(body, `{"events":[{"seq":1}]}`) {
		t.Fatalf("body missing the raw snapshot payload: %q", body)
	}
}

// TestApplyOnClose_SharedSnapshot_MultipleProcessors_ExactlyOneBlock is the
// mitto-353 core acceptance criterion: an N-processor batch must deliver
// exactly one copy of the snapshot, positioned above the combined
// requirements list, not once per processor.
func TestApplyOnClose_SharedSnapshot_MultipleProcessors_ExactlyOneBlock(t *testing.T) {
	for _, n := range []int{2, 4} {
		n := n
		t.Run(string(rune('0'+n))+"-processors", func(t *testing.T) {
			m := NewManager("", nil)
			for i := 0; i < n; i++ {
				m.processors = append(m.processors, &Processor{
					Name:   "proc-" + string(rune('a'+i)),
					When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
					Prompt: "Body for processor " + string(rune('a'+i)) + ".",
				})
			}

			body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-multi", CloseProcessorInput{
				SessionID:       "sess-multi",
				HistorySnapshot: `{"events":[{"seq":42}]}`,
			})

			if got := snapshotBlockCount(body); got != 1 {
				t.Fatalf("snapshot block count = %d, want exactly 1 for a %d-processor batch; body=%q", got, n, body)
			}
			reqIdx := strings.Index(body, "## Requirement 1:")
			snapIdx := strings.Index(body, "<mitto_close_history_snapshot>")
			if snapIdx < 0 || reqIdx < 0 || snapIdx > reqIdx {
				t.Fatalf("snapshot block must appear above the requirements list; snapIdx=%d reqIdx=%d body=%q", snapIdx, reqIdx, body)
			}
			for i := 0; i < n; i++ {
				want := "Body for processor " + string(rune('a'+i)) + "."
				if !strings.Contains(body, want) {
					t.Errorf("body missing processor %d's own text: %q", i, want)
				}
			}
		})
	}
}

// TestApplyOnClose_SharedSnapshot_CustomWorkspaceProcessorMixedWithBuiltin
// verifies the "exactly one" invariant holds when a non-builtin (workspace-
// defined) prompt-mode close processor is batched alongside another.
func TestApplyOnClose_SharedSnapshot_CustomWorkspaceProcessorMixedWithBuiltin(t *testing.T) {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "extract-memories-on-close", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Builtin-style body."},
		{Name: "my-custom-workspace-processor", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Custom workspace body."},
	}

	body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-custom", CloseProcessorInput{
		SessionID:       "sess-custom",
		HistorySnapshot: `{"events":[{"seq":7}]}`,
	})

	if got := snapshotBlockCount(body); got != 1 {
		t.Fatalf("snapshot block count = %d, want exactly 1 for builtin+custom mix; body=%q", got, body)
	}
	if !strings.Contains(body, "Custom workspace body.") {
		t.Fatalf("body missing custom processor's own text: %q", body)
	}
}

// TestApplyOnClose_SharedSnapshot_TemplateRenderingStillApplies verifies
// per-processor template placeholders still expand while the snapshot
// payload itself is never template-processed (it is untrusted data).
func TestApplyOnClose_SharedSnapshot_TemplateRenderingStillApplies(t *testing.T) {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "templated", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Working dir is {{ .Workspace.Folder }}."},
	}

	body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-tmpl", CloseProcessorInput{
		SessionID:       "sess-tmpl",
		WorkingDir:      "/tmp/example-workspace",
		HistorySnapshot: `{"events":[{"seq":1,"text":"{{ .Workspace.Folder }}"}]}`,
	})

	if !strings.Contains(body, "Working dir is /tmp/example-workspace.") {
		t.Fatalf("processor body template was not rendered: %q", body)
	}
	if strings.Contains(body, `"text":"/tmp/example-workspace"`) {
		t.Fatalf("snapshot payload must NOT be template-processed (untrusted data): %q", body)
	}
	if !strings.Contains(body, `"text":"{{ .Workspace.Folder }}"`) {
		t.Fatalf("snapshot payload text was mutated; want the raw literal preserved: %q", body)
	}
}

// TestApplyOnClose_SharedSnapshot_EmptySnapshot_NoBlock verifies an empty
// HistorySnapshot produces a combined body with no snapshot block at all
// (same gate as the pre-refactor per-processor append).
func TestApplyOnClose_SharedSnapshot_EmptySnapshot_NoBlock(t *testing.T) {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "proc-a", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Body A."},
		{Name: "proc-b", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Body B."},
	}

	body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-empty-snap", CloseProcessorInput{
		SessionID:       "sess-empty-snap",
		HistorySnapshot: "",
	})

	if got := snapshotBlockCount(body); got != 0 {
		t.Fatalf("snapshot block count = %d, want 0 when HistorySnapshot is empty; body=%q", got, body)
	}
}

// TestApplyOnClose_SharedSnapshot_SpoolReplay_PreservesExactlyOneBlock
// verifies the spooled combined prompt contains exactly one snapshot block
// and that FlushPendingDispatches replays the identical byte string.
func TestApplyOnClose_SharedSnapshot_SpoolReplay_PreservesExactlyOneBlock(t *testing.T) {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "proc-a", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Replay body A."},
		{Name: "proc-b", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Replay body B."},
	}

	const workspaceUUID = "ws-replay"
	body := applyCloseAndCaptureSpooledPrompt(t, m, workspaceUUID, CloseProcessorInput{
		SessionID:       "sess-replay",
		HistorySnapshot: `{"events":[{"seq":99}]}`,
	})
	if got := snapshotBlockCount(body); got != 1 {
		t.Fatalf("spooled body snapshot block count = %d, want exactly 1", got)
	}

	var replayed string
	m.SetPromptFunc(func(_ context.Context, _, _, prompt string) error {
		replayed = prompt
		return nil
	})
	// Allow real delivery on the retry: the forced-defer predicate from
	// applyCloseAndCaptureSpooledPrompt still applies to this manager, so
	// disable it for the flush.
	m.SetShouldDeferDispatchFunc(func(string) bool { return false })
	m.FlushPendingDispatches(context.Background(), workspaceUUID)

	if replayed != body {
		t.Fatalf("replayed prompt does not match the pre-spool body byte-for-byte\nspooled:  %q\nreplayed: %q", body, replayed)
	}
	if got := snapshotBlockCount(replayed); got != 1 {
		t.Fatalf("replayed prompt snapshot block count = %d, want exactly 1", got)
	}
}
