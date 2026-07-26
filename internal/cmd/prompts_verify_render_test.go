package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestPromptsVerifyRender_Registered verifies both subcommands are wired onto
// the plural "prompts" command with the expected shapes.
func TestPromptsVerifyRender_Registered(t *testing.T) {
	var verify, render *cobra.Command
	for _, c := range promptsCmd.Commands() {
		switch c.Name() {
		case "verify":
			verify = c
		case "render":
			render = c
		}
	}
	if verify == nil {
		t.Fatal("'verify' subcommand not registered on promptsCmd")
	}
	if render == nil {
		t.Fatal("'render' subcommand not registered on promptsCmd")
	}

	// verify: accepts zero args (no explicit Args validator — cobra default
	// is ArbitraryArgs, so we just confirm the command is runnable).
	if verify.RunE == nil {
		t.Error("verify: RunE not set")
	}

	// render: exactly one positional arg
	if err := render.Args(render, []string{"foo"}); err != nil {
		t.Errorf("render rejected single arg: %v", err)
	}
	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := render.Args(render, args); err == nil {
			t.Errorf("render accepted %d args, want error", len(args))
		}
	}
}

// TestPromptsVerifyRender_Flags checks that the documented flags exist with
// their documented defaults so the CLI contract does not silently drift.
func TestPromptsVerifyRender_Flags(t *testing.T) {
	verifyFlags := map[string]string{
		"dir":     "",
		"verbose": "false",
	}
	for name, want := range verifyFlags {
		f := promptsVerifyCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("verify: flag --%s missing", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("verify: --%s default = %q, want %q", name, f.DefValue, want)
		}
	}

	renderFlags := map[string]string{
		"arg":           "[]",
		"acp":           "auggie",
		"workspace-dir": "",
		"output":        "",
		"dir":           "",
	}
	for name, want := range renderFlags {
		f := promptsRenderCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("render: flag --%s missing", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("render: --%s default = %q, want %q", name, f.DefValue, want)
		}
	}
}

// TestParseKVArgs covers the --arg parser edge cases.
func TestParseKVArgs(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr string
	}{
		{
			name: "nil input yields nil map",
			in:   nil,
			want: nil,
		},
		{
			name: "single kv",
			in:   []string{"K=V"},
			want: map[string]string{"K": "V"},
		},
		{
			name: "value may contain =",
			in:   []string{"K=a=b=c"},
			want: map[string]string{"K": "a=b=c"},
		},
		{
			name: "empty value is allowed",
			in:   []string{"K="},
			want: map[string]string{"K": ""},
		},
		{
			name: "multiple pairs, later wins on duplicate key",
			in:   []string{"K=1", "K=2", "Other=x"},
			want: map[string]string{"K": "2", "Other": "x"},
		},
		{
			name:    "missing separator rejected",
			in:      []string{"KEY"},
			wantErr: "expected KEY=VALUE",
		},
		{
			name:    "empty key rejected",
			in:      []string{"=V"},
			wantErr: "expected KEY=VALUE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKVArgs(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPromptTreeDirs_Split confirms the split-by-concern layout: prompts
// scan only the user dir (recursive walk covers builtin/) while fragments
// scan builtin/ + user root as separate roots so short names resolve.
func TestPromptTreeDirs_Split(t *testing.T) {
	promptDirs, fragmentDirs, err := promptTreeDirs("")
	if err != nil {
		t.Fatalf("promptTreeDirs: %v", err)
	}
	if len(promptDirs) != 1 {
		t.Errorf("promptDirs = %v, want exactly 1 entry (avoid double-loading builtin)", promptDirs)
	}
	if len(fragmentDirs) != 2 {
		t.Errorf("fragmentDirs = %v, want exactly 2 entries (builtin root + user root)", fragmentDirs)
	}
	// With an --dir override, each list gets the extra dir appended.
	pd2, fd2, err := promptTreeDirs("/tmp/extra")
	if err != nil {
		t.Fatalf("promptTreeDirs with extra: %v", err)
	}
	if len(pd2) != len(promptDirs)+1 || pd2[len(pd2)-1] != "/tmp/extra" {
		t.Errorf("promptDirs with extra = %v, want extra appended last", pd2)
	}
	if len(fd2) != len(fragmentDirs)+1 || fd2[len(fd2)-1] != "/tmp/extra" {
		t.Errorf("fragmentDirs with extra = %v, want extra appended last", fd2)
	}
}
