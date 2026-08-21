package beads

import (
	"context"
	"errors"
	"time"
)

// migrateTimeout bounds a "bd migrate schema" invocation. Schema migrations
// on the Dolt backend touch many rows and can take a while when the DB is
// well populated, so it is generously above the write-path defaultTimeout.
const migrateTimeout = 5 * time.Minute

// bootstrapTimeout bounds a "bd bootstrap --non-interactive" invocation.
// Bootstrap may clone from a remote, so it is also generous.
const bootstrapTimeout = 5 * time.Minute

// LocalMigrator is the focused capability for schema-only local migrations.
type LocalMigrator interface {
	MigrateLocal(context.Context, string) ([]byte, error)
}

// MigrateLocal invokes the local-only migration capability on client.
func MigrateLocal(ctx context.Context, client any, dir string) ([]byte, error) {
	migrator, ok := client.(LocalMigrator)
	if !ok {
		return nil, errors.New("beads client does not support local schema migration")
	}
	return migrator.MigrateLocal(ctx, dir)
}

// MigrateLocal runs only the local schema migration. Local mode can retain a
// dormant remote, so it bypasses bd's remote-migration gate for this invocation
// while deliberately never invoking dolt push or bootstrap.
func (c *cliClient) MigrateLocal(ctx context.Context, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	out, stderr, err := c.runner.RunWithEnv(ctx, dir,
		[]string{"BD_ALLOW_REMOTE_MIGRATE=1"},
		"migrate", "schema", "--json",
	)
	if err != nil {
		return nil, &CmdError{Err: err, Stderr: diagnosticOutput(stderr, string(out)), ExitCode: exitCodeFromErr(err)}
	}
	return out, nil
}

// MigrateRemote runs the two-step remote-backed schema migration:
//
//  1. BD_ALLOW_REMOTE_MIGRATE=1 bd migrate schema --json
//     (bypasses bd's remote-migrate gate for exactly this invocation).
//  2. bd dolt push (publishes the reconciled schema to the remote).
//
// Both steps must succeed. When step 1 fails the error is returned directly
// without attempting step 2. Step 2 failures are wrapped with
// Stage: StagePublish so callers can distinguish a local migration that ran
// but did not publish (see IsPublishFailure) from a migrate-stage failure —
// either way the overall call still returns a non-nil error: a failed
// publish is never reported as an overall success.
//
// The returned bytes are step 1's stdout (bd migrate --json), useful for
// surfacing migration details in the API response — including on a step-2
// failure, where it lets the caller report exactly what the (successful)
// local migration applied even though publishing it failed.
func (c *cliClient) MigrateRemote(ctx context.Context, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	out, stderr, err := c.runner.RunWithEnv(ctx, dir,
		[]string{"BD_ALLOW_REMOTE_MIGRATE=1"},
		"migrate", "schema", "--json",
	)
	if err != nil {
		return nil, &CmdError{Err: err, Stderr: diagnosticOutput(stderr, string(out)), ExitCode: exitCodeFromErr(err)}
	}

	pushOut, pushStderr, pushErr := c.runner.Run(ctx, dir, "dolt", "push")
	if pushErr != nil {
		return out, &CmdError{Err: pushErr, Stderr: diagnosticOutput(pushStderr, string(pushOut)), ExitCode: exitCodeFromErr(pushErr), Stage: StagePublish}
	}

	return out, nil
}

// Bootstrap runs "bd bootstrap --non-interactive" in dir. It adopts a schema
// that another clone has already migrated (bd auto-detects: clones from
// sync.remote, restores from backup, or imports from JSONL as appropriate).
// Returns bd's stdout for the API response.
func (c *cliClient) Bootstrap(ctx context.Context, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()

	out, stderr, err := c.runner.Run(ctx, dir, "bootstrap", "--non-interactive")
	if err != nil {
		return nil, &CmdError{Err: err, Stderr: diagnosticOutput(stderr, string(out)), ExitCode: exitCodeFromErr(err)}
	}
	return out, nil
}
