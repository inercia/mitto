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

// DeployBuiltinAgentsResult contains the result of deploying builtin agents.
type DeployBuiltinAgentsResult struct {
	// Deployed is the list of files that were deployed.
	Deployed []string
	// Skipped is the list of files that were skipped (already exist).
	Skipped []string
	// Errors is the list of errors that occurred during deployment.
	Errors []error
}

// DeployBuiltinAgents deploys the embedded builtin agents to the target directory.
// If force is true, existing files will be overwritten.
// If force is false, existing files will be skipped.
// The directory structure is preserved recursively.
// Returns a result containing the list of deployed, skipped files, and any errors.
func DeployBuiltinAgents(targetDir string, force bool) (*DeployBuiltinAgentsResult, error) {
	result := &DeployBuiltinAgentsResult{}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	// Walk the embedded filesystem recursively
	err := fs.WalkDir(BuiltinAgentsFS, BuiltinAgentsDir, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute the relative path from the base dir
		relPath, err := filepath.Rel(BuiltinAgentsDir, srcPath)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", srcPath, err)
		}

		// Skip the root itself
		if relPath == "." {
			return nil
		}

		dstPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			// Create the directory in the target
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("failed to create directory %s: %w", relPath, err))
			}
			return nil
		}

		// Check if file already exists
		if _, err := os.Stat(dstPath); err == nil && !force {
			result.Skipped = append(result.Skipped, relPath)
			return nil
		}

		// Read file content from embedded filesystem
		content, err := fs.ReadFile(BuiltinAgentsFS, srcPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to read %s: %w", relPath, err))
			return nil
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to create parent dir for %s: %w", relPath, err))
			return nil
		}

		// Write file to target directory (use 0755 for shell scripts)
		perm := os.FileMode(0644)
		if strings.HasSuffix(relPath, ".sh") {
			perm = 0755
		}
		if err := os.WriteFile(dstPath, content, perm); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("failed to write %s: %w", relPath, err))
			return nil
		}

		result.Deployed = append(result.Deployed, relPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk embedded agents directory: %w", err)
	}

	return result, nil
}

// EnsureBuiltinAgents deploys embedded builtin agents to the target directory.
// On first run (empty directory), all agents are deployed.
// On subsequent runs, any agent files whose content differs from the embedded version
// are updated (e.g., when a new build adds or modifies builtin agents).
// Returns true if any agents were deployed or updated, false if all were up to date.
func EnsureBuiltinAgents(targetDir string) (bool, error) {
	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	deployed := false
	var errs []error

	// Walk the embedded filesystem recursively
	err := fs.WalkDir(BuiltinAgentsFS, BuiltinAgentsDir, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute the relative path from the base dir
		relPath, err := filepath.Rel(BuiltinAgentsDir, srcPath)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", srcPath, err)
		}

		// Skip the root itself
		if relPath == "." {
			return nil
		}

		dstPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				errs = append(errs, fmt.Errorf("failed to create directory %s: %w", relPath, err))
			}
			return nil
		}

		// Read embedded content
		embeddedContent, err := fs.ReadFile(BuiltinAgentsFS, srcPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read embedded agent file %s: %w", relPath, err))
			return nil
		}

		// Check if deployed file exists and matches
		existingContent, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(existingContent, embeddedContent) {
			return nil // Already up to date
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			errs = append(errs, fmt.Errorf("failed to create parent dir for %s: %w", relPath, err))
			return nil
		}

		// Deploy or update the file (use 0755 for shell scripts)
		perm := os.FileMode(0644)
		if strings.HasSuffix(relPath, ".sh") {
			perm = 0755
		}
		if err := os.WriteFile(dstPath, embeddedContent, perm); err != nil {
			errs = append(errs, fmt.Errorf("failed to write agent file %s: %w", relPath, err))
			return nil
		}
		deployed = true
		return nil
	})

	if err != nil {
		return deployed, fmt.Errorf("failed to walk embedded agents directory: %w", err)
	}

	if len(errs) > 0 {
		return deployed, fmt.Errorf("some builtin agents failed to deploy: %w", errors.Join(errs...))
	}

	return deployed, nil
}

// ListEmbeddedAgents returns the list of embedded builtin agent directory names.
func ListEmbeddedAgents() ([]string, error) {
	entries, err := fs.ReadDir(BuiltinAgentsFS, BuiltinAgentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded agents directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
