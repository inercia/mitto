package processors

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestDispatchPromptBatch_BatchedLogsCombinedTokensAndPerProcessorBreakdown
// covers mitto-sl5's first acceptance criterion ("processor token usage is
// observable in logs/metrics"): the batched-dispatch log line must carry
// combined_estimated_tokens plus parallel per-processor
// processor_names/processor_prompt_lens/processor_estimated_tokens slices,
// in the same order as the input prompts.
func TestDispatchPromptBatch_BatchedLogsCombinedTokensAndPerProcessorBreakdown(t *testing.T) {
	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	prompts := []pendingPromptDispatch{
		{name: "extract-memories-on-close", prompt: strings.Repeat("a", 100)},
		{name: "claude-update-memory", prompt: strings.Repeat("b", 250)},
	}
	m.dispatchPromptBatch("ws", prompts, false, "")

	rec := findLogRecord(t, handler.snapshot(), "prompt-mode processors dispatched (batched)")

	names, ok := rec.Attrs["processor_names"].([]string)
	if !ok || len(names) != 2 || names[0] != "extract-memories-on-close" || names[1] != "claude-update-memory" {
		t.Fatalf("processor_names = %#v, want [extract-memories-on-close claude-update-memory] in order", rec.Attrs["processor_names"])
	}
	promptLens, ok := rec.Attrs["processor_prompt_lens"].([]int)
	if !ok || len(promptLens) != 2 || promptLens[0] != 100 || promptLens[1] != 250 {
		t.Fatalf("processor_prompt_lens = %#v, want [100 250]", rec.Attrs["processor_prompt_lens"])
	}
	estTokens, ok := rec.Attrs["processor_estimated_tokens"].([]int)
	if !ok || len(estTokens) != 2 || estTokens[0] != EstimateTokens(prompts[0].prompt) || estTokens[1] != EstimateTokens(prompts[1].prompt) {
		t.Fatalf("processor_estimated_tokens = %#v, want [%d %d]", rec.Attrs["processor_estimated_tokens"],
			EstimateTokens(prompts[0].prompt), EstimateTokens(prompts[1].prompt))
	}

	combinedLen, ok := rec.Attrs["combined_prompt_len"].(int64)
	if !ok {
		t.Fatalf("combined_prompt_len = %#v, want int64", rec.Attrs["combined_prompt_len"])
	}
	// The combined prompt wraps each processor's prompt in a header/footer
	// (see dispatchPromptBatch), so it must be strictly larger than the sum
	// of the raw per-processor prompt lengths.
	if int(combinedLen) <= promptLens[0]+promptLens[1] {
		t.Fatalf("combined_prompt_len = %d, want > sum of processor prompt lens (%d)", combinedLen, promptLens[0]+promptLens[1])
	}
	wantCombinedTokens := EstimateTokens(strings.Repeat("x", int(combinedLen)))
	combinedTokens, ok := rec.Attrs["combined_estimated_tokens"].(int64)
	if !ok || int(combinedTokens) != wantCombinedTokens {
		t.Fatalf("combined_estimated_tokens = %#v, want %d (derived from combined_prompt_len via EstimateTokens)",
			rec.Attrs["combined_estimated_tokens"], wantCombinedTokens)
	}
}

// TestDispatchPromptBatch_SingleLogsEstimatedTokens covers the single-
// processor parity fix: "prompt-mode processor dispatched (single)" must
// also carry estimated_tokens.
func TestDispatchPromptBatch_SingleLogsEstimatedTokens(t *testing.T) {
	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	prompt := strings.Repeat("z", 40)
	m.dispatchPromptBatch("ws", []pendingPromptDispatch{{name: "solo-processor", prompt: prompt}}, false, "")

	rec := findLogRecord(t, handler.snapshot(), "prompt-mode processor dispatched (single)")
	if got := rec.Attrs["estimated_tokens"]; got != int64(EstimateTokens(prompt)) {
		t.Errorf("estimated_tokens = %v, want %d", got, EstimateTokens(prompt))
	}
}

// TestDispatchPromptBatch_WarnsWhenCombinedPromptExceedsSoftCeiling covers
// mitto-sl5's second acceptance criterion ("combined close-phase prompt
// size has a documented/bounded ceiling"): once combined_prompt_len exceeds
// maxCombinedCloseBatchPromptBytes, an additional WARN line must fire with
// the same per-processor breakdown plus soft_ceiling_bytes. The batch
// itself must still be dispatched (warn-only enforcement).
func TestDispatchPromptBatch_WarnsWhenCombinedPromptExceedsSoftCeiling(t *testing.T) {
	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	// Two prompts whose combined size comfortably exceeds the 256KB ceiling.
	big := strings.Repeat("m", 150_000)
	prompts := []pendingPromptDispatch{
		{name: "proc-a", prompt: big},
		{name: "proc-b", prompt: big},
	}
	m.dispatchPromptBatch("ws", prompts, false, "")

	batched := findLogRecord(t, handler.snapshot(), "prompt-mode processors dispatched (batched)")
	combinedLen := batched.Attrs["combined_prompt_len"].(int64)
	if int(combinedLen) <= maxCombinedCloseBatchPromptBytes {
		t.Fatalf("test setup: combined_prompt_len = %d, want > %d ceiling", combinedLen, maxCombinedCloseBatchPromptBytes)
	}

	warn := findLogRecord(t, handler.snapshot(), "close-phase batched prompt exceeds soft ceiling")
	if warn.Level != slog.LevelWarn {
		t.Errorf("ceiling-exceeded record level = %v, want WARN", warn.Level)
	}
	if got := warn.Attrs["soft_ceiling_bytes"]; got != int64(maxCombinedCloseBatchPromptBytes) {
		t.Errorf("soft_ceiling_bytes = %v, want %d", got, maxCombinedCloseBatchPromptBytes)
	}
	if got := warn.Attrs["combined_prompt_len"]; got != combinedLen {
		t.Errorf("warn combined_prompt_len = %v, want %d (match the batched dispatch line)", got, combinedLen)
	}
}

// TestDispatchPromptBatch_NoWarnUnderSoftCeiling ensures the WARN line is
// NOT emitted for ordinary, well-under-ceiling batches — the ceiling check
// must not be a false-positive noise source.
func TestDispatchPromptBatch_NoWarnUnderSoftCeiling(t *testing.T) {
	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	prompts := []pendingPromptDispatch{
		{name: "proc-a", prompt: "short prompt one"},
		{name: "proc-b", prompt: "short prompt two"},
	}
	m.dispatchPromptBatch("ws", prompts, false, "")

	for _, rec := range handler.snapshot() {
		if rec.Message == "close-phase batched prompt exceeds soft ceiling" {
			t.Fatalf("unexpected soft-ceiling WARN for a small batch: %+v", rec)
		}
	}
}

// findLogRecord returns the first captured record with the given message,
// failing the test if none is found.
func findLogRecord(t *testing.T, records []capturedLogRecord, message string) capturedLogRecord {
	t.Helper()
	for _, rec := range records {
		if rec.Message == message {
			return rec
		}
	}
	t.Fatalf("no log record with message %q found: %+v", message, records)
	return capturedLogRecord{}
}
