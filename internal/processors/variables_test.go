package processors

import "testing"

func TestSubstituteVariables(t *testing.T) {
	input := &ProcessorInput{
		SessionID:       "session-123",
		ParentSessionID: "parent-456",
		SessionName:     "Fix login bug",
		WorkingDir:      "/home/user/project",
		ACPServer:       "claude-code",
		WorkspaceUUID:   "ws-789",
		AvailableACPServers: []AvailableACPServer{
			{Name: "auggie", Type: "auggie", Tags: []string{"coding", "ai-assistant"}, Current: false},
			{Name: "claude-code", Type: "claude-code", Tags: []string{"coding"}, Current: true},
		},
	}

	tests := []struct {
		name     string
		message  string
		input    *ProcessorInput
		expected string
	}{
		{
			name:     "no variables",
			message:  "hello world",
			input:    input,
			expected: "hello world",
		},
		{
			name:     "session_id",
			message:  "id: @mitto:session_id",
			input:    input,
			expected: "id: session-123",
		},
		{
			name:     "parent_session_id",
			message:  "parent: @mitto:parent_session_id",
			input:    input,
			expected: "parent: parent-456",
		},
		{
			name:     "session_name",
			message:  "name: @mitto:session_name",
			input:    input,
			expected: "name: Fix login bug",
		},
		{
			name:     "working_dir",
			message:  "dir: @mitto:working_dir",
			input:    input,
			expected: "dir: /home/user/project",
		},
		{
			name:     "acp_server",
			message:  "server: @mitto:acp_server",
			input:    input,
			expected: "server: claude-code",
		},
		{
			name:     "workspace_uuid",
			message:  "ws: @mitto:workspace_uuid",
			input:    input,
			expected: "ws: ws-789",
		},
		{
			name:     "multiple variables",
			message:  "@mitto:session_id in @mitto:working_dir",
			input:    input,
			expected: "session-123 in /home/user/project",
		},
		{
			name:     "empty value substitutes to empty string",
			message:  "parent: @mitto:parent_session_id",
			input:    &ProcessorInput{SessionID: "x"}, // ParentSessionID is empty
			expected: "parent: ",
		},
		{
			name:     "unknown variable is left as-is",
			message:  "@mitto:unknown",
			input:    input,
			expected: "@mitto:unknown",
		},
		{
			name:     "no braces fast path",
			message:  "plain text without any variables",
			input:    input,
			expected: "plain text without any variables",
		},
		{
			name:     "all variables together",
			message:  "@mitto:session_id @mitto:parent_session_id @mitto:session_name @mitto:working_dir @mitto:acp_server @mitto:workspace_uuid",
			input:    input,
			expected: "session-123 parent-456 Fix login bug /home/user/project claude-code ws-789",
		},
		{
			name:     "variable repeated",
			message:  "@mitto:session_id and @mitto:session_id again",
			input:    input,
			expected: "session-123 and session-123 again",
		},
		{
			name:     "bare @session_id not substituted",
			message:  "@session_id",
			input:    input,
			expected: "@session_id",
		},
		{
			name:     "other @namespace not substituted",
			message:  "@git:status stays",
			input:    input,
			expected: "@git:status stays",
		},
		{
			name:     "mixed known and unknown variables",
			message:  "@mitto:session_id and @mitto:unknown",
			input:    input,
			expected: "session-123 and @mitto:unknown",
		},
		{
			name:     "available_acp_servers with tags and current marker",
			message:  "servers: @mitto:available_acp_servers",
			input:    input,
			expected: "servers: auggie [coding, ai-assistant], claude-code [coding] (current)",
		},
		{
			name:     "available_acp_servers empty list",
			message:  "servers: @mitto:available_acp_servers",
			input:    &ProcessorInput{},
			expected: "servers: ",
		},
		{
			name:    "available_acp_servers single server no tags",
			message: "@mitto:available_acp_servers",
			input: &ProcessorInput{
				AvailableACPServers: []AvailableACPServer{
					{Name: "my-agent", Current: true},
				},
			},
			expected: "my-agent (current)",
		},
		{
			name:    "available_acp_servers multiple servers no current",
			message: "@mitto:available_acp_servers",
			input: &ProcessorInput{
				AvailableACPServers: []AvailableACPServer{
					{Name: "agent-a", Tags: []string{"fast"}},
					{Name: "agent-b", Tags: []string{"smart", "expensive"}},
				},
			},
			expected: "agent-a [fast], agent-b [smart, expensive]",
		},
		{
			name:     "escaped variable not substituted",
			message:  `id: \@mitto:session_id`,
			input:    input,
			expected: "id: @mitto:session_id",
		},
		{
			name:     "escaped and unescaped mixed",
			message:  `@mitto:session_id and \@mitto:working_dir`,
			input:    input,
			expected: `session-123 and @mitto:working_dir`,
		},
		{
			name:     "multiple escaped variables",
			message:  `\@mitto:session_id \@mitto:acp_server`,
			input:    input,
			expected: "@mitto:session_id @mitto:acp_server",
		},
		{
			name:     "escaped unknown variable strips backslash",
			message:  `\@mitto:unknown`,
			input:    input,
			expected: "@mitto:unknown",
		},
		{
			name:     "double backslash before mitto not treated as escape",
			message:  `\\@mitto:session_id`,
			input:    input,
			expected: `\session-123`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubstituteVariables(tt.message, tt.input)
			if got != tt.expected {
				t.Errorf("SubstituteVariables(%q) = %q, want %q", tt.message, got, tt.expected)
			}
		})
	}
}

func TestSubstituteVariables_Parent(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		input    *ProcessorInput
		expected string
	}{
		{
			name:    "parent with name",
			message: "parent: @mitto:parent",
			input: &ProcessorInput{
				ParentSessionID:   "20260407-100000-aabbccdd",
				ParentSessionName: "Main session",
			},
			expected: "parent: 20260407-100000-aabbccdd (Main session)",
		},
		{
			name:    "parent without name",
			message: "parent: @mitto:parent",
			input: &ProcessorInput{
				ParentSessionID: "20260407-100000-aabbccdd",
			},
			expected: "parent: 20260407-100000-aabbccdd",
		},
		{
			name:     "no parent",
			message:  "parent: @mitto:parent",
			input:    &ProcessorInput{},
			expected: "parent: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubstituteVariables(tt.message, tt.input)
			if got != tt.expected {
				t.Errorf("SubstituteVariables(%q) = %q, want %q", tt.message, got, tt.expected)
			}
		})
	}
}

func TestSubstituteVariables_Children(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		input    *ProcessorInput
		expected string
	}{
		{
			name:     "no children",
			message:  "children: @mitto:children",
			input:    &ProcessorInput{},
			expected: "children: ",
		},
		{
			name:    "single child with name and server",
			message: "@mitto:children",
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "sess-1", Name: "Research", ACPServer: "claude-code"},
				},
			},
			expected: "sess-1 (Research) [claude-code]",
		},
		{
			name:    "child without name",
			message: "@mitto:children",
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "sess-1", ACPServer: "auggie"},
				},
			},
			expected: "sess-1 [auggie]",
		},
		{
			name:    "child without server",
			message: "@mitto:children",
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "sess-1", Name: "Test"},
				},
			},
			expected: "sess-1 (Test)",
		},
		{
			name:    "multiple children",
			message: "@mitto:children",
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "sess-1", Name: "Research", ACPServer: "claude-code"},
					{ID: "sess-2", Name: "Tests", ACPServer: "auggie"},
				},
			},
			expected: "sess-1 (Research) [claude-code], sess-2 (Tests) [auggie]",
		},
		{
			name:    "child with bare id only",
			message: "@mitto:children",
			input: &ProcessorInput{
				ChildSessions: []ChildSession{
					{ID: "sess-1"},
				},
			},
			expected: "sess-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubstituteVariables(tt.message, tt.input)
			if got != tt.expected {
				t.Errorf("SubstituteVariables(%q) = %q, want %q", tt.message, got, tt.expected)
			}
		})
	}
}

func TestFormatAvailableACPServers(t *testing.T) {
	tests := []struct {
		name     string
		servers  []AvailableACPServer
		expected string
	}{
		{
			name:     "nil slice",
			servers:  nil,
			expected: "",
		},
		{
			name:     "empty slice",
			servers:  []AvailableACPServer{},
			expected: "",
		},
		{
			name: "single server no tags not current",
			servers: []AvailableACPServer{
				{Name: "claude-code"},
			},
			expected: "claude-code",
		},
		{
			name: "single server with tags current",
			servers: []AvailableACPServer{
				{Name: "auggie", Tags: []string{"coding", "ai-assistant"}, Current: true},
			},
			expected: "auggie [coding, ai-assistant] (current)",
		},
		{
			name: "two servers",
			servers: []AvailableACPServer{
				{Name: "auggie", Tags: []string{"coding"}, Current: false},
				{Name: "claude-code", Tags: []string{"coding", "fast"}, Current: true},
			},
			expected: "auggie [coding], claude-code [coding, fast] (current)",
		},
		{
			name: "server with type is formatted by name only",
			servers: []AvailableACPServer{
				{Name: "claude-fast", Type: "claude-code", Tags: []string{"fast"}, Current: true},
			},
			expected: "claude-fast [fast] (current)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAvailableACPServers(tt.servers)
			if got != tt.expected {
				t.Errorf("formatAvailableACPServers() = %q, want %q", got, tt.expected)
			}
		})
	}
}
