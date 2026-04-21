package auxiliary

import (
	"reflect"
	"testing"
)

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences unchanged",
			input: `{"tools": []}`,
			want:  `{"tools": []}`,
		},
		{
			name:  "json fenced block",
			input: "```json\n{\"tools\": []}\n```",
			want:  `{"tools": []}`,
		},
		{
			name:  "plain fenced block",
			input: "```\n{\"tools\": []}\n```",
			want:  `{"tools": []}`,
		},
		{
			name:  "fence with trailing commentary containing braces",
			input: "```json\n{\"tools\": [{\"name\": \"t\"}]}\n```\n\nNote: {1 tool listed}",
			want:  `{"tools": [{"name": "t"}]}`,
		},
		{
			name:  "fence with leading whitespace after trimspace",
			input: "```json\n  {\"key\": \"val\"}\n```",
			want:  `{"key": "val"}`,
		},
		{
			name:  "unclosed fence unchanged",
			input: "```json\n{\"tools\": []}",
			want:  "```json\n{\"tools\": []}",
		},
		{
			name:  "fence with no newline unchanged",
			input: "```json",
			want:  "```json",
		},
		{
			name:  "empty content between fences",
			input: "```\n\n```",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownFences(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMCPToolsList(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTools  int
		wantErrMsg string
		wantErr    bool
	}{
		{
			name:      "raw JSON object with tools",
			input:     `{"tools": [{"name": "tool1", "description": "desc1"}]}`,
			wantTools: 1,
		},
		{
			name:      "markdown fenced JSON object",
			input:     "```json\n{\"tools\": [{\"name\": \"tool1\", \"description\": \"desc1\"}]}\n```",
			wantTools: 1,
		},
		{
			name:      "markdown fenced with trailing commentary and braces",
			input:     "```json\n{\"tools\": [{\"name\": \"t\", \"description\": \"d\"}]}\n```\n\nNote: {1 tool listed}",
			wantTools: 1,
		},
		{
			name:      "bare JSON array fallback",
			input:     `[{"name": "tool1", "description": "desc1"}]`,
			wantTools: 1,
		},
		{
			name:      "fenced bare JSON array",
			input:     "```\n[{\"name\": \"tool1\", \"description\": \"desc1\"}]\n```",
			wantTools: 1,
		},
		{
			name:       "agent error response",
			input:      `{"error": "no tools available"}`,
			wantErrMsg: "no tools available",
		},
		{
			name:    "invalid JSON fails",
			input:   `not json at all`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, agentErr, err := parseMCPToolsList(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErrMsg != "" && agentErr != tt.wantErrMsg {
				t.Errorf("agentErr = %q, want %q", agentErr, tt.wantErrMsg)
			}
			if len(tools) != tt.wantTools {
				t.Errorf("len(tools) = %d, want %d", len(tools), tt.wantTools)
			}
		})
	}
}

func TestParseEnabledWhenMCPCheck_ValidJSON(t *testing.T) {
	input := `{"patterns": {"jira_*": true, "slack_*": false}}`
	got, err := parseEnabledWhenMCPCheck(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{"jira_*": true, "slack_*": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseEnabledWhenMCPCheck_JSONWithSurroundingText(t *testing.T) {
	input := `Here is the result: {"patterns": {"jira_*": true}} - done`
	got, err := parseEnabledWhenMCPCheck(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{"jira_*": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseEnabledWhenMCPCheck_InvalidJSON(t *testing.T) {
	input := `not valid json at all`
	_, err := parseEnabledWhenMCPCheck(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseEnabledWhenMCPCheck_EmptyPatterns(t *testing.T) {
	input := `{"patterns": {}}`
	got, err := parseEnabledWhenMCPCheck(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestStripPromptPreamble(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no preamble unchanged",
			input: "Fix the login bug in auth.go",
			want:  "Fix the login bug in auth.go",
		},
		{
			name:  "here is the improved prompt with double newline",
			input: "Here is the improved prompt:\n\nFix the login bug in auth.go",
			want:  "Fix the login bug in auth.go",
		},
		{
			name:  "here is the improved prompt with single newline",
			input: "Here is the improved prompt:\nFix the login bug in auth.go",
			want:  "Fix the login bug in auth.go",
		},
		{
			name:  "here is the improved prompt colon only",
			input: "Here is the improved prompt:Fix the login bug",
			want:  "Fix the login bug",
		},
		{
			name:  "here's the improved prompt",
			input: "Here's the improved prompt:\n\nWrite a comprehensive unit test",
			want:  "Write a comprehensive unit test",
		},
		{
			name:  "sure here is the improved prompt",
			input: "Sure, here is the improved prompt:\n\nAdd error handling",
			want:  "Add error handling",
		},
		{
			name:  "sure exclamation here's the improved prompt",
			input: "Sure! Here's the improved prompt:\n\nRefactor the database layer",
			want:  "Refactor the database layer",
		},
		{
			name:  "improved prompt prefix",
			input: "Improved prompt:\n\nCreate a REST API endpoint",
			want:  "Create a REST API endpoint",
		},
		{
			name:  "case insensitive matching",
			input: "HERE IS THE IMPROVED PROMPT:\n\nFix the bug",
			want:  "Fix the bug",
		},
		{
			name:  "leading and trailing whitespace trimmed",
			input: "  Fix the login bug in auth.go  ",
			want:  "Fix the login bug in auth.go",
		},
		{
			name:  "preamble with surrounding whitespace",
			input: "  Here is the improved prompt:\n\nFix the bug  ",
			want:  "Fix the bug",
		},
		{
			name:  "empty string unchanged",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripPromptPreamble(tt.input)
			if got != tt.want {
				t.Errorf("stripPromptPreamble(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTrimQuotes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"double quotes", `"Hello World"`, "Hello World"},
		{"single quotes", `'Hello World'`, "Hello World"},
		{"no quotes", "Hello World", "Hello World"},
		{"empty string", "", ""},
		{"single char", "a", "a"},
		{"only quotes", `""`, ""},
		{"mismatched quotes start double", `"Hello'`, `"Hello'`},
		{"mismatched quotes start single", `'Hello"`, `'Hello"`},
		{"nested quotes", `"'Hello'"`, "'Hello'"},
		{"quotes in middle", `Hello "World"`, `Hello "World"`},
		{"single quote only", `'`, `'`},
		{"double quote only", `"`, `"`},
		{"with whitespace", `  "Hello"  `, "Hello"},
		{"with newlines", "\n\"Hello\"\n", "Hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimQuotes(tt.input)
			if got != tt.want {
				t.Errorf("trimQuotes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateForLog(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"long string truncated", "hello world", 8, "hello..."},
		{"very short maxLen", "hello", 3, "hel"},
		{"maxLen of 4", "hello world", 4, "h..."},
		{"empty string", "", 10, ""},
		{"realistic log preview", "This is a long agent response that needs to be truncated for logging", 30, "This is a long agent respon..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForLog(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateForLog(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestParseFollowUpSuggestions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []FollowUpSuggestion
	}{
		{
			name:  "valid JSON array",
			input: `[{"label": "Yes, run tests", "value": "Yes, please run the tests"}]`,
			want: []FollowUpSuggestion{
				{Label: "Yes, run tests", Value: "Yes, please run the tests"},
			},
		},
		{
			name:  "empty array",
			input: `[]`,
			want:  nil, // Empty array returns nil slice
		},
		{
			name:  "JSON with extra text before",
			input: `Here are the suggestions: [{"label": "Continue", "value": "Yes, continue"}]`,
			want: []FollowUpSuggestion{
				{Label: "Continue", Value: "Yes, continue"},
			},
		},
		{
			name:  "JSON with extra text after",
			input: `[{"label": "Proceed", "value": "Yes, proceed"}] - these are the options`,
			want: []FollowUpSuggestion{
				{Label: "Proceed", Value: "Yes, proceed"},
			},
		},
		{
			name:  "invalid JSON",
			input: `not valid json`,
			want:  []FollowUpSuggestion{},
		},
		{
			name:  "empty string",
			input: ``,
			want:  []FollowUpSuggestion{},
		},
		{
			name:  "multiple suggestions",
			input: `[{"label": "Yes", "value": "Yes, do it"}, {"label": "No", "value": "No, skip it"}]`,
			want: []FollowUpSuggestion{
				{Label: "Yes", Value: "Yes, do it"},
				{Label: "No", Value: "No, skip it"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFollowUpSuggestions(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseFollowUpSuggestions(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateSuggestions(t *testing.T) {
	tests := []struct {
		name  string
		input []FollowUpSuggestion
		want  []FollowUpSuggestion
	}{
		{
			name: "valid suggestions",
			input: []FollowUpSuggestion{
				{Label: "Yes", Value: "Yes, do it"},
				{Label: "No", Value: "No, skip it"},
			},
			want: []FollowUpSuggestion{
				{Label: "Yes", Value: "Yes, do it"},
				{Label: "No", Value: "No, skip it"},
			},
		},
		{
			name: "empty label filtered out",
			input: []FollowUpSuggestion{
				{Label: "", Value: "Some value"},
				{Label: "Valid", Value: "Valid value"},
			},
			want: []FollowUpSuggestion{
				{Label: "Valid", Value: "Valid value"},
			},
		},
		{
			name: "empty value filtered out",
			input: []FollowUpSuggestion{
				{Label: "Some label", Value: ""},
				{Label: "Valid", Value: "Valid value"},
			},
			want: []FollowUpSuggestion{
				{Label: "Valid", Value: "Valid value"},
			},
		},
		{
			name: "whitespace trimmed",
			input: []FollowUpSuggestion{
				{Label: "  Yes  ", Value: "  Yes, do it  "},
			},
			want: []FollowUpSuggestion{
				{Label: "Yes", Value: "Yes, do it"},
			},
		},
		{
			name: "limit to 5 suggestions",
			input: []FollowUpSuggestion{
				{Label: "1", Value: "One"},
				{Label: "2", Value: "Two"},
				{Label: "3", Value: "Three"},
				{Label: "4", Value: "Four"},
				{Label: "5", Value: "Five"},
				{Label: "6", Value: "Six"},
				{Label: "7", Value: "Seven"},
			},
			want: []FollowUpSuggestion{
				{Label: "1", Value: "One"},
				{Label: "2", Value: "Two"},
				{Label: "3", Value: "Three"},
				{Label: "4", Value: "Four"},
				{Label: "5", Value: "Five"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateSuggestions(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("validateSuggestions() = %v, want %v", got, tt.want)
			}
		})
	}
}
