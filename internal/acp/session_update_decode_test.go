package acp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

// Reproduces mitto-jll: the ACP SDK rejects some session/update notifications
// from Claude Code with "-32602 Invalid params" / "invalid variant payload",
// but the error string is opaque — it contains no discriminator, no field
// name, and no payload preview — so live-log occurrences cannot be diagnosed.
//
// This test drives realistic session/update payloads (matching the shapes
// investigated in the mitto-jll research comment) through the SDK's
// SessionNotification.UnmarshalJSON and asserts that when decoding fails,
// the error carries enough context to identify the failing variant. It fails
// today because the SDK's generated union UnmarshalJSON returns the bare
// string "invalid variant payload" with zero context.
func TestSessionNotification_DecodeFailure_CarriesActionableContext(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		// expectedDiscriminator is the sessionUpdate variant that the failing
		// payload targets; the SDK's error should mention it.
		expectedDiscriminator string
	}{
		{
			// tool_call_update whose content field is a bare string — a
			// clean type mismatch: SessionToolCallUpdate.Content is
			// []ToolCallContent but the wire value is a string, so
			// json.Unmarshal(b, &SessionToolCallUpdate{}) fails. This is
			// one of the concrete failure shapes the mitto-jll investigation
			// identified as plausible for what Claude Code emitted.
			name:                  "tool_call_update_with_string_where_array_expected",
			expectedDiscriminator: "tool_call_update",
			payload: `{
				"sessionId": "sess-1",
				"update": {
					"sessionUpdate": "tool_call_update",
					"toolCallId": "toolu_01WN1SdSMxo6Q9KQvLhPHzLF",
					"content": "should-be-array"
				}
			}`,
		},
		{
			// tool_call_update whose content field is an object instead of an
			// array — a shape-level type mismatch on SessionToolCallUpdate.Content.
			name:                  "tool_call_update_with_wrong_content_shape",
			expectedDiscriminator: "tool_call_update",
			payload: `{
				"sessionId": "sess-1",
				"update": {
					"sessionUpdate": "tool_call_update",
					"toolCallId": "toolu_017cS5RxUn9fuNij8R1xq3Z4",
					"content": {}
				}
			}`,
		},
		{
			// tool_call_update whose nested ToolCallContent element has a
			// content field of the wrong type (string where ContentBlock is
			// expected). ToolCallContent.UnmarshalJSON matches "content" as
			// the type discriminator, then fails to decode the payload into
			// ToolCallContentContent because Content is typed as ContentBlock
			// (an object union), not a string.
			name:                  "tool_call_update_with_bad_nested_contentblock",
			expectedDiscriminator: "tool_call_update",
			payload: `{
				"sessionId": "sess-1",
				"update": {
					"sessionUpdate": "tool_call_update",
					"toolCallId": "toolu_017cS5RxUn9fuNij8R1xq3Z4",
					"content": [
						{"type": "content", "content": "should-be-object"}
					]
				}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n acp.SessionNotification
			err := json.Unmarshal([]byte(tt.payload), &n)
			if err == nil {
				t.Fatalf("expected decode to fail for %s, but it succeeded: %+v", tt.name, n)
			}

			msg := err.Error()

			// Fail-safe: ensure this is the mitto-jll error class.
			if !strings.Contains(msg, "invalid variant payload") &&
				!strings.Contains(msg, "no matching variant for union") {
				t.Fatalf("unexpected error class: %q", msg)
			}

			// The failing assertion for mitto-jll: the error MUST carry enough
			// context to identify the failing variant in a production log line.
			// Today the SDK returns the bare literal "invalid variant payload"
			// with no discriminator, no field name, and no payload preview —
			// which is precisely what makes the two observed live occurrences
			// unresolvable.
			if !strings.Contains(msg, tt.expectedDiscriminator) {
				t.Errorf("mitto-jll: error message is opaque; want it to mention "+
					"discriminator %q so live-log occurrences can be triaged, got: %q",
					tt.expectedDiscriminator, msg)
			}

			// Additionally require a payload preview (any snippet of the offending
			// JSON) so live logs record what actually failed. A JSON-preview
			// marker or any character sequence unique to the payload works —
			// require the toolCallId (when present) or the discriminant string
			// to appear alongside the discriminator, forcing the error to
			// include >1 useful token.
			hasToolCallID := strings.Contains(tt.payload, "toolu_")
			if hasToolCallID {
				// Look for at least the "toolu_" prefix, so live logs surface
				// the specific Claude Code tool call that triggered the failure.
				if !strings.Contains(msg, "toolu_") {
					t.Errorf("mitto-jll: error message lacks payload preview; want it "+
						"to include the offending toolCallId (toolu_...) so live-log "+
						"occurrences can be traced back to the specific tool call, got: %q",
						msg)
				}
			}
		})
	}
}
