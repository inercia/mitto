// Package config provides embedded default configuration for Mitto.
package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
func DeployBuiltinPrompts(targetDir string, force bool) (*DeployBuiltinPromptsResult, error) {
	result := &DeployBuiltinPromptsResult{}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	// Read all files from the embedded filesystem
	entries, err := fs.ReadDir(BuiltinPromptsFS, BuiltinPromptsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded prompts directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Skip directories
		}

		filename := entry.Name()
		srcPath := filepath.Join(BuiltinPromptsDir, filename)
		dstPath := filepath.Join(targetDir, filename)

		// Check if file already exists
		if _, err := os.Stat(dstPath); err == nil && !force {
			result.Skipped = append(result.Skipped, filename)
			continue
		}

		// Read file content from embedded filesystem
		content, err := fs.ReadFile(BuiltinPromptsFS, srcPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to read %s: %w", filename, err))
			continue
		}

		// Write file to target directory
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to write %s: %w", filename, err))
			continue
		}

		result.Deployed = append(result.Deployed, filename)
	}

	return result, nil
}

// EnsureBuiltinPrompts checks if the builtin prompts directory exists and deploys
// the embedded prompts if it doesn't. This is called on first run.
// Returns true if prompts were deployed, false if they already existed.
func EnsureBuiltinPrompts(targetDir string) (bool, error) {
	// Check if the builtin prompts directory exists
	if _, err := os.Stat(targetDir); err == nil {
		// Directory exists, check if it has any files
		entries, err := os.ReadDir(targetDir)
		if err != nil {
			return false, fmt.Errorf("failed to read builtin prompts directory: %w", err)
		}
		if len(entries) > 0 {
			// Directory has files, skip deployment
			return false, nil
		}
	}

	// Deploy builtin prompts (don't force overwrite)
	result, err := DeployBuiltinPrompts(targetDir, false)
	if err != nil {
		return false, err
	}

	// Return true if any files were deployed
	return len(result.Deployed) > 0, nil
}

// ListEmbeddedPrompts returns the list of embedded builtin prompt filenames.
func ListEmbeddedPrompts() ([]string, error) {
	entries, err := fs.ReadDir(BuiltinPromptsFS, BuiltinPromptsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded prompts directory: %w", err)
	}

	var filenames []string
	for _, entry := range entries {
		if !entry.IsDir() {
			filenames = append(filenames, entry.Name())
		}
	}
	return filenames, nil
}
