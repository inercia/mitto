package cmd

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/prompts"
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

// -----------------------------------------------------------------------------
// End-to-end verify behavior (mitto-11m)
//
// These tests exercise runPromptsVerify against an isolated MITTO_DIR to pin
// the exact acceptance criterion the CI wiring is meant to enforce:
//
//   "Introducing a broken fragment reference or an unknown modelTag: in a
//    builtin prompt causes CI to fail."
//
// The unknown-modelTag half is covered by
// TestBuiltinPrompts_ModelTagsAreCanonical (internal/config) and
// TestBuiltinProcessors_HasModelTagArgsAreCanonical (internal/processors);
// the broken-fragment-reference and broken-YAML halves are covered here.
// -----------------------------------------------------------------------------

// verifyTestSetup isolates runPromptsVerify from developer-machine state:
//   - MITTO_DIR is redirected to a fresh tempdir.
//   - The appdir cache is reset before + after so the redirect takes effect.
//   - The verifyExtraDir global is saved and cleared.
//   - The current fragment registry is saved and restored so successive tests
//     don't see each other's fragments.
//   - Stdout is drained through a pipe so runPromptsVerify's diagnostic tables
//     don't spam `go test` output.
//
// Returns the MITTO_DIR path; the caller writes the fixture prompt tree into
// filepath.Join(dir, "prompts").
func verifyTestSetup(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("MITTO_DIR", tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	oldExtraDir := verifyExtraDir
	verifyExtraDir = ""
	t.Cleanup(func() { verifyExtraDir = oldExtraDir })

	prevFrags := prompts.CurrentFragments()
	t.Cleanup(func() { prompts.SetCurrentFragments(prevFrags) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = w.Close()
		<-done
		_ = r.Close()
	})

	return tmpDir
}

// writePromptFile writes a *.prompt.yaml file under MITTO_DIR/prompts/<name>.
func writePromptFile(t *testing.T, mittoDir, name, body string) {
	t.Helper()
	promptsSubDir := filepath.Join(mittoDir, "prompts")
	if err := os.MkdirAll(promptsSubDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	path := filepath.Join(promptsSubDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPromptsVerify_ValidTree_Succeeds pins the positive baseline: a prompt
// tree with no errors returns nil (VERIFY OK path).
func TestPromptsVerify_ValidTree_Succeeds(t *testing.T) {
	mittoDir := verifyTestSetup(t)
	writePromptFile(t, mittoDir, "ok.prompt.yaml", `name: "OK"
prompt: |
  hello world
`)

	if err := runPromptsVerify(nil, nil); err != nil {
		t.Fatalf("runPromptsVerify on a clean tree returned error: %v", err)
	}
}

// TestPromptsVerify_BrokenFragmentRef_ReturnsError is the direct pin of the
// mitto-11m acceptance criterion — this is the exact class of failure the
// CI gate is meant to catch. A prompt whose body references a fragment name
// that no *.tmpl in the loaded scan roots defines must fail verification.
//
// This test is what makes the CI wiring load-bearing: if a future refactor
// silently downgrades this failure to a warning, the wiring in
// Makefile:check-prompts + tests.yml lint job would no longer block merges,
// but this test would still fail loudly locally.
func TestPromptsVerify_BrokenFragmentRef_ReturnsError(t *testing.T) {
	mittoDir := verifyTestSetup(t)
	writePromptFile(t, mittoDir, "broken.prompt.yaml", `name: "BrokenFragmentRef"
prompt: |
  intro {{ template "mitto-11m/does-not-exist" . }} outro
`)

	err := runPromptsVerify(nil, nil)
	if err == nil {
		t.Fatal("runPromptsVerify accepted a broken fragment reference; expected non-nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "verification failed") {
		t.Errorf("error %q should contain 'verification failed'", msg)
	}
}

// TestPromptsVerify_BrokenYAML_ReturnsError pins the second failure class the
// CI gate must catch: a prompt file that does not parse as valid YAML at all.
// Regressions in ParsePromptFile that silently accept a malformed file must
// surface here.
func TestPromptsVerify_BrokenYAML_ReturnsError(t *testing.T) {
	mittoDir := verifyTestSetup(t)
	writePromptFile(t, mittoDir, "malformed.prompt.yaml", `name: "Malformed"
prompt: |
  this file has a stray colon
weird: [unbalanced
`)

	err := runPromptsVerify(nil, nil)
	if err == nil {
		t.Fatal("runPromptsVerify accepted a malformed prompt YAML; expected non-nil error")
	}
	if !strings.Contains(err.Error(), "verification failed") {
		t.Errorf("error %q should contain 'verification failed'", err.Error())
	}
}

// -----------------------------------------------------------------------------
// CI wiring assertions (mitto-11m)
//
// These tests are the load-bearing enforcement for acceptance criteria #1, #2:
//   - make check-model-tags runs on every PR
//   - mitto prompts verify runs on every PR
//
// They read the actual Makefile and workflow file to prove the umbrella target
// exists, chains both validators, and is invoked from the CI lint job. A
// future PR that accidentally deletes the wiring will trip these tests
// locally before it can reach CI.
// -----------------------------------------------------------------------------

// repoRootFile reads a file relative to the repository root
// (internal/cmd/../../<path>) and returns its contents. Fatal on error so
// callers can assume a non-empty return.
func repoRootFile(t *testing.T, relPath string) string {
	t.Helper()
	path := filepath.Join("..", "..", relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestCheckPromptsTarget_WiredInMakefile asserts the Makefile umbrella target
// exists and chains both validators the bead calls for.
func TestCheckPromptsTarget_WiredInMakefile(t *testing.T) {
	mk := repoRootFile(t, "Makefile")

	// The umbrella target line itself. It MUST depend on check-model-tags
	// (validator #1) so the two validators are wired as one CI step, and it
	// MUST also invoke `mitto prompts verify` (validator #2) in its recipe.
	if !strings.Contains(mk, "\ncheck-prompts:") && !strings.HasPrefix(mk, "check-prompts:") {
		t.Fatal("Makefile is missing the `check-prompts:` target (mitto-11m acceptance criterion #1+#2)")
	}
	// The target line must list check-model-tags as a prerequisite so the
	// Go-test-only validator half is executed by the same umbrella.
	targetLine := ""
	for _, line := range strings.Split(mk, "\n") {
		if strings.HasPrefix(line, "check-prompts:") {
			targetLine = line
			break
		}
	}
	if targetLine == "" {
		t.Fatal("could not locate `check-prompts:` target line")
	}
	if !strings.Contains(targetLine, "check-model-tags") {
		t.Errorf("check-prompts target line %q should depend on check-model-tags", targetLine)
	}

	// The recipe must run the compiled `mitto prompts verify` — that's
	// validator #2. We don't pin the exact invocation form (./mitto or
	// $(BINARY_NAME)) so a future rename doesn't false-positive this test.
	if !strings.Contains(mk, "prompts verify") {
		t.Error("Makefile should invoke `mitto prompts verify` (found no `prompts verify` substring)")
	}

	// .PHONY declaration keeps the target from being confused with a
	// same-named file. Not strictly required for correctness, but it is the
	// project convention (every other check-* target is .PHONY).
	if !strings.Contains(mk, "check-prompts") || !strings.Contains(mk, ".PHONY") {
		t.Error("Makefile should declare check-prompts as .PHONY")
	}
}

// TestCheckPromptsTarget_WiredInCI asserts the GitHub Actions lint job
// actually invokes `make check-prompts`. Without this, the Makefile target
// could exist but never run on any PR — silently defeating the whole bead.
func TestCheckPromptsTarget_WiredInCI(t *testing.T) {
	yml := repoRootFile(t, ".github/workflows/tests.yml")

	if !strings.Contains(yml, "make check-prompts") {
		t.Fatal(".github/workflows/tests.yml is missing a `make check-prompts` step " +
			"(mitto-11m acceptance criteria #1 + #2: validators must run on every PR)")
	}

	// The lint job is where it belongs — fails PR early, no Node toolchain
	// needed. If a future edit moves it into a job that only runs on some
	// branches, the bead's intent is silently broken.
	if !strings.Contains(yml, "lint:") {
		t.Fatal(".github/workflows/tests.yml should declare a `lint:` job")
	}
	lintIdx := strings.Index(yml, "lint:")
	// Scan forward from lint: to the next top-level job (line beginning with
	// two spaces + name + colon at the same indent as `lint:`). Anything the
	// step touches must be inside that window.
	rest := yml[lintIdx:]
	// Find the next job header — a line that starts with "  " + name + ":"
	// following blank line. Practically, tests.yml separates jobs with a
	// blank line; scanning until the next "\n  " + non-whitespace + eventual
	// ":" is close enough for this pin.
	stepIdx := strings.Index(rest, "make check-prompts")
	if stepIdx < 0 {
		t.Fatal("`make check-prompts` invocation not found after `lint:` job header")
	}
	// Sanity: no top-level job header appears between lint: and the step,
	// which would mean the step is actually part of a later job. Top-level
	// job names in this workflow are two-space indented (unit-tests:,
	// integration:, etc.), so we look for "\n  " + letter + ... + ":\n"
	// between lintIdx and lintIdx+stepIdx.
	window := rest[:stepIdx]
	// Skip past the `lint:` header itself before scanning.
	scan := window
	if nl := strings.Index(scan, "\n"); nl >= 0 {
		scan = scan[nl+1:]
	}
	for _, line := range strings.Split(scan, "\n") {
		// Match "  name:" (two spaces, letter, ends in colon, no leading
		// hyphen so it's not a step).
		if len(line) > 2 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' && line[2] != '-' && line[2] != '#' && strings.HasSuffix(strings.TrimRight(line, " "), ":") {
			t.Fatalf("`make check-prompts` appears after a different top-level job header (%q), not inside the lint job", line)
		}
	}
}
