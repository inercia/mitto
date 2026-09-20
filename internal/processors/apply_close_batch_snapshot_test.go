package processors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
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

// TestApplyOnClose_SharedSnapshot_TelemetryAttributedExactlyOnce is the
// mitto-353 telemetry acceptance criterion: for an N-processor batch, the
// persisted close-run-summary sidecar must contain exactly ONE synthetic
// sharedCloseHistorySnapshotRunName entry carrying the snapshot's own
// bytes/tokens, and every real processor's own entry must remain body-only
// (not inflated by a per-processor copy of the snapshot).
func TestApplyOnClose_SharedSnapshot_TelemetryAttributedExactlyOnce(t *testing.T) {
	sessionID := "sess-telemetry-once"
	store := newCloseRouterApplyTestStore(t, sessionID)

	m := NewManager("", nil)
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })
	m.processors = []*Processor{
		{Name: "proc-a", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Body for processor a."},
		{Name: "proc-b", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Body for processor b."},
		{Name: "proc-c", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Prompt: "Body for processor c."},
	}

	// A snapshot payload deliberately much larger than any processor's own
	// body, so a mis-attributed (per-processor) copy would be obvious.
	snapshot := `{"events":[{"seq":1,"text":"this is a realistic chunk of archived conversation history that is far longer than any single processor instruction body"}]}`

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       sessionID,
		SessionStore:    store,
		ArchiveReason:   "manual",
		HistorySnapshot: snapshot,
	})

	summary, err := session.ReadCloseRunSummary(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if len(summary.Runs) != 1 {
		t.Fatalf("Runs = %+v, want exactly 1 persisted run", summary.Runs)
	}
	run := summary.Runs[0]

	var sharedEntries []session.CloseRunProcessorEntry
	var processorEntries []session.CloseRunProcessorEntry
	for _, p := range run.Processors {
		if p.Name == sharedCloseHistorySnapshotRunName {
			sharedEntries = append(sharedEntries, p)
			continue
		}
		processorEntries = append(processorEntries, p)
	}

	if len(sharedEntries) != 1 {
		t.Fatalf("synthetic %s entries = %d, want exactly 1; run.Processors=%+v", sharedCloseHistorySnapshotRunName, len(sharedEntries), run.Processors)
	}
	shared := sharedEntries[0]
	if shared.RenderedBytes == 0 || shared.EstimatedTokens == 0 {
		t.Errorf("synthetic snapshot entry has zero size: %+v", shared)
	}
	if shared.Target != RunTargetAuxiliary {
		t.Errorf("synthetic snapshot entry Target = %q, want %q", shared.Target, RunTargetAuxiliary)
	}

	if len(processorEntries) != len(m.processors) {
		t.Fatalf("processor entries = %d, want %d (one per real processor, no duplication)", len(processorEntries), len(m.processors))
	}
	for _, p := range processorEntries {
		// Each real processor's own RenderedBytes must reflect only its own
		// body, never a duplicated copy of the (much larger) shared snapshot.
		if p.RenderedBytes >= shared.RenderedBytes {
			t.Errorf("processor %q RenderedBytes = %d, want far smaller than the shared snapshot's %d (would indicate a duplicated per-processor copy)",
				p.Name, p.RenderedBytes, shared.RenderedBytes)
		}
	}
}

// TestApplyOnClose_SharedSnapshot_FourProcessorBatchTokenReduction is the
// mitto-353 acceptance criterion: "a representative four-processor batch
// demonstrates at least a 50 percent rendered-token reduction without losing
// source history". It compares the actual (shared-envelope) dispatched
// prompt against the arithmetic equivalent of the pre-refactor behavior
// (the same snapshot block duplicated once per processor instead of once
// for the whole batch), while confirming the source history text itself
// still appears intact exactly once.
func TestApplyOnClose_SharedSnapshot_FourProcessorBatchTokenReduction(t *testing.T) {
	m := NewManager("", nil)
	for _, name := range []string{"proc-a", "proc-b", "proc-c", "proc-d"} {
		m.processors = append(m.processors, &Processor{
			Name:   name,
			When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			Prompt: "Please review the archived conversation and act on requirement " + name + ".",
		})
	}

	// A realistic bounded snapshot: several events, each with modest text,
	// intentionally large relative to any single processor's own body so a
	// per-processor duplication would dominate the token count.
	const snapshotEventText = "user or assistant message text from the archived conversation, bounded per the last-50-event and per-text size caps"
	snapshot := `{"events":[`
	for i := 0; i < 12; i++ {
		if i > 0 {
			snapshot += ","
		}
		snapshot += `{"seq":` + string(rune('0'+i%10)) + `,"text":"` + snapshotEventText + `"}`
	}
	snapshot += `]}`

	body := applyCloseAndCaptureSpooledPrompt(t, m, "ws-four-reduction", CloseProcessorInput{
		SessionID:       "sess-four-reduction",
		HistorySnapshot: snapshot,
	})

	if got := snapshotBlockCount(body); got != 1 {
		t.Fatalf("snapshot block count = %d, want exactly 1 in the actual dispatched batch", got)
	}
	if !strings.Contains(body, snapshotEventText) {
		t.Fatalf("dispatched batch lost the source history text: %q", body)
	}

	startTag, endTag := "<mitto_close_history_snapshot>", "</mitto_close_history_snapshot>"
	startIdx := strings.Index(body, startTag)
	endIdx := strings.Index(body, endTag)
	if startIdx < 0 || endIdx < 0 || endIdx < startIdx {
		t.Fatalf("could not locate snapshot block bounds in body: %q", body)
	}
	snapshotBlockLen := (endIdx + len(endTag)) - startIdx

	const numProcessors = 4
	actualTokens := EstimateTokens(body)
	// Arithmetic equivalent of the pre-refactor behavior: the same
	// non-snapshot content (batch header + all processor bodies), but with
	// the snapshot block duplicated once per processor instead of shared.
	bodyWithoutOneSnapshotCopy := len(body) - snapshotBlockLen
	preRefactorEquivalentLen := bodyWithoutOneSnapshotCopy + numProcessors*snapshotBlockLen
	preRefactorEquivalentTokens := EstimateTokens(string(make([]byte, preRefactorEquivalentLen)))

	if preRefactorEquivalentTokens == 0 {
		t.Fatalf("preRefactorEquivalentTokens = 0, cannot compute a reduction ratio")
	}
	reduction := float64(preRefactorEquivalentTokens-actualTokens) / float64(preRefactorEquivalentTokens)
	if reduction < 0.5 {
		t.Fatalf("rendered-token reduction = %.1f%%, want >= 50%%: actual=%d tokens, pre-refactor-equivalent=%d tokens (snapshot block = %d bytes, duplicated %dx)",
			reduction*100, actualTokens, preRefactorEquivalentTokens, snapshotBlockLen, numProcessors)
	}
}

// TestBuiltinCloseConversationProcessors_DoNotInvokeConversationHistoryTool
// is the mitto-353 acceptance criterion: "built-in close processors do not
// call mitto_conversation_history for the source conversation". The four
// migrated conversationClosed prompts still *mention* the tool name (to warn
// the agent away from it, since the source session may already be deleted),
// so this asserts the specific "do not call" phrasing survives and rejects
// any positive invocation instruction creeping back in.
func TestBuiltinCloseConversationProcessors_DoNotInvokeConversationHistoryTool(t *testing.T) {
	for _, name := range []string{
		"extract-memories-on-close",
		"memorize-preferences",
		"claude-update-memory",
		"auggie-update-rules",
	} {
		t.Run(name, func(t *testing.T) {
			proc := loadBuiltinProcessorForTest(t, name)

			if !strings.Contains(proc.Prompt, "do not call") {
				t.Errorf("%s prompt no longer warns against calling the tool (missing \"do not call\"):\n%s", name, proc.Prompt)
			}
			if !strings.Contains(proc.Prompt, "mitto_conversation_history") {
				t.Errorf("%s prompt dropped the mitto_conversation_history mention entirely; the warning needs to name the tool:\n%s", name, proc.Prompt)
			}
			for _, positivePhrase := range []string{
				"Use the `mitto_conversation_history`",
				"Use `mitto_conversation_history`",
				"call the `mitto_conversation_history` tool",
				"call `mitto_conversation_history`",
			} {
				if strings.Contains(proc.Prompt, positivePhrase) {
					t.Errorf("%s prompt reintroduces a positive instruction to invoke the tool (%q):\n%s", name, positivePhrase, proc.Prompt)
				}
			}
		})
	}
}
