package acp

import (
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		wantArgs    []string
		wantErr     bool
		errContains string
	}{
		{
			name:     "simple command",
			command:  "echo hello",
			wantArgs: []string{"echo", "hello"},
		},
		{
			name:     "command with single quotes",
			command:  "sh -c 'cd /some/dir && my-agent --acp'",
			wantArgs: []string{"sh", "-c", "cd /some/dir && my-agent --acp"},
		},
		{
			name:     "command with double quotes",
			command:  `sh -c "cd /some/dir && my-agent --acp"`,
			wantArgs: []string{"sh", "-c", "cd /some/dir && my-agent --acp"},
		},
		{
			name:     "command with quoted argument containing spaces",
			command:  `auggie --profile "my profile"`,
			wantArgs: []string{"auggie", "--profile", "my profile"},
		},
		{
			name:     "single word command",
			command:  "auggie",
			wantArgs: []string{"auggie"},
		},
		{
			name:     "command with path",
			command:  "/usr/local/bin/my-agent --acp",
			wantArgs: []string{"/usr/local/bin/my-agent", "--acp"},
		},
		{
			name:     "npx command",
			command:  "npx -y @zed-industries/claude-code-acp@latest",
			wantArgs: []string{"npx", "-y", "@zed-industries/claude-code-acp@latest"},
		},
		{
			name:        "empty command",
			command:     "",
			wantErr:     true,
			errContains: "empty command",
		},
		{
			name:        "whitespace only",
			command:     "   ",
			wantErr:     true,
			errContains: "empty command",
		},
		{
			name:        "unclosed single quote",
			command:     "sh -c 'unclosed",
			wantErr:     true,
			errContains: "failed to parse command",
		},
		{
			name:        "unclosed double quote",
			command:     `sh -c "unclosed`,
			wantErr:     true,
			errContains: "failed to parse command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := ParseCommand(tt.command)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseCommand(%q) expected error, got nil", tt.command)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ParseCommand(%q) error = %v, want error containing %q", tt.command, err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseCommand(%q) unexpected error: %v", tt.command, err)
				return
			}

			if len(args) != len(tt.wantArgs) {
				t.Errorf("ParseCommand(%q) got %d args, want %d", tt.command, len(args), len(tt.wantArgs))
				return
			}

			for i, arg := range args {
				if arg != tt.wantArgs[i] {
					t.Errorf("ParseCommand(%q) arg[%d] = %q, want %q", tt.command, i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}
