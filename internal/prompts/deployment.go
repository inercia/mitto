package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeploymentMarkerName brackets a multi-file prompts deployment. It lives in
// the watched prompts root so readers can retain their last-good snapshot until
// every prompt and fragment has landed.
const DeploymentMarkerName = ".mitto-prompts-deploying"

// DeploymentGenerationName changes immediately before DeploymentMarkerName is
// removed. A cache reload that spans an entire fast deployment can therefore
// detect the generation change even if it never observes the marker itself.
const DeploymentGenerationName = ".mitto-prompts-generation"

const deploymentMarkerMaxAge = 10 * time.Minute

// BeginDeployment starts a prompts filesystem transaction rooted at dir. The
// returned finish function publishes a new generation and removes the marker.
// Callers must invoke finish only after all related files have been written.
func BeginDeployment(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create prompts deployment root: %w", err)
	}

	markerPath := filepath.Join(dir, DeploymentMarkerName)
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(markerPath)
				return nil, fmt.Errorf("close prompts deployment marker: %w", closeErr)
			}
			return func() error {
				generationPath := filepath.Join(dir, DeploymentGenerationName)
				generation := []byte(fmt.Sprintf("%d\n", time.Now().UnixNano()))
				if err := os.WriteFile(generationPath, generation, 0644); err != nil {
					return fmt.Errorf("publish prompts deployment generation: %w", err)
				}
				if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove prompts deployment marker: %w", err)
				}
				return nil
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create prompts deployment marker: %w", err)
		}

		info, statErr := os.Stat(markerPath)
		if statErr != nil || time.Since(info.ModTime()) <= deploymentMarkerMaxAge {
			return nil, fmt.Errorf("prompts deployment already in progress in %s", dir)
		}
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale prompts deployment marker: %w", err)
		}
	}

	return nil, fmt.Errorf("could not start prompts deployment in %s", dir)
}

type deploymentSnapshot map[string]string

// DeploymentInProgress reports whether any prompt root is inside a recent
// multi-file deployment. Stale markers are ignored so a killed deployer cannot
// suppress reloads indefinitely.
func DeploymentInProgress(dirs []string) bool {
	_, active := captureDeploymentSnapshot(dirs)
	return active
}

func captureDeploymentSnapshot(dirs []string) (deploymentSnapshot, bool) {
	snapshot := make(deploymentSnapshot)
	active := false
	now := time.Now()
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		markerPath := filepath.Join(dir, DeploymentMarkerName)
		if info, err := os.Stat(markerPath); err == nil {
			age := now.Sub(info.ModTime())
			if age < 0 || age <= deploymentMarkerMaxAge {
				active = true
			}
		}

		generationPath := filepath.Join(dir, DeploymentGenerationName)
		if info, err := os.Stat(generationPath); err == nil {
			snapshot[generationPath] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
		}
	}
	return snapshot, active
}

func (s deploymentSnapshot) equal(other deploymentSnapshot) bool {
	if len(s) != len(other) {
		return false
	}
	for path, generation := range s {
		if other[path] != generation {
			return false
		}
	}
	return true
}
