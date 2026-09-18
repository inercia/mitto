package main

import "testing"

// TestExpandCaptures exercises expandCaptures' "${N}" substitution directly
// (mitto-3od.6): the mechanism findMatchingResponse relies on to echo a
// runtime-generated value (e.g. a dispatch_id) captured from the incoming
// prompt back into a scripted response.
func TestExpandCaptures(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		submatches []string
		want       string
	}{
		{
			name:       "no placeholder is left untouched",
			in:         "plain text, no substitution",
			submatches: []string{"full match", "group1"},
			want:       "plain text, no substitution",
		},
		{
			name:       "nil submatches leaves placeholder literal",
			in:         "id=${1}",
			submatches: nil,
			want:       "id=${1}",
		},
		{
			name:       "empty submatches leaves placeholder literal",
			in:         "id=${1}",
			submatches: []string{},
			want:       "id=${1}",
		},
		{
			name:       "single capture group substituted",
			in:         `{"dispatch_id":"${1}","save_count":0}`,
			submatches: []string{`"dispatch_id":"abc-123"`, "abc-123"},
			want:       `{"dispatch_id":"abc-123","save_count":0}`,
		},
		{
			name:       "multiple placeholders, multiple groups",
			in:         "${1}-${2}-${1}",
			submatches: []string{"full", "aaa", "bbb"},
			want:       "aaa-bbb-aaa",
		},
		{
			name:       "${0} expands to the full match",
			in:         "matched: ${0}",
			submatches: []string{"WHOLE", "part"},
			want:       "matched: WHOLE",
		},
		{
			name:       "out-of-range group left as literal text",
			in:         "id=${5}",
			submatches: []string{"full", "only-group"},
			want:       "id=${5}",
		},
		{
			name:       "unterminated placeholder left literal",
			in:         "id=${1",
			submatches: []string{"full", "x"},
			want:       "id=${1",
		},
		{
			name:       "bare $ without braces left untouched",
			in:         "price: $5",
			submatches: []string{"full", "x"},
			want:       "price: $5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandCaptures(tt.in, tt.submatches); got != tt.want {
				t.Errorf("expandCaptures(%q, %v) = %q, want %q", tt.in, tt.submatches, got, tt.want)
			}
		})
	}
}

// TestResponseUsesCaptureTemplate exercises the templated-response detector
// used by findMatchingResponse to give "${N}"-referencing responses match
// priority over the plain shortest-span heuristic (mitto-3od.6).
func TestResponseUsesCaptureTemplate(t *testing.T) {
	tests := []struct {
		name string
		resp *Response
		want bool
	}{
		{
			name: "no actions",
			resp: &Response{},
			want: false,
		},
		{
			name: "plain agent_message chunk, no placeholder",
			resp: &Response{Actions: []Action{{Type: "agent_message", Chunks: []string{"hello"}}}},
			want: false,
		},
		{
			name: "templated agent_message chunk",
			resp: &Response{Actions: []Action{{Type: "agent_message", Chunks: []string{"id=${1}"}}}},
			want: true,
		},
		{
			name: "templated agent_thought text",
			resp: &Response{Actions: []Action{{Type: "agent_thought", Text: "thinking about ${1}"}}},
			want: true,
		},
		{
			name: "templated tool_call title",
			resp: &Response{Actions: []Action{{Type: "tool_call", Title: "call ${1}"}}},
			want: true,
		},
		{
			name: "templated rpc_error message",
			resp: &Response{Actions: []Action{{Type: "rpc_error", Message: "failed: ${1}"}}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responseUsesCaptureTemplate(tt.resp); got != tt.want {
				t.Errorf("responseUsesCaptureTemplate(%+v) = %v, want %v", tt.resp, got, tt.want)
			}
		})
	}
}

// TestFindMatchingResponse_TemplatedOutranksShorterSpan pins the
// mitto-3od.6 selection-heuristic fix: a "${N}"-templated response must win
// even when a DIFFERENT scenario matches a strictly shorter span of the same
// message — reproducing the real-world collision between the knowledge
// router's own prompt text ("...update the smallest-diff file...") and the
// unrelated tool-calls-interleaved fixture's "(fix|edit|update).*file"
// pattern. Without the templated-priority check, the shorter-span
// non-templated match would win and the dispatch_id would never be echoed.
func TestFindMatchingResponse_TemplatedOutranksShorterSpan(t *testing.T) {
	longMessage := `Please update the router's file with the routing rule. "dispatch_id":"cad03c4f-3653-4517-8e30-b41079aac1bc"`

	s := &MockACPServer{scenarios: map[string]*Scenario{
		// Non-templated: short 20-ish char span ("update the router's f").
		"a-short-span-no-template": {Responses: []Response{{
			Trigger: Trigger{Type: "prompt", Pattern: `(update).*(file)`},
			Actions: []Action{{Type: "agent_message", Chunks: []string{"generic reply"}}},
		}}},
		// Templated: spans the whole ~50-char captured UUID.
		"z-templated-long-span": {Responses: []Response{{
			Trigger: Trigger{Type: "prompt", Pattern: `"dispatch_id":"([0-9a-fA-F-]{36})"`},
			Actions: []Action{{Type: "agent_message", Chunks: []string{"MITTO_PROCESSOR_COMPLETION ${1}"}}},
		}}},
	}}

	resp, submatches := s.findMatchingResponse(longMessage)
	if resp == nil {
		t.Fatal("expected a match, got nil")
	}
	if !responseUsesCaptureTemplate(resp) {
		t.Fatalf("expected the templated response to win, got non-templated: %+v", resp)
	}
	if len(submatches) < 2 || submatches[1] != "cad03c4f-3653-4517-8e30-b41079aac1bc" {
		t.Fatalf("expected captured dispatch_id in submatches, got %v", submatches)
	}
}

// TestFindMatchingResponse_ShortestSpanWinsAmongNonTemplated pins the
// pre-existing (unchanged) shortest-span tie-break for the common case where
// no candidate response is templated — regression coverage ensuring
// mitto-3od.6's new templated-priority branch didn't disturb it.
func TestFindMatchingResponse_ShortestSpanWinsAmongNonTemplated(t *testing.T) {
	message := "TEST:code-block-split please also list the files in this directory"

	s := &MockACPServer{scenarios: map[string]*Scenario{
		"file-list": {Responses: []Response{{
			Trigger: Trigger{Type: "prompt", Pattern: `(?i)(list|show|what).*(files|directory|folder)`},
			Actions: []Action{{Type: "agent_message", Chunks: []string{"file list reply"}}},
		}}},
		"code-block-split": {Responses: []Response{{
			Trigger: Trigger{Type: "prompt", Pattern: `TEST:code-block-split`},
			Actions: []Action{{Type: "agent_message", Chunks: []string{"code block reply"}}},
		}}},
	}}

	resp, _ := s.findMatchingResponse(message)
	if resp == nil {
		t.Fatal("expected a match, got nil")
	}
	if resp.Actions[0].Chunks[0] != "code block reply" {
		t.Fatalf("expected the shorter, more specific span to win, got %q", resp.Actions[0].Chunks[0])
	}
}
