// coalesce_test.go — unit tests for target.reuseCoalesce plumbing (mitto-djs1).
// Covers PromptMatchesActiveOrQueued (shared helper) and BackgroundSession's
// ActivePromptDispatch/clearActiveDispatchLocked pairing.
package conversation

import (
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// stubBS is a package-private stub implementing bgSessionLike so
// PromptMatchesActiveOrQueued can be exercised without wiring a real
// BackgroundSession (which requires a live ACP process / context).
type stubBS struct {
	name string
	args map[string]string
	ok   bool
}

func (s *stubBS) ActivePromptDispatch() (string, map[string]string, bool) {
	return s.name, s.args, s.ok
}

// --- argsDeepEqual ---

func TestArgsDeepEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, map[string]string{}, true},
		{"empty vs empty", map[string]string{}, map[string]string{}, true},
		{"identical single entry", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"identical multi entry", map[string]string{"a": "1", "b": "2"}, map[string]string{"b": "2", "a": "1"}, true},
		{"different length", map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}, false},
		{"same length different key", map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
		{"same length different value", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argsDeepEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("argsDeepEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// --- PromptMatchesActiveOrQueued ---

// enqueue adds a queued message with the given promptName/arguments to q.
// Panics on error since the caller is a test.
func enqueue(t *testing.T, q *session.Queue, promptName string, args map[string]string) {
	t.Helper()
	if _, err := q.Add("", nil, nil, "test-client", nil, 0, args, promptName); err != nil {
		t.Fatalf("q.Add(promptName=%q): %v", promptName, err)
	}
}

func TestPromptMatchesActiveOrQueued_FreeTextBypass(t *testing.T) {
	// Free-text dispatches (empty promptName) NEVER coalesce — even when the
	// active dispatch is also free-text, the check must bypass to normal enqueue.
	bs := &stubBS{name: "", args: nil, ok: true}
	q := session.NewQueue(t.TempDir())
	if PromptMatchesActiveOrQueued(bs, q, "", nil) {
		t.Error("free-text (empty promptName) must never coalesce, got true")
	}
	if PromptMatchesActiveOrQueued(bs, q, "", map[string]string{"x": "y"}) {
		t.Error("free-text with arguments must never coalesce, got true")
	}
}

func TestPromptMatchesActiveOrQueued_NilInputs(t *testing.T) {
	// Nil bs + empty queue → false. Nil q → false. Both nil → false.
	q := session.NewQueue(t.TempDir())
	if PromptMatchesActiveOrQueued(nil, q, "Weekly", nil) {
		t.Error("nil bs + empty queue should not match")
	}
	if PromptMatchesActiveOrQueued(nil, nil, "Weekly", nil) {
		t.Error("nil bs + nil queue should not match")
	}
	// Non-nil bs but not prompting (ok=false) + empty queue → false.
	idle := &stubBS{ok: false}
	if PromptMatchesActiveOrQueued(idle, q, "Weekly", nil) {
		t.Error("idle bs + empty queue should not match")
	}
}

func TestPromptMatchesActiveOrQueued_ActiveDispatch(t *testing.T) {
	q := session.NewQueue(t.TempDir())

	t.Run("matches active by name+nil args", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", ok: true}
		if !PromptMatchesActiveOrQueued(bs, q, "Weekly", nil) {
			t.Error("expected match on identical name + nil args")
		}
	})

	t.Run("matches active with same args", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", args: map[string]string{"scope": "all"}, ok: true}
		if !PromptMatchesActiveOrQueued(bs, q, "Weekly", map[string]string{"scope": "all"}) {
			t.Error("expected match on identical name + args")
		}
	})

	t.Run("no match on different name", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", ok: true}
		if PromptMatchesActiveOrQueued(bs, q, "Monthly", nil) {
			t.Error("expected no match on different prompt name")
		}
	})

	t.Run("no match on different args", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", args: map[string]string{"scope": "all"}, ok: true}
		if PromptMatchesActiveOrQueued(bs, q, "Weekly", map[string]string{"scope": "one"}) {
			t.Error("expected no match on different args value")
		}
	})

	t.Run("nil args vs empty args on active is equal", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", args: nil, ok: true}
		if !PromptMatchesActiveOrQueued(bs, q, "Weekly", map[string]string{}) {
			t.Error("nil args on active should equal empty args on incoming")
		}
	})

	t.Run("active with args, incoming empty → no match", func(t *testing.T) {
		bs := &stubBS{name: "Weekly", args: map[string]string{"scope": "all"}, ok: true}
		if PromptMatchesActiveOrQueued(bs, q, "Weekly", nil) {
			t.Error("expected no match: active has args, incoming has none")
		}
	})
}

func TestPromptMatchesActiveOrQueued_QueuedEntries(t *testing.T) {
	t.Run("matches on queued entry", func(t *testing.T) {
		q := session.NewQueue(t.TempDir())
		enqueue(t, q, "Weekly", map[string]string{"scope": "all"})
		if !PromptMatchesActiveOrQueued(nil, q, "Weekly", map[string]string{"scope": "all"}) {
			t.Error("expected match on identical queued entry")
		}
	})

	t.Run("queued entry with different name is skipped", func(t *testing.T) {
		q := session.NewQueue(t.TempDir())
		enqueue(t, q, "Monthly", nil)
		if PromptMatchesActiveOrQueued(nil, q, "Weekly", nil) {
			t.Error("expected no match: only entry has different prompt name")
		}
	})

	t.Run("queued free-text (empty PromptName) is ignored", func(t *testing.T) {
		q := session.NewQueue(t.TempDir())
		// Free-text queued message: promptName == "" → must NOT participate.
		enqueue(t, q, "", nil)
		if PromptMatchesActiveOrQueued(nil, q, "Weekly", nil) {
			t.Error("free-text queued entry must be skipped, got coalesce match")
		}
	})

	t.Run("multi entry mix — one match among many", func(t *testing.T) {
		q := session.NewQueue(t.TempDir())
		enqueue(t, q, "Monthly", nil)
		enqueue(t, q, "", nil) // free-text
		enqueue(t, q, "Weekly", map[string]string{"scope": "all"})
		enqueue(t, q, "Other", map[string]string{"x": "y"})
		if !PromptMatchesActiveOrQueued(nil, q, "Weekly", map[string]string{"scope": "all"}) {
			t.Error("expected match among mixed queue entries")
		}
	})

	t.Run("active dispatch match wins even with unrelated queue", func(t *testing.T) {
		q := session.NewQueue(t.TempDir())
		enqueue(t, q, "Monthly", nil)
		bs := &stubBS{name: "Weekly", ok: true}
		if !PromptMatchesActiveOrQueued(bs, q, "Weekly", nil) {
			t.Error("expected match on active dispatch even with non-matching queue")
		}
	})
}

// --- BackgroundSession.ActivePromptDispatch / clearActiveDispatchLocked ---

func TestActivePromptDispatch_DefaultIdle(t *testing.T) {
	bs := &BackgroundSession{}
	name, args, ok := bs.ActivePromptDispatch()
	if ok {
		t.Errorf("default BackgroundSession must be idle; got ok=%v name=%q args=%v", ok, name, args)
	}
	if name != "" || args != nil {
		t.Errorf("idle dispatch must return zero values; got name=%q args=%v", name, args)
	}
}

func TestActivePromptDispatch_ReportsActive(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.activePromptName = "Weekly"
	bs.activePromptArgs = map[string]string{"scope": "all", "mode": "fast"}
	bs.promptMu.Unlock()

	name, args, ok := bs.ActivePromptDispatch()
	if !ok {
		t.Fatal("expected ok=true when isPrompting")
	}
	if name != "Weekly" {
		t.Errorf("name = %q, want %q", name, "Weekly")
	}
	if len(args) != 2 || args["scope"] != "all" || args["mode"] != "fast" {
		t.Errorf("args = %v, want scope=all mode=fast", args)
	}
}

func TestActivePromptDispatch_ReturnsShallowCopy(t *testing.T) {
	// Callers must not be able to mutate the internal live state by writing
	// to the returned args map.
	bs := &BackgroundSession{}
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.activePromptName = "Weekly"
	bs.activePromptArgs = map[string]string{"scope": "all"}
	bs.promptMu.Unlock()

	_, args, ok := bs.ActivePromptDispatch()
	if !ok {
		t.Fatal("expected ok=true")
	}
	args["scope"] = "hijacked"
	args["new"] = "added"

	bs.promptMu.Lock()
	internal := bs.activePromptArgs["scope"]
	_, hasNew := bs.activePromptArgs["new"]
	bs.promptMu.Unlock()

	if internal != "all" {
		t.Errorf("internal args mutated via returned map: got %q, want %q", internal, "all")
	}
	if hasNew {
		t.Error("returned map's new key leaked into internal state")
	}
}

func TestActivePromptDispatch_FreeTextInFlight(t *testing.T) {
	// A free-text prompt in flight: isPrompting=true but activePromptName == "".
	// ok is true; name is empty; args are whatever was captured (may be nil).
	bs := &BackgroundSession{}
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.activePromptName = ""
	bs.activePromptArgs = nil
	bs.promptMu.Unlock()

	name, args, ok := bs.ActivePromptDispatch()
	if !ok {
		t.Fatal("expected ok=true for a free-text in-flight prompt")
	}
	if name != "" {
		t.Errorf("expected empty name for free-text, got %q", name)
	}
	if args != nil {
		t.Errorf("expected nil args for free-text with no args, got %v", args)
	}
}

func TestClearActiveDispatchLocked(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptMu.Lock()
	bs.activePromptName = "Weekly"
	bs.activePromptArgs = map[string]string{"scope": "all"}
	bs.clearActiveDispatchLocked()
	name := bs.activePromptName
	args := bs.activePromptArgs
	bs.promptMu.Unlock()
	if name != "" {
		t.Errorf("activePromptName after clear = %q, want empty", name)
	}
	if args != nil {
		t.Errorf("activePromptArgs after clear = %v, want nil", args)
	}
}

func TestSimulatePromptComplete_ClearsActiveDispatch(t *testing.T) {
	// SimulatePromptComplete is the tests' seam for what pdMarkPromptComplete
	// does in production: clear isPrompting + activePromptName/Args and
	// broadcast on promptCond. Verifies the mitto-djs1 clear is paired with
	// the isPrompting=false transition.
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.activePromptName = "Weekly"
	bs.activePromptArgs = map[string]string{"scope": "all"}
	bs.promptMu.Unlock()

	bs.SimulatePromptComplete()

	name, args, ok := bs.ActivePromptDispatch()
	if ok {
		t.Errorf("after SimulatePromptComplete: expected ok=false, got ok=true name=%q args=%v", name, args)
	}
	if name != "" || args != nil {
		t.Errorf("after SimulatePromptComplete: expected zero values, got name=%q args=%v", name, args)
	}
}
