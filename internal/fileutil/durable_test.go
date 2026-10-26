package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingDurableFile struct {
	*os.File
	stage, fail string
	err         error
}

func (f failingDurableFile) Write(p []byte) (int, error) {
	if f.fail == "write" {
		return 0, f.err
	}
	if f.fail == "short" {
		return 0, nil
	}
	return f.File.Write(p)
}
func (f failingDurableFile) Sync() error {
	if f.fail == f.stage+"sync" {
		return f.err
	}
	return f.File.Sync()
}
func (f failingDurableFile) Close() error {
	err := f.File.Close()
	if f.fail == f.stage+"close" {
		return f.err
	}
	return err
}

func TestDurableAtomicFailures(t *testing.T) {
	for _, fail := range []string{"create", "write", "short", "filesync", "fileclose", "rename", "diropen", "dirsync", "dirclose", ""} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "value.json")
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + fail)
			ops := durableFileOps{
				create: func(name string, perm os.FileMode) (durableFile, error) {
					if fail == "create" {
						return nil, injected
					}
					f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
					return failingDurableFile{f, "file", fail, injected}, err
				},
				openDir: func(name string) (durableFile, error) {
					if fail == "diropen" {
						return nil, injected
					}
					f, err := os.Open(name)
					return failingDurableFile{f, "dir", fail, injected}, err
				},
				rename: func(a, b string) error {
					if fail == "rename" {
						return injected
					}
					return os.Rename(a, b)
				}, remove: os.Remove,
			}
			err := writeAtomicDurable(path, []byte("new"), 0600, ops)
			if (err == nil) != (fail == "") {
				t.Fatalf("error=%v for %q", err, fail)
			}
			if fail != "" && fail != "short" && !errors.Is(err, injected) {
				t.Fatalf("lost injected error: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "old"
			if fail == "" || strings.HasPrefix(fail, "dir") {
				want = "new"
			}
			if string(got) != want {
				t.Fatalf("visible=%s want=%s", got, want)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatalf("temporary files leaked: %v %v", files, err)
			}
		})
	}
}

func TestDurableAtomicModeAndMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "value.json")
	if err := WriteJSONAtomicDurableIfDirExists(path, map[string]int{"n": 1}, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	missing := filepath.Join(dir, "missing", "value.json")
	if err := WriteJSONAtomicDurableIfDirExists(missing, true, 0600); err == nil {
		t.Fatal("accepted missing directory")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("recreated missing directory: %v", err)
	}
	if err := WriteJSONAtomicDurableIfDirExists(path, make(chan int), 0600); err == nil {
		t.Fatal("accepted unmarshalable value")
	}
}
