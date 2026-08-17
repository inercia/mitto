//go:build linux || darwin

package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileBackendSaveLoadAndAtomicReplace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "credentials", "vault.json")
	backend := NewFileBackend(path)
	for _, data := range []string{`{"version":1,"namespaces":{}}`, strings.Repeat("x", 32*1024)} {
		if err := backend.Save([]byte(data)); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		got, err := backend.Load()
		if err != nil || string(got) != data {
			t.Fatalf("Load() = (%d bytes, %v), want %d bytes", len(got), err, len(data))
		}
	}
	assertMode(t, filepath.Dir(path), 0700)
	assertMode(t, path, 0600)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "vault.json" {
		t.Fatalf("credential directory entries = %v, want only vault.json", entries)
	}
}

func TestFileBackendMissingAndCorruptVault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "vault.json")
	backend := NewFileBackend(path)
	if _, err := backend.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(missing) error = %v, want ErrNotFound", err)
	}
	if err := backend.Save([]byte("not-json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(backend).Resolve(GlobalCredential("token")); !errors.Is(err, ErrCorruptVault) {
		t.Fatalf("Resolve(corrupt) error = %v, want ErrCorruptVault", err)
	}
}

func TestFileBackendRejectsUnsafeDirectory(t *testing.T) {
	t.Run("permissions", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "credentials")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := NewFileBackend(filepath.Join(dir, "vault.json")).Save([]byte("x")); !errors.Is(err, ErrUnsafeVaultPath) {
			t.Fatalf("Save() error = %v, want ErrUnsafeVaultPath", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		realDir := filepath.Join(root, "real")
		if err := os.Mkdir(realDir, 0700); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "credentials")
		if err := os.Symlink(realDir, dir); err != nil {
			t.Fatal(err)
		}
		if err := NewFileBackend(filepath.Join(dir, "vault.json")).Save([]byte("x")); !errors.Is(err, ErrUnsafeVaultPath) {
			t.Fatalf("Save() error = %v, want ErrUnsafeVaultPath", err)
		}
	})
}

func TestFileBackendRejectsUnsafeFile(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"permissions", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(filepath.Dir(path)), "target")
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"non-regular", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "credentials")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "vault.json")
			test.setup(t, path)
			backend := NewFileBackend(path)
			if _, err := backend.Load(); !errors.Is(err, ErrUnsafeVaultPath) {
				t.Errorf("Load() error = %v, want ErrUnsafeVaultPath", err)
			}
			if err := backend.Save([]byte("new")); !errors.Is(err, ErrUnsafeVaultPath) {
				t.Errorf("Save() error = %v, want ErrUnsafeVaultPath", err)
			}
		})
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
