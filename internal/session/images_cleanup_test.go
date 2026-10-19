package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOldImagesProtectsReferencesAndRemovesOrphans(t *testing.T) {
	tests := []struct {
		name       string
		history    string
		queue      string
		writeQueue bool
		wantKeep   bool
	}{
		{name: "event reference", history: `{"seq":1,"type":"user_prompt","data":{"message":"x","images":[{"id":"img_event.png"}]}}`, wantKeep: true},
		{name: "duplicate sequence reference", history: "{\"seq\":1,\"type\":\"user_prompt\",\"data\":{\"message\":\"x\"}}\n{\"seq\":1,\"type\":\"user_prompt\",\"data\":{\"message\":\"x\",\"images\":[{\"id\":\"img_event.png\"}]}}", wantKeep: true},
		{name: "queue reference", history: `{"seq":1,"type":"session_start","data":{}}`, queue: `{"messages":[{"id":"q1","image_ids":["img_event.png"]}]}`, writeQueue: true, wantKeep: true},
		{name: "empty valid history allows orphan cleanup", history: "", wantKeep: false},
		{name: "corrupt history fails closed", history: "not-json", wantKeep: true},
		{name: "null history event fails closed", history: "null", wantKeep: true},
		{name: "user prompt with malformed data fails closed", history: `{"seq":1,"type":"user_prompt","data":null}`, wantKeep: true},
		{name: "corrupt queue fails closed", history: `{"seq":1,"type":"session_start","data":{}}`, queue: "not-json", writeQueue: true, wantKeep: true},
		{name: "null queue fails closed", history: "", queue: "null", writeQueue: true, wantKeep: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, sessionID, imagePath := newCleanupFixture(t, tt.history)
			defer store.Close()
			if tt.writeQueue {
				if err := os.WriteFile(filepath.Join(store.sessionDir(sessionID), queueFileName), []byte(tt.queue), 0600); err != nil {
					t.Fatal(err)
				}
			}
			removed, err := store.CleanupOldImages(time.Hour, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantKeep {
				if removed != 0 {
					t.Fatalf("removed = %d, want 0", removed)
				}
				if _, err := os.Stat(imagePath); err != nil {
					t.Fatalf("referenced or fail-closed image missing: %v", err)
				}
			} else {
				if removed != 1 {
					t.Fatalf("removed = %d, want 1", removed)
				}
				if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
					t.Fatalf("orphan image still exists (stat err %v)", err)
				}
			}
		})
	}
}

func TestCleanupOldImagesMissingHistoryPreservesAllFiles(t *testing.T) {
	store, sessionID, imagePath := newCleanupFixture(t, "")
	defer store.Close()
	if err := os.Remove(filepath.Join(store.sessionDir(sessionID), eventsFileName)); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CleanupOldImages(time.Hour, 0); err != nil || removed != 0 {
		t.Fatalf("CleanupOldImages() = (%d, %v), want (0, nil)", removed, err)
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("image deleted despite missing history: %v", err)
	}
}

func TestCleanupOldImagesPreservesRecentSessionAndUnsupportedEntries(t *testing.T) {
	store, sessionID, imagePath := newCleanupFixture(t, "")
	defer store.Close()
	meta, err := store.readMetadata(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	meta.UpdatedAt = time.Now()
	if err := store.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}
	unsupported := filepath.Join(store.imagesDir(sessionID), "notes.txt")
	if err := os.WriteFile(unsupported, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CleanupOldImages(time.Hour, time.Hour); err != nil || removed != 0 {
		t.Fatalf("CleanupOldImages() = (%d, %v), want (0, nil)", removed, err)
	}
	for _, path := range []string{imagePath, unsupported} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected preserved path %s: %v", path, err)
		}
	}
}

func TestCleanupOldImagesPreservesRecentFileInOldSession(t *testing.T) {
	store, _, imagePath := newCleanupFixture(t, "")
	defer store.Close()
	now := time.Now()
	if err := os.Chtimes(imagePath, now, now); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CleanupOldImages(time.Hour, time.Hour); err != nil || removed != 0 {
		t.Fatalf("CleanupOldImages() = (%d, %v), want (0, nil)", removed, err)
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("recent file in old session was removed: %v", err)
	}
}

func TestCleanupOldImagesPreservesUnsupportedEntriesInOldSession(t *testing.T) {
	store, sessionID, _ := newCleanupFixture(t, "")
	defer store.Close()
	unsupported := filepath.Join(store.imagesDir(sessionID), "notes.txt")
	directory := filepath.Join(store.imagesDir(sessionID), "nested.png")
	if err := os.WriteFile(unsupported, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, path := range []string{unsupported, directory} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := store.CleanupOldImages(time.Hour, 0); err != nil || removed != 1 {
		t.Fatalf("CleanupOldImages() = (%d, %v), want (1, nil)", removed, err)
	}
	for _, path := range []string{unsupported, directory} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected preserved path %s: %v", path, err)
		}
	}
}

func TestCleanupOldImagesPreservesSavedImageAcrossReopen(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	meta := Metadata{SessionID: "persisted", ACPServer: "test", WorkingDir: t.TempDir()}
	if err := store.Create(meta); err != nil {
		t.Fatal(err)
	}
	data := []byte("original image bytes")
	image, err := store.SaveImage(meta.SessionID, data, "image/png", "original.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(meta.SessionID, Event{Seq: 1, Type: EventTypeUserPrompt, Data: UserPromptData{Message: "prompt", Images: []ImageRef{{ID: image.ID, MimeType: "image/png"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meta, err = store.readMetadata(meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	meta.UpdatedAt = time.Now().Add(-48 * time.Hour)
	if err := store.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(store.imagesDir(meta.SessionID), image.ID)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(imagePath, old, old); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.CleanupOldImages(time.Hour, 0); err != nil || removed != 0 {
		t.Fatalf("CleanupOldImages() = (%d, %v), want (0, nil)", removed, err)
	}
	resolved, err := store.GetImagePath(meta.SessionID, image.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(resolved)
	if err != nil || string(got) != string(data) {
		t.Fatalf("reopened image bytes = %q, %v; want %q", got, err, data)
	}
}

func TestCleanupOldImagesClosedStore(t *testing.T) {
	store, _, _ := newCleanupFixture(t, "")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CleanupOldImages(time.Hour, 0); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("CleanupOldImages() error = %v, want %v", err, ErrStoreClosed)
	}
}

func newCleanupFixture(t *testing.T, history string) (*Store, string, string) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta := Metadata{SessionID: "cleanup-session", ACPServer: "test", WorkingDir: t.TempDir()}
	if err := store.Create(meta); err != nil {
		t.Fatal(err)
	}
	meta, err = store.readMetadata(meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	meta.UpdatedAt = time.Now().Add(-48 * time.Hour)
	if err := store.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(store.sessionDir(meta.SessionID), eventsFileName)
	if err := os.WriteFile(eventsPath, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(store.imagesDir(meta.SessionID), "img_event.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("old image"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(imagePath, old, old); err != nil {
		t.Fatal(err)
	}
	return store, meta.SessionID, imagePath
}
