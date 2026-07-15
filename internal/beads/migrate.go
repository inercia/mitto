package beads

import (
	"context"
	"time"
)

// migrateTimeout bounds a "bd migrate schema" invocation. Schema migrations
// on the Dolt backend touch many rows and can take a while when the DB is
// well populated, so it is generously above the write-path defaultTimeout.
const migrateTimeout = 5 * time.Minute

// bootstrapTimeout bounds a "bd bootstrap --non-interactive" invocation.
// Bootstrap may clone from a remote, so it is also generous.
const bootstrapTimeout = 5 * time.Minute

// MigrateRemote runs the two-step remote-backed schema migration:
//
//  1. BD_ALLOW_REMOTE_MIGRATE=1 bd migrate schema --json
//     (bypasses bd's remote-migrate gate for exactly this invocation).
//  2. bd dolt push (publishes the reconciled schema to the remote).
//
// Both steps must succeed. When step 1 fails the error is returned directly
// without attempting step 2. Step 2 failures are wrapped so callers can
// distinguish a local migration that ran but did not publish.
//
// The returned bytes are step 1's stdout (bd migrate --json), useful for
// surfacing migration details in the API response.
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
		return out, &CmdError{Err: pushErr, Stderr: diagnosticOutput(pushStderr, string(pushOut)), ExitCode: exitCodeFromErr(pushErr)}
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
