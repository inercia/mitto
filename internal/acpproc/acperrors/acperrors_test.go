package acperrors

import (
	"errors"
	"fmt"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

// TestIsAgentQueryClosedErr is the classifier truth table for the mitto-aoo
// wedge signature: a JSON-RPC -32603 ("Internal error") whose data carries
// "query closed before response received". This is the agent's OWN
// self-reported wedge, distinct from IsAgentInternalDeadlineErr (which
// requires a "deadline exceeded" message instead).
func TestIsAgentQueryClosedErr(t *testing.T) {
	wedgeErr := &acp.RequestError{
		Code:    -32603,
		Message: "Internal error",
		Data:    map[string]string{"details": "Query closed before response received"},
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"exact wedge signature", wedgeErr, true},
		{"wedge wrapped by fmt.Errorf %w", fmt.Errorf("session/new: %w", wedgeErr), true},
		{
			"case-insensitive message match",
			&acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]string{"details": "QUERY CLOSED BEFORE RESPONSE RECEIVED"}},
			true,
		},
		{
			"wrong code (-32000) same message",
			&acp.RequestError{Code: -32000, Message: "Internal error", Data: map[string]string{"details": "query closed before response received"}},
			false,
		},
		{
			"right code, unrelated message (method not found)",
			&acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]string{"details": "some other failure"}},
			false,
		},
		{
			"right code, deadline-exceeded message (the OTHER wedge, mitto-13ck.2)",
			&acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]string{"details": "context deadline exceeded"}},
			false,
		},
		{"plain non-RequestError error", errors.New("query closed before response received"), false},
		{"unrelated plain error", errors.New("context canceled"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAgentQueryClosedErr(tt.err); got != tt.want {
				t.Errorf("IsAgentQueryClosedErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
