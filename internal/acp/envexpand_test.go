package acp

import (
	"testing"
)

func TestBuildMittoEnv(t *testing.T) {
	t.Run("all keys present with provided values", func(t *testing.T) {
		env := BuildMittoEnv("sess-123", "/home/user/project", "auggie", "ws-uuid-456")

		expected := map[string]string{
			"MITTO_SESSION_ID":     "sess-123",
			"MITTO_WORKING_DIR":    "/home/user/project",
			"MITTO_ACP_SERVER":     "auggie",
			"MITTO_WORKSPACE_UUID": "ws-uuid-456",
		}

		for key, want := range expected {
			if got := env[key]; got != want {
				t.Errorf("env[%q] = %q, want %q", key, got, want)
			}
		}
	})

	t.Run("empty string params produce keys with empty values", func(t *testing.T) {
		env := BuildMittoEnv("", "", "", "")

		for _, key := range []string{"MITTO_SESSION_ID", "MITTO_WORKING_DIR", "MITTO_ACP_SERVER", "MITTO_WORKSPACE_UUID"} {
			if _, ok := env[key]; !ok {
				t.Errorf("key %q missing from env", key)
			}
			if got := env[key]; got != "" {
				t.Errorf("env[%q] = %q, want empty string", key, got)
			}
		}
	})

	t.Run("MITTO_DATA_DIR and MITTO_LOGS_DIR are populated", func(t *testing.T) {
		env := BuildMittoEnv("s", "/w", "a", "u")

		if env["MITTO_DATA_DIR"] == "" {
			t.Error("MITTO_DATA_DIR is empty, expected non-empty path from appdir.Dir()")
		}
		if env["MITTO_LOGS_DIR"] == "" {
			t.Error("MITTO_LOGS_DIR is empty, expected non-empty path from appdir.LogsDir()")
		}
	})
}

func TestExpandCommand(t *testing.T) {
	env := BuildMittoEnv("sess-abc", "/workspace/myproject", "auggie-server", "uuid-789")

	tests := []struct {
		name    string
		command string
		env     map[string]string
		want    string
	}{
		{
			name:    "simple expansion",
			command: "auggie --root $MITTO_WORKING_DIR",
			env:     env,
			want:    "auggie --root /workspace/myproject",
		},
		{
			name:    "braced expansion",
			command: "auggie --root ${MITTO_WORKING_DIR}",
			env:     env,
			want:    "auggie --root /workspace/myproject",
		},
		{
			name:    "multiple vars",
			command: "$MITTO_SESSION_ID $MITTO_ACP_SERVER $MITTO_WORKING_DIR",
			env:     env,
			want:    "sess-abc auggie-server /workspace/myproject",
		},
		{
			name:    "non-MITTO vars left untouched",
			command: "auggie --root $HOME/project",
			env:     env,
			want:    "auggie --root $HOME/project",
		},
		{
			name:    "undefined MITTO_ var becomes empty string",
			command: "cmd $MITTO_UNDEFINED_VAR end",
			env:     env,
			want:    "cmd  end",
		},
		{
			name:    "no vars unchanged",
			command: "auggie --no-vars",
			env:     env,
			want:    "auggie --no-vars",
		},
		{
			name:    "path with spaces in quoted argument",
			command: `auggie --root "$MITTO_WORKING_DIR" --acp`,
			env:     env,
			want:    `auggie --root "/workspace/myproject" --acp`,
		},
		{
			name:    "real-world example",
			command: "auggie --workspace-root $MITTO_WORKING_DIR --acp",
			env:     env,
			want:    "auggie --workspace-root /workspace/myproject --acp",
		},
		{
			name:    "empty mittoEnv map - MITTO vars become empty, non-MITTO untouched",
			command: "cmd $MITTO_SESSION_ID $HOME",
			env:     map[string]string{},
			want:    "cmd  $HOME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandCommand(tt.command, tt.env)
			if got != tt.want {
				t.Errorf("ExpandCommand(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestExpandArgs(t *testing.T) {
	env := BuildMittoEnv("sess-abc", "/workspace/myproject", "auggie-server", "uuid-789")

	tests := []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{
			name: "basic expansion",
			args: []string{"auggie", "--root", "$MITTO_WORKING_DIR"},
			env:  env,
			want: []string{"auggie", "--root", "/workspace/myproject"},
		},
		{
			name: "path with spaces preserved as single arg",
			args: []string{"auggie", "--root", "$MITTO_WORKING_DIR", "--acp"},
			env: map[string]string{
				"MITTO_WORKING_DIR": "/path/with spaces/here",
			},
			want: []string{"auggie", "--root", "/path/with spaces/here", "--acp"},
		},
		{
			name: "concatenated path in single arg",
			args: []string{"--config=${MITTO_WORKING_DIR}/config.yaml"},
			env:  env,
			want: []string{"--config=/workspace/myproject/config.yaml"},
		},
		{
			name: "non-MITTO vars left untouched",
			args: []string{"$HOME/bin/cmd"},
			env:  env,
			want: []string{"$HOME/bin/cmd"},
		},
		{
			name: "multiple args with mixed vars",
			args: []string{"auggie", "--session", "$MITTO_SESSION_ID", "--root", "$MITTO_WORKING_DIR"},
			env:  env,
			want: []string{"auggie", "--session", "sess-abc", "--root", "/workspace/myproject"},
		},
		{
			name: "empty args slice",
			args: []string{},
			env:  env,
			want: []string{},
		},
		{
			name: "no vars unchanged",
			args: []string{"auggie", "--no-vars"},
			env:  env,
			want: []string{"auggie", "--no-vars"},
		},
		{
			name: "spaces in expansion do not split arg",
			args: []string{"auggie", "$MITTO_WORKING_DIR"},
			env: map[string]string{
				"MITTO_WORKING_DIR": "/Users/John Doe/My Drive/Finances & Taxes",
			},
			want: []string{"auggie", "/Users/John Doe/My Drive/Finances & Taxes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandArgs(tt.args, tt.env)
			if len(got) != len(tt.want) {
				t.Errorf("ExpandArgs() len = %d, want %d\n  got:  %v\n  want: %v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExpandArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseCommandThenExpandArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		env     map[string]string
		want    []string
	}{
		{
			name:    "path with spaces — the bug scenario",
			command: "auggie --workspace-root $MITTO_WORKING_DIR --allow-indexing --acp --model opus4.6",
			env: map[string]string{
				"MITTO_WORKING_DIR": "/Users/alvaro/Library/CloudStorage/GoogleDrive-user@gmail.com/My Drive/Personal/Finances & Taxes/Investments",
			},
			want: []string{
				"auggie", "--workspace-root",
				"/Users/alvaro/Library/CloudStorage/GoogleDrive-user@gmail.com/My Drive/Personal/Finances & Taxes/Investments",
				"--allow-indexing", "--acp", "--model", "opus4.6",
			},
		},
		{
			name:    "path with spaces and var concatenation",
			command: "cmd --config ${MITTO_WORKING_DIR}/config.yaml",
			env: map[string]string{
				"MITTO_WORKING_DIR": "/path/with spaces",
			},
			want: []string{"cmd", "--config", "/path/with spaces/config.yaml"},
		},
		{
			name:    "path with special chars (ampersand, parentheses)",
			command: "auggie --workspace-root $MITTO_WORKING_DIR --acp",
			env: map[string]string{
				"MITTO_WORKING_DIR": "/Users/test/My Documents (2)/Work & Play",
			},
			want: []string{"auggie", "--workspace-root", "/Users/test/My Documents (2)/Work & Play", "--acp"},
		},
		{
			name:    "simple path (no spaces) — baseline",
			command: "auggie --workspace-root $MITTO_WORKING_DIR --acp",
			env: map[string]string{
				"MITTO_WORKING_DIR": "/home/user/project",
			},
			want: []string{"auggie", "--workspace-root", "/home/user/project", "--acp"},
		},
		{
			name:    "multiple MITTO vars, one with spaces",
			command: "cmd --session $MITTO_SESSION_ID --root $MITTO_WORKING_DIR",
			env: map[string]string{
				"MITTO_SESSION_ID":  "sess-123",
				"MITTO_WORKING_DIR": "/path/with spaces",
			},
			want: []string{"cmd", "--session", "sess-123", "--root", "/path/with spaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := ParseCommand(tt.command)
			if err != nil {
				t.Fatalf("ParseCommand(%q) error: %v", tt.command, err)
			}
			got := ExpandArgs(args, tt.env)
			if len(got) != len(tt.want) {
				t.Errorf("pipeline len = %d, want %d\n  got:  %v\n  want: %v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("pipeline[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
