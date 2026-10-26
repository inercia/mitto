package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/fileutil"
)

func TestDurableSidecarMigrationFailureAndRetry(t *testing.T) {
	for _, failAt := range []int{-2, -1, 1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			s, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			if err := s.Create(Metadata{SessionID: "p"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.sessionDir("p"), "state.json")
			original := []byte(`{"old":true,"unknown":9007199254740993}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			convert := func(raw, _ json.RawMessage) (json.RawMessage, error) {
				if bytes.Contains(raw, []byte(`"version"`)) {
					return raw, nil
				}
				return json.RawMessage(`{"version":1}`), nil
			}
			calls := []string{}
			injected := errors.New("post-write durability failure")
			write := func(path string, value any, mode os.FileMode) error {
				calls = append(calls, filepath.Base(path))
				if len(calls) == -failAt {
					return injected
				}
				if err := fileutil.WriteJSONAtomicDurableIfDirExists(path, value, mode); err != nil {
					return err
				}
				if len(calls) == failAt {
					return injected
				}
				return nil
			}
			if _, err := s.migrateSessionSidecarJSON("p", "state.json", "snapshot.json", convert, write); !errors.Is(err, injected) {
				t.Fatalf("failure=%v", err)
			}
			if failAt != 2 {
				got, _ := os.ReadFile(path)
				if !bytes.Equal(got, original) {
					t.Fatal("primary changed on pre-primary failure")
				}
			}
			calls = nil
			writeOK := func(path string, value any, mode os.FileMode) error {
				calls = append(calls, filepath.Base(path))
				return fileutil.WriteJSONAtomicDurableIfDirExists(path, value, mode)
			}
			if _, err := s.migrateSessionSidecarJSON("p", "state.json", "snapshot.json", convert, writeOK); err != nil {
				t.Fatal(err)
			}
			wantCalls := []string{"snapshot.json", "state.json"}
			if failAt == 2 {
				wantCalls = []string{"state.json"}
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("retry order=%v", calls)
			}
			backup, err := os.ReadFile(filepath.Join(s.sessionDir("p"), "snapshot.json"))
			if err != nil || !bytes.Contains(backup, []byte("9007199254740993")) {
				t.Fatalf("snapshot lost unknown number: %s %v", backup, err)
			}
		})
	}
}

func TestDurableSidecarMigrationLocks(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Create(Metadata{SessionID: "p"}); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(s.metadataPath("p"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MigrateSessionSidecarJSON("p", "state.json", "backup.json", func(raw, backup json.RawMessage) (json.RawMessage, error) {
		s.sessionLocksMu.Lock()
		entry := s.sessionLocks["p"]
		s.sessionLocksMu.Unlock()
		if entry == nil {
			t.Error("no session lock registered")
		} else if entry.mu.TryLock() {
			entry.mu.Unlock()
			t.Error("migration did not hold session write lock")
		}
		if s.mu.TryLock() {
			s.mu.Unlock()
			t.Error("migration did not hold lifecycle gate")
		}
		if raw != nil || backup != nil {
			t.Error("missing sidecars not nil")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSessionSidecarJSONDurable("p", "state.json", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(s.metadataPath("p"))
	if err != nil || !bytes.Equal(metadata, after) {
		t.Fatalf("metadata changed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSessionSidecarJSONDurable("p", "state.json", true); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed write: %v", err)
	}
	if _, err := s.MigrateSessionSidecarJSON("p", "state.json", "backup.json", func(a, b json.RawMessage) (json.RawMessage, error) { return nil, nil }); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed migration: %v", err)
	}
}

func TestDurableSidecarValidationAndDeletedParent(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Create(Metadata{SessionID: "p"}); err != nil {
		t.Fatal(err)
	}
	convert := func(json.RawMessage, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("converter called for missing parent or invalid name")
		return nil, nil
	}
	for _, name := range []string{"", ".", "..", "../other", "/other"} {
		if err := s.WriteSessionSidecarJSONDurable("p", name, true); err == nil {
			t.Fatalf("accepted name %q", name)
		}
		if _, err := s.MigrateSessionSidecarJSON("p", name, "backup.json", convert); err == nil {
			t.Fatalf("accepted migration name %q", name)
		}
		if _, err := s.MigrateSessionSidecarJSON("p", "state.json", name, convert); err == nil {
			t.Fatalf("accepted backup name %q", name)
		}
	}
	if err := s.Delete("p"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSessionSidecarJSONDurable("p", "state.json", true); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted write: %v", err)
	}
	if _, err := s.MigrateSessionSidecarJSON("p", "state.json", "backup.json", convert); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted migration: %v", err)
	}
	if _, err := os.Stat(s.sessionDir("p")); !os.IsNotExist(err) {
		t.Fatalf("resurrected parent: %v", err)
	}
}
