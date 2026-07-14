package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/mcpserver"
)

// TestPromptCmd_Registered verifies the singular "prompt" command is wired onto
// the root command, is distinct from the plural "prompts" command, and requires
// exactly one positional argument (the prompt text).
func TestPromptCmd_Registered(t *testing.T) {
	var prompt, prompts *cobra.Command
	for _, c := range rootCmd.Commands() {
		switch c.Name() {
		case "prompt":
			prompt = c
		case "prompts":
			prompts = c
		}
	}
	if prompt == nil {
		t.Fatal("'prompt' command not registered on rootCmd")
	}
	if prompts == nil {
		t.Fatal("'prompts' command missing; expected the plural command to coexist")
	}
	if prompt == prompts {
		t.Fatal("'prompt' and 'prompts' resolved to the same command")
	}
	if !strings.HasPrefix(prompt.Use, "prompt") {
		t.Errorf("Use = %q, want it to start with 'prompt'", prompt.Use)
	}
	if err := prompt.Args(prompt, []string{"only-one"}); err != nil {
		t.Errorf("ExactArgs(1) rejected a single arg: %v", err)
	}
	for _, args := range [][]string{{}, {"a", "b"}} {
		if err := prompt.Args(prompt, args); err == nil {
			t.Errorf("expected error for %d args, got nil", len(args))
		}
	}
}

// TestPromptCmd_Flags verifies the debugging flags exist with their documented
// defaults.
func TestPromptCmd_Flags(t *testing.T) {
	cases := []struct{ name, def string }{
		{"with-mcp-server", "false"},
		{"mcp-port", "5757"},
		{"timeout", "0s"},
		{"concurrent", "0"},
		{"with-aux", "false"},
		{"aux-fork", "false"},
		{"wait-aux", "false"},
	}
	if mcpserver.DefaultPort != 5757 {
		t.Fatalf("mcpserver.DefaultPort = %d, test assumed 5757", mcpserver.DefaultPort)
	}
	for _, c := range cases {
		f := promptCmd.Flags().Lookup(c.name)
		if f == nil {
			t.Errorf("flag --%s not registered", c.name)
			continue
		}
		if f.DefValue != c.def {
			t.Errorf("flag --%s default = %q, want %q", c.name, f.DefValue, c.def)
		}
	}
}

// TestStripServerPrefix covers server-prefixed values, Windows drive letters,
// and plain paths.
func TestStripServerPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"auggie:/tmp/x", "/tmp/x"},
		{"Auggie (Opus):/a/b", "/a/b"},
		{`C:\work\repo`, `C:\work\repo`},
		{"/plain/path", "/plain/path"},
		{"relative", "relative"},
	}
	for _, c := range cases {
		if got := stripServerPrefix(c.in); got != c.want {
			t.Errorf("stripServerPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIndexColon covers the drive-letter skip and the first non-drive colon.
func TestIndexColon(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"noColon", -1},
		{"C:\\path", -1},
		{"a:b", -1}, // colon at index 1 is treated as a drive-letter separator
		{"srv:/p", 3},
	}
	for _, c := range cases {
		if got := indexColon(c.in); got != c.want {
			t.Errorf("indexColon(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestResolvePromptWorkDir covers a server-prefixed --dir, a plain absolute
// --dir, and the default (current working directory).
func TestResolvePromptWorkDir(t *testing.T) {
	saved := dirFlags
	t.Cleanup(func() { dirFlags = saved })

	abs := t.TempDir()

	dirFlags = []string{"auggie:" + abs}
	if got, err := resolvePromptWorkDir(); err != nil || got != abs {
		t.Errorf("server-prefixed --dir: got %q err %v, want %q", got, err, abs)
	}

	dirFlags = []string{abs}
	if got, err := resolvePromptWorkDir(); err != nil || got != abs {
		t.Errorf("plain --dir: got %q err %v, want %q", got, err, abs)
	}

	dirFlags = nil
	got, err := resolvePromptWorkDir()
	if err != nil {
		t.Fatalf("default work dir: unexpected err %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("default work dir %q is not absolute", got)
	}
}

// TestAuxPromptFor verifies each auxiliary purpose maps to its representative
// prompt, that mcp-check embeds the configured port, and that an unknown purpose
// falls back to a trivial prompt.
func TestAuxPromptFor(t *testing.T) {
	savedPort := promptMCPPort
	t.Cleanup(func() { promptMCPPort = savedPort })
	promptMCPPort = 5757

	if got := auxPromptFor(auxiliary.PurposeTitleGen); !strings.Contains(got, "simulated cold-start prompt") {
		t.Errorf("title-gen prompt missing injected text: %q", got)
	}
	if got := auxPromptFor(auxiliary.PurposeFollowUp); !strings.Contains(got, "simulated agent response") {
		t.Errorf("follow-up prompt missing injected text: %q", got)
	}
	if got := auxPromptFor(auxiliary.PurposeMCPCheck); !strings.Contains(got, "http://127.0.0.1:5757/mcp") {
		t.Errorf("mcp-check prompt missing endpoint URL: %q", got)
	}
	if got := auxPromptFor(auxiliary.PurposeMCPTools); got != auxiliary.FetchMCPToolsPromptTemplate {
		t.Errorf("mcp-tools prompt = %q, want the FetchMCPTools template", got)
	}
	if got := auxPromptFor("unknown-purpose"); got != "reply with: ok" {
		t.Errorf("default prompt = %q, want %q", got, "reply with: ok")
	}
}

// TestPromptLogger always returns a usable logger regardless of --debug.
func TestPromptLogger(t *testing.T) {
	saved := debug
	t.Cleanup(func() { debug = saved })
	for _, d := range []bool{false, true} {
		debug = d
		if promptLogger() == nil {
			t.Errorf("promptLogger() returned nil for debug=%v", d)
		}
	}
}
