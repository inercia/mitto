package beads

import (
	"context"
	"errors"
	"fmt"

	"github.com/inercia/mitto/internal/workspaces"
)

type databaseModeGuard struct {
	key   string
	value string
}

var databaseModeGuards = []databaseModeGuard{
	{key: "no-push", value: "true"},
	{key: "dolt.local-only", value: "true"},
	{key: "dolt.auto-push", value: "false"},
}

// ErrSharedModeRequiresRemote is returned when shared mode is requested for a
// folder with no existing Dolt remote. Mitto never creates or edits remotes.
var ErrSharedModeRequiresRemote = errors.New("shared beads database mode requires an existing Dolt remote; configure one with bd first, then retry")

// DatabaseModeReconciler is the focused capability implemented by clients that
// can apply Mitto's native database-mode safeguards.
type DatabaseModeReconciler interface {
	ReconcileDatabaseMode(context.Context, string, workspaces.BeadsDatabaseMode) error
}

// ReconcileDatabaseMode invokes the client's native policy capability.
func ReconcileDatabaseMode(ctx context.Context, client any, workingDir string, mode workspaces.BeadsDatabaseMode) error {
	reconciler, ok := client.(DatabaseModeReconciler)
	if !ok {
		return errors.New("beads client does not support database-mode reconciliation")
	}
	return reconciler.ReconcileDatabaseMode(ctx, workingDir, mode)
}

// ReconcileDatabaseMode applies or removes only Mitto's policy-owned native
// guards. Local mode leaves configured remotes dormant; shared mode requires a
// remote but never creates, removes, or edits its definition.
func (c *cliClient) ReconcileDatabaseMode(ctx context.Context, dir string, mode workspaces.BeadsDatabaseMode) error {
	if !isInitialized(dir) {
		if mode == workspaces.BeadsDatabaseModeShared {
			return ErrSharedModeRequiresRemote
		}
		return nil
	}
	return c.reconcileInitializedDatabaseMode(ctx, dir, mode)
}

func (c *cliClient) reconcileInitializedDatabaseMode(ctx context.Context, dir string, mode workspaces.BeadsDatabaseMode) error {
	switch mode {
	case workspaces.BeadsDatabaseModeLocal:
		for _, guard := range databaseModeGuards {
			if _, err := c.runRaw(ctx, defaultTimeout, dir, "config", "set", guard.key, guard.value); err != nil {
				return fmt.Errorf("apply local beads safeguard %s=%s: %w", guard.key, guard.value, err)
			}
		}
	case workspaces.BeadsDatabaseModeShared:
		hasRemote, err := c.HasDoltRemote(ctx, dir)
		if err != nil {
			return fmt.Errorf("verify Dolt remote before enabling shared beads mode: %w", err)
		}
		if !hasRemote {
			return ErrSharedModeRequiresRemote
		}
		// Remove no-push last so a partial failure remains fail-safe: the
		// strongest network guard stays active until every other guard is gone.
		for i := len(databaseModeGuards) - 1; i >= 0; i-- {
			guard := databaseModeGuards[i]
			if _, err := c.runRaw(ctx, defaultTimeout, dir, "config", "unset", guard.key); err != nil {
				return fmt.Errorf("remove local-only beads safeguard %s: %w", guard.key, err)
			}
		}
	default:
		return fmt.Errorf("invalid beads database mode %q", mode)
	}
	return nil
}

// ReconcileDatabaseMode delegates to the wrapped client and invalidates reads
// because native config changes can alter subsequent bd behavior.
func (c *CachingClient) ReconcileDatabaseMode(ctx context.Context, dir string, mode workspaces.BeadsDatabaseMode) error {
	defer c.Invalidate(dir)
	return ReconcileDatabaseMode(ctx, c.inner, dir, mode)
}

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
