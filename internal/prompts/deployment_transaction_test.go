package prompts

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
)

func TestPromptsCache_FragmentRetryPreservesBuiltinShortNames(t *testing.T) {
	previous := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(previous) })

	root := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, root)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	builtinDir := filepath.Join(root, appdir.PromptsDirName, "builtin")
	sharedDir := filepath.Join(builtinDir, "_shared")
	if err := os.MkdirAll(sharedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "base.tmpl"), []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	consumer := "name: Consumer\nprompt: |\n  {{ template \"_shared/base\" . }}\n"
	if err := os.WriteFile(filepath.Join(builtinDir, "consumer.prompt.yaml"), []byte(consumer), 0644); err != nil {
		t.Fatal(err)
	}

	SetCurrentFragments(NewFragmentRegistry())
	cache := NewPromptsCache()
	promptFiles, err := cache.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(promptFiles) != 1 || promptFiles[0].Name != "Consumer" {
		t.Fatalf("prompts = %#v, want Consumer", promptFiles)
	}
	if _, ok := CurrentFragments().Get("_shared/base"); !ok {
		t.Fatal("builtin fragment retry lost short name _shared/base")
	}
	if errs := cache.LoadErrors(); len(errs) != 0 {
		t.Fatalf("LoadErrors = %v, want none", errs)
	}
}

type transactionReloadSubscriber struct {
	mu           sync.Mutex
	cache        *PromptsCache
	fragmentDirs []string
	notified     chan PromptsChangeEvent
	err          error
}

func (s *transactionReloadSubscriber) OnPromptsChanged(event PromptsChangeEvent) {
	if event.HasFragmentChanges {
		registry, _, err := ReloadFragmentsFromDirs(s.fragmentDirs)
		if err == nil {
			SetCurrentFragments(registry)
		} else {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
		}
	}
	if _, err := s.cache.ForceReload(); err != nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
	s.notified <- event
}

func (s *transactionReloadSubscriber) wait(t *testing.T) PromptsChangeEvent {
	t.Helper()
	select {
	case event := <-s.notified:
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("subscriber reload: %v", err)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompts deployment event")
		return PromptsChangeEvent{}
	}
}

func TestBulkDeploymentWithWatcherPublishesOnlyCompleteSnapshot(t *testing.T) {
	previous := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(previous) })

	root := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, root)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	promptsDir := filepath.Join(root, appdir.PromptsDirName)
	builtinDir := filepath.Join(promptsDir, "builtin")
	if err := os.MkdirAll(filepath.Join(builtinDir, "_shared"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtinDir, "old.prompt.yaml"), []byte("name: Old\nprompt: old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	registry, _, err := ReloadFragmentsFromDirs([]string{builtinDir, promptsDir})
	if err != nil {
		t.Fatal(err)
	}
	SetCurrentFragments(registry)
	cache := NewPromptsCache()
	if _, err := cache.Get(); err != nil {
		t.Fatal(err)
	}

	watcher, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	watcher.SetDebounceDelay(20 * time.Millisecond)
	subscriber := &transactionReloadSubscriber{cache: cache, fragmentDirs: []string{builtinDir, promptsDir}, notified: make(chan PromptsChangeEvent, 8)}
	if err := watcher.Subscribe(subscriber, []string{promptsDir}); err != nil {
		t.Fatal(err)
	}
	watcher.Start()

	finish, err := BeginDeployment(promptsDir)
	if err != nil {
		t.Fatal(err)
	}
	partial := "name: New\nprompt: |\n  {{ template \"_shared/new\" . }}\n"
	if err := os.WriteFile(filepath.Join(builtinDir, "new.prompt.yaml"), []byte(partial), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ForceReload(); err != nil {
		t.Fatalf("ForceReload during deployment: %v", err)
	}
	if got := cache.NamesSnapshot().Names; len(got) != 1 || got[0] != "Old" {
		t.Fatalf("cache published partial deployment: names=%v", got)
	}

	if err := os.WriteFile(filepath.Join(builtinDir, "_shared", "new.tmpl"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtinDir, "second.prompt.yaml"), []byte("name: Second\nprompt: second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	event := subscriber.wait(t)
	if !event.HasPromptChanges || !event.HasFragmentChanges {
		t.Fatalf("completion event flags = %+v, want full reload", event)
	}

	names := cache.NamesSnapshot().Names
	if !containsPromptName(names, "Old") || !containsPromptName(names, "New") || !containsPromptName(names, "Second") {
		t.Fatalf("final cache names = %v, want Old/New/Second", names)
	}
	if errs := cache.LoadErrors(); len(errs) != 0 {
		t.Fatalf("final LoadErrors = %v, want none", errs)
	}
}

func containsPromptName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
