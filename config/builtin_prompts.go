// Package config provides embedded default configuration for Mitto.
package config

import (
	"bytes"
	"errors"
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

// EnsureBuiltinPrompts deploys embedded builtin prompts to the target directory.
// On first run (empty directory), all prompts are deployed.
// On subsequent runs, any prompts whose content differs from the embedded version
// are updated (e.g., when a new build adds fields like "group" to frontmatter).
// Returns true if any prompts were deployed or updated, false if all were up to date.
func EnsureBuiltinPrompts(targetDir string) (bool, error) {
	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	// Read all embedded prompt files
	entries, err := fs.ReadDir(BuiltinPromptsFS, BuiltinPromptsDir)
	if err != nil {
		return false, fmt.Errorf("failed to read embedded prompts directory: %w", err)
	}

	deployed := false
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		srcPath := filepath.Join(BuiltinPromptsDir, filename)
		dstPath := filepath.Join(targetDir, filename)

		// Read embedded content
		embeddedContent, err := fs.ReadFile(BuiltinPromptsFS, srcPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read embedded prompt %s: %w", filename, err))
			continue
		}

		// Check if deployed file exists and matches
		existingContent, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(existingContent, embeddedContent) {
			continue // Already up to date
		}

		// Deploy or update the file
		if err := os.WriteFile(dstPath, embeddedContent, 0644); err != nil {
			errs = append(errs, fmt.Errorf("failed to write prompt %s: %w", filename, err))
			continue
		}
		deployed = true
	}

	if len(errs) > 0 {
		return deployed, fmt.Errorf("some builtin prompts failed to deploy: %w", errors.Join(errs...))
	}

	return deployed, nil
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
