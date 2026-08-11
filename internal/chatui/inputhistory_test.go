package chatui

import (
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// withTempMittoDir points appdir at a fresh temp dir for the duration of
// the test, mirroring internal/appdir/appdir_test.go's own pattern.
func withTempMittoDir(t *testing.T) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

func TestInputHistory_Add_SkipsEmptyAndConsecutiveDuplicates(t *testing.T) {
	h := newInputHistory()
	h.Add("")
	h.Add("one")
	h.Add("one")
	h.Add("two")

	if got := len(h.entries); got != 2 {
		t.Fatalf("len(entries) = %d, want 2 (empty skipped, consecutive dup skipped)", got)
	}
	if h.entries[0] != "one" || h.entries[1] != "two" {
		t.Errorf("entries = %v, want [one two]", h.entries)
	}
}

func TestInputHistory_Add_CapsAtMaxEntries(t *testing.T) {
	h := newInputHistory()
	for i := 0; i < maxInputHistoryEntries+10; i++ {
		h.Add(string(rune('a' + (i % 26))))
	}
	if got := len(h.entries); got != maxInputHistoryEntries {
		t.Fatalf("len(entries) = %d, want cap %d", got, maxInputHistoryEntries)
	}
}

func TestInputHistory_PrevNext_WalksAndRestoresDraft(t *testing.T) {
	h := newInputHistory()
	h.Add("first")
	h.Add("second")

	// Prev from the live draft stashes it and recalls the newest entry.
	got, ok := h.Prev("draft in progress")
	if !ok || got != "second" {
		t.Fatalf("first Prev() = (%q, %v), want (second, true)", got, ok)
	}
	got, ok = h.Prev("second")
	if !ok || got != "first" {
		t.Fatalf("second Prev() = (%q, %v), want (first, true)", got, ok)
	}
	// No more history to walk back to.
	if _, ok := h.Prev("first"); ok {
		t.Error("Prev() at the oldest entry should return ok=false")
	}

	// Next walks forward, restoring the stashed draft once past the newest.
	got, ok = h.Next("first")
	if !ok || got != "second" {
		t.Fatalf("first Next() = (%q, %v), want (second, true)", got, ok)
	}
	got, ok = h.Next("second")
	if !ok || got != "draft in progress" {
		t.Fatalf("second Next() = (%q, %v), want the restored draft, got %q", got, ok, got)
	}
	if _, ok := h.Next("draft in progress"); ok {
		t.Error("Next() when not recalling should return ok=false")
	}
}

func TestInputHistory_PrevNext_EmptyHistoryIsNoop(t *testing.T) {
	h := newInputHistory()
	if _, ok := h.Prev("x"); ok {
		t.Error("Prev() on empty history should return ok=false")
	}
	if _, ok := h.Next("x"); ok {
		t.Error("Next() on empty history should return ok=false")
	}
}

func TestInputHistory_ResetCursor_AbandonsRecall(t *testing.T) {
	h := newInputHistory()
	h.Add("one")
	h.Add("two")
	h.Prev("draft")
	h.ResetCursor()

	if _, ok := h.Next("anything"); ok {
		t.Error("after ResetCursor, Next() should report not-recalling (ok=false)")
	}
}

func TestInputHistory_Seed_PreservesOrder(t *testing.T) {
	h := newInputHistory()
	h.Seed([]string{"a", "b", "c"})

	if got, want := h.entries, []string{"a", "b", "c"}; len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	got, ok := h.Prev("")
	if !ok || got != "c" {
		t.Errorf("Prev() after Seed = (%q, %v), want (c, true) — most recent seeded entry", got, ok)
	}
}

func TestSaveAndLoadInputHistory_RoundTrips(t *testing.T) {
	withTempMittoDir(t)

	want := []string{"hello", "world", "/help"}
	if err := SaveInputHistory("conv-1", want); err != nil {
		t.Fatalf("SaveInputHistory: %v", err)
	}

	got, err := LoadInputHistory("conv-1")
	if err != nil {
		t.Fatalf("LoadInputHistory: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("LoadInputHistory() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadInputHistory_MissingFileReturnsError(t *testing.T) {
	withTempMittoDir(t)

	if _, err := LoadInputHistory("no-such-conversation"); err == nil {
		t.Error("LoadInputHistory for a never-saved conversation should return an error (caller treats it as non-fatal)")
	}
}

func TestSaveInputHistory_KeyedByConversationID(t *testing.T) {
	withTempMittoDir(t)

	if err := SaveInputHistory("conv-a", []string{"a-only"}); err != nil {
		t.Fatalf("SaveInputHistory(conv-a): %v", err)
	}
	if err := SaveInputHistory("conv-b", []string{"b-only"}); err != nil {
		t.Fatalf("SaveInputHistory(conv-b): %v", err)
	}

	a, err := LoadInputHistory("conv-a")
	if err != nil || len(a) != 1 || a[0] != "a-only" {
		t.Errorf("LoadInputHistory(conv-a) = %v, %v, want [a-only], nil", a, err)
	}
	b, err := LoadInputHistory("conv-b")
	if err != nil || len(b) != 1 || b[0] != "b-only" {
		t.Errorf("LoadInputHistory(conv-b) = %v, %v, want [b-only], nil", b, err)
	}
}
