package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestVariableResolver_Resolve(t *testing.T) {
	home, _ := os.UserHomeDir()
	workspace := "/path/to/workspace"

	resolver, err := NewVariableResolver(workspace)
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "workspace variable",
			input:    "$WORKSPACE/src",
			expected: "/path/to/workspace/src",
		},
		{
			name:     "workspace variable with braces",
			input:    "${WORKSPACE}/src",
			expected: "/path/to/workspace/src",
		},
		{
			name:     "home variable",
			input:    "$HOME/.config",
			expected: home + "/.config",
		},
		{
			name:     "home variable with braces",
			input:    "${HOME}/.config",
			expected: home + "/.config",
		},
		{
			name:     "tilde expansion",
			input:    "~/.config",
			expected: filepath.Join(home, ".config"),
		},
		{
			name:     "multiple variables",
			input:    "$WORKSPACE/build/$USER",
			expected: "/path/to/workspace/build/" + resolver.user,
		},
		{
			name:     "no variables",
			input:    "/absolute/path",
			expected: "/absolute/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestVariableResolver_ResolvePaths(t *testing.T) {
	workspace := "/workspace"
	resolver, err := NewVariableResolver(workspace)
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	input := []string{
		"$WORKSPACE/src",
		"$HOME/.config",
		"/absolute/path",
	}

	resolved := resolver.ResolvePaths(input)

	if len(resolved) != 3 {
		t.Errorf("expected 3 paths, got %d", len(resolved))
	}

	if resolved[0] != "/workspace/src" {
		t.Errorf("expected /workspace/src, got %s", resolved[0])
	}

	// Check that absolute path is unchanged
	if resolved[2] != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %s", resolved[2])
	}
}

func TestVariableResolver_ResolvePathsEmpty(t *testing.T) {
	resolver, err := NewVariableResolver("/workspace")
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	resolved := resolver.ResolvePaths(nil)
	if resolved != nil {
		t.Errorf("expected nil for empty input, got %v", resolved)
	}

	resolved = resolver.ResolvePaths([]string{})
	if resolved != nil {
		t.Errorf("expected nil for empty slice, got %v", resolved)
	}
}

func TestResolveVariables(t *testing.T) {
	workspace := "/workspace"
	resolver, err := NewVariableResolver(workspace)
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	trueVal := true
	restrictions := &config.RunnerRestrictions{
		AllowNetworking: &trueVal,
		AllowReadFolders: []string{
			"$WORKSPACE/src",
			"$HOME/.config",
		},
		AllowWriteFolders: []string{
			"$WORKSPACE/build",
		},
		DenyFolders: []string{
			"$HOME/.ssh",
		},
	}

	resolved := resolveVariables(restrictions, resolver)

	if resolved == nil {
		t.Fatal("expected resolved restrictions, got nil")
	}

	// Check that networking flag is preserved
	if *resolved.AllowNetworking != true {
		t.Errorf("expected allow_networking=true")
	}

	// Check that variables are resolved
	if len(resolved.AllowReadFolders) != 2 {
		t.Errorf("expected 2 read folders, got %d", len(resolved.AllowReadFolders))
	}
	if resolved.AllowReadFolders[0] != "/workspace/src" {
		t.Errorf("expected /workspace/src, got %s", resolved.AllowReadFolders[0])
	}

	if len(resolved.AllowWriteFolders) != 1 {
		t.Errorf("expected 1 write folder, got %d", len(resolved.AllowWriteFolders))
	}
	if resolved.AllowWriteFolders[0] != "/workspace/build" {
		t.Errorf("expected /workspace/build, got %s", resolved.AllowWriteFolders[0])
	}

	if len(resolved.DenyFolders) != 1 {
		t.Errorf("expected 1 deny folder, got %d", len(resolved.DenyFolders))
	}
}

func TestResolveVariables_Nil(t *testing.T) {
	resolver, err := NewVariableResolver("/workspace")
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	resolved := resolveVariables(nil, resolver)
	if resolved != nil {
		t.Errorf("expected nil for nil input, got %+v", resolved)
	}
}
