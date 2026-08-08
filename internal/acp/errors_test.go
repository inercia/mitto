package acp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// containsIgnoreCase reports whether substr appears in s, case-insensitively.
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

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
		// --- Permanent: MCP init timeout (mitto-8ul.1) ---
		{
			name:          "MCP initialization timed out in wrapped error",
			err:           fmt.Errorf("session/new: mcp initialization timed out (agent reported MCP-init wait exhausted): context deadline exceeded"),
			stderr:        "",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "MCP server initialization timed out",
		},
		{
			name:          "MCP initialization timed out in stderr",
			err:           fmt.Errorf("failed to create session"),
			stderr:        "auggie: MCP initialization timed out after 225s",
			wantClass:     ACPErrorPermanent,
			wantRetryable: false,
			wantContains:  "MCP server initialization timed out",
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
			result := ClassifyACPError(tt.err, tt.stderr)

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
	classified := ClassifyACPError(orig, "some stderr")

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
		if got := FormatClassifiedError(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("with guidance", func(t *testing.T) {
		e := &ACPClassifiedError{
			UserMessage:  "Something broke",
			UserGuidance: "Fix it this way",
		}
		got := FormatClassifiedError(e)
		if got != "Something broke. Fix it this way" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("without guidance", func(t *testing.T) {
		e := &ACPClassifiedError{
			UserMessage: "Something broke",
		}
		got := FormatClassifiedError(e)
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
			delays[i] = BackoffDelay(i, base, max, jitter)
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
		got := BackoffDelay(10, base, max, jitter)
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
			d := BackoffDelay(0, base, max, jitter)
			minExpected := time.Duration(float64(base) * (1 - jitter))
			maxExpected := time.Duration(float64(base) * (1 + jitter))
			if d < minExpected || d > maxExpected {
				t.Errorf("iteration %d: delay %v outside bounds [%v, %v]", i, d, minExpected, maxExpected)
			}
		}
	})

	t.Run("zero jitter is deterministic", func(t *testing.T) {
		d1 := BackoffDelay(2, time.Second, 10*time.Second, 0.0)
		d2 := BackoffDelay(2, time.Second, 10*time.Second, 0.0)
		if d1 != d2 {
			t.Errorf("zero jitter should be deterministic: %v != %v", d1, d2)
		}
	})
}

// TestFormatACPError_QueryClosedHandshake verifies the cause-neutral,
// remedy-first handshake message (mitto-biu, correcting mitto-bov). The SDK
// tears down its session/new async iterator on this signature both on
// cold-start auth failure AND on a wedged shared process (mitto-aoo) — so the
// message must NOT assert an auth diagnosis, must lead with "Restart ACP", and
// may only mention re-authentication as a hedged secondary hint.
func TestFormatACPError_QueryClosedHandshake(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantContains string
		wantExcludes string
	}{
		{
			name:         "exact SDK payload triggers remedy-first guidance",
			err:          fmt.Errorf(`failed to create session: {"code":-32603,"message":"Internal error","data":{"details":"Query closed before response received"}}`),
			wantContains: "Restart ACP",
		},
		{
			name:         "exact SDK payload does not assert an auth diagnosis",
			err:          fmt.Errorf(`failed to create session: {"code":-32603,"message":"Internal error","data":{"details":"Query closed before response received"}}`),
			wantExcludes: "authentication has expired",
		},
		{
			name:         "case-insensitive match on details substring",
			err:          fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"details":"QUERY CLOSED BEFORE RESPONSE RECEIVED"}}`),
			wantContains: "handshake failed",
		},
		{
			name:         "case-insensitive match still does not assert auth expiry",
			err:          fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"details":"QUERY CLOSED BEFORE RESPONSE RECEIVED"}}`),
			wantExcludes: "authentication has expired",
		},
		{
			name:         "generic -32603 without query-closed falls through to catch-all",
			err:          fmt.Errorf(`{"code":-32603,"message":"Internal error"}`),
			wantExcludes: "authentication has expired",
		},
		{
			name:         "query-closed phrase without -32603 does not hijack",
			err:          fmt.Errorf(`something else: query closed before response received`),
			wantExcludes: "authentication has expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatACPError(tt.err)
			if tt.wantContains != "" && !containsIgnoreCase(got, tt.wantContains) {
				t.Errorf("FormatACPError(%v) = %q, want to contain %q", tt.err, got, tt.wantContains)
			}
			if tt.wantExcludes != "" && containsIgnoreCase(got, tt.wantExcludes) {
				t.Errorf("FormatACPError(%v) = %q, must NOT contain %q", tt.err, got, tt.wantExcludes)
			}
		})
	}
}

// TestFormatACPErrorWithContext_QueryClosedHandshake_ProcessHistory verifies
// the mitto-azk warm/cold/unknown split: a shared process that previously
// completed a session RPC (ProcessHistoryWarm) gets a wedge-oriented message
// with the authentication hint dropped entirely, while ProcessHistoryCold and
// the zero-value ProcessHistoryUnknown both retain the original mitto-biu
// hedged wording (auth remains a plausible cause on a first-contact failure).
func TestFormatACPErrorWithContext_QueryClosedHandshake_ProcessHistory(t *testing.T) {
	queryClosedErr := fmt.Errorf(`failed to create session: {"code":-32603,"message":"Internal error","data":{"details":"Query closed before response received"}}`)

	tests := []struct {
		name         string
		hints        FormatErrorHints
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "warm process drops the auth hint entirely",
			hints:        FormatErrorHints{ProcessHistory: ProcessHistoryWarm},
			wantContains: []string{"Restart ACP", "handshake failed", "wedged"},
			wantExcludes: []string{"authenticated", "authentication", "claude auth login", "auggie auth login"},
		},
		{
			name:         "cold process retains the hedged auth hint",
			hints:        FormatErrorHints{ProcessHistory: ProcessHistoryCold},
			wantContains: []string{"Restart ACP", "handshake failed", "authenticated"},
		},
		{
			name:         "unknown (zero-value) process history retains the hedged auth hint",
			hints:        FormatErrorHints{ProcessHistory: ProcessHistoryUnknown},
			wantContains: []string{"Restart ACP", "handshake failed", "authenticated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatACPErrorWithContext(queryClosedErr, tt.hints)
			for _, want := range tt.wantContains {
				if !containsIgnoreCase(got, want) {
					t.Errorf("FormatACPErrorWithContext(err, %+v) = %q, want to contain %q", tt.hints, got, want)
				}
			}
			for _, exclude := range tt.wantExcludes {
				if containsIgnoreCase(got, exclude) {
					t.Errorf("FormatACPErrorWithContext(err, %+v) = %q, must NOT contain %q", tt.hints, got, exclude)
				}
			}
		})
	}
}

// TestFormatACPError_IsZeroHintWrapperAroundFormatACPErrorWithContext locks in
// that FormatACPError(err) stays byte-identical to
// FormatACPErrorWithContext(err, FormatErrorHints{}) for every existing
// caller — the mitto-azk contract that the context-aware variant is
// opt-in-only and does not alter default behavior.
func TestFormatACPError_IsZeroHintWrapperAroundFormatACPErrorWithContext(t *testing.T) {
	errs := []error{
		nil,
		fmt.Errorf(`failed to create session: {"code":-32603,"message":"Internal error","data":{"details":"Query closed before response received"}}`),
		fmt.Errorf(`{"code":-32603,"message":"Internal error"}`),
		errors.New("rate limit exceeded"),
		errors.New("Authentication required"),
		errors.New("peer disconnected"),
		errors.New("some unrecognized failure"),
	}

	for _, err := range errs {
		wrapper := FormatACPError(err)
		explicit := FormatACPErrorWithContext(err, FormatErrorHints{})
		if wrapper != explicit {
			t.Errorf("FormatACPError(%v) = %q, want byte-identical to FormatACPErrorWithContext(err, FormatErrorHints{}) = %q", err, wrapper, explicit)
		}
	}
}

// TestFormatACPErrorWithContext_UpstreamConnectTimeout_mitto_gbf5 reproduces
// mitto-gbf5: an upstream connect-timeout brownout to xlb.api.augmentcode.com
// (data.apiStatus == "unavailable", UND_ERR_CONNECT_TIMEOUT) currently falls
// through to the generic -32603 "internal error" branch of
// FormatACPErrorWithContext because extractHTTPStatus finds no HTTP status in
// the envelope. The condition should instead be named for the user (e.g.
// "unreachable"/"unavailable") so it is distinguishable from an unrelated
// agent-side internal error. This test is expected to FAIL until a dedicated
// upstream-unavailable classifier is added ahead of the generic -32603 branch.
func TestFormatACPErrorWithContext_UpstreamConnectTimeout_mitto_gbf5(t *testing.T) {
	// Exact payload observed in the 2026-08-08 11:38-11:41 brownout (see
	// mitto-gbf5 description / Investigation comment).
	err := fmt.Errorf(`{"code":-32603,"message":"Internal error: fetch failed (UND_ERR_CONNECT_TIMEOUT: Connect Timeout Error (attempted address: xlb.api.augmentcode.com:443, timeout: 10000ms))","data":{"apiStatus":"unavailable"}}`)

	got := FormatACPErrorWithContext(err, FormatErrorHints{})

	if containsIgnoreCase(got, "encountered an internal error") {
		t.Errorf("FormatACPErrorWithContext(err) = %q; upstream connect-timeout brownout must not be shaped as a generic internal error", got)
	}
	if !containsIgnoreCase(got, "unreachable") && !containsIgnoreCase(got, "unavailable") {
		t.Errorf("FormatACPErrorWithContext(err) = %q; want a message naming the upstream-unreachable condition (mitto-gbf5)", got)
	}
}

// TestIsHandshakeQueryClosedError is the classifier truth table for the
// string-based predicate extracted in mitto-biu, mirroring the structured twin
// acperrors.IsAgentQueryClosedErr (internal/acpproc/acperrors/acperrors_test.go).
func TestIsHandshakeQueryClosedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{
			"exact wedge signature",
			fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"details":"Query closed before response received"}}`),
			true,
		},
		{
			"case-insensitive message match",
			fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"details":"QUERY CLOSED BEFORE RESPONSE RECEIVED"}}`),
			true,
		},
		{
			"right code, unrelated message",
			fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"details":"some other failure"}}`),
			false,
		},
		{
			"query-closed phrase without -32603",
			errors.New("something else: query closed before response received"),
			false,
		},
		{"unrelated plain error", errors.New("context canceled"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsHandshakeQueryClosedError(tt.err); got != tt.want {
				t.Errorf("IsHandshakeQueryClosedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsAuthError verifies the predicate used by handlePromptError to stop
// queue advancement when the upstream CLI's authentication has expired
// (mitto-r5o). Match is case-insensitive on "authentication required".
func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Claude Code -32000 payload",
			err:  fmt.Errorf(`{"code":-32000,"message":"Authentication required"}`),
			want: true,
		},
		{
			name: "lowercase phrase",
			err:  fmt.Errorf("authentication required"),
			want: true,
		},
		{
			name: "mixed case phrase",
			err:  fmt.Errorf("Authentication Required"),
			want: true,
		},
		{
			name: "wrapped in ACPClassifiedError",
			err: &ACPClassifiedError{
				Class:         ACPErrorTransient,
				OriginalError: fmt.Errorf(`{"code":-32000,"message":"Authentication required"}`),
			},
			want: true,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("some other failure"),
			want: false,
		},
		{
			name: "bare -32000 without auth phrase does not match",
			err:  fmt.Errorf(`{"code":-32000,"message":"Some unrelated server error"}`),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAuthError(tt.err); got != tt.want {
				t.Errorf("IsAuthError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsMCPInitTimeout verifies the predicate used by the auto-resume paths to
// carve out the transient cold-start MCP-init timeout from the hard failure
// counter (mitto-54k.6).
func TestIsMCPInitTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "plain error with substring",
			err:  fmt.Errorf("mcp initialization timed out after 30s"),
			want: true,
		},
		{
			name: "plain error case-insensitive",
			err:  fmt.Errorf("MCP INITIALIZATION TIMED OUT after 30s"),
			want: true,
		},
		{
			name: "wrapped in ACPClassifiedError",
			err: &ACPClassifiedError{
				Class:         ACPErrorPermanent,
				OriginalError: fmt.Errorf("mcp initialization timed out after 30s"),
			},
			want: true,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("some other failure"),
			want: false,
		},
		{
			name: "wrapped unrelated error",
			err: &ACPClassifiedError{
				Class:         ACPErrorPermanent,
				OriginalError: fmt.Errorf("Cannot find module '@anthropic-ai/claude-code'"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMCPInitTimeout(tt.err); got != tt.want {
				t.Errorf("IsMCPInitTimeout(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsContextTooLargeError_mitto_2efc_UncorroboratedInvalidArgument
// reproduces mitto-2efc: IsContextTooLargeError's mitto-k4x pair-match on
// `"httpStatus":400` + `"apiStatus":"invalidArgument"` is unconditional, with
// no requirement that the payload actually mention a token/length overflow.
// This misclassifies ANY upstream 400 invalidArgument (e.g. a deferred
// model-switch race, or any other malformed-request 400) as
// contextWindowExceeded, sending the loop runner down the wrong recovery path
// (3-strike context-window ceiling instead of the generic 8-strike delivery
// failure ceiling) and producing a misleading stopped_reason on the bead.
//
// This test is expected to FAIL until IsContextTooLargeError requires
// corroborating evidence (a token/length signal in the payload) before
// classifying an uncorroborated 400/invalidArgument as context-too-large.
func TestIsContextTooLargeError_mitto_2efc_UncorroboratedInvalidArgument(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "uncorroborated 400 invalidArgument (no token/length signal) is NOT context-too-large",
			err:  fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"httpStatus":400,"apiStatus":"invalidArgument","details":"model claude-opus-5 is not yet available for this account"}}`),
			want: false,
		},
		{
			name: "400 invalidArgument corroborated by a token/length phrase IS still context-too-large",
			err:  fmt.Errorf(`{"code":-32603,"message":"Internal error","data":{"httpStatus":400,"apiStatus":"invalidArgument","details":"request exceeds maximum token length"}}`),
			want: true,
		},
		{
			name: "HTTP 413 is unaffected by the corroboration requirement",
			err:  fmt.Errorf(`HTTP error: 413 Payload Too Large`),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextTooLargeError(tt.err); got != tt.want {
				t.Errorf("IsContextTooLargeError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
