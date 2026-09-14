package acperrors

import (
	"context"
	"errors"
	"testing"
)

// This file reproduces mitto-3a4: GitHub Copilot `--acp` failed_to_start is
// actually caused by copilot hanging in its OWN pre-ACP self-updater, before
// the ACP server (and therefore the Initialize handshake) ever starts. Mitto's
// startup watchdog (internal/acpproc/procstart/startup_watchdog.go) already
// detects the exact signature — no stderr output and no handshake observed —
// but only logs a WARN/ERROR; the returned error from the Initialize-failure
// branches (internal/acpproc/shared_acp_process.go and
// internal/conversation/bgsession_acp_process.go) stays an opaque "context
// deadline exceeded", giving the user no actionable hint.
//
// ClassifySilentStartupHang does not exist yet: this test currently FAILS TO
// COMPILE, which is the expected reproduction signal for this bug (the
// classifier described in the mitto-3a4 Investigation comment has not been
// implemented). It will pass once a pure, spawn-free classifier is added to
// this package that, given (initErr, stderrOutput, processExited), detects
// the "DeadlineExceeded + empty stderr + still alive" signature and returns a
// non-empty actionable hint — while staying fail-open (no hint) for every
// other input shape.
func TestClassifySilentStartupHang(t *testing.T) {
	deadlineErr := context.DeadlineExceeded
	wrappedDeadlineErr := errors.New("failed to initialize: " + deadlineErr.Error())
	canceledErr := context.Canceled

	tests := []struct {
		name          string
		err           error
		stderrOutput  string
		processExited bool
		wantMatched   bool
	}{
		{
			name:          "silent pre-handshake hang: deadline exceeded, no stderr, process alive",
			err:           deadlineErr,
			stderrOutput:  "",
			processExited: false,
			wantMatched:   true,
		},
		{
			name:          "silent pre-handshake hang: wrapped deadline error also matches",
			err:           wrappedDeadlineErr,
			stderrOutput:  "   \n  ",
			processExited: false,
			wantMatched:   true,
		},
		{
			name:          "no hint: nil error",
			err:           nil,
			stderrOutput:  "",
			processExited: false,
			wantMatched:   false,
		},
		{
			name:          "no hint: non-empty stderr (agent reported something)",
			err:           deadlineErr,
			stderrOutput:  "some diagnostic output",
			processExited: false,
			wantMatched:   false,
		},
		{
			name:          "no hint: process already exited (crash, not a silent hang)",
			err:           deadlineErr,
			stderrOutput:  "",
			processExited: true,
			wantMatched:   false,
		},
		{
			name:          "no hint: context canceled (connection/process torn down), not a timeout",
			err:           canceledErr,
			stderrOutput:  "",
			processExited: false,
			wantMatched:   false,
		},
		{
			name:          "no hint: unrelated plain error",
			err:           errors.New("some other failure"),
			stderrOutput:  "",
			processExited: false,
			wantMatched:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint, matched := ClassifySilentStartupHang(tt.err, tt.stderrOutput, tt.processExited)
			if matched != tt.wantMatched {
				t.Fatalf("ClassifySilentStartupHang(%v, %q, %v) matched=%v, want %v (hint=%q)",
					tt.err, tt.stderrOutput, tt.processExited, matched, tt.wantMatched, hint)
			}
			if matched && hint == "" {
				t.Fatalf("ClassifySilentStartupHang(%v, %q, %v) matched=true but returned empty hint",
					tt.err, tt.stderrOutput, tt.processExited)
			}
			if !matched && hint != "" {
				t.Fatalf("ClassifySilentStartupHang(%v, %q, %v) matched=false but returned non-empty hint %q",
					tt.err, tt.stderrOutput, tt.processExited, hint)
			}
		})
	}
}
