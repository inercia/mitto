package prompts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionStateRecipePreservesCheckpoint(t *testing.T) {
	for _, tool := range []string{"bash", "perl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("recipe requires %s", tool)
		}
	}
	data, err := os.ReadFile("../../config/prompts/builtin/beads-issues/shared/execution-state-write.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(string(data), "```bash\n")
	if !ok {
		t.Fatal("missing executable recipe")
	}
	script, _, _ = strings.Cut(script, "```")
	lines := strings.Split(script, "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "  ")
	}
	script = strings.Join(lines, "\n")
	script = strings.ReplaceAll(script, "{{ . }}", "test-target")
	const start = "<!-- mitto:execution-state:v1 -->\n"
	const end = "\n<!-- /mitto:execution-state -->"
	const checkpoint = "resolve_blocked:\n  input: abc\n  child_id: child-1\n  task_id: task-1\n\n  recovery:\n    disposition: delivered\n    owner: worker-1\n    extra: preserve-me"
	for _, tc := range []struct {
		name, notes, changed string
		wantWrite, wantError bool
	}{
		{"nested", "Human prefix\n\n" + start + "phase: old\n" + checkpoint + "\nnext: old" + end + "\nHuman suffix\n\n", "", true, false},
		{"inline", start + "resolve_blocked: {input: abc, retry: used, unknown: keep}\nphase: old" + end, "", true, false},
		{"absent", "Human notes", "", true, false},
		{"empty", "", "", true, false},
		{"malformed", start + "phase: old", "", false, true},
		{"duplicate-markers", start + checkpoint + end + "\n" + start + "phase: old" + end, "", false, true},
		{"duplicate-checkpoints", start + checkpoint + "\nresolve_blocked: {}" + end, "", false, true},
		{"read-failure", start + checkpoint + end, "", false, true},
		{"concurrent", start + checkpoint + end, "Concurrent human change", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fixture := func(name, notes string) {
				b, _ := json.Marshal([]map[string]string{{"notes": notes}})
				if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			fixture("before.json", tc.notes)
			changed := tc.notes
			if tc.changed != "" {
				changed = tc.changed
			}
			fixture("after.json", changed)
			const mock = `#!/bin/bash
if [ "$1" = show ]; then
  if [ "$RECIPE_CASE" = read-failure ]; then exit 1; fi
  if [ -f "$RECIPE_DIR/read" ]; then cat "$RECIPE_DIR/after.json";
  else touch "$RECIPE_DIR/read"; cat "$RECIPE_DIR/before.json"; fi
elif [ "$1" = update ] && [ "$3" = --notes ]; then
  printf '%s' "$4" > "$RECIPE_DIR/written"
else exit 2; fi
`
			if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RECIPE_DIR="+dir, "RECIPE_CASE="+tc.name)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v: %s", err, tc.wantError, out)
			}
			written, err := os.ReadFile(filepath.Join(dir, "written"))
			if !tc.wantWrite {
				if !os.IsNotExist(err) {
					t.Fatalf("unsafe write: %s (%v)", written, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := string(written)
			if tc.name == "nested" && !strings.Contains(got, checkpoint) {
				t.Fatal("nested checkpoint lost")
			}
			if tc.name == "inline" && !strings.Contains(got, "resolve_blocked: {input: abc, retry: used, unknown: keep}") {
				t.Fatal("inline checkpoint lost")
			}
			if i := strings.Index(tc.notes, start); i >= 0 {
				prefix := tc.notes[:i]
				_, suffix, _ := strings.Cut(tc.notes, end)
				if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
					t.Fatal("unrelated notes changed")
				}
			} else if !strings.HasPrefix(got, tc.notes) {
				t.Fatal("unrelated notes lost")
			}
		})
	}
}
