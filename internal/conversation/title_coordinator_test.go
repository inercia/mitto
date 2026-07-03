package conversation

import (
	"errors"
	"log/slog"
	"testing"
)

// compile-time check that fakeTitleDeps satisfies titleDeps.
var _ titleDeps = (*fakeTitleDeps)(nil)

type fakeTitleDeps struct {
	noTitle      bool
	resolved     string
	configured   bool
	resolveErr   error
	resolverName string // records last name passed to resolvePromptName
	resolverHits int
	started      []string // records messages passed to startTitleGeneration
}

func (f *fakeTitleDeps) sessionHasNoTitle() bool { return f.noTitle }
func (f *fakeTitleDeps) startTitleGeneration(m string) {
	f.started = append(f.started, m)
}
func (f *fakeTitleDeps) resolvePromptName(name string) (string, bool, error) {
	f.resolverHits++
	f.resolverName = name
	return f.resolved, f.configured, f.resolveErr
}
func (f *fakeTitleDeps) titleLogger() *slog.Logger { return nil }
func (f *fakeTitleDeps) titleSessionID() string    { return "test-session" }

func TestTitleCoordinator_NeedsTitle(t *testing.T) {
	tc := titleCoordinator{}

	t.Run("false when title exists", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: false}
		if tc.needsTitle(d) {
			t.Fatal("expected false when session has a title")
		}
	})

	t.Run("true when no title", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true}
		if !tc.needsTitle(d) {
			t.Fatal("expected true when session has no title")
		}
	})
}

func TestTitleCoordinator_RetryIfNeeded(t *testing.T) {
	tc := titleCoordinator{}

	t.Run("noop when title exists", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: false}
		tc.retryIfNeeded(d, "hello")
		if len(d.started) != 0 {
			t.Fatalf("expected no startTitleGeneration call, got %d", len(d.started))
		}
	})

	t.Run("calls startTitleGeneration when no title", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true}
		tc.retryIfNeeded(d, "fix the bug")
		if len(d.started) != 1 || d.started[0] != "fix the bug" {
			t.Fatalf("expected startTitleGeneration(\"fix the bug\"), got %v", d.started)
		}
	})
}

func TestTitleCoordinator_Trigger(t *testing.T) {
	tc := titleCoordinator{}

	t.Run("noop when title exists", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: false}
		tc.trigger(d, "hello")
		if len(d.started) != 0 {
			t.Fatalf("expected no call, got %d", len(d.started))
		}
	})

	t.Run("calls startTitleGeneration when no title", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true}
		tc.trigger(d, "refactor auth")
		if len(d.started) != 1 || d.started[0] != "refactor auth" {
			t.Fatalf("expected startTitleGeneration(\"refactor auth\"), got %v", d.started)
		}
	})
}

func TestTitleCoordinator_TriggerFromPeriodic(t *testing.T) {
	tc := titleCoordinator{}

	t.Run("usable inline used; resolver not consulted", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: true}
		tc.triggerFromPeriodic(d, "Real text here", "some-prompt")
		if len(d.started) != 1 || d.started[0] != "Real text here" {
			t.Fatalf("expected \"Real text here\", got %v", d.started)
		}
		if d.resolverHits != 0 {
			t.Fatalf("expected resolver not called, got %d hits", d.resolverHits)
		}
	})

	t.Run("(pending) + resolver returns non-empty → use resolved", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: true, resolved: "Resolved prompt text"}
		tc.triggerFromPeriodic(d, "(pending)", "my-prompt")
		if len(d.started) != 1 || d.started[0] != "Resolved prompt text" {
			t.Fatalf("expected resolved text, got %v", d.started)
		}
	})

	t.Run("(pending) + resolver error → use bare name", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: true, resolveErr: errors.New("lookup failed")}
		tc.triggerFromPeriodic(d, "(pending)", "my-prompt")
		if len(d.started) != 1 || d.started[0] != "my-prompt" {
			t.Fatalf("expected bare name \"my-prompt\", got %v", d.started)
		}
	})

	t.Run("empty inline + no resolver configured → use bare name", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: false}
		tc.triggerFromPeriodic(d, "", "bare-prompt")
		if len(d.started) != 1 || d.started[0] != "bare-prompt" {
			t.Fatalf("expected bare name, got %v", d.started)
		}
	})

	t.Run("both empty → noop", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true}
		tc.triggerFromPeriodic(d, "", "")
		if len(d.started) != 0 {
			t.Fatalf("expected no call, got %v", d.started)
		}
	})

	t.Run("whitespace-only inline treated as empty → use bare name", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: false}
		tc.triggerFromPeriodic(d, "   ", "the-prompt")
		if len(d.started) != 1 || d.started[0] != "the-prompt" {
			t.Fatalf("expected bare name, got %v", d.started)
		}
	})

	t.Run("resolver configured but returns empty, no err → fall back to bare name", func(t *testing.T) {
		d := &fakeTitleDeps{noTitle: true, configured: true, resolved: ""}
		tc.triggerFromPeriodic(d, "(pending)", "my-prompt")
		if len(d.started) != 1 || d.started[0] != "my-prompt" {
			t.Fatalf("expected bare name, got %v", d.started)
		}
		if d.resolverHits < 1 {
			t.Fatalf("expected resolver to be called")
		}
	})
}
