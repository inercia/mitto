package auxiliary

import (
	"reflect"
	"testing"
)

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

