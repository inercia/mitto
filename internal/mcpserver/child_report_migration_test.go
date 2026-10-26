package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func equalStoredHistory(a, b childReportHistoryState) bool {
	// Pretty-printing may change insignificant whitespace inside RawMessage.
	// Compare the complete JSON representation, preserving values and order.
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func TestInactiveChildReportMigration(t *testing.T) {
	root := t.TempDir()
	s, err := session.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Create(session.Metadata{SessionID: "p"}); err != nil {
		t.Fatal(err)
	}
	legacy := json.RawMessage(`{"unknown":{"huge":9007199254740993},"reports":{"c":{"report":{"status":"custom","summary":"résumé","details":"","extra":9007199254740993},"completed":true,"timestamp":"2026-10-26T12:00:00Z"}}}`)
	if err := s.WriteSessionSidecarJSON("p", childReportsStateFile, legacy); err != nil {
		t.Fatal(err)
	}
	want, err := decodeChildReportHistory("p", legacy)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readOrMigrateChildReportHistory(s, "p")
	if err != nil || !equalStoredHistory(got, want) {
		t.Fatalf("migration: %+v %v", got, err)
	}
	var backup json.RawMessage
	if err := s.ReadSessionSidecarJSON("p", childReportsLegacySnapshotFile, &backup); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, backup); err != nil || !bytes.Equal(compact.Bytes(), legacy) {
		t.Fatalf("incomplete snapshot: %s %v", backup, err)
	}
	// Existing v1 reload preserves values/order/IDs and reaffirms durability.
	var before json.RawMessage
	if err := s.ReadSessionSidecarJSON("p", childReportsStateFile, &before); err != nil {
		t.Fatal(err)
	}
	again, err := readOrMigrateChildReportHistory(s, "p")
	if err != nil || !equalStoredHistory(again, got) {
		t.Fatalf("reload: %+v %v", again, err)
	}
	var after json.RawMessage
	if err := s.ReadSessionSidecarJSON("p", childReportsStateFile, &after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("reload rewrote v1")
	}
	// Retry with matching snapshot and same legacy source gives stable IDs.
	if err := s.WriteSessionSidecarJSON("p", childReportsStateFile, legacy); err != nil {
		t.Fatal(err)
	}
	retry, err := readOrMigrateChildReportHistory(s, "p")
	if err != nil || !equalStoredHistory(retry, got) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if err := writeChildReportHistory(s, "p", retry); err != nil {
		t.Fatal(err)
	}
	// Inspect sidecar modes via the fixture root, without reaching Store internals.
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == childReportsStateFile || d.Name() == childReportsLegacySnapshotFile {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm() != 0600 {
				t.Errorf("%s mode=%v", path, info.Mode())
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveChildReportMigrationRejectsWithoutMutation(t *testing.T) {
	valid := json.RawMessage(`{"reports":{}}`)
	v1 := json.RawMessage(`{"version":1,"entries":[],"diagnostics":{}}`)
	for _, tc := range []struct {
		name           string
		source, backup json.RawMessage
	}{
		{"corrupt source", json.RawMessage(`{"reports":`), nil},
		{"empty source", json.RawMessage(``), nil},
		{"future source", json.RawMessage(`{"version":99,"entries":[],"diagnostics":{}}`), nil},
		{"corrupt backup", valid, json.RawMessage(`{"broken":`)},
		{"conflicting backup", valid, json.RawMessage(`{"reports":{},"extra":1}`)},
		{"future backup", valid, json.RawMessage(`{"version":99}`)},
		{"v1 backup", valid, v1},
		{"v1 with corrupt backup", v1, json.RawMessage(`{`)},
		{"large number conflict", json.RawMessage(`{"reports":{},"unknown":9007199254740993}`), json.RawMessage(`{"reports":{},"unknown":9007199254740992}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s, err := session.NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			if err := s.Create(session.Metadata{SessionID: "p"}); err != nil {
				t.Fatal(err)
			}
			// Locate fixture directory through an ordinary valid sidecar write.
			if err := s.WriteSessionSidecarJSON("p", childReportsStateFile, valid); err != nil {
				t.Fatal(err)
			}
			var primary string
			if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err == nil && d.Name() == childReportsStateFile {
					primary = path
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(primary, tc.source, 0600); err != nil {
				t.Fatal(err)
			}
			backupPath := filepath.Join(filepath.Dir(primary), childReportsLegacySnapshotFile)
			if tc.backup != nil {
				if err := os.WriteFile(backupPath, tc.backup, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readOrMigrateChildReportHistory(s, "p"); err == nil {
				t.Fatal("accepted invalid state")
			}
			got, _ := os.ReadFile(primary)
			if !bytes.Equal(got, tc.source) {
				t.Fatal("source changed on rejection")
			}
			backup, err := os.ReadFile(backupPath)
			if tc.backup == nil {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected snapshot: %v", err)
				}
			} else if !bytes.Equal(backup, tc.backup) {
				t.Fatal("snapshot overwritten")
			}
		})
	}
}

func TestInactiveChildReportMissingAndDeletedParent(t *testing.T) {
	s, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Create(session.Metadata{SessionID: "p"}); err != nil {
		t.Fatal(err)
	}
	state, err := readOrMigrateChildReportHistory(s, "p")
	if err != nil || len(state.Entries) != 0 || state.Version != 1 {
		t.Fatalf("missing: %+v %v", state, err)
	}
	invalid := state
	invalid.Version = 99
	if err := writeChildReportHistory(s, "p", invalid); err == nil {
		t.Fatal("wrote invalid history")
	}
	if err := s.Delete("p"); err != nil {
		t.Fatal(err)
	}
	if _, err := readOrMigrateChildReportHistory(s, "p"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}
