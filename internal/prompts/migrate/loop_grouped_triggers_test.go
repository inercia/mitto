package migrate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// join builds a multi-line YAML fixture from individual lines, always ending
// with a trailing newline (matching how real files are saved on disk).
func join(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func parseYAMLNode(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &doc
}

// TestLoopGroupedTriggersMigration_Applies pins which loop: shapes migration
// 0001 considers "needs migration" vs. already-current.
func TestLoopGroupedTriggersMigration_Applies(t *testing.T) {
	cases := []struct {
		name string
		loop string
		want bool
	}{
		{"already grouped", "  trigger: [onCompletion]\n  onCompletion:\n    delay: 30\n", false},
		{"no loop fields at all", "  maxIterations: 5\n", false},
		{"scalar trigger only", "  trigger: onCompletion\n", true},
		{"legacy delay", "  delay: 30\n", true},
		{"legacy value/unit", "  value: 1\n  unit: hours\n", true},
		{"legacy condition", "  condition: ''\n", true},
		{"legacy alongside list trigger", "  trigger: [onCompletion]\n  delay: 30\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseYAMLNode(t, "name: X\nloop:\n"+tc.loop+"prompt: |\n  body\n")
			m := loopGroupedTriggersMigration{}
			if got := m.Applies(doc); got != tc.want {
				t.Errorf("Applies() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMigrateYAML_FieldMappingGoldenFiles pins every old->new field mapping
// from the mitto-r6j.3 design table, asserting the exact spliced output byte
// for byte, and that a second migration pass is a no-op (idempotency).
func TestMigrateYAML_FieldMappingGoldenFiles(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "value/unit/at move to schedule, trigger inferred",
			input: join("name: X", "loop:", "  value: 2", "  unit: days", `  at: "09:00"`, "prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [schedule]", "  schedule:", "    value: 2", "    unit: days",
				`    at: "09:00"`, "prompt: |", "  body"),
		},
		{
			name:  "delay moves to onCompletion, trigger inferred",
			input: join("name: X", "loop:", "  delay: 45", "prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [onCompletion]", "  onCompletion:", "    delay: 45",
				"prompt: |", "  body"),
		},
		{
			name: "onTasks fields move together, trigger inferred",
			input: join("name: X", "loop:", "  condition: 'true'", "  conditionPreset: any",
				"  coalesceDuringBusy: false", "  settleWindow: 5", "  cooldown: 10", "prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [onTasks]", "  onTasks:", "    condition: 'true'",
				"    conditionPreset: any", "    coalesceDuringBusy: false", "    settleWindow: 5",
				"    cooldown: 10", "prompt: |", "  body"),
		},
		{
			name: "scalar trigger converts to list; inert blocks preserved",
			input: join("name: X", "loop:", "  trigger: onCompletion", "  delay: 30", "  value: 1",
				"  unit: hours", "  maxIterations: 10", "prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [onCompletion]", "  schedule:", "    value: 1",
				"    unit: hours", "  onCompletion:", "    delay: 30", "  maxIterations: 10", "prompt: |", "  body"),
		},
		{
			name: "legacy key merges into an already-existing block",
			input: join("name: X", "loop:", "  trigger: [onTasks]", "  onTasks:", "    coalesceDuringBusy: true",
				"  cooldown: 5", "prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [onTasks]", "  onTasks:", "    coalesceDuringBusy: true",
				"    cooldown: 5", "prompt: |", "  body"),
		},
		{
			name: "legacy keys distributed under an existing list trigger, untouched",
			input: join("name: X", "loop:", "  trigger: [onCompletion, onTasks]", "  delay: 5", "  cooldown: 2",
				"prompt: |", "  body"),
			want: join("name: X", "loop:", "  trigger: [onCompletion, onTasks]", "  onCompletion:", "    delay: 5",
				"  onTasks:", "    cooldown: 2", "prompt: |", "  body"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, res, err := MigrateYAML([]byte(tc.input))
			if err != nil {
				t.Fatalf("MigrateYAML: %v", err)
			}
			if !res.Changed {
				t.Fatal("expected Changed=true")
			}
			if len(res.Fired) != 1 || res.Fired[0] != "0001-loop-grouped-triggers" {
				t.Errorf("Fired = %v, want [0001-loop-grouped-triggers]", res.Fired)
			}
			if string(out) != tc.want {
				t.Errorf("output mismatch:\n--- got ---\n%s--- want ---\n%s", out, tc.want)
			}

			out2, res2, err := MigrateYAML(out)
			if err != nil {
				t.Fatalf("second MigrateYAML: %v", err)
			}
			if res2.Changed {
				t.Errorf("second pass reported Changed=true, want idempotent no-op")
			}
			if string(out2) != string(out) {
				t.Errorf("second pass output differs:\n%s\n---vs---\n%s", out2, out)
			}
		})
	}
}

// TestMigrateYAML_NoLoopBlock_NoOp pins that files without a loop: block (the
// overwhelming majority of prompts) are left completely untouched.
func TestMigrateYAML_NoLoopBlock_NoOp(t *testing.T) {
	input := join("name: X", "prompt: |", "  body")
	out, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false (no loop: block)")
	}
	if string(out) != input {
		t.Errorf("output = %q, want unchanged %q", out, input)
	}
}

// TestMigrateYAML_AlreadyGrouped_NoOp pins the "no mtime churn" requirement:
// a file already on the grouped schema is never modified.
func TestMigrateYAML_AlreadyGrouped_NoOp(t *testing.T) {
	input := join("name: X", "loop:", "  trigger: [onCompletion]", "  onCompletion:", "    delay: 30",
		"  maxIterations: 5", "prompt: |", "  body")
	out, res, err := MigrateYAML([]byte(input))
	if err != nil {
		t.Fatalf("MigrateYAML: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false (already grouped)")
	}
	if string(out) != input {
		t.Errorf("output = %q, want unchanged %q", out, input)
	}
}
