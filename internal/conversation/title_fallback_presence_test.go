package conversation

import (
	"testing"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// TestFallbackTitleMakesAutoChildNoLongerUntitled reproduces mitto-zzzr.
// A fallback remains eligible for an LLM upgrade, but it is still a real
// display title and must not make sessionHasNoTitle report true.
func TestFallbackTitleMakesAutoChildNoLongerUntitled(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sessionID = "auto-child-fallback-title"
	if err := store.Create(session.Metadata{
		SessionID:       sessionID,
		ACPServer:       "test-server",
		WorkingDir:      t.TempDir(),
		ParentSessionID: "parent-session",
		IsAutoChild:     true,
		ChildOrigin:     session.ChildOriginAuto,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	provider := &wedgeProvider{}
	mgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)
	callbackCount := 0
	cfg := TitleGenerationConfig{
		Store:            store,
		SessionID:        sessionID,
		Message:          "Preserve fallback child titles after auxiliary failure",
		WorkspaceUUID:    "test-workspace",
		AuxiliaryManager: mgr,
		OnTitleGenerated: func(_, _ string) { callbackCount++ },
	}
	GenerateAndSetTitle(cfg)

	deadline := time.Now().Add(time.Second)
	for provider.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if provider.calls.Load() == 0 {
		t.Fatal("auxiliary failure path was not exercised")
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.Name == "" || !meta.NameIsFallback {
		t.Fatalf("fallback was not persisted: name=%q fallback=%v", meta.Name, meta.NameIsFallback)
	}

	bs := &BackgroundSession{store: store, persistedID: sessionID}
	if bs.sessionHasNoTitle() {
		t.Fatal("mitto-zzzr: auto-child has a persisted fallback title but sessionHasNoTitle returned true")
	}

	GenerateAndSetTitle(cfg)
	if callbackCount != 1 {
		t.Fatalf("mitto-zzzr: fallback callback fired %d times, want once for the single metadata change", callbackCount)
	}

	// GenerateAndSetTitle is asynchronous. Do not let this test return while its
	// job can still read retry globals that a following test overrides.
	key := titleJobKey{store: store, sessionID: sessionID}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		titleJobs.Lock()
		_, active := titleJobs.active[key]
		titleJobs.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("title generation job did not finish")
}
