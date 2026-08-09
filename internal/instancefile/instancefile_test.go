package instancefile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
)

func TestWriteTo_ReadFrom_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")

	inst := &Instance{
		PID:         os.Getpid(),
		URL:         "http://127.0.0.1:8080",
		APIPrefix:   "/mitto",
		ExternalURL: "http://0.0.0.0:8443",
	}
	if err := WriteTo(path, inst); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}
	if inst.Token == "" {
		t.Fatal("WriteTo did not populate a token")
	}

	got, err := ReadFrom(path)
	if err != nil {
		t.Fatalf("ReadFrom failed: %v", err)
	}
	if got.URL != inst.URL || got.APIPrefix != inst.APIPrefix || got.ExternalURL != inst.ExternalURL {
		t.Errorf("round-trip mismatch: got %+v, want fields from %+v", got, inst)
	}
	if got.Token != inst.Token {
		t.Errorf("Token = %q, want %q", got.Token, inst.Token)
	}
	if got.Version != Version {
		t.Errorf("Version = %d, want %d", got.Version, Version)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt was not set")
	}
}

func TestWriteTo_PermissionBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := WriteTo(path, &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if got := info.Mode().Perm(); got != filePerm {
		t.Errorf("mode = %o, want %o", got, filePerm)
	}
}

func TestReadFrom_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	_, err := ReadFrom(path)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestReadFrom_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	_, err := ReadFrom(path)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v, want ErrCorrupt", err)
	}
}

func TestReadFrom_UnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	raw, _ := json.Marshal(Instance{Version: Version + 1, PID: os.Getpid(), URL: "http://x", Token: "tok"})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	_, err := ReadFrom(path)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v, want ErrCorrupt", err)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		inst Instance
	}{
		{"missing url", Instance{Version: 1, Token: "tok"}},
		{"missing token", Instance{Version: 1, URL: "http://x"}},
		{"zero version", Instance{URL: "http://x", Token: "tok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.inst.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestIsStale_DeadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	raw, _ := json.Marshal(Instance{
		Version:   Version,
		PID:       999999, // extremely unlikely to be a live PID
		URL:       "http://127.0.0.1:1",
		Token:     "tok",
		StartedAt: time.Now(),
	})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got, err := ReadFrom(path)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if got == nil || got.URL != "http://127.0.0.1:1" {
		t.Errorf("expected parsed instance alongside ErrStale, got %+v", got)
	}
}

func TestIsStale_LivePID(t *testing.T) {
	inst := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}
	if inst.IsStale() {
		t.Error("own PID should not be reported stale")
	}
}

func TestIsStale_ZeroOrNegativePID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		inst := &Instance{PID: pid, URL: "http://x", Token: "tok"}
		if !inst.IsStale() {
			t.Errorf("PID %d should be reported stale", pid)
		}
	}
}

func TestWriteTo_TokenReuseAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")

	first := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}
	if err := WriteTo(path, first); err != nil {
		t.Fatalf("first WriteTo failed: %v", err)
	}

	second := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:2"}
	if err := WriteTo(path, second); err != nil {
		t.Fatalf("second WriteTo failed: %v", err)
	}

	if second.Token != first.Token {
		t.Errorf("token not reused: first=%q second=%q", first.Token, second.Token)
	}
}

func TestWriteTo_TokenRegeneratedWhenCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	inst := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}
	if err := WriteTo(path, inst); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}
	if inst.Token == "" {
		t.Error("expected a fresh token to be generated over a corrupt prior file")
	}
}

func TestWriteTo_ExplicitTokenNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	inst := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1", Token: "explicit-token"}
	if err := WriteTo(path, inst); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}
	if inst.Token != "explicit-token" {
		t.Errorf("Token = %q, want explicit-token preserved", inst.Token)
	}
}

func TestGenerateToken(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if a == b {
		t.Error("two generated tokens were identical")
	}
	if len(a) < 32 {
		t.Errorf("token %q looks too short", a)
	}
}

func TestRemoveFrom_NotFoundIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if err := RemoveFrom(path); err != nil {
		t.Errorf("RemoveFrom on missing file returned error: %v", err)
	}
}

func TestRemoveFrom_OwnPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := WriteTo(path, &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	if err := RemoveFrom(path); err != nil {
		t.Fatalf("RemoveFrom failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("instance file still exists after RemoveFrom")
	}
}

func TestRemoveFrom_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := WriteTo(path, &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}
	if err := RemoveFrom(path); err != nil {
		t.Fatalf("first RemoveFrom failed: %v", err)
	}
	if err := RemoveFrom(path); err != nil {
		t.Fatalf("second RemoveFrom (idempotent) failed: %v", err)
	}
}

func TestRemoveFrom_RefusesLiveForeignPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	// Use the parent PID: guaranteed live and signalable (same user) for the
	// duration of this test, and guaranteed different from os.Getpid() —
	// simulating a different, still-running owner of the record.
	raw, _ := json.Marshal(Instance{
		Version:   Version,
		PID:       os.Getppid(),
		URL:       "http://127.0.0.1:1",
		Token:     "tok",
		StartedAt: time.Now(),
	})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if err := RemoveFrom(path); err != nil {
		t.Fatalf("RemoveFrom returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("instance file owned by a different live PID was removed, want left untouched")
	}
}

func TestRemoveFrom_CorruptFileIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := RemoveFrom(path); err != nil {
		t.Fatalf("RemoveFrom failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("corrupt instance file was not removed")
	}
}

func TestPath_UsesAppdir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() failed: %v", err)
	}
	want := filepath.Join(dir, appdir.InstanceFileName)
	if path != want {
		t.Errorf("Path() = %q, want %q", path, want)
	}
}

func TestWrite_Read_Remove_ViaAppdir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, dir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	inst := &Instance{PID: os.Getpid(), URL: "http://127.0.0.1:9"}
	if err := Write(inst); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	got, err := Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got.URL != inst.URL || got.Token != inst.Token {
		t.Errorf("Read() = %+v, want fields from %+v", got, inst)
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := Read(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read() after Remove() = %v, want ErrNotFound", err)
	}
}
