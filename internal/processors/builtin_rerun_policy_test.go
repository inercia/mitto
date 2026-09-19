package processors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinProcessors_UserPromptFirstRerun_TokenOnly pins the mitto-8n5
// retune: every builtin on:userPrompt + match:first processor that declares
// a `rerun` block must be token-only (afterTime and afterSentMsgs both
// unset). Wall-clock time and message count correlate poorly with whether
// injected guidance is still retained in the model's context — a fixed
// 30-minute/20-message cadence was reinjecting immutable static guidance
// while it was still fully present in context. afterTokens is kept as the
// sole trigger because token consumption is the honest proxy for context
// turnover, and a genuine fresh-context event (session resume/compaction)
// already refires match:first processors naturally via IsFirstMessage, with
// no separate mechanism needed (see internal/processors/apply.go
// checkRerunEligibility).
//
// This is a whole-directory policy test, not a per-file allowlist: any new
// builtin processor that reintroduces afterTime/afterSentMsgs on a
// match:first + rerun processor fails this test immediately, without
// needing to be added to an explicit registry first.
func TestBuiltinProcessors_UserPromptFirstRerun_TokenOnly(t *testing.T) {
	const dir = "../../config/processors/builtin"
	loader := NewLoader(dir, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		procs, err := loader.LoadFileAll(path)
		if err != nil {
			t.Fatalf("LoadFileAll(%q): %v", path, err)
		}
		for _, proc := range procs {
			if proc == nil || proc.When.On != PhaseUserPrompt || proc.When.Match != MatchFirst {
				continue
			}
			if proc.When.Rerun == nil {
				continue // one-shot processor — no cadence to pin here
			}
			checked++
			r := proc.When.Rerun
			if r.AfterTime != "" {
				t.Errorf("%s (%s): afterTime=%q must be empty — match:first rerun processors must be token-only (mitto-8n5)",
					proc.Name, e.Name(), r.AfterTime)
			}
			if r.AfterSentMsgs != 0 {
				t.Errorf("%s (%s): afterSentMsgs=%d must be 0 — match:first rerun processors must be token-only (mitto-8n5)",
					proc.Name, e.Name(), r.AfterSentMsgs)
			}
			if r.AfterTokens <= 0 {
				t.Errorf("%s (%s): afterTokens must be > 0 when a rerun block is configured (mitto-8n5)",
					proc.Name, e.Name())
			}
		}
	}

	if checked == 0 {
		t.Fatalf("no userPrompt+match:first processors with a rerun block were found under %q — test setup likely broken", dir)
	}
}

// TestBuiltinProcessors_OneShot_NoRerunConfigured pins the specific
// processors that are intentionally one-shot (fire only on the true first
// message, never reinjected): their `rerun` block must remain absent so a
// future edit doesn't mistake them for a candidate needing a token
// threshold. Each of these processors self-suppresses permanently via
// enabledWhen once its one-time task is done (see mitto-8n5 audit
// comments in the YAML files themselves).
func TestBuiltinProcessors_OneShot_NoRerunConfigured(t *testing.T) {
	const dir = "../../config/processors/builtin"
	loader := NewLoader(dir, nil)

	oneShot := []string{
		"auggie-manage-rules.yaml",
		"claude-manage-memory.yaml",
		"identify-workspace-metadata.yaml",
	}
	for _, name := range oneShot {
		path := filepath.Join(dir, name)
		proc, err := loader.LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile(%q): %v", path, err)
		}
		if proc == nil {
			t.Fatalf("LoadFile(%q): nil processor (file may be empty)", path)
		}
		if proc.When.On != PhaseUserPrompt || proc.When.Match != MatchFirst {
			t.Errorf("%s: expected on:userPrompt + match:first, got on=%q match=%q", name, proc.When.On, proc.When.Match)
		}
		if proc.When.Rerun != nil {
			t.Errorf("%s: expected no rerun block (intentionally one-shot), got %+v", name, proc.When.Rerun)
		}
	}
}
