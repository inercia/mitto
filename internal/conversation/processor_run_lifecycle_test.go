package conversation

import (
	"log/slog"
	"testing"

	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

func TestAfterProcessorRunPersistenceOmissionSummarizedAfterSessionClose(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sessionID = "after-phase-delete-race"
	recorder := session.NewRecorderWithID(store, sessionID)
	if err := recorder.Start("test-server", t.TempDir(), ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	handler := &recordingHandler{}
	bs := &BackgroundSession{
		persistedID: sessionID,
		recorder:    recorder,
		logger:      slog.New(handler),
		nextSeq:     2,
	}
	bs.closed.Store(1)
	if err := recorder.End(session.SessionEndData{Reason: "parent_archived"}); err != nil {
		t.Fatalf("End failed: %v", err)
	}

	bs.recordProcessorRun(processors.ProcessorRun{Name: "one", Phase: "after", Outcome: "applied"})
	bs.recordProcessorRun(processors.ProcessorRun{Name: "two", Phase: "after", Outcome: "applied"})
	bs.logAfterProcessorRunOmissions()
	bs.logAfterProcessorRunOmissions() // A drained count must not log twice.

	if handler.hasRecord(slog.LevelWarn, "Failed to persist processor run") {
		t.Fatal("session-close omission emitted a generic persistence WARN")
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	summaries := 0
	for _, record := range handler.records {
		if record.Level != slog.LevelDebug || record.Message != "processor_run persistence skipped" {
			continue
		}
		summaries++
		attrs := make(map[string]any)
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.Any()
			return true
		})
		if attrs["phase"] != "after" || attrs["reason"] != "session_closed" {
			t.Fatalf("unexpected lifecycle attrs: %#v", attrs)
		}
		if attrs["omitted_processor_runs"] != int64(2) {
			t.Fatalf("omitted_processor_runs = %#v, want 2", attrs["omitted_processor_runs"])
		}
	}
	if summaries != 1 {
		t.Fatalf("lifecycle summaries = %d, want 1", summaries)
	}
}
