package rememberedargs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func newResponsesFixture(t *testing.T) (*session.Store, *ConversationResponses, string, string) {
	t.Helper()
	sessions, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	if err := sessions.Create(session.Metadata{SessionID: "conv-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	legacyDir := t.TempDir()
	return sessions, NewConversationResponses(sessions, legacyDir), "conv-1", legacyDir
}

func TestConversationResponses_UnsetWithoutRecord(t *testing.T) {
	_, c, sid, _ := newResponsesFixture(t)
	st, err := c.Get(sid, "ws-1", "Review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != ResponsesUnset || st.Arguments != nil || st.Legacy != nil {
		t.Fatalf("fresh state=%+v, want unset and empty", st)
	}
}

func TestConversationResponses_ReplaceEnabledThenReload(t *testing.T) {
	sessions, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "auth", "Empty": "", "Flag": "false", "Zero": "0"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "billing"}); err != nil {
		t.Fatalf("second ReplaceEnabled: %v", err)
	}
	reopened := NewConversationResponses(sessions, "")
	st, err := reopened.Get(sid, "ws-1", "Review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != ResponsesEnabled {
		t.Fatalf("state=%q, want enabled", st.State)
	}
	if len(st.Arguments) != 1 || st.Arguments["Topic"] != "billing" {
		t.Fatalf("arguments=%v, want replaced snapshot only", st.Arguments)
	}
}

func TestConversationResponses_EnabledEmptySnapshotIsValid(t *testing.T) {
	_, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	st, err := c.Get(sid, "ws-1", "Review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != ResponsesEnabled {
		t.Fatalf("empty enabled snapshot state=%q, want enabled", st.State)
	}
}

func TestConversationResponses_DisableTombstoneDropsValues(t *testing.T) {
	_, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "auth"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	if err := c.Disable(sid, "ws-1", "Review"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	st, err := c.Get(sid, "ws-1", "Review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != ResponsesDisabled || len(st.Arguments) != 0 {
		t.Fatalf("after disable state=%+v, want disabled without values", st)
	}
}

func TestConversationResponses_IdentityIsolation(t *testing.T) {
	_, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "auth"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	cases := []struct {
		name, ws, prompt string
		want             string
	}{
		{"case-insensitive prompt", "ws-1", "review", ResponsesEnabled},
		{"other prompt", "ws-1", "Summarize", ResponsesUnset},
		{"other workspace", "ws-2", "Review", ResponsesUnset},
	}
	for _, tc := range cases {
		st, err := c.Get(sid, tc.ws, tc.prompt)
		if err != nil {
			t.Fatalf("%s: Get: %v", tc.name, err)
		}
		if st.State != tc.want {
			t.Errorf("%s: state=%q, want %q", tc.name, st.State, tc.want)
		}
	}
}

func TestConversationResponses_RequiresIdentity(t *testing.T) {
	_, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "", "Review", map[string]string{}); err == nil {
		t.Fatal("accepted empty workspace UUID")
	}
	if err := c.Disable(sid, "ws-1", "  "); err == nil {
		t.Fatal("accepted blank prompt name")
	}
}

func TestConversationResponses_MissingSessionDoesNotCreateFile(t *testing.T) {
	sessions, c, _, _ := newResponsesFixture(t)
	err := c.ReplaceEnabled("ghost", "ws-1", "Review", map[string]string{"Topic": "x"})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing session error=%v, want ErrSessionNotFound", err)
	}
	if _, statErr := os.Stat(sessions.SessionDir("ghost")); !os.IsNotExist(statErr) {
		t.Fatalf("missing session directory was recreated: %v", statErr)
	}
}

func TestConversationResponses_CorruptFileFailsWithoutSilentSuccess(t *testing.T) {
	sessions, c, sid, _ := newResponsesFixture(t)
	path := filepath.Join(sessions.SessionDir(sid), ConversationResponsesFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, err := c.Get(sid, "ws-1", "Review"); err == nil {
		t.Fatal("Get silently accepted a corrupt file")
	}
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "x"}); err == nil {
		t.Fatal("ReplaceEnabled overwrote a corrupt file without error")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Fatalf("corrupt file was modified: %q err=%v", raw, err)
	}
}

func TestConversationResponses_LegacyMigrationIsUnsetAndRemovesSource(t *testing.T) {
	sessions, c, sid, legacyDir := newResponsesFixture(t)
	legacyPath := filepath.Join(legacyDir, sid+".json")
	if err := os.WriteFile(legacyPath, []byte(`{"Review":{"Topic":"legacy"}}`), 0600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	st, err := c.Get(sid, "ws-1", "review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.State != ResponsesUnset {
		t.Fatalf("legacy migration granted state %q, want unset (no consent)", st.State)
	}
	if st.Legacy["Topic"] != "legacy" {
		t.Fatalf("legacy values=%v, want Topic=legacy", st.Legacy)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy source not removed after durable migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessions.SessionDir(sid), ConversationResponsesFileName)); err != nil {
		t.Fatalf("session-local file not written: %v", err)
	}
}

func TestConversationResponses_LegacyNeverMergedWhenSessionFileExists(t *testing.T) {
	_, c, sid, legacyDir := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Other", map[string]string{"A": "1"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, sid+".json")
	if err := os.WriteFile(legacyPath, []byte(`{"Review":{"Topic":"legacy"}}`), 0600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	st, err := c.Get(sid, "ws-1", "Review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.Legacy != nil || st.State != ResponsesUnset {
		t.Fatalf("legacy merged into existing session file: %+v", st)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy file touched although session file exists: %v", err)
	}
}

func TestConversationResponses_LegacyFailureKeepsSource(t *testing.T) {
	_, c, sid, legacyDir := newResponsesFixture(t)
	legacyPath := filepath.Join(legacyDir, sid+".json")
	if err := os.WriteFile(legacyPath, []byte(`not json`), 0600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if _, err := c.Get(sid, "ws-1", "Review"); err != nil {
		t.Fatalf("Get with unreadable legacy should fail open: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("unreadable legacy source was removed: %v", err)
	}
}

func TestConversationResponses_DeletedSessionRemovesFile(t *testing.T) {
	sessions, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "x"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	if err := sessions.Delete(sid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(sessions.SessionDir(sid)); !os.IsNotExist(err) {
		t.Fatalf("session directory survived delete: %v", err)
	}
	if _, err := c.Get(sid, "ws-1", "Review"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("Get after delete err=%v, want ErrSessionNotFound", err)
	}
	if err := c.Disable(sid, "ws-1", "Review"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("late write after delete err=%v, want ErrSessionNotFound", err)
	}
	if _, err := os.Stat(sessions.SessionDir(sid)); !os.IsNotExist(err) {
		t.Fatalf("late write recreated deleted session directory: %v", err)
	}
}

func TestConversationResponses_FileIsVersionedJSON(t *testing.T) {
	sessions, c, sid, _ := newResponsesFixture(t)
	if err := c.ReplaceEnabled(sid, "ws-1", "Review", map[string]string{"Topic": "x"}); err != nil {
		t.Fatalf("ReplaceEnabled: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(sessions.SessionDir(sid), ConversationResponsesFileName))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var doc struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version != conversationResponsesVersion {
		t.Fatalf("file=%s version=%d err=%v", raw, doc.Version, err)
	}
}
