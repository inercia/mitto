package beads

import (
	"context"
	"fmt"

	"github.com/inercia/mitto/internal/workspaces"
)

// ResolveDatabaseMode returns the authoritative effective folder policy. An
// explicit folders.json value wins. A legacy missing value is inferred once
// from configured Dolt remotes and persisted so later native-config drift does
// not silently change policy.
func ResolveDatabaseMode(ctx context.Context, client any, workingDir string) (workspaces.BeadsDatabaseMode, error) {
	mode, configured, err := workspaces.ConfiguredFolderBeadsDatabaseMode(workingDir)
	if err != nil {
		return "", fmt.Errorf("load beads database mode: %w", err)
	}
	if configured {
		return mode, nil
	}

	hasRemote, err := DoltRemoteConfigured(ctx, client, workingDir)
	if err != nil {
		return "", fmt.Errorf("inspect Dolt remotes for beads database mode: %w", err)
	}
	mode = workspaces.BeadsDatabaseModeLocal
	if hasRemote {
		mode = workspaces.BeadsDatabaseModeShared
	}
	if err := workspaces.SetFolderBeadsDatabaseMode(workingDir, mode); err != nil {
		return "", fmt.Errorf("persist inferred beads database mode: %w", err)
	}
	return mode, nil
}
