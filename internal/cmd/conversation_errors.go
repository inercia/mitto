package cmd

import (
	"context"
	"errors"
	"net"
	"syscall"

	client "github.com/inercia/mitto/pkg/api"

	"github.com/inercia/mitto/internal/instancefile"
)

// Exit codes for the conversation/auth command trees, per
// docs/devel/cli-conversation.md §5. Stable from first release.
//
// exitWaitTimeout is a documented extension of the original 0-5 table
// (mitto-pscc.6): `conversation send --wait` uses it when --wait-timeout
// expires, distinct from exitUnreachable (3) since the server is reachable
// and the message is still queued/running — only the client gave up
// waiting for it to finish.
const (
	exitOK          = 0
	exitGeneric     = 1
	exitUsage       = 2
	exitUnreachable = 3
	exitAuthFailure = 4
	exitNotFound    = 5
	exitWaitTimeout = 6
)

// exitCodeError wraps err with the CLI exit code it should produce. main()
// unwraps it via errors.As; commands never call os.Exit directly.
type exitCodeError struct {
	code int
	err  error
}

func newExitCodeError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitCodeError{code: code, err: err}
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }
func (e *exitCodeError) ExitCode() int { return e.code }

// classify maps err to an exitCodeError per the table in
// docs/devel/cli-conversation.md §5. It is the single mechanical mapping
// point for the conversation/auth command trees — individual commands must
// wrap their SDK call results in classify() rather than reimplementing any
// part of this logic.
//
// Order matters: typed auth/not-found *client.APIError values are checked
// before the transport-unreachable bucket, since an *APIError also may
// satisfy some transport-like checks incidentally (it does not today, but
// keeping auth/not-found first keeps the precedence documented and stable).
func classify(err error) error {
	if err == nil {
		return nil
	}

	var existing *exitCodeError
	if errors.As(err, &existing) {
		return err
	}

	if errors.Is(err, client.ErrUnauthenticated) || errors.Is(err, client.ErrForbidden) {
		return newExitCodeError(exitAuthFailure, err)
	}
	if errors.Is(err, client.ErrNotFound) {
		return newExitCodeError(exitNotFound, err)
	}

	if isUnreachable(err) {
		return newExitCodeError(exitUnreachable, err)
	}

	return newExitCodeError(exitGeneric, err)
}

// isUnreachable reports whether err indicates the server could not be
// reached at all (as opposed to reaching it and getting an error response):
// connection refused, DNS failure, timeout/deadline exceeded, or one of the
// instancefile sentinel errors (no/stale/corrupt instance.json).
func isUnreachable(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, instancefile.ErrNotFound) || errors.Is(err, instancefile.ErrStale) || errors.Is(err, instancefile.ErrCorrupt) {
		return true
	}
	return false
}
