package session

import (
	"errors"
	"testing"
)

func TestStoreSessionSidecarJSON(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(Metadata{SessionID: "sidecar-session"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := map[string]string{"status": "completed"}
	if err := store.WriteSessionSidecarJSON("sidecar-session", "child-reports.json", want); err != nil {
		t.Fatalf("WriteSessionSidecarJSON: %v", err)
	}
	var got map[string]string
	if err := store.ReadSessionSidecarJSON("sidecar-session", "child-reports.json", &got); err != nil {
		t.Fatalf("ReadSessionSidecarJSON: %v", err)
	}
	if got["status"] != want["status"] {
		t.Fatalf("status=%q, want %q", got["status"], want["status"])
	}

	for _, name := range []string{"", ".", "..", "../metadata.json", "/tmp/report.json"} {
		if err := store.WriteSessionSidecarJSON("sidecar-session", name, want); err == nil {
			t.Errorf("WriteSessionSidecarJSON accepted unsafe name %q", name)
		}
	}
	if err := store.WriteSessionSidecarJSON("missing", "report.json", want); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session error=%v, want ErrSessionNotFound", err)
	}
}
