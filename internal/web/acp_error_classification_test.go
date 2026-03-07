package web

import (
	"fmt"
	"testing"
	"time"
)

func TestClassifyACPError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		stderr        string
		wantNil       bool
		wantClass     ACPErrorClass
		wantRetryable bool
		wantContains  string // substring expected in UserMessage
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			wantNil: true,
		},
		// --- Permanent: missing module ---
		{
			name:          "Cannot find module in stderr",
			err:           fmt.Errorf("failed to initialize"),
			stderr:        "Error: Cannot find module '@anthropic-ai/claude-code'",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Node.js module",
		},
		{
			name:          "MODULE_NOT_FOUND in stderr",
			err:           fmt.Errorf("exit status 1"),
			stderr:        "Error [MODULE_NOT_FOUND]: Cannot find package",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Node.js module",
		},
		{
			name:          "Cannot find module in error message",
			err:           fmt.Errorf("Cannot find module 'some-package'"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Node.js module",
		},
		// --- Permanent: command not found ---
		{
			name:          "command not found in error",
			err:           fmt.Errorf("exec: \"claude-code-acp\": executable file not found in $PATH"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "not found",
		},
		{
			name:          "no such file or directory in stderr",
			err:           fmt.Errorf("failed to start"),
			stderr:        "/bin/sh: auggie: no such file or directory",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "not found",
		},
		{
			name:          "executable file not found in error",
			err:           fmt.Errorf("executable file not found in $PATH"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "not found",
		},
		// --- Permanent: permission denied ---
		{
			name:          "EACCES in stderr",
			err:           fmt.Errorf("failed to start"),
			stderr:        "Error: EACCES: permission denied, open '/usr/local/lib'",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Permission denied",
		},
		{
			name:          "permission denied in error",
			err:           fmt.Errorf("permission denied"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Permission denied",
		},
		{
			name:          "Operation not permitted in stderr",
			err:           fmt.Errorf("process exited"),
			stderr:        "Operation not permitted",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "Permission denied",
		},
		// --- Permanent: syntax error ---
		{
			name:          "SyntaxError in stderr",
			err:           fmt.Errorf("process exited"),
			stderr:        "SyntaxError: Unexpected token {",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "syntax error",
		},
		{
			name:          "Unexpected token in stderr",
			err:           fmt.Errorf("exit status 1"),
			stderr:        "Unexpected token 'export'",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "syntax error",
		},
		// --- Permanent: ENOENT ---
		{
			name:          "ENOENT in stderr",
			err:           fmt.Errorf("failed to start"),
			stderr:        "Error: ENOENT: no such file or directory, stat '/app/server.js'",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "not found",
		},
		// --- Permanent: empty command ---
		{
			name:          "empty ACP command in error",
			err:           fmt.Errorf("empty ACP command"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "No ACP command",
		},
		// --- Transient: unrecognized errors ---
		{
			name:          "network timeout is transient",
			err:           fmt.Errorf("connection timeout after 30s"),
			stderr:        "",
			wantClass:     ACPErrorTransient,
			wantRetryable: true,
			wantContains:  "failed to start",
		},
		{
			name:          "generic crash is transient",
			err:           fmt.Errorf("peer disconnected before response"),
			stderr:        "segfault",
			wantClass:     ACPErrorTransient,
			wantRetryable: true,
		},
		{
			name:          "empty stderr is transient",
			err:           fmt.Errorf("exit status 1"),
			stderr:        "",
			wantClass:     ACPErrorTransient,
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyACPError(tt.err, tt.stderr)

			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if result.Class != tt.wantClass {
				t.Errorf("Class = %v, want %v", result.Class, tt.wantClass)
			}

			if result.IsRetryable() != tt.wantRetryable {
				t.Errorf("IsRetryable() = %v, want %v", result.IsRetryable(), tt.wantRetryable)
			}

			if result.OriginalError != tt.err {
				t.Errorf("OriginalError = %v, want %v", result.OriginalError, tt.err)
			}

			if result.Stderr != tt.stderr {
				t.Errorf("Stderr = %q, want %q", result.Stderr, tt.stderr)
			}

			if tt.wantContains != "" {
				if !containsIgnoreCase(result.UserMessage, tt.wantContains) {
					t.Errorf("UserMessage %q does not contain %q", result.UserMessage, tt.wantContains)
				}
			}

			// Permanent errors must have guidance
			if result.Class == ACPErrorPermanent && result.UserGuidance == "" {
				t.Error("permanent errors should have non-empty UserGuidance")
			}
		})
	}
}

func TestClassifyACPError_ErrorInterface(t *testing.T) {
	orig := fmt.Errorf("original error: %s", "details")
	classified := classifyACPError(orig, "some stderr")

	// Must satisfy error interface
	var err error = classified
	if err.Error() != orig.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), orig.Error())
	}

	// Must support Unwrap
	if classified.Unwrap() != orig {
		t.Errorf("Unwrap() = %v, want %v", classified.Unwrap(), orig)
	}
}

func TestACPErrorClass_String(t *testing.T) {
	tests := []struct {
		class ACPErrorClass
		want  string
	}{
		{ACPErrorTransient, "transient"},
		{ACPErrorPermanent, "permanent"},
		{ACPErrorClass(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.class.String(); got != tt.want {
			t.Errorf("ACPErrorClass(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
	}
}

func TestFormatClassifiedError(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		if got := formatClassifiedError(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("with guidance", func(t *testing.T) {
		e := &ACPClassifiedError{
			UserMessage:  "Something broke",
			UserGuidance: "Fix it this way",
		}
		got := formatClassifiedError(e)
		if got != "Something broke. Fix it this way" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("without guidance", func(t *testing.T) {
		e := &ACPClassifiedError{
			UserMessage: "Something broke",
		}
		got := formatClassifiedError(e)
		if got != "Something broke" {
			t.Errorf("got %q", got)
		}
	})
}

func TestBackoffDelay(t *testing.T) {
	t.Run("exponential growth", func(t *testing.T) {
		base := 500 * time.Millisecond
		max := 10 * time.Second
		jitter := 0.0 // no jitter for deterministic test

		delays := make([]time.Duration, 5)
		for i := 0; i < 5; i++ {
			delays[i] = backoffDelay(i, base, max, jitter)
		}

		// Expected: 500ms, 1s, 2s, 4s, 8s
		expected := []time.Duration{
			500 * time.Millisecond,
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
		}

		for i, want := range expected {
			if delays[i] != want {
				t.Errorf("attempt %d: got %v, want %v", i, delays[i], want)
			}
		}
	})

	t.Run("max cap", func(t *testing.T) {
		base := 500 * time.Millisecond
		max := 2 * time.Second
		jitter := 0.0

		// Attempt 10 should still be capped at max
		got := backoffDelay(10, base, max, jitter)
		if got != max {
			t.Errorf("got %v, want %v (max cap)", got, max)
		}
	})

	t.Run("jitter stays within bounds", func(t *testing.T) {
		base := 1 * time.Second
		max := 10 * time.Second
		jitter := 0.3 // ±30%

		// Run many times and check bounds
		for i := 0; i < 1000; i++ {
			d := backoffDelay(0, base, max, jitter)
			minExpected := time.Duration(float64(base) * (1 - jitter))
			maxExpected := time.Duration(float64(base) * (1 + jitter))
			if d < minExpected || d > maxExpected {
				t.Errorf("iteration %d: delay %v outside bounds [%v, %v]", i, d, minExpected, maxExpected)
			}
		}
	})

	t.Run("zero jitter is deterministic", func(t *testing.T) {
		d1 := backoffDelay(2, time.Second, 10*time.Second, 0.0)
		d2 := backoffDelay(2, time.Second, 10*time.Second, 0.0)
		if d1 != d2 {
			t.Errorf("zero jitter should be deterministic: %v != %v", d1, d2)
		}
	})
}
