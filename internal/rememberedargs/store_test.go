package rememberedargs

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_EmptyIdentifiers_NoOp(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Set("", "prompt", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set with empty uuid: %v", err)
	}
	if err := s.Set("ws", "", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set with empty prompt: %v", err)
	}
	if err := s.Set("ws", "p", nil); err != nil {
		t.Fatalf("Set with nil args: %v", err)
	}

	got, err := s.Get("", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get empty uuid: got=%v err=%v", got, err)
	}
	got, err = s.Get("ws", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get empty prompt: got=%v err=%v", got, err)
	}
}

func TestStore_InertWhenBaseDirEmpty(t *testing.T) {
	s := NewStore("")
	if err := s.Set("ws", "p", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("Set inert: %v", err)
	}
	got, err := s.Get("ws", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("Get inert: got=%v err=%v", got, err)
	}
}

func TestStore_RoundTrip_And_MergesExisting(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Set("ws1", "commit", map[string]string{"Message": "first", "Amend": "true"}); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	got, err := s.Get("ws1", "commit")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got["Message"] != "first" || got["Amend"] != "true" {
		t.Fatalf("round-trip mismatch: %v", got)
	}

	// Merge: existing "Amend" preserved; "Message" overwritten.
	if err := s.Set("ws1", "commit", map[string]string{"Message": "second"}); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	got, _ = s.Get("ws1", "commit")
	if got["Message"] != "second" {
		t.Fatalf("Message not overwritten: %v", got)
	}
	if got["Amend"] != "true" {
		t.Fatalf("Amend not preserved on merge: %v", got)
	}

	// Different prompt name is isolated.
	if err := s.Set("ws1", "review", map[string]string{"Kind": "quick"}); err != nil {
		t.Fatalf("Set review: %v", err)
	}
	got, _ = s.Get("ws1", "commit")
	if _, has := got["Kind"]; has {
		t.Fatalf("prompt isolation broken: %v", got)
	}

	// Returned map is a copy — mutating it must not affect subsequent Gets.
	got["Message"] = "MUTATED"
	got2, _ := s.Get("ws1", "commit")
	if got2["Message"] == "MUTATED" {
		t.Fatalf("Get did not return a copy")
	}
}

func TestStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	if err := s1.Set("ws2", "run", map[string]string{"Target": "prod"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Ensure the file materialised where we expect.
	if _, err := readAllFile(t, filepath.Join(dir, "ws2.json")); err != nil {
		t.Fatalf("expected snapshot file: %v", err)
	}

	s2 := NewStore(dir)
	got, err := s2.Get("ws2", "run")
	if err != nil {
		t.Fatalf("Get on fresh Store: %v", err)
	}
	if got["Target"] != "prod" {
		t.Fatalf("persistence broken: %v", got)
	}
}

func TestStore_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	got, err := s.Get("nope", "p")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestStore_ConcurrentSetGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = s.Set("wsC", "p", map[string]string{"k": "v"})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = s.Get("wsC", "p")
		}()
	}
	wg.Wait()

	got, _ := s.Get("wsC", "p")
	if got["k"] != "v" {
		t.Fatalf("final state wrong: %v", got)
	}
}

func readAllFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return readFileHelper(path)
}

// TestStore_ConversationInertByDefault verifies that GetConversation /
// SetConversation are no-ops until WithConversationBaseDir enables the
// per-session namespace (mitto-47y.6.2).
func TestStore_ConversationInertByDefault(t *testing.T) {
	s := NewStore(t.TempDir()) // folder namespace enabled; conversation not.
	if err := s.SetConversation("sess1", "p", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("SetConversation inert: %v", err)
	}
	got, err := s.GetConversation("sess1", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("GetConversation inert: got=%v err=%v", got, err)
	}
}

// TestStore_ConversationEmptyIdentifiers_NoOp mirrors
// TestStore_EmptyIdentifiers_NoOp for the conversation namespace: empty
// sessionID / promptName / nil args are all no-ops (mitto-47y.6.2).
func TestStore_ConversationEmptyIdentifiers_NoOp(t *testing.T) {
	s := NewStore(t.TempDir()).WithConversationBaseDir(t.TempDir())

	if err := s.SetConversation("", "prompt", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("SetConversation with empty sessionID: %v", err)
	}
	if err := s.SetConversation("sess", "", map[string]string{"a": "1"}); err != nil {
		t.Fatalf("SetConversation with empty prompt: %v", err)
	}
	if err := s.SetConversation("sess", "p", nil); err != nil {
		t.Fatalf("SetConversation with nil args: %v", err)
	}

	got, err := s.GetConversation("", "p")
	if err != nil || len(got) != 0 {
		t.Fatalf("GetConversation empty sessionID: got=%v err=%v", got, err)
	}
	got, err = s.GetConversation("sess", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("GetConversation empty prompt: got=%v err=%v", got, err)
	}
}

// TestStore_ConversationRoundTrip_And_MergesExisting mirrors the folder
// round-trip test for the per-session namespace: Set merges into an existing
// snapshot, Get returns a mutable copy, and different prompt names are
// isolated (mitto-47y.6.2).
func TestStore_ConversationRoundTrip_And_MergesExisting(t *testing.T) {
	convDir := t.TempDir()
	s := NewStore(t.TempDir()).WithConversationBaseDir(convDir)

	if err := s.SetConversation("sess1", "commit", map[string]string{"Message": "first", "Amend": "true"}); err != nil {
		t.Fatalf("SetConversation first: %v", err)
	}
	got, err := s.GetConversation("sess1", "commit")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got["Message"] != "first" || got["Amend"] != "true" {
		t.Fatalf("round-trip mismatch: %v", got)
	}

	// Merge: existing "Amend" preserved; "Message" overwritten.
	if err := s.SetConversation("sess1", "commit", map[string]string{"Message": "second"}); err != nil {
		t.Fatalf("SetConversation second: %v", err)
	}
	got, _ = s.GetConversation("sess1", "commit")
	if got["Message"] != "second" {
		t.Fatalf("Message not overwritten: %v", got)
	}
	if got["Amend"] != "true" {
		t.Fatalf("Amend not preserved on merge: %v", got)
	}

	// Different prompt name is isolated within the same session.
	if err := s.SetConversation("sess1", "review", map[string]string{"Kind": "quick"}); err != nil {
		t.Fatalf("SetConversation review: %v", err)
	}
	got, _ = s.GetConversation("sess1", "commit")
	if _, has := got["Kind"]; has {
		t.Fatalf("prompt isolation broken: %v", got)
	}

	// Returned map is a copy — mutating it must not affect subsequent Gets.
	got["Message"] = "MUTATED"
	got2, _ := s.GetConversation("sess1", "commit")
	if got2["Message"] == "MUTATED" {
		t.Fatalf("GetConversation did not return a copy")
	}
}

// TestStore_ConversationPersistsAcrossInstances verifies that a fresh Store
// wired to the same conversationBaseDir loads previously-written per-session
// snapshots (mitto-47y.6.2).
func TestStore_ConversationPersistsAcrossInstances(t *testing.T) {
	convDir := t.TempDir()
	s1 := NewStore(t.TempDir()).WithConversationBaseDir(convDir)
	if err := s1.SetConversation("sess2", "run", map[string]string{"Target": "prod"}); err != nil {
		t.Fatalf("SetConversation: %v", err)
	}
	// Ensure the file materialised where we expect.
	if _, err := readAllFile(t, filepath.Join(convDir, "sess2.json")); err != nil {
		t.Fatalf("expected snapshot file: %v", err)
	}

	s2 := NewStore(t.TempDir()).WithConversationBaseDir(convDir)
	got, err := s2.GetConversation("sess2", "run")
	if err != nil {
		t.Fatalf("GetConversation on fresh Store: %v", err)
	}
	if got["Target"] != "prod" {
		t.Fatalf("persistence broken: %v", got)
	}
}

// TestStore_ConversationSessionIsolation verifies that two different sessions
// each keep their own snapshot: writes to session A do not appear in
// GetConversation for session B (mitto-47y.6.2).
func TestStore_ConversationSessionIsolation(t *testing.T) {
	s := NewStore(t.TempDir()).WithConversationBaseDir(t.TempDir())
	if err := s.SetConversation("sessA", "p", map[string]string{"k": "A"}); err != nil {
		t.Fatalf("SetConversation A: %v", err)
	}
	if err := s.SetConversation("sessB", "p", map[string]string{"k": "B"}); err != nil {
		t.Fatalf("SetConversation B: %v", err)
	}
	gotA, _ := s.GetConversation("sessA", "p")
	gotB, _ := s.GetConversation("sessB", "p")
	if gotA["k"] != "A" || gotB["k"] != "B" {
		t.Fatalf("session isolation broken: A=%v B=%v", gotA, gotB)
	}
}

// TestStore_ConversationAndFolder_AreIndependent verifies the two namespaces
// coexist without cross-contamination: identical (id, prompt, arg) tuples in
// different scopes do not overwrite each other (mitto-47y.6.2).
func TestStore_ConversationAndFolder_AreIndependent(t *testing.T) {
	s := NewStore(t.TempDir()).WithConversationBaseDir(t.TempDir())
	// Same prompt name + same arg name in both scopes (with the same textual
	// identifier used for workspace UUID and session ID) MUST keep two
	// separate slots.
	const id = "shared-id"
	if err := s.Set(id, "commit", map[string]string{"Msg": "folder-val"}); err != nil {
		t.Fatalf("Set folder: %v", err)
	}
	if err := s.SetConversation(id, "commit", map[string]string{"Msg": "conv-val"}); err != nil {
		t.Fatalf("SetConversation: %v", err)
	}

	folder, _ := s.Get(id, "commit")
	conv, _ := s.GetConversation(id, "commit")
	if folder["Msg"] != "folder-val" {
		t.Errorf("folder Msg = %q; want %q", folder["Msg"], "folder-val")
	}
	if conv["Msg"] != "conv-val" {
		t.Errorf("conversation Msg = %q; want %q", conv["Msg"], "conv-val")
	}
}
