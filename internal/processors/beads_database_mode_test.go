package processors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestBeadsTrackTasksRendersDatabaseModeMatrix(t *testing.T) {
	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile("../../config/processors/builtin/beads-track-tasks.yaml")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	for _, mode := range []config.BeadsDatabaseMode{
		config.BeadsDatabaseModeLocal,
		config.BeadsDatabaseModeShared,
	} {
		for _, upstream := range []string{"", "jira", "github"} {
			name := string(mode) + "/" + upstream
			t.Run(name, func(t *testing.T) {
				ctx := BuildCELContext(&ProcessorInput{DatabaseMode: mode, TasksUpstream: upstream})
				got, err := config.RenderPromptTemplate(proc.Name, proc.Text, ctx, config.BuildTemplateFuncMap(ctx))
				if err != nil {
					t.Fatalf("RenderPromptTemplate: %v", err)
				}
				if mode == config.BeadsDatabaseModeLocal {
					for _, want := range []string{"Database mode: local-only", "Never run `bd dolt push`", "source-code Git operations remain unaffected"} {
						if !strings.Contains(got, want) {
							t.Errorf("local render missing %q:\n%s", want, got)
						}
					}
					if strings.Contains(got, "Database mode: shared") {
						t.Errorf("local render contains shared guidance:\n%s", got)
					}
				} else {
					if !strings.Contains(got, "Database mode: shared") {
						t.Errorf("shared render missing shared guidance:\n%s", got)
					}
					if strings.Contains(got, "Never run `bd dolt push`") {
						t.Errorf("shared render contains local-only prohibition:\n%s", got)
					}
				}
			})
		}
	}
}

func TestBuildCELContext_BeadsDatabaseMode(t *testing.T) {
	ctx := BuildCELContext(&ProcessorInput{DatabaseMode: config.BeadsDatabaseModeShared})
	if got := ctx.Workspace.BeadsDatabaseMode; got != "shared" {
		t.Fatalf("Workspace.BeadsDatabaseMode = %q, want shared", got)
	}
}

func TestApplyProcessorsEmitsDatabaseMode(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "echo-mode.sh")
	script := `#!/bin/sh
mode=$(sed -n 's/.*"database_mode":"\([^"]*\)".*/\1/p')
echo "{\"message\": \"mode=${mode}\"}"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	procs := []*Processor{{
		Name: "echo-mode", Command: scriptPath,
		When:   WhenConfig{On: PhaseUserPrompt, Match: MatchAll},
		Output: OutputTransform, Input: InputConversation, HookDir: tmpDir,
	}}
	input := &ProcessorInput{
		Message: "original", SessionID: "session", WorkingDir: tmpDir,
		DatabaseMode: config.BeadsDatabaseModeShared,
	}
	result, err := ApplyProcessors(context.Background(), procs, input, tmpDir, nil, nil)
	if err != nil {
		t.Fatalf("ApplyProcessors: %v", err)
	}
	if result.Message != "mode=shared" {
		t.Fatalf("message = %q, want mode=shared", result.Message)
	}
}
