package bdexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBDVersion(t *testing.T) {
	tests := []struct {
		output string
		want   string
		ok     bool
	}{
		{"bd version 1.2.0", "1.2.0", true},
		{"bd version 1.2.1 (Homebrew)", "1.2.1", true},
		{"bd v1.2.2", "1.2.2", true},
		{"unexpected output", "", false},
	}
	for _, tt := range tests {
		got, ok := parseBDVersion(tt.output)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseBDVersion(%q) = (%q, %v), want (%q, %v)", tt.output, got, ok, tt.want, tt.ok)
		}
	}
}

func TestAcquireRejectsUnsafeBDVersions(t *testing.T) {
	for _, version := range []string{"1.2.0", "1.2.1"} {
		t.Run(version, func(t *testing.T) {
			binary := fakeBDVersion(t, version)
			release, err := Acquire(context.Background(), t.TempDir(), binary)
			if release != nil {
				release()
				t.Fatal("Acquire() returned a release function for an unsafe bd binary")
			}
			if err == nil || !strings.Contains(err.Error(), version) {
				t.Fatalf("Acquire() error = %v, want unsafe-version diagnostic containing %s", err, version)
			}
		})
	}
}

func TestAcquireAllowsSafeBDVersions(t *testing.T) {
	for _, version := range []string{"1.1.2", "1.2.2", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			release, err := Acquire(context.Background(), t.TempDir(), fakeBDVersion(t, version))
			if err != nil {
				t.Fatalf("Acquire() error = %v, want allowed version %s", err, version)
			}
			release()
		})
	}
}

func fakeBDVersion(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bd")
	script := fmt.Sprintf("#!/bin/sh\nprintf 'bd version %s (test)\\n'\n", version)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
