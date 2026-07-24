// Package config provides embedded default configuration for Mitto.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DeployBuiltinPromptsResult contains the result of deploying builtin prompts.
type DeployBuiltinPromptsResult struct {
	// Deployed is the list of files that were deployed.
	Deployed []string
	// Skipped is the list of files that were skipped (already exist).
	Skipped []string
	// Errors is the list of errors that occurred during deployment.
	Errors []error
}

// DeployBuiltinPrompts deploys the embedded builtin prompts to the target directory.
// If force is true, existing files will be overwritten.
// If force is false, existing files will be skipped.
// Returns a result containing the list of deployed, skipped files, and any errors.
//
// The embedded tree may contain subdirectories (topic-based groupings such as
// `beads/`, `github/`, `jira/`); files are walked recursively and Deployed /
// Skipped entries are reported as forward-slash rel-paths from BuiltinPromptsDir
// (e.g. `beads/foo.prompt.yaml`) so nested layout is visible to callers.
func DeployBuiltinPrompts(targetDir string, force bool) (*DeployBuiltinPromptsResult, error) {
	result := &DeployBuiltinPromptsResult{}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	walkErr := fs.WalkDir(BuiltinPromptsFS, BuiltinPromptsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}

		relPath := strings.TrimPrefix(path, BuiltinPromptsDir+"/")
		dstPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

		// Check if file already exists
		if _, err := os.Stat(dstPath); err == nil && !force {
			result.Skipped = append(result.Skipped, relPath)
			return nil
		}

		// Read file content from embedded filesystem
		content, err := fs.ReadFile(BuiltinPromptsFS, path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to read %s: %w", relPath, err))
			return nil
		}

		// Ensure destination subdirectory exists before writing
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to create dir for %s: %w", relPath, err))
			return nil
		}

		// Write file to target directory
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to write %s: %w", relPath, err))
			return nil
		}

		result.Deployed = append(result.Deployed, relPath)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("failed to walk embedded prompts directory: %w", walkErr)
	}

	return result, nil
}

// EnsureBuiltinPrompts deploys embedded builtin prompts to the target directory.
// On first run (empty directory), all prompts are deployed.
// On subsequent runs, any prompts whose content differs from the embedded version
// are updated (e.g., when a new build adds fields like "group" to frontmatter).
// Returns true if any prompts were deployed or updated, false if all were up to date.
//
// The embed tree and target tree are both walked recursively (mitto-j88.1):
// nested subdirectories under BuiltinPromptsDir are preserved, and the prune
// pass removes stale nested files as well. After pruning files, any empty
// subdirectories that remain are also removed so the on-disk layout mirrors
// the embedded layout exactly.
func EnsureBuiltinPrompts(targetDir string) (bool, error) {
	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	deployed := false
	var errs []error
	// Track the embedded rel-paths so we can prune orphaned builtin prompts
	// (files that were removed or renamed in a newer build) from targetDir.
	// Keys are forward-slash rel-paths from BuiltinPromptsDir (e.g.
	// `beads/foo.prompt.yaml`); the top level uses just `foo.prompt.yaml`.
	embeddedNames := make(map[string]struct{})
	walkErr := fs.WalkDir(BuiltinPromptsFS, BuiltinPromptsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}

		relPath := strings.TrimPrefix(path, BuiltinPromptsDir+"/")
		embeddedNames[relPath] = struct{}{}
		dstPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

		// Read embedded content
		embeddedContent, err := fs.ReadFile(BuiltinPromptsFS, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read embedded prompt %s: %w", relPath, err))
			return nil
		}

		// Check if deployed file exists and matches
		existingContent, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(existingContent, embeddedContent) {
			return nil // Already up to date
		}

		// Ensure destination subdirectory exists before writing.
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			errs = append(errs, fmt.Errorf("failed to create dir for prompt %s: %w", relPath, err))
			return nil
		}

		// Deploy or update the file
		if err := os.WriteFile(dstPath, embeddedContent, 0644); err != nil {
			errs = append(errs, fmt.Errorf("failed to write prompt %s: %w", relPath, err))
			return nil
		}
		deployed = true
		return nil
	})
	if walkErr != nil {
		return deployed, fmt.Errorf("failed to walk embedded prompts directory: %w", walkErr)
	}

	// Prune orphaned builtin prompts: the builtin directory is fully managed by
	// Mitto, so any deployed .prompt.yaml file not present in the embedded set is stale
	// (e.g. a prompt that was consolidated or removed in a newer build). Legacy
	// old-format *.md builtin files left over from pre-migration versions are always
	// stale (the embedded set is *.prompt.yaml only) and are removed as well.
	// Walk the target tree recursively so stale nested files are also pruned.
	pruneErr := filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		isPromptYAML := strings.HasSuffix(name, ".prompt.yaml")
		isLegacyMD := strings.HasSuffix(name, ".md")
		if !isPromptYAML && !isLegacyMD {
			return nil
		}
		// Compute rel-path (forward slashes) for the embedded lookup. Legacy
		// *.md files at any depth are always stale (never in embeddedNames).
		rel, relErr := filepath.Rel(targetDir, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := embeddedNames[relSlash]; ok {
			return nil
		}
		if err := os.Remove(path); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove stale prompt %s: %w", relSlash, err))
			return nil
		}
		deployed = true
		return nil
	})
	if pruneErr != nil {
		errs = append(errs, fmt.Errorf("failed to walk target prompts directory: %w", pruneErr))
	}

	// Sweep now-empty subdirectories bottom-up so removed nested groups leave
	// no orphan dirs behind. os.Remove fails silently on non-empty dirs; that
	// is desirable — we only want to prune what is provably empty. The root
	// targetDir itself is never removed (skipped via depth check).
	//
	// We collect directories in a slice and iterate in reverse so children
	// are processed before their parents, giving us a bottom-up sweep in a
	// single lexicographic-order WalkDir.
	var dirs []string
	_ = filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // best-effort sweep; skip unreadable entries
		}
		if path == targetDir {
			return nil
		}
		dirs = append(dirs, path)
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		// Remove only if empty; ignore errors (non-empty or permissions).
		_ = os.Remove(dirs[i])
	}

	if len(errs) > 0 {
		return deployed, fmt.Errorf("some builtin prompts failed to deploy: %w", errors.Join(errs...))
	}

	return deployed, nil
}

// ListEmbeddedPrompts returns the list of embedded builtin prompt rel-paths.
// Rel-paths use forward slashes and are rooted at BuiltinPromptsDir, so a
// top-level prompt appears as `foo.prompt.yaml` and a nested one as
// `beads/foo.prompt.yaml`. The list is walked recursively (mitto-j88.1).
func ListEmbeddedPrompts() ([]string, error) {
	var filenames []string
	err := fs.WalkDir(BuiltinPromptsFS, BuiltinPromptsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		filenames = append(filenames, strings.TrimPrefix(path, BuiltinPromptsDir+"/"))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk embedded prompts directory: %w", err)
	}
	return filenames, nil
}
