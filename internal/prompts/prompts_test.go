package prompts

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/cel"
)

// promptTargetReuseIssue / promptTargetReuseTitle / promptTargetReuseCoalesce
// are nil-safe accessors for the nested target.reuse block (mitto-6b3). The
// TestBuiltinPrompts_* sweeps read these three flags from every fixture, and
// a nil Reuse block is semantically equivalent to all three off — the helpers
// collapse the two-level nil-check into a single expression at each call site.
func promptTargetReuseIssue(p *PromptFile) bool {
	if p == nil || p.Target == nil || p.Target.Reuse == nil {
		return false
	}
	return p.Target.Reuse.Issue
}

func promptTargetReuseTitle(p *PromptFile) bool {
	if p == nil || p.Target == nil || p.Target.Reuse == nil {
		return false
	}
	return p.Target.Reuse.Title
}

func promptTargetReuseCoalesce(p *PromptFile) *bool {
	if p == nil || p.Target == nil || p.Target.Reuse == nil {
		return nil
	}
	return p.Target.Reuse.Coalesce
}

func TestParsePromptFile_WithFrontMatter(t *testing.T) {
	data := []byte(`name: "Test Prompt"
description: "A test prompt"
backgroundColor: "#E8F5E9"
icon: "code"
tags: ["test", "example"]
prompt: |
  This is the prompt content.

  It can span multiple lines.
`)

	prompt, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Test Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Test Prompt")
	}
	if prompt.Description != "A test prompt" {
		t.Errorf("Description = %q, want %q", prompt.Description, "A test prompt")
	}
	if prompt.BackgroundColor != "#E8F5E9" {
		t.Errorf("BackgroundColor = %q, want %q", prompt.BackgroundColor, "#E8F5E9")
	}
	if prompt.Icon != "code" {
		t.Errorf("Icon = %q, want %q", prompt.Icon, "code")
	}
	if len(prompt.Tags) != 2 || prompt.Tags[0] != "test" || prompt.Tags[1] != "example" {
		t.Errorf("Tags = %v, want [test example]", prompt.Tags)
	}
	wantContent := "This is the prompt content.\n\nIt can span multiple lines.\n"
	if prompt.Content != wantContent {
		t.Errorf("Content = %q, want %q", prompt.Content, wantContent)
	}
	if !prompt.IsEnabled() {
		t.Error("IsEnabled() = false, want true (default)")
	}
}

func TestParsePromptFile_NameFromFilenameNoNameField(t *testing.T) {
	data := []byte(`prompt: |
  This is just content with no name.

  Multiple lines work too.
`)

	prompt, err := ParsePromptFile("my-prompt.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	// Name should be derived from filename (strips .prompt.yaml)
	if prompt.Name != "my-prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "my-prompt")
	}
	wantContent := "This is just content with no name.\n\nMultiple lines work too.\n"
	if prompt.Content != wantContent {
		t.Errorf("Content = %q, want %q", prompt.Content, wantContent)
	}
}

func TestParsePromptFile_DisabledPrompt(t *testing.T) {
	data := []byte(`name: "Disabled Prompt"
enabled: false
prompt: |
  This prompt is disabled.
`)

	prompt, err := ParsePromptFile("disabled.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.IsEnabled() {
		t.Error("IsEnabled() = true, want false")
	}
}

func TestParsePromptFile_NameFromFilename(t *testing.T) {
	data := []byte(`description: "No name specified"
prompt: |
  Content here.
`)

	prompt, err := ParsePromptFile("code-review.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "code-review" {
		t.Errorf("Name = %q, want %q", prompt.Name, "code-review")
	}
}

func TestParsePromptFile_SubdirectoryPath(t *testing.T) {
	data := []byte(`name: "Git Commit"
prompt: |
  Write a commit message.
`)

	prompt, err := ParsePromptFile("git/commit.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Path != "git/commit.prompt.yaml" {
		t.Errorf("Path = %q, want %q", prompt.Path, "git/commit.prompt.yaml")
	}
	if prompt.Name != "Git Commit" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Git Commit")
	}
}

func TestToWebPrompt(t *testing.T) {
	prompt := &PromptFile{
		Name:            "Test",
		Content:         "Content here",
		BackgroundColor: "#FF0000",
		Icon:            "beads",
		Description:     "Test description",
		Group:           "Testing",
		Menus:           "conversation",
		EnabledWhen:     `ACP.MatchesServerType(["auggie", "claude-code"])`,
	}

	wp := prompt.ToWebPrompt()

	if wp.Name != "Test" {
		t.Errorf("WebPrompt.Name = %q, want %q", wp.Name, "Test")
	}
	if wp.Prompt != "Content here" {
		t.Errorf("WebPrompt.Prompt = %q, want %q", wp.Prompt, "Content here")
	}
	if wp.BackgroundColor != "#FF0000" {
		t.Errorf("WebPrompt.BackgroundColor = %q, want %q", wp.BackgroundColor, "#FF0000")
	}
	if wp.Icon != "beads" {
		t.Errorf("WebPrompt.Icon = %q, want %q", wp.Icon, "beads")
	}
	if wp.Description != "Test description" {
		t.Errorf("WebPrompt.Description = %q, want %q", wp.Description, "Test description")
	}
	if wp.Group != "Testing" {
		t.Errorf("WebPrompt.Group = %q, want %q", wp.Group, "Testing")
	}
	if wp.Menus != "conversation" {
		t.Errorf("WebPrompt.Menus = %q, want %q", wp.Menus, "conversation")
	}
	// File-based prompts should have Source=PromptSourceFile
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
	// EnabledWhen CEL expression should be passed through
	wantEnabledWhen := `ACP.MatchesServerType(["auggie", "claude-code"])`
	if wp.EnabledWhen != wantEnabledWhen {
		t.Errorf("WebPrompt.EnabledWhen = %q, want %q", wp.EnabledWhen, wantEnabledWhen)
	}
}

func TestParsePromptFile_WithLoop(t *testing.T) {
	data := []byte(`name: "Daily Standup"
description: "Run daily standup"
loop:
  trigger: [schedule]
  schedule:
    value: 1
    unit: days
    at: "09:00"
prompt: |
  Run the daily standup.
`)

	prompt, err := ParsePromptFile("daily-standup.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Daily Standup" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Daily Standup")
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.FrequencyValue() != 1 {
		t.Errorf("Loop.FrequencyValue() = %d, want 1", prompt.Loop.FrequencyValue())
	}
	if prompt.Loop.FrequencyUnit() != "days" {
		t.Errorf("Loop.FrequencyUnit() = %q, want %q", prompt.Loop.FrequencyUnit(), "days")
	}
	if prompt.Loop.FrequencyAt() != "09:00" {
		t.Errorf("Loop.FrequencyAt() = %q, want %q", prompt.Loop.FrequencyAt(), "09:00")
	}

	// Verify ToWebPrompt carries the Loop field.
	wp := prompt.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil after ToWebPrompt()")
	}
	if wp.Loop.FrequencyValue() != 1 {
		t.Errorf("WebPrompt.Loop.FrequencyValue() = %d, want 1", wp.Loop.FrequencyValue())
	}
	if wp.Loop.FrequencyUnit() != "days" {
		t.Errorf("WebPrompt.Loop.FrequencyUnit() = %q, want %q", wp.Loop.FrequencyUnit(), "days")
	}
	if wp.Loop.FrequencyAt() != "09:00" {
		t.Errorf("WebPrompt.Loop.FrequencyAt() = %q, want %q", wp.Loop.FrequencyAt(), "09:00")
	}
}

// TestParsePromptFile_WithLoop_RunOnStart verifies that the loop.runOnStart
// frontmatter field is parsed into PromptLoop.RunOnStart (mitto-ystk).
func TestParsePromptFile_WithLoop_RunOnStart(t *testing.T) {
	data := []byte(`name: "Boot Pulse"
loop:
  trigger: [onTasks]
  runOnStart: true
prompt: |
  Boot pulse loop.
`)

	prompt, err := ParsePromptFile("boot-pulse.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.RunOnStart == nil {
		t.Fatal("Loop.RunOnStart = nil, want *true")
	}
	if *prompt.Loop.RunOnStart != true {
		t.Errorf("Loop.RunOnStart = %v, want *true", *prompt.Loop.RunOnStart)
	}

	// Explicit false must round-trip as *false, distinct from unset/nil.
	dataFalse := []byte(`name: "No Boot Pulse"
loop:
  trigger: [onTasks]
  runOnStart: false
prompt: |
  No boot pulse.
`)
	promptFalse, err := ParsePromptFile("no-boot-pulse.prompt.yaml", dataFalse, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile(false) failed: %v", err)
	}
	if promptFalse.Loop.RunOnStart == nil {
		t.Fatal("Loop.RunOnStart = nil after explicit false, want *false")
	}
	if *promptFalse.Loop.RunOnStart != false {
		t.Errorf("Loop.RunOnStart = %v, want *false", *promptFalse.Loop.RunOnStart)
	}

	// Absent runOnStart must remain nil (default).
	dataAbsent := []byte(`name: "Default"
loop:
  trigger: [onTasks]
prompt: |
  Default.
`)
	promptAbsent, err := ParsePromptFile("default.prompt.yaml", dataAbsent, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile(absent) failed: %v", err)
	}
	if promptAbsent.Loop.RunOnStart != nil {
		t.Errorf("Loop.RunOnStart = %v, want nil (unset)", *promptAbsent.Loop.RunOnStart)
	}
}

func TestParsePromptFile_WithLoop_NoAt(t *testing.T) {
	data := []byte(`name: "Hourly Check"
loop:
  trigger: [schedule]
  schedule:
    value: 2
    unit: hours
prompt: |
  Check every 2 hours.
`)

	prompt, err := ParsePromptFile("hourly.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.FrequencyValue() != 2 {
		t.Errorf("Loop.FrequencyValue() = %d, want 2", prompt.Loop.FrequencyValue())
	}
	if prompt.Loop.FrequencyUnit() != "hours" {
		t.Errorf("Loop.FrequencyUnit() = %q, want %q", prompt.Loop.FrequencyUnit(), "hours")
	}
	if prompt.Loop.FrequencyAt() != "" {
		t.Errorf("Loop.FrequencyAt() = %q, want empty (no at for hours)", prompt.Loop.FrequencyAt())
	}
}

func TestParsePromptFile_WithLoop_MaxIterations(t *testing.T) {
	data := []byte(`name: "Capped"
loop:
  trigger: [schedule]
  schedule:
    value: 1
    unit: hours
  maxIterations: 5
prompt: |
  do thing
`)

	prompt, err := ParsePromptFile("capped.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.MaxIterations != 5 {
		t.Errorf("Loop.MaxIterations = %d, want 5", prompt.Loop.MaxIterations)
	}

	// Verify ToWebPrompt carries the MaxIterations field.
	wp := prompt.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil after ToWebPrompt()")
	}
	if wp.Loop.MaxIterations != 5 {
		t.Errorf("WebPrompt.Loop.MaxIterations = %d, want 5", wp.Loop.MaxIterations)
	}
}

func TestParsePromptFile_NoLoop(t *testing.T) {
	data := []byte(`name: "One-time Prompt"
prompt: |
  Just a regular prompt.
`)

	prompt, err := ParsePromptFile("one-time.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop != nil {
		t.Errorf("Loop = %+v, want nil for prompt without loop field", prompt.Loop)
	}

	wp := prompt.ToWebPrompt()
	if wp.Loop != nil {
		t.Errorf("WebPrompt.Loop = %+v, want nil", wp.Loop)
	}
}

func TestParsePromptFile_WithLoop_OnTasksCondition(t *testing.T) {
	data := []byte(`name: "On Tasks"
loop:
  trigger: [onTasks]
  onTasks:
    condition: 'Tasks.Open > Prev.Open'
  maxIterations: 20
  maxDuration: "4h"
prompt: |
  Fire when open task count grows.
`)

	prompt, err := ParsePromptFile("on-tasks.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if !prompt.Loop.hasTrigger("onTasks") {
		t.Errorf("Loop.Trigger = %v, want to include %q", prompt.Loop.Trigger, "onTasks")
	}
	if prompt.Loop.TasksCondition() != `Tasks.Open > Prev.Open` {
		t.Errorf("Loop.TasksCondition() = %q, want %q", prompt.Loop.TasksCondition(), `Tasks.Open > Prev.Open`)
	}
	if prompt.Loop.MaxIterations != 20 {
		t.Errorf("Loop.MaxIterations = %d, want 20", prompt.Loop.MaxIterations)
	}
	if prompt.Loop.MaxDuration != "4h" {
		t.Errorf("Loop.MaxDuration = %q, want %q", prompt.Loop.MaxDuration, "4h")
	}

	// Verify ToWebPrompt carries the Condition and Trigger fields through.
	wp := prompt.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil after ToWebPrompt()")
	}
	if !wp.Loop.hasTrigger("onTasks") {
		t.Errorf("WebPrompt.Loop.Trigger = %v, want to include %q", wp.Loop.Trigger, "onTasks")
	}
	if wp.Loop.TasksCondition() != `Tasks.Open > Prev.Open` {
		t.Errorf("WebPrompt.Loop.TasksCondition() = %q, want %q", wp.Loop.TasksCondition(), `Tasks.Open > Prev.Open`)
	}
}

// TestParsePromptFile_WithLoop_OnChild pins the mitto-987y.2 acceptance
// criteria: loop.onChild parses, its When list round-trips through
// (*PromptLoop).ChildEvents(), and ToWebPrompt carries the block through.
func TestParsePromptFile_WithLoop_OnChild(t *testing.T) {
	data := []byte(`name: "On Child"
loop:
  trigger: [onChild, onCompletion]
  onChild:
    when: [anyEndResponse, anyDeleted]
  onCompletion:
    delay: 30
prompt: |
  Fire when a child conversation ends or is deleted.
`)

	prompt, err := ParsePromptFile("on-child.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if !prompt.Loop.hasTrigger("onChild") {
		t.Errorf("Loop.Trigger = %v, want to include %q", prompt.Loop.Trigger, "onChild")
	}
	if got := prompt.Loop.ChildEvents(); len(got) != 2 || got[0] != "anyEndResponse" || got[1] != "anyDeleted" {
		t.Errorf("Loop.ChildEvents() = %v, want [anyEndResponse anyDeleted]", got)
	}

	// Verify ToWebPrompt carries the onChild block through.
	wp := prompt.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil after ToWebPrompt()")
	}
	if !wp.Loop.hasTrigger("onChild") {
		t.Errorf("WebPrompt.Loop.Trigger = %v, want to include %q", wp.Loop.Trigger, "onChild")
	}
	if got := wp.Loop.ChildEvents(); len(got) != 2 || got[0] != "anyEndResponse" || got[1] != "anyDeleted" {
		t.Errorf("WebPrompt.Loop.ChildEvents() = %v, want [anyEndResponse anyDeleted]", got)
	}
}

// TestParsePromptFile_WithLoop_OnChild_AnyLoopStopped pins the mitto-q6my
// acceptance criteria: loop.onChild.when accepts "anyLoopStopped" alongside
// the two pre-existing events, and it round-trips through
// (*PromptLoop).ChildEvents().
func TestParsePromptFile_WithLoop_OnChild_AnyLoopStopped(t *testing.T) {
	data := []byte(`name: "On Child Loop Stopped"
loop:
  trigger: [onChild, onCompletion]
  onChild:
    when: [anyEndResponse, anyDeleted, anyLoopStopped]
  onCompletion:
    delay: 30
prompt: |
  Fire when a child ends, is deleted, or its own loop stops.
`)

	prompt, err := ParsePromptFile("on-child-loop-stopped.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	want := []string{"anyEndResponse", "anyDeleted", "anyLoopStopped"}
	got := prompt.Loop.ChildEvents()
	if len(got) != len(want) {
		t.Fatalf("Loop.ChildEvents() = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Loop.ChildEvents()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestParsePromptFile_WithLoop_OnChild_AnyLoopStoppedAlone pins that
// anyLoopStopped may be armed on its own, without the other two events —
// it is a standalone opt-in signal, not a modifier of the default pair.
func TestParsePromptFile_WithLoop_OnChild_AnyLoopStoppedAlone(t *testing.T) {
	data := []byte(`name: "Only LoopStopped"
loop:
  trigger: [onChild, schedule]
  onChild:
    when: [anyLoopStopped]
prompt: |
  Fire only when a child's own loop stops.
`)

	prompt, err := ParsePromptFile("only-loop-stopped.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if got := prompt.Loop.ChildEvents(); len(got) != 1 || got[0] != "anyLoopStopped" {
		t.Errorf("Loop.ChildEvents() = %v, want [anyLoopStopped]", got)
	}
}

// TestParsePromptFile_WithLoop_OnChildAlone_Errors pins the "onChild cannot
// be the sole trigger" rule (mitto-987y.2, mirrors session.ErrOnChildAlone) at
// the ParsePromptFile level, exercising ValidatePromptLoop's plumbing end to
// end rather than calling ValidateLoopTriggers directly.
func TestParsePromptFile_WithLoop_OnChildAlone_Errors(t *testing.T) {
	data := []byte(`name: "Only Child"
loop:
  trigger: [onChild]
  onChild:
    when: [anyEndResponse]
prompt: |
  Broken: onChild alone.
`)

	_, err := ParsePromptFile("only-child.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile succeeded, want error for onChild as sole trigger")
	}
	if !strings.Contains(err.Error(), "onChild cannot be the only trigger") {
		t.Errorf("error = %q, want it to mention onChild cannot be the only trigger", err.Error())
	}
}

// TestParsePromptFile_WithLoop_OnChild_InvalidWhenEntry pins the
// loop.onChild.when validation rule: an unknown event name is rejected.
func TestParsePromptFile_WithLoop_OnChild_InvalidWhenEntry(t *testing.T) {
	data := []byte(`name: "Bad Child Event"
loop:
  trigger: [onChild, schedule]
  onChild:
    when: [anyStarted]
prompt: |
  Broken: unknown child event.
`)

	_, err := ParsePromptFile("bad-child-event.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile succeeded, want error for invalid onChild.when entry")
	}
	if !strings.Contains(err.Error(), "onChild.when") || !strings.Contains(err.Error(), "anyStarted") {
		t.Errorf("error = %q, want it to mention onChild.when and anyStarted", err.Error())
	}
}

func TestParsePromptFile_WithLoop_InvalidCondition(t *testing.T) {
	data := []byte(`name: "Bad Condition"
loop:
  trigger: [onTasks]
  onTasks:
    condition: 'Tasks.Open > '
prompt: |
  Broken CEL.
`)

	_, err := ParsePromptFile("bad-condition.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile succeeded, want error for invalid CEL condition")
	}
	if !strings.Contains(err.Error(), "loop.onTasks.condition") {
		t.Errorf("error = %q, want it to mention loop.onTasks.condition", err.Error())
	}
}

func TestParsePromptFile_WithSingleton(t *testing.T) {
	data := []byte(`name: "Singleton Prompt"
singleton: true
prompt: |
  Only one instance at a time.
`)

	prompt, err := ParsePromptFile("singleton.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if !prompt.Singleton {
		t.Errorf("Singleton = false, want true")
	}

	// Round-trips through ToWebPrompt.
	wp := prompt.ToWebPrompt()
	if !wp.Singleton {
		t.Errorf("WebPrompt.Singleton = false, want true")
	}
}

func TestParsePromptFile_WithTargetReuseIssue(t *testing.T) {
	data := []byte(`name: "test"
target:
  reuse:
    issue: true
prompt: hi
`)

	prompt, err := ParsePromptFile("reuse.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if prompt.Target.Reuse == nil || !prompt.Target.Reuse.Issue {
		t.Errorf("Target.Reuse.Issue = %+v, want true", prompt.Target.Reuse)
	}

	// Round-trips through ToWebPrompt.
	wp := prompt.ToWebPrompt()
	if wp.Target == nil {
		t.Fatal("WebPrompt.Target = nil, want non-nil")
	}
	if wp.Target.Reuse == nil || !wp.Target.Reuse.Issue {
		t.Errorf("WebPrompt.Target.Reuse.Issue = %+v, want true", wp.Target.Reuse)
	}
}

// TestParsePromptFile_LegacyReuseKeysMigrated pins mitto-6b3 + mitto-a4yg: the
// three pre-refactor flat keys under target: — reuseIssue / reuseTitle /
// reuseCoalesce — must load successfully, with the value migrated onto the
// new nested target.reuse path in memory. Earlier behavior (pre-mitto-a4yg)
// hard-failed ParsePromptFile for these keys, which evicted the ENTIRE prompt
// file from PromptsCache over a single lint-class field — the same blast-
// radius bug class that mitto-r6j.3 already fixed for loop.*. A migration
// miss must never silently degrade dispatch behavior, so this still asserts
// the field actually lands on the new struct path, not merely that parsing
// succeeds.
func TestParsePromptFile_LegacyReuseKeysMigrated(t *testing.T) {
	cases := []struct {
		name string
		body string
		want func(*testing.T, *PromptTargetReuse)
	}{
		{
			name: "reuseIssue",
			body: `name: "x"
target:
  reuseIssue: true
prompt: hi
`,
			want: func(t *testing.T, r *PromptTargetReuse) {
				if r == nil || !r.Issue {
					t.Errorf("Target.Reuse.Issue = %+v, want true", r)
				}
			},
		},
		{
			name: "reuseTitle",
			body: `name: "x"
target:
  title: "x"
  reuseTitle: true
prompt: hi
`,
			want: func(t *testing.T, r *PromptTargetReuse) {
				if r == nil || !r.Title {
					t.Errorf("Target.Reuse.Title = %+v, want true", r)
				}
			},
		},
		{
			name: "reuseCoalesce",
			body: `name: "x"
target:
  reuseIssue: true
  reuseCoalesce: true
prompt: hi
`,
			want: func(t *testing.T, r *PromptTargetReuse) {
				if r == nil || !r.Issue {
					t.Errorf("Target.Reuse.Issue = %+v, want true", r)
				}
				if r == nil || r.Coalesce == nil || !*r.Coalesce {
					t.Errorf("Target.Reuse.Coalesce = %+v, want true", r)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt, err := ParsePromptFile("legacy.prompt.yaml", []byte(tc.body), time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile: err = %v, want a successful migrate+WARN load", err)
			}
			if prompt.Target == nil {
				t.Fatal("Target = nil, want non-nil after legacy-key migration")
			}
			tc.want(t, prompt.Target.Reuse)
		})
	}
}

// TestParsePromptFile_LegacyReuseMentionInBodyIsIgnored pins mitto-6b3: the
// legacy-key rejection walks the document root's target: mapping only, so a
// prompt body (or any other scalar) that happens to mention the string
// "reuseTitle" as prose must NOT trip the check.
func TestParsePromptFile_LegacyReuseMentionInBodyIsIgnored(t *testing.T) {
	data := []byte(`name: "prose"
target:
  title: "ok"
  reuse:
    title: true
prompt: |
  Historical note: this prompt used to declare reuseTitle: true at the
  flat position; it now nests under target.reuse.title.
`)
	prompt, err := ParsePromptFile("prose.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v (prose mention should not trip legacy-key check)", err)
	}
	if prompt.Target == nil || prompt.Target.Reuse == nil || !prompt.Target.Reuse.Title {
		t.Errorf("Target.Reuse.Title = %+v, want true", prompt.Target)
	}
}

func TestParsePromptFile_WithoutTarget(t *testing.T) {
	data := []byte(`name: "plain"
prompt: hi
`)

	prompt, err := ParsePromptFile("plain-target.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target != nil {
		t.Errorf("Target = %+v, want nil (absent should decode to nil)", prompt.Target)
	}
}

// TestParsePromptFile_WithTargetSuppressAutoChildren pins mitto-nlx: the
// new target.suppressAutoChildren key parses under target: as a peer of
// title / reuse, survives the round-trip through ToWebPrompt so HTTP
// handlers can resolve it without re-parsing the file, and defaults to
// false when absent (opt-in flag, unchanged behavior).
func TestParsePromptFile_WithTargetSuppressAutoChildren(t *testing.T) {
	data := []byte(`name: "no-children"
target:
  suppressAutoChildren: true
prompt: hi
`)

	prompt, err := ParsePromptFile("suppress.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if !prompt.Target.SuppressAutoChildren {
		t.Errorf("Target.SuppressAutoChildren = false, want true")
	}
	// Round-trips through ToWebPrompt so the resolver can read it off the
	// merged WebPrompt list without re-parsing.
	wp := prompt.ToWebPrompt()
	if wp.Target == nil {
		t.Fatal("WebPrompt.Target = nil, want non-nil")
	}
	if !wp.Target.SuppressAutoChildren {
		t.Errorf("WebPrompt.Target.SuppressAutoChildren = false, want true")
	}
}

// TestParsePromptFile_TargetSuppressAutoChildrenAbsentDefaultsFalse pins
// mitto-nlx: absent suppressAutoChildren must decode to the zero value
// (false), so existing prompts that only declare target.title or
// target.reuse.* keep unchanged auto-children behavior.
func TestParsePromptFile_TargetSuppressAutoChildrenAbsentDefaultsFalse(t *testing.T) {
	data := []byte(`name: "titled"
target:
  title: "Only a title"
prompt: hi
`)

	prompt, err := ParsePromptFile("titled.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if prompt.Target.SuppressAutoChildren {
		t.Errorf("Target.SuppressAutoChildren = true, want false (absent must default to false)")
	}
	wp := prompt.ToWebPrompt()
	if wp.Target == nil || wp.Target.SuppressAutoChildren {
		t.Errorf("WebPrompt.Target.SuppressAutoChildren = %+v, want false", wp.Target)
	}
}

// TestParsePromptFile_TargetSuppressAutoChildrenJSONRoundTrip pins
// mitto-nlx: the flag survives a JSON round-trip through WebPrompt so
// callers that receive the merged prompt list (frontend, resolver, MCP
// tool-list handlers) see it under the "suppressAutoChildren" JSON key.
func TestParsePromptFile_TargetSuppressAutoChildrenJSONRoundTrip(t *testing.T) {
	data := []byte(`name: "no-children"
target:
  suppressAutoChildren: true
prompt: hi
`)

	prompt, err := ParsePromptFile("suppress.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	wp := prompt.ToWebPrompt()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"suppressAutoChildren":true`) {
		t.Errorf("JSON body missing suppressAutoChildren:true key; got %s", body)
	}

	var round WebPrompt
	if err := json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&round); err != nil {
		t.Fatalf("json.Decode(WebPrompt): %v", err)
	}
	if round.Target == nil || !round.Target.SuppressAutoChildren {
		t.Errorf("round-tripped WebPrompt.Target.SuppressAutoChildren = %+v, want true", round.Target)
	}
}

// TestParsePromptFile_WithTargetNoArchive pins mitto-yvel.1: the new
// target.noArchive key parses under target: as a peer of title / reuse /
// suppressAutoChildren, and survives the round-trip through ToWebPrompt so
// HTTP handlers can resolve it without re-parsing the file.
func TestParsePromptFile_WithTargetNoArchive(t *testing.T) {
	data := []byte(`name: "no-archive"
target:
  noArchive: true
prompt: hi
`)

	prompt, err := ParsePromptFile("no-archive.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if !prompt.Target.NoArchive {
		t.Errorf("Target.NoArchive = false, want true")
	}
	// Round-trips through ToWebPrompt so the resolver can read it off the
	// merged WebPrompt list without re-parsing.
	wp := prompt.ToWebPrompt()
	if wp.Target == nil {
		t.Fatal("WebPrompt.Target = nil, want non-nil")
	}
	if !wp.Target.NoArchive {
		t.Errorf("WebPrompt.Target.NoArchive = false, want true")
	}
}

// TestParsePromptFile_TargetNoArchiveAbsentDefaultsFalse pins mitto-yvel.1:
// absent noArchive must decode to the zero value (false), so existing
// prompts that only declare target.title or target.reuse.* keep unchanged
// (archivable) behavior.
func TestParsePromptFile_TargetNoArchiveAbsentDefaultsFalse(t *testing.T) {
	data := []byte(`name: "titled"
target:
  title: "Only a title"
prompt: hi
`)

	prompt, err := ParsePromptFile("titled.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if prompt.Target.NoArchive {
		t.Errorf("Target.NoArchive = true, want false (absent must default to false)")
	}
	wp := prompt.ToWebPrompt()
	if wp.Target == nil || wp.Target.NoArchive {
		t.Errorf("WebPrompt.Target.NoArchive = %+v, want false", wp.Target)
	}
}

// TestParsePromptFile_TargetNoArchiveJSONRoundTrip pins mitto-yvel.1: the
// flag survives a JSON round-trip through WebPrompt so callers that receive
// the merged prompt list (frontend, resolver, MCP tool-list handlers) see it
// under the "noArchive" JSON key, and is omitted entirely (omitempty) when
// false so existing prompts serialize unchanged.
func TestParsePromptFile_TargetNoArchiveJSONRoundTrip(t *testing.T) {
	data := []byte(`name: "no-archive"
target:
  noArchive: true
prompt: hi
`)

	prompt, err := ParsePromptFile("no-archive.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	wp := prompt.ToWebPrompt()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"noArchive":true`) {
		t.Errorf("JSON body missing noArchive:true key; got %s", body)
	}

	var round WebPrompt
	if err := json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&round); err != nil {
		t.Fatalf("json.Decode(WebPrompt): %v", err)
	}
	if round.Target == nil || !round.Target.NoArchive {
		t.Errorf("round-tripped WebPrompt.Target.NoArchive = %+v, want true", round.Target)
	}
}

// TestParsePromptFile_TargetNoArchiveExplicitFalse pins mitto-yvel.1: an
// explicit `noArchive: false` in the frontmatter parses identically to the
// absent case (zero value, archivable), completing the present/true,
// present/false, absent trio called out in the bead's acceptance criteria.
func TestParsePromptFile_TargetNoArchiveExplicitFalse(t *testing.T) {
	data := []byte(`name: "explicit-false"
target:
  noArchive: false
prompt: hi
`)

	prompt, err := ParsePromptFile("explicit-false.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Target == nil {
		t.Fatal("Target = nil, want non-nil")
	}
	if prompt.Target.NoArchive {
		t.Errorf("Target.NoArchive = true, want false (explicit false)")
	}
	wp := prompt.ToWebPrompt()
	if wp.Target == nil || wp.Target.NoArchive {
		t.Errorf("WebPrompt.Target.NoArchive = %+v, want false", wp.Target)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	if strings.Contains(buf.String(), "noArchive") {
		t.Errorf("JSON body should omit noArchive key when explicitly false; got %s", buf.String())
	}
}

// TestParsePromptFile_TargetNoArchiveFalseOmittedFromJSON pins mitto-yvel.1:
// an explicit noArchive: false (or absent) must NOT appear in the
// serialized JSON (omitempty), so existing /api/prompts payloads for
// prompts that don't use this flag are byte-identical to before.
func TestParsePromptFile_TargetNoArchiveFalseOmittedFromJSON(t *testing.T) {
	data := []byte(`name: "titled"
target:
  title: "Only a title"
prompt: hi
`)

	prompt, err := ParsePromptFile("titled.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	wp := prompt.ToWebPrompt()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	if strings.Contains(buf.String(), "noArchive") {
		t.Errorf("JSON body should omit noArchive key when false; got %s", buf.String())
	}
}

// TestParsePromptFile_CollectInnerArgs pins mitto-48c: a `type: prompts`
// picker's collectInnerArgs opt-out parses to a non-nil pointer, defaults to
// nil (=> ShouldCollectInnerArgs() true) when absent, and survives the
// WebPrompt JSON round-trip under the "collectInnerArgs" key (mirrors
// TestParsePromptFile_TargetSuppressAutoChildrenJSONRoundTrip above).
func TestParsePromptFile_CollectInnerArgs(t *testing.T) {
	data := []byte(`name: "Specialize"
parameters:
  - name: Prompt
    type: prompts
    collectInnerArgs: false
  - name: Other
    type: prompts
prompt: hi
`)

	prompt, err := ParsePromptFile("specialize.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}

	optedOut := prompt.Parameters[0]
	if optedOut.CollectInnerArgs == nil || *optedOut.CollectInnerArgs != false {
		t.Fatalf("Parameters[0].CollectInnerArgs = %v, want pointer to false", optedOut.CollectInnerArgs)
	}
	if optedOut.ShouldCollectInnerArgs() {
		t.Errorf("ShouldCollectInnerArgs() = true, want false when collectInnerArgs: false is set")
	}

	// Absent collectInnerArgs defaults to nil => ShouldCollectInnerArgs() true,
	// so every pre-mitto-48c picker is unaffected.
	defaulted := prompt.Parameters[1]
	if defaulted.CollectInnerArgs != nil {
		t.Errorf("Parameters[1].CollectInnerArgs = %v, want nil (absent from YAML)", defaulted.CollectInnerArgs)
	}
	if !defaulted.ShouldCollectInnerArgs() {
		t.Errorf("ShouldCollectInnerArgs() = false, want true when collectInnerArgs is absent")
	}

	wp := prompt.ToWebPrompt()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"collectInnerArgs":false`) {
		t.Errorf("JSON body missing collectInnerArgs:false key for the opted-out param; got %s", body)
	}
	// omitempty on a *bool nil field: the "Other" param's JSON must not
	// carry a spurious collectInnerArgs key at all (nil, not false).
	if strings.Count(body, "collectInnerArgs") != 1 {
		t.Errorf("expected exactly one collectInnerArgs occurrence (nil is omitted); got body %s", body)
	}

	var round WebPrompt
	if err := json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&round); err != nil {
		t.Fatalf("json.Decode(WebPrompt): %v", err)
	}
	if len(round.Parameters) != 2 || round.Parameters[0].CollectInnerArgs == nil || *round.Parameters[0].CollectInnerArgs != false {
		t.Errorf("round-tripped WebPrompt.Parameters[0].CollectInnerArgs = %+v, want pointer to false", round.Parameters)
	}
}

// TestParsePromptFile_WithParameterGroup covers mitto-boio: a parameter-level
// `group` string parses into PromptParameter.Group and survives the
// PromptFile -> WebPrompt JSON path the frontend consumes.
func TestParsePromptFile_WithParameterGroup(t *testing.T) {
	data := []byte(`name: "Grouped"
parameters:
  - name: Message
    type: text
    group: "Changes Submission"
  - name: Extra
    type: text
prompt: hi
`)

	prompt, err := ParsePromptFile("grouped.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}
	if got := prompt.Parameters[0].Group; got != "Changes Submission" {
		t.Errorf("Parameters[0].Group = %q, want %q", got, "Changes Submission")
	}
	if got := prompt.Parameters[1].Group; got != "" {
		t.Errorf("Parameters[1].Group = %q, want empty (not declared)", got)
	}

	wp := prompt.ToWebPrompt()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wp); err != nil {
		t.Fatalf("json.Encode(WebPrompt): %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"group":"Changes Submission"`) {
		t.Errorf("JSON body missing group key for the grouped param; got %s", body)
	}
	// omitempty: the ungrouped param must not carry a spurious "group" key.
	if strings.Count(body, `"group"`) != 1 {
		t.Errorf("expected exactly one \"group\" occurrence (empty is omitted); got body %s", body)
	}
}

func TestParsePromptFile_WithoutSingleton(t *testing.T) {
	data := []byte(`name: "Plain Prompt"
prompt: |
  Many instances allowed.
`)

	prompt, err := ParsePromptFile("plain-singleton.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Singleton {
		t.Errorf("Singleton = true, want false (absent defaults to false)")
	}

	wp := prompt.ToWebPrompt()
	if wp.Singleton {
		t.Errorf("WebPrompt.Singleton = true, want false")
	}
}

func TestMergePrompts_PreservesLoopField(t *testing.T) {
	loop := &PromptLoop{Schedule: &PromptLoopSchedule{Value: 3, Unit: "hours"}}
	globalPrompts := []WebPrompt{
		{Name: "Loop Prompt", Prompt: "do it", Loop: loop, Source: PromptSourceFile},
		{Name: "Regular Prompt", Prompt: "also do it", Source: PromptSourceFile},
	}

	// MergePrompts should carry the Loop field through.
	merged := MergePrompts(globalPrompts, nil, nil)

	var found *WebPrompt
	for i := range merged {
		if merged[i].Name == "Loop Prompt" {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		t.Fatal("Loop Prompt not found in merged result")
	}
	if found.Loop == nil {
		t.Fatal("merged Loop Prompt has nil Loop field, want non-nil")
	}
	if found.Loop.FrequencyValue() != 3 {
		t.Errorf("merged Loop.FrequencyValue() = %d, want 3", found.Loop.FrequencyValue())
	}
	if found.Loop.FrequencyUnit() != "hours" {
		t.Errorf("merged Loop.FrequencyUnit() = %q, want hours", found.Loop.FrequencyUnit())
	}
}

func TestLoadPromptsFromDir(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	// Create root prompt
	rootPrompt := `name: "Root Prompt"
prompt: |
  Root content.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "root.prompt.yaml"), []byte(rootPrompt), 0644); err != nil {
		t.Fatalf("Failed to write root.prompt.yaml: %v", err)
	}

	// Create subdirectory with prompt
	subDir := filepath.Join(tmpDir, "git")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	subPrompt := `name: "Git Commit"
backgroundColor: "#E8F5E9"
prompt: |
  Write a commit message.
`
	if err := os.WriteFile(filepath.Join(subDir, "commit.prompt.yaml"), []byte(subPrompt), 0644); err != nil {
		t.Fatalf("Failed to write commit.prompt.yaml: %v", err)
	}

	// Create disabled prompt (should be excluded)
	disabledPrompt := `name: "Disabled"
enabled: false
prompt: |
  This should not appear.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "disabled.prompt.yaml"), []byte(disabledPrompt), 0644); err != nil {
		t.Fatalf("Failed to write disabled.prompt.yaml: %v", err)
	}

	// Create non-prompt.yaml file (should be ignored)
	if err := os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("Failed to write readme.txt: %v", err)
	}

	// Load prompts
	prompts, err := LoadPromptsFromDir(tmpDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir failed: %v", err)
	}

	// Should have 3 prompts (root + git/commit + disabled)
	// LoadPromptsFromDir now includes disabled prompts so they can participate in merge
	if len(prompts) != 3 {
		t.Errorf("len(prompts) = %d, want 3", len(prompts))
	}

	// Check that we have the expected prompts
	names := make(map[string]bool)
	for _, p := range prompts {
		names[p.Name] = true
	}

	if !names["Root Prompt"] {
		t.Error("Missing 'Root Prompt'")
	}
	if !names["Git Commit"] {
		t.Error("Missing 'Git Commit'")
	}
	if !names["Disabled"] {
		t.Error("Missing 'Disabled' - disabled prompts should be included for merge")
	}

	// Verify the disabled prompt has IsEnabled() == false
	for _, p := range prompts {
		if p.Name == "Disabled" {
			if p.IsEnabled() {
				t.Error("'Disabled' prompt should have IsEnabled() == false")
			}
		}
	}
}

func TestLoadPromptsFromDir_NonExistent(t *testing.T) {
	prompts, err := LoadPromptsFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("LoadPromptsFromDir failed: %v", err)
	}
	if prompts != nil {
		t.Errorf("prompts = %v, want nil for non-existent directory", prompts)
	}
}

func TestLoadPromptsFromDirWithErrors_ReportsBadFileAndKeepsGood(t *testing.T) {
	tmpDir := t.TempDir()

	// Valid prompt file.
	goodPrompt := `name: "Good Prompt"
prompt: |
  Some content.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "good.prompt.yaml"), []byte(goodPrompt), 0644); err != nil {
		t.Fatalf("Failed to write good.prompt.yaml: %v", err)
	}

	// Invalid prompt file: malformed YAML (unclosed flow sequence).
	badPrompt := `name: [unclosed
prompt: |
  Should fail to parse.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.prompt.yaml"), []byte(badPrompt), 0644); err != nil {
		t.Fatalf("Failed to write bad.prompt.yaml: %v", err)
	}

	prompts, loadErrors, err := LoadPromptsFromDirWithErrors(tmpDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDirWithErrors failed: %v", err)
	}

	// Good prompt must still be returned.
	if len(prompts) != 1 {
		t.Fatalf("len(prompts) = %d, want 1", len(prompts))
	}
	if prompts[0].Name != "Good Prompt" {
		t.Errorf("prompts[0].Name = %q, want %q", prompts[0].Name, "Good Prompt")
	}

	// Exactly one load error for the bad file.
	if len(loadErrors) != 1 {
		t.Fatalf("len(loadErrors) = %d, want 1 (%+v)", len(loadErrors), loadErrors)
	}
	if loadErrors[0].Path != "bad.prompt.yaml" {
		t.Errorf("loadErrors[0].Path = %q, want %q", loadErrors[0].Path, "bad.prompt.yaml")
	}
	if loadErrors[0].Err == nil {
		t.Error("loadErrors[0].Err = nil, want non-nil")
	}
}

// warnCountHandler is a minimal slog.Handler that counts WARN+ records whose
// Message matches a fixed string. Safe for concurrent use.
type warnCountHandler struct {
	mu     sync.Mutex
	target string
	count  int
}

func (h *warnCountHandler) Enabled(_ context.Context, lv slog.Level) bool {
	return lv >= slog.LevelWarn
}

func (h *warnCountHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.target {
		h.mu.Lock()
		h.count++
		h.mu.Unlock()
	}
	return nil
}

func (h *warnCountHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *warnCountHandler) WithGroup(_ string) slog.Handler      { return h }

// TestLoadPromptsFromDirWithErrors_LogsFailedFileOncePerReload is the reproduction
// for mitto-e8r: LoadPromptsFromDirWithErrors currently emits an unbounded
// slog.Warn("failed to load prompt file", ...) on EVERY reload for the same
// broken file, polluting the log (60x in 15h in the field). The structured
// PromptLoadError already flows to LoadErrors() (and thence to UI toasts via
// mitto-mqe + the `mitto prompts verify` CLI), so the WARN is redundant with
// existing observability paths and safe to de-duplicate per process lifetime.
//
// This test loads the same tmpdir containing one permanently-broken
// .prompt.yaml twice and asserts the WARN was emitted exactly once. Today it
// emits twice, so the test fails at the second assertion. When the AC3 fix
// lands (dedupe keyed by path+err.Error() in prompts.go:674) the assertion
// flips green. PromptLoadError entries must still be returned on BOTH calls
// so the UI/CLI paths keep working — that invariant is asserted explicitly.
func TestLoadPromptsFromDirWithErrors_LogsFailedFileOncePerReload(t *testing.T) {
	tmpDir := t.TempDir()

	// Prompt body with an invalid Cond literal — trips PrecompileTemplateConds
	// in LoadPromptFile, mirroring the real-world .Workspace.WorkingDir case
	// (both go through the same "cond precompile" error path).
	badPrompt := `name: "Repro mitto-e8r"
prompt: |
  {{ if Cond "this is ::: not valid CEL" }}x{{ end }}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.prompt.yaml"), []byte(badPrompt), 0644); err != nil {
		t.Fatalf("Failed to write bad.prompt.yaml: %v", err)
	}

	// Install a WARN counter on the slog default so prompts.go:674's emission
	// is captured. Restore the original default on cleanup.
	cap := &warnCountHandler{target: "failed to load prompt file"}
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })

	// First load: baseline — WARN fires, PromptLoadError recorded.
	_, loadErrors1, err := LoadPromptsFromDirWithErrors(tmpDir)
	if err != nil {
		t.Fatalf("first LoadPromptsFromDirWithErrors failed: %v", err)
	}
	if len(loadErrors1) != 1 {
		t.Fatalf("first call: len(loadErrors) = %d, want 1 (%+v)", len(loadErrors1), loadErrors1)
	}

	// Second load: same tmpdir, same broken file — WARN must NOT fire again
	// (post-fix), but PromptLoadError MUST still be returned (invariant).
	_, loadErrors2, err := LoadPromptsFromDirWithErrors(tmpDir)
	if err != nil {
		t.Fatalf("second LoadPromptsFromDirWithErrors failed: %v", err)
	}
	if len(loadErrors2) != 1 {
		t.Fatalf("second call: len(loadErrors) = %d, want 1 (invariant: structured error must still be returned on every reload; %+v)", len(loadErrors2), loadErrors2)
	}

	cap.mu.Lock()
	got := cap.count
	cap.mu.Unlock()

	if got != 1 {
		t.Fatalf("WARN 'failed to load prompt file' fired %d times across 2 reloads, want 1 (mitto-e8r: dedupe per (path, err) per process lifetime)", got)
	}
}

func TestPromptsToWebPrompts(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "One", Content: "Content 1"},
		{Name: "Two", Content: "Content 2", BackgroundColor: "#FF0000"},
	}

	webPrompts := PromptsToWebPrompts(prompts)

	if len(webPrompts) != 2 {
		t.Fatalf("len(webPrompts) = %d, want 2", len(webPrompts))
	}
	if webPrompts[0].Name != "One" {
		t.Errorf("webPrompts[0].Name = %q, want %q", webPrompts[0].Name, "One")
	}
	if webPrompts[1].BackgroundColor != "#FF0000" {
		t.Errorf("webPrompts[1].BackgroundColor = %q, want %q", webPrompts[1].BackgroundColor, "#FF0000")
	}
}

func TestPromptsToWebPrompts_Empty(t *testing.T) {
	webPrompts := PromptsToWebPrompts(nil)
	if webPrompts != nil {
		t.Errorf("PromptsToWebPrompts(nil) = %v, want nil", webPrompts)
	}

	webPrompts = PromptsToWebPrompts([]*PromptFile{})
	if webPrompts != nil {
		t.Errorf("PromptsToWebPrompts([]) = %v, want nil", webPrompts)
	}
}

func TestParsePromptFile_WithACPs(t *testing.T) {
	data := []byte(`name: "Claude Only Prompt"
enabledWhen: 'ACP.MatchesServerType("claude-code")'
prompt: |
  This prompt is only for Claude Code.
`)

	prompt, err := ParsePromptFile("claude-only.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Claude Only Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Claude Only Prompt")
	}
	want := `ACP.MatchesServerType("claude-code")`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestParsePromptFile_WithMultipleACPs(t *testing.T) {
	data := []byte(`name: "Multi ACP Prompt"
enabledWhen: 'ACP.MatchesServerType(["auggie", "claude-code", "custom-acp"])'
prompt: |
  This prompt works with multiple ACPs.
`)

	prompt, err := ParsePromptFile("multi-acp.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	want := `ACP.MatchesServerType(["auggie", "claude-code", "custom-acp"])`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestParsePromptFile_WithGroup(t *testing.T) {
	data := []byte(`name: "Test Prompt"
description: "A test prompt"
group: "Testing"
backgroundColor: "#E8F5E9"
prompt: |
  This is a test prompt with a group.
`)

	prompt, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Test Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Test Prompt")
	}
	if prompt.Group != "Testing" {
		t.Errorf("Group = %q, want %q", prompt.Group, "Testing")
	}
	if prompt.Description != "A test prompt" {
		t.Errorf("Description = %q, want %q", prompt.Description, "A test prompt")
	}
}

func TestParsePromptFile_WithMenus(t *testing.T) {
	data := []byte(`name: "Context Prompt"
group: "Workflow"
menus: conversation
prompt: |
  This prompt appears in the conversation context menu.
`)

	prompt, err := ParsePromptFile("context.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Context Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Context Prompt")
	}
	if prompt.Group != "Workflow" {
		t.Errorf("Group = %q, want %q", prompt.Group, "Workflow")
	}
	if prompt.Menus != "conversation" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "conversation")
	}

	// The menus field must survive conversion to WebPrompt for the API response.
	wp := prompt.ToWebPrompt()
	if wp.Menus != "conversation" {
		t.Errorf("WebPrompt.Menus = %q, want %q", wp.Menus, "conversation")
	}
}

func TestParsePromptFile_WithMultipleMenus(t *testing.T) {
	data := []byte(`name: "Multi Menu Prompt"
menus: "conversation, group"
prompt: |
  This prompt appears in multiple menus.
`)

	prompt, err := ParsePromptFile("multi.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Menus != "conversation, group" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "conversation, group")
	}
}

func TestParsePromptFile_WithoutMenus(t *testing.T) {
	data := []byte(`name: "Plain Prompt"
prompt: |
  A prompt without a menus attribute.
`)

	prompt, err := ParsePromptFile("plain.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Menus != "" {
		t.Errorf("Menus = %q, want empty", prompt.Menus)
	}
}

func TestIsSpecificToACP(t *testing.T) {
	tests := []struct {
		name        string
		enabledWhen string
		acpServer   string
		want        bool
	}{
		{"empty enabledWhen is not specific", "", "auggie", false},
		{"empty ACP server", `ACP.MatchesServerType("auggie")`, "", false},
		{"exact match single", `ACP.MatchesServerType("auggie")`, "auggie", true},
		{"case insensitive match", `ACP.MatchesServerType("Auggie")`, "auggie", true},
		{"no match", `ACP.MatchesServerType("claude-code")`, "auggie", false},
		{"multiple ACPs with match", `ACP.MatchesServerType(["claude-code", "auggie"])`, "auggie", true},
		{"multiple ACPs without match", `ACP.MatchesServerType(["claude-code", "other"])`, "auggie", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PromptFile{EnabledWhen: tt.enabledWhen}
			got := p.IsSpecificToACP(tt.acpServer)
			if got != tt.want {
				t.Errorf("IsSpecificToACP(%q) = %v, want %v", tt.acpServer, got, tt.want)
			}
		})
	}
}

func TestCollectRequiredToolPatterns(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "P1", EnabledWhen: `Tools.HasAllPatterns(["jira_*", "slack_*"])`},
		{Name: "P2", EnabledWhen: `Tools.HasAllPatterns(["jira_*", "github_*"])`},
		{Name: "P3", EnabledWhen: ""},
		{Name: "P4", EnabledWhen: `Tools.HasPattern("slack_*")`},
	}

	patterns := CollectRequiredToolPatterns(prompts)

	// Should have jira_*, slack_*, github_* (deduplicated)
	if len(patterns) != 3 {
		t.Errorf("CollectRequiredToolPatterns returned %d patterns, want 3: %v", len(patterns), patterns)
	}

	seen := make(map[string]bool)
	for _, p := range patterns {
		seen[p] = true
	}

	for _, expected := range []string{"jira_*", "slack_*", "github_*"} {
		if !seen[expected] {
			t.Errorf("CollectRequiredToolPatterns missing pattern %q", expected)
		}
	}
}

func TestCollectRequiredToolPatterns_Empty(t *testing.T) {
	// All prompts have no required tools
	prompts := []*PromptFile{
		{Name: "P1", EnabledWhen: ""},
		{Name: "P2"},
	}
	patterns := CollectRequiredToolPatterns(prompts)
	if len(patterns) != 0 {
		t.Errorf("CollectRequiredToolPatterns with no required tools returned %v, want empty", patterns)
	}

	// Nil slice
	patterns = CollectRequiredToolPatterns(nil)
	if len(patterns) != 0 {
		t.Errorf("CollectRequiredToolPatterns(nil) returned %v, want empty", patterns)
	}
}

func TestParsePromptFile_WithEnabledWhenTools(t *testing.T) {
	data := []byte(`name: "Jira Prompt"
enabledWhen: 'Tools.HasAllPatterns(["jira_*", "slack_*"])'
prompt: |
  This prompt requires Jira and Slack tools.
`)

	prompt, err := ParsePromptFile("jira-prompt.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Jira Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Jira Prompt")
	}
	want := `Tools.HasAllPatterns(["jira_*", "slack_*"])`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestToWebPrompt_IncludesEnabledWhen(t *testing.T) {
	prompt := &PromptFile{
		Name:        "Test",
		Content:     "Content here",
		EnabledWhen: `ACP.MatchesServerType("auggie") && Tools.HasAllPatterns(["jira_*", "slack_*"])`,
	}

	wp := prompt.ToWebPrompt()

	want := `ACP.MatchesServerType("auggie") && Tools.HasAllPatterns(["jira_*", "slack_*"])`
	if wp.EnabledWhen != want {
		t.Errorf("WebPrompt.EnabledWhen = %q, want %q", wp.EnabledWhen, want)
	}
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
}

func TestFilterPromptsSpecificToACP(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "All ACPs", EnabledWhen: ""},
		{Name: "Claude Only", EnabledWhen: `ACP.MatchesServerType("claude-code")`},
		{Name: "Auggie Only", EnabledWhen: `ACP.MatchesServerType("auggie")`},
		{Name: "Both", EnabledWhen: `ACP.MatchesServerType(["claude-code", "auggie"])`},
	}

	// Filter for auggie - should only get prompts with explicit acp filter in enabledWhen
	filtered := FilterPromptsSpecificToACP(prompts, "auggie")
	if len(filtered) != 2 {
		t.Errorf("FilterPromptsSpecificToACP(auggie) returned %d prompts, want 2", len(filtered))
	}

	// Check that "All ACPs" and "Claude Only" are not in the filtered list
	for _, p := range filtered {
		if p.Name == "All ACPs" {
			t.Error("FilterPromptsSpecificToACP(auggie) should not include 'All ACPs' (generic prompt)")
		}
		if p.Name == "Claude Only" {
			t.Error("FilterPromptsSpecificToACP(auggie) should not include 'Claude Only'")
		}
	}

	// Filter for claude-code
	filtered = FilterPromptsSpecificToACP(prompts, "claude-code")
	if len(filtered) != 2 {
		t.Errorf("FilterPromptsSpecificToACP(claude-code) returned %d prompts, want 2", len(filtered))
	}

	// Empty ACP server should return nil
	filtered = FilterPromptsSpecificToACP(prompts, "")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP('') = %v, want nil", filtered)
	}

	// Empty prompts should return nil
	filtered = FilterPromptsSpecificToACP([]*PromptFile{}, "auggie")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP on empty slice = %v, want nil", filtered)
	}

	// Nil prompts should return nil
	filtered = FilterPromptsSpecificToACP(nil, "auggie")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP(nil) = %v, want nil", filtered)
	}
}

func TestParsePromptFile_WithLoop_OnCompletion(t *testing.T) {
	data := []byte(`name: "On Completion Prompt"
loop:
  trigger: [onCompletion]
  onCompletion:
    delay: 10
  maxDuration: "2h"
prompt: |
  Fire after agent stops.
`)

	prompt, err := ParsePromptFile("on-completion.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if !prompt.Loop.hasTrigger("onCompletion") {
		t.Errorf("Loop.Trigger = %v, want to include %q", prompt.Loop.Trigger, "onCompletion")
	}
	if prompt.Loop.CompletionDelay() != 10 {
		t.Errorf("Loop.CompletionDelay() = %d, want 10", prompt.Loop.CompletionDelay())
	}
	if prompt.Loop.MaxDuration != "2h" {
		t.Errorf("Loop.MaxDuration = %q, want %q", prompt.Loop.MaxDuration, "2h")
	}
	// value/unit absent → zero values
	if prompt.Loop.FrequencyValue() != 0 {
		t.Errorf("Loop.FrequencyValue() = %d, want 0 (not set)", prompt.Loop.FrequencyValue())
	}
	if prompt.Loop.FrequencyUnit() != "" {
		t.Errorf("Loop.FrequencyUnit() = %q, want empty (not set)", prompt.Loop.FrequencyUnit())
	}
}

func TestParsePromptFile_WithLoop_ScheduleNoTrigger(t *testing.T) {
	data := []byte(`name: "Schedule Prompt"
loop:
  schedule:
    value: 2
    unit: hours
  maxIterations: 5
prompt: |
  Run every 2 hours.
`)

	prompt, err := ParsePromptFile("schedule.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	// Trigger absent → defaults to ["schedule"]
	if got := prompt.Loop.Triggers(); len(got) != 1 || got[0] != "schedule" {
		t.Errorf("Loop.Triggers() = %v, want [schedule] (default)", got)
	}
	if prompt.Loop.FrequencyValue() != 2 {
		t.Errorf("Loop.FrequencyValue() = %d, want 2", prompt.Loop.FrequencyValue())
	}
	if prompt.Loop.FrequencyUnit() != "hours" {
		t.Errorf("Loop.FrequencyUnit() = %q, want %q", prompt.Loop.FrequencyUnit(), "hours")
	}
	if prompt.Loop.MaxIterations != 5 {
		t.Errorf("Loop.MaxIterations = %d, want 5", prompt.Loop.MaxIterations)
	}
	if prompt.Loop.CompletionDelay() != 0 {
		t.Errorf("Loop.CompletionDelay() = %d, want 0 (not set)", prompt.Loop.CompletionDelay())
	}
	if prompt.Loop.MaxDuration != "" {
		t.Errorf("Loop.MaxDuration = %q, want empty (not set)", prompt.Loop.MaxDuration)
	}
}

func TestToWebPrompt_OnCompletion_JSONRoundTrip(t *testing.T) {
	pf := &PromptFile{
		Name:    "On Completion",
		Content: "body",
		Loop: &PromptLoop{
			Trigger:      []string{"onCompletion"},
			OnCompletion: &PromptLoopOnCompletion{Delay: 10},
			MaxDuration:  "2h",
		},
	}

	wp := pf.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil")
	}

	raw, err := json.Marshal(wp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(raw)

	if !strings.Contains(jsonStr, `"trigger":["onCompletion"]`) {
		t.Errorf("JSON missing trigger field; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"delay":10`) {
		t.Errorf("JSON missing delay field; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"maxDuration":"2h"`) {
		t.Errorf("JSON missing maxDuration field; got: %s", jsonStr)
	}

	// Also verify via struct fields.
	if !wp.Loop.hasTrigger("onCompletion") {
		t.Errorf("WebPrompt.Loop.Trigger = %v, want to include %q", wp.Loop.Trigger, "onCompletion")
	}
	if wp.Loop.CompletionDelay() != 10 {
		t.Errorf("WebPrompt.Loop.CompletionDelay() = %d, want 10", wp.Loop.CompletionDelay())
	}
	if wp.Loop.MaxDuration != "2h" {
		t.Errorf("WebPrompt.Loop.MaxDuration = %q, want %q", wp.Loop.MaxDuration, "2h")
	}
}

func TestParsePromptFile_WithLoop_OptionalDefaultFalse(t *testing.T) {
	data := []byte(`name: "Optional Loop"
loop:
  mode: optional
  default: false
  trigger: [onCompletion]
prompt: |
  Maybe run periodically.
`)

	prompt, err := ParsePromptFile("optional.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.Mode != "optional" {
		t.Errorf("Loop.Mode = %q, want %q", prompt.Loop.Mode, "optional")
	}
	if prompt.Loop.Default == nil || *prompt.Loop.Default != false {
		t.Errorf("Loop.Default = %v, want *false", prompt.Loop.Default)
	}

	// Round-trips through ToWebPrompt (whole-pointer copy).
	wp := prompt.ToWebPrompt()
	if wp.Loop == nil {
		t.Fatal("WebPrompt.Loop = nil, want non-nil")
	}
	if wp.Loop.Mode != "optional" {
		t.Errorf("WebPrompt.Loop.Mode = %q, want %q", wp.Loop.Mode, "optional")
	}
	if wp.Loop.Default == nil || *wp.Loop.Default != false {
		t.Errorf("WebPrompt.Loop.Default = %v, want *false", wp.Loop.Default)
	}

	raw, err := json.Marshal(wp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"mode":"optional"`) {
		t.Errorf("JSON missing mode field; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"default":false`) {
		t.Errorf("JSON missing default field; got: %s", jsonStr)
	}
}

func TestParsePromptFile_WithLoop_NoMode(t *testing.T) {
	data := []byte(`name: "Always Loop"
loop:
  schedule:
    value: 1
    unit: hours
prompt: |
  Always runs periodically.
`)

	prompt, err := ParsePromptFile("always.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if prompt.Loop.Mode != "" {
		t.Errorf("Loop.Mode = %q, want empty (absent => treated as always)", prompt.Loop.Mode)
	}
	if prompt.Loop.Default != nil {
		t.Errorf("Loop.Default = %v, want nil (absent)", prompt.Loop.Default)
	}
}

func TestParsePromptFile_LoopUnknownMode(t *testing.T) {
	data := []byte(`name: "Bad Mode"
loop:
  mode: sometimes
prompt: |
  body
`)

	_, err := ParsePromptFile("bad-mode.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for unknown loop.mode, got nil error")
	}
	if !strings.Contains(err.Error(), "loop.mode") {
		t.Errorf("error = %q, want it to mention 'loop.mode'", err.Error())
	}
	if !strings.Contains(err.Error(), "sometimes") {
		t.Errorf("error = %q, want it to mention the invalid value 'sometimes'", err.Error())
	}
}

// ---- mitto-r6j.1: grouped multi-trigger loop schema ----

// TestPromptLoop_UnmarshalYAML_RejectsLegacyFlatKeys pins the strict
// UnmarshalYAML added for mitto-r6j.1 as a defense-in-depth layer: decoding
// a loop: mapping directly into a *PromptLoop (bypassing the mitto-r6j.3
// migration registry that now runs ahead of it in ParsePromptFile) still
// fails with an error naming the offending key, its new nested path, and the
// migration bead — yaml.v3 would otherwise silently ignore the unknown key
// and parse a stale flat-form loop: block into an all-zero grouped struct.
// The end-to-end ParsePromptFile path is covered separately by
// TestParsePromptFile_MigratesLegacyLoopSchema, since mitto-r6j.3 made that
// path migrate-then-WARN instead of erroring.
func TestPromptLoop_UnmarshalYAML_RejectsLegacyFlatKeys(t *testing.T) {
	for legacyKey, newPath := range legacyPromptLoopFlatKeys {
		t.Run(legacyKey, func(t *testing.T) {
			data := []byte("mode: always\n" + legacyKey + ": whatever\n")
			var loop PromptLoop
			err := yaml.Unmarshal(data, &loop)
			if err == nil {
				t.Fatalf("UnmarshalYAML should fail for legacy flat key %q, got nil error", legacyKey)
			}
			if !strings.Contains(err.Error(), "loop."+legacyKey) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), "loop."+legacyKey)
			}
			if !strings.Contains(err.Error(), "loop."+newPath) {
				t.Errorf("error = %q, want it to mention the new nested path %q", err.Error(), "loop."+newPath)
			}
			if !strings.Contains(err.Error(), "mitto-r6j.3") {
				t.Errorf("error = %q, want it to mention the migration bead mitto-r6j.3", err.Error())
			}
		})
	}
}

// TestParsePromptFile_MigratesLegacyLoopSchema pins the mitto-r6j.3 behavior
// change: ParsePromptFile now runs the migration registry ahead of
// PromptLoop.UnmarshalYAML, so a prompt file still on the pre-r6j flat loop
// schema loads successfully (migrated in memory) instead of hard-failing.
func TestParsePromptFile_MigratesLegacyLoopSchema(t *testing.T) {
	data := []byte(`name: "Legacy Loop"
loop:
  trigger: onCompletion
  delay: 30
  maxIterations: 10
prompt: |
  body
`)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	prompt, err := ParsePromptFile("legacy-loop.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile should migrate the legacy schema, got error: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if !prompt.Loop.hasTrigger("onCompletion") {
		t.Errorf("Loop.Trigger = %v, want it to contain onCompletion", prompt.Loop.Trigger)
	}
	if prompt.Loop.CompletionDelay() != 30 {
		t.Errorf("Loop.CompletionDelay() = %d, want 30", prompt.Loop.CompletionDelay())
	}
	if prompt.Loop.MaxIterations != 10 {
		t.Errorf("Loop.MaxIterations = %d, want 10", prompt.Loop.MaxIterations)
	}
	if !strings.Contains(logBuf.String(), "0001-loop-grouped-triggers") {
		t.Errorf("expected a WARN naming the migration ID, log = %q", logBuf.String())
	}
}

// TestLoadPromptFile_MigratesLegacyLoopSchema_WritesBackToDisk closes the gap
// between TestParsePromptFile_MigratesLegacyLoopSchema (in-memory parse only)
// and migrate's own WriteBackIfNeeded tests (isolated from the load path): it
// drives a legacy flat-schema prompt file through LoadPromptFile end-to-end
// and asserts the on-disk bytes are actually rewritten to the grouped form,
// a second load is a no-op (idempotent, mtime untouched), and an
// already-grouped file is never written to (mitto-p10q).
func TestLoadPromptFile_MigratesLegacyLoopSchema_WritesBackToDisk(t *testing.T) {
	dir := t.TempDir()
	const relPath = "legacy-loop.prompt.yaml"
	fullPath := filepath.Join(dir, relPath)

	original := []byte(`name: "Legacy Loop"
loop:
  # a hand-written comment on the trigger line
  trigger: onCompletion
  delay: 30
  maxIterations: 10
prompt: |
  body
`)
	if err := os.WriteFile(fullPath, original, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prompt, err := LoadPromptFile(dir, relPath)
	if err != nil {
		t.Fatalf("LoadPromptFile() error = %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	if !prompt.Loop.hasTrigger("onCompletion") {
		t.Errorf("Loop.Trigger = %v, want it to contain onCompletion", prompt.Loop.Trigger)
	}

	rewritten, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("ReadFile() after load error = %v", err)
	}
	if bytes.Equal(rewritten, original) {
		t.Error("on-disk bytes unchanged after loading a legacy-schema file; want the loop: block rewritten to the grouped form")
	}
	if !bytes.Contains(rewritten, []byte(`trigger: [onCompletion]`)) || !bytes.Contains(rewritten, []byte(`onCompletion:`)) {
		t.Errorf("rewritten bytes do not look like the grouped trigger-list form:\n%s", rewritten)
	}
	if !bytes.Contains(rewritten, []byte("a hand-written comment on the trigger line")) {
		t.Errorf("rewritten bytes lost the hand-written comment:\n%s", rewritten)
	}
	if !bytes.Contains(rewritten, []byte(`name: "Legacy Loop"`)) || !bytes.Contains(rewritten, []byte("body")) {
		t.Errorf("rewritten bytes lost unrelated top-level content:\n%s", rewritten)
	}

	info1, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("Stat() after first load error = %v", err)
	}

	// Second load: the file is now already on the grouped schema, so this
	// must be a true no-op — same bytes, mtime untouched.
	prompt2, err := LoadPromptFile(dir, relPath)
	if err != nil {
		t.Fatalf("LoadPromptFile() second load error = %v", err)
	}
	if !prompt2.Loop.hasTrigger("onCompletion") {
		t.Errorf("second load: Loop.Trigger = %v, want it to contain onCompletion", prompt2.Loop.Trigger)
	}

	afterSecondLoad, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("ReadFile() after second load error = %v", err)
	}
	if !bytes.Equal(afterSecondLoad, rewritten) {
		t.Errorf("second load rewrote an already-grouped file:\nbefore=%s\nafter=%s", rewritten, afterSecondLoad)
	}

	info2, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("Stat() after second load error = %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("mtime changed on an already-grouped file: before=%v after=%v", info1.ModTime(), info2.ModTime())
	}
}

// TestPromptLoop_UnmarshalYAML_RejectsUnknownKeys pins the strict-key rule for
// mitto-r6j.1: an unrecognized key under loop: or under any per-trigger block
// must fail the parse rather than being silently dropped by yaml.v3.
func TestPromptLoop_UnmarshalYAML_RejectsUnknownKeys(t *testing.T) {
	cases := []struct {
		name     string
		loopYAML string
		wantMsg  string
	}{
		{
			name:     "top-level",
			loopYAML: "  trigger: [schedule]\n  bogusTop: 3\n",
			wantMsg:  "loop.bogusTop is not a known key",
		},
		{
			name:     "schedule block",
			loopYAML: "  trigger: [schedule]\n  schedule:\n    value: 1\n    unit: hours\n    bogus: 7\n",
			wantMsg:  "loop.schedule.bogus is not a known key",
		},
		{
			name:     "onCompletion block",
			loopYAML: "  trigger: [onCompletion]\n  onCompletion:\n    delay: 30\n    bogus: 7\n",
			wantMsg:  "loop.onCompletion.bogus is not a known key",
		},
		{
			name:     "onTasks block",
			loopYAML: "  trigger: [onTasks]\n  onTasks:\n    cooldown: 5\n    bogus: 7\n",
			wantMsg:  "loop.onTasks.bogus is not a known key",
		},
		{
			name:     "onChild block",
			loopYAML: "  trigger: [onChild, schedule]\n  onChild:\n    when: [anyEndResponse]\n    bogus: 7\n",
			wantMsg:  "loop.onChild.bogus is not a known key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("name: \"Bad Loop\"\nloop:\n" + tc.loopYAML + "prompt: |\n  body\n")
			_, err := ParsePromptFile("bad-loop.prompt.yaml", data, time.Now())
			if err == nil {
				t.Fatalf("ParsePromptFile should fail for unknown key, got nil error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// TestParsePromptFile_WithLoop_MultipleTriggers verifies that a loop:
// block can declare more than one simultaneous trigger (mitto-r6j.1), and
// that each trigger's own nested block is parsed independently.
func TestParsePromptFile_WithLoop_MultipleTriggers(t *testing.T) {
	data := []byte(`name: "Multi Trigger"
loop:
  trigger: [schedule, onCompletion]
  schedule:
    value: 1
    unit: hours
  onCompletion:
    delay: 30
prompt: |
  Fire on a timer AND after every turn.
`)

	prompt, err := ParsePromptFile("multi-trigger.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Loop == nil {
		t.Fatal("Loop = nil, want non-nil")
	}
	got := prompt.Loop.Triggers()
	if len(got) != 2 || got[0] != "schedule" || got[1] != "onCompletion" {
		t.Errorf("Loop.Triggers() = %v, want [schedule onCompletion]", got)
	}
	if !prompt.Loop.hasTrigger("schedule") || !prompt.Loop.hasTrigger("onCompletion") {
		t.Errorf("Loop.hasTrigger: want both schedule and onCompletion present, Trigger=%v", prompt.Loop.Trigger)
	}
	if prompt.Loop.hasTrigger("onTasks") {
		t.Error("Loop.hasTrigger(onTasks) = true, want false (not declared)")
	}
	if prompt.Loop.FrequencyValue() != 1 || prompt.Loop.FrequencyUnit() != "hours" {
		t.Errorf("Loop.Frequency = (%d, %q), want (1, hours)", prompt.Loop.FrequencyValue(), prompt.Loop.FrequencyUnit())
	}
	if prompt.Loop.CompletionDelay() != 30 {
		t.Errorf("Loop.CompletionDelay() = %d, want 30", prompt.Loop.CompletionDelay())
	}

	// Must also validate cleanly — two known, non-duplicate triggers is valid.
	if err := ValidatePromptLoop(prompt.Name, prompt.Loop); err != nil {
		t.Errorf("ValidatePromptLoop unexpected error: %v", err)
	}
}

// TestPromptLoop_YAMLRoundTrip pins the mitto-r6j.1 acceptance criterion
// "new form round-trips YAML -> struct -> YAML": marshal a populated
// PromptLoop (every trigger block set) to YAML, unmarshal it back, and
// verify every field survives the round trip unchanged.
func TestPromptLoop_YAMLRoundTrip(t *testing.T) {
	settle := 10
	coalesce := false
	orig := &PromptLoop{
		Trigger:  []string{"schedule", "onCompletion", "onTasks", "onChild"},
		Schedule: &PromptLoopSchedule{Value: 2, Unit: "days", At: "09:00"},
		OnCompletion: &PromptLoopOnCompletion{
			Delay: 45,
		},
		OnTasks: &PromptLoopOnTasks{
			Condition:          `tasks.exists(t, t.status == "open")`,
			ConditionPreset:    "any-open",
			CoalesceDuringBusy: &coalesce,
			SettleWindow:       settle,
			Cooldown:           120,
		},
		OnChild: &PromptLoopOnChild{
			When: []string{"anyEndResponse", "anyDeleted"},
		},
		MaxIterations: 10,
		MaxDuration:   "4h",
		Mode:          PromptLoopModeOptional,
	}

	raw, err := yaml.Marshal(orig)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var roundTripped PromptLoop
	if err := yaml.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v\nYAML was:\n%s", err, raw)
	}

	if got := roundTripped.Triggers(); len(got) != 4 || got[0] != "schedule" || got[1] != "onCompletion" || got[2] != "onTasks" || got[3] != "onChild" {
		t.Errorf("Triggers() = %v, want [schedule onCompletion onTasks onChild]", got)
	}
	if roundTripped.FrequencyValue() != 2 || roundTripped.FrequencyUnit() != "days" || roundTripped.FrequencyAt() != "09:00" {
		t.Errorf("Schedule = (%d, %q, %q), want (2, days, 09:00)",
			roundTripped.FrequencyValue(), roundTripped.FrequencyUnit(), roundTripped.FrequencyAt())
	}
	if roundTripped.CompletionDelay() != 45 {
		t.Errorf("CompletionDelay() = %d, want 45", roundTripped.CompletionDelay())
	}
	if roundTripped.TasksCondition() != orig.OnTasks.Condition {
		t.Errorf("TasksCondition() = %q, want %q", roundTripped.TasksCondition(), orig.OnTasks.Condition)
	}
	if roundTripped.TasksConditionPreset() != "any-open" {
		t.Errorf("TasksConditionPreset() = %q, want %q", roundTripped.TasksConditionPreset(), "any-open")
	}
	if got := roundTripped.TasksCoalesceDuringBusy(); got == nil || *got != false {
		t.Errorf("TasksCoalesceDuringBusy() = %v, want *false", got)
	}
	if roundTripped.TasksSettleWindow() != 10 {
		t.Errorf("TasksSettleWindow() = %d, want 10", roundTripped.TasksSettleWindow())
	}
	if roundTripped.TasksCooldown() != 120 {
		t.Errorf("TasksCooldown() = %d, want 120", roundTripped.TasksCooldown())
	}
	if got := roundTripped.ChildEvents(); len(got) != 2 || got[0] != "anyEndResponse" || got[1] != "anyDeleted" {
		t.Errorf("ChildEvents() = %v, want [anyEndResponse anyDeleted]", got)
	}
	if roundTripped.MaxIterations != 10 {
		t.Errorf("MaxIterations = %d, want 10", roundTripped.MaxIterations)
	}
	if roundTripped.MaxDuration != "4h" {
		t.Errorf("MaxDuration = %q, want %q", roundTripped.MaxDuration, "4h")
	}
	if roundTripped.Mode != PromptLoopModeOptional {
		t.Errorf("Mode = %q, want %q", roundTripped.Mode, PromptLoopModeOptional)
	}

	// Also validate cleanly.
	if err := ValidatePromptLoop("round-trip", &roundTripped); err != nil {
		t.Errorf("ValidatePromptLoop unexpected error: %v", err)
	}
}

// TestValidateLoopTriggers pins every rule documented on ValidateLoopTriggers
// (mitto-r6j.1): unknown trigger value, duplicate trigger entries, and
// schedule.at requiring unit: days are hard errors naming the prompt and the
// offending value; a block present for a trigger not listed in Trigger is
// tolerated (inert, warning-only — verified by absence of an error).
func TestValidateLoopTriggers(t *testing.T) {
	t.Run("nil loop is OK", func(t *testing.T) {
		if err := ValidateLoopTriggers("p", nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown trigger value returns error naming prompt and value", func(t *testing.T) {
		err := ValidateLoopTriggers("My Prompt", &PromptLoop{Trigger: []string{"weekly"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "My Prompt") {
			t.Errorf("error = %q, want it to mention prompt name 'My Prompt'", err.Error())
		}
		if !strings.Contains(err.Error(), "weekly") {
			t.Errorf("error = %q, want it to mention the invalid value 'weekly'", err.Error())
		}
	})

	t.Run("duplicate trigger entries return error naming the duplicate", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{Trigger: []string{"schedule", "schedule"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("error = %q, want it to contain 'duplicate'", err.Error())
		}
		if !strings.Contains(err.Error(), "schedule") {
			t.Errorf("error = %q, want it to mention the duplicated value 'schedule'", err.Error())
		}
	})

	t.Run("multiple distinct known triggers are OK", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{Trigger: []string{"schedule", "onCompletion", "onTasks"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("schedule.at without unit days is an error", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"schedule"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "hours", At: "09:00"},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "schedule.at") {
			t.Errorf("error = %q, want it to mention 'schedule.at'", err.Error())
		}
	})

	t.Run("schedule.at with unit days is OK", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"schedule"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "days", At: "09:00"},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("block present for trigger not listed is inert, not an error", func(t *testing.T) {
		// onCompletion block declared but "onCompletion" absent from Trigger:
		// tolerated (matches today's tolerance for inert flat fields), only
		// logs a warning.
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:      []string{"schedule"},
			Schedule:     &PromptLoopSchedule{Value: 1, Unit: "hours"},
			OnCompletion: &PromptLoopOnCompletion{Delay: 10},
		})
		if err != nil {
			t.Errorf("unexpected error for inert onCompletion block: %v", err)
		}
	})

	t.Run("inert schedule block not listed in Trigger is tolerated", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"onCompletion"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "hours"},
		})
		if err != nil {
			t.Errorf("unexpected error for inert schedule block: %v", err)
		}
	})

	t.Run("inert onTasks block not listed in Trigger is tolerated", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"schedule"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "hours"},
			OnTasks:  &PromptLoopOnTasks{Condition: "true"},
		})
		if err != nil {
			t.Errorf("unexpected error for inert onTasks block: %v", err)
		}
	})

	t.Run("multiple inert blocks at once are all tolerated", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:      []string{"schedule"},
			Schedule:     &PromptLoopSchedule{Value: 1, Unit: "hours"},
			OnCompletion: &PromptLoopOnCompletion{Delay: 10},
			OnTasks:      &PromptLoopOnTasks{Condition: "true"},
		})
		if err != nil {
			t.Errorf("unexpected error for multiple inert blocks: %v", err)
		}
	})

	t.Run("mixed list with one valid and one unknown trigger errors naming the unknown one", func(t *testing.T) {
		err := ValidateLoopTriggers("My Prompt", &PromptLoop{Trigger: []string{"schedule", "hourly"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "hourly") {
			t.Errorf("error = %q, want it to mention the invalid value 'hourly'", err.Error())
		}
		if strings.Contains(err.Error(), `"schedule"`) {
			t.Errorf("error = %q, should not name the valid entry 'schedule' as invalid", err.Error())
		}
	})

	t.Run("empty trigger list is OK (defaults to schedule)", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// mitto-987y.2: onChild trigger and its When-list validation.

	t.Run("onChild with a co-armed trigger and valid when entries is OK", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger: []string{"schedule", "onChild"},
			OnChild: &PromptLoopOnChild{When: []string{"anyEndResponse", "anyDeleted"}},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("onChild with empty when list is OK (defaults at the session layer)", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger: []string{"schedule", "onChild"},
			OnChild: &PromptLoopOnChild{},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("onChild.when unknown entry returns error naming the entry", func(t *testing.T) {
		err := ValidateLoopTriggers("My Prompt", &PromptLoop{
			Trigger: []string{"schedule", "onChild"},
			OnChild: &PromptLoopOnChild{When: []string{"anyStarted"}},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "onChild.when") {
			t.Errorf("error = %q, want it to mention 'onChild.when'", err.Error())
		}
		if !strings.Contains(err.Error(), "anyStarted") {
			t.Errorf("error = %q, want it to mention the invalid value 'anyStarted'", err.Error())
		}
	})

	t.Run("onChild as the sole trigger is an error", func(t *testing.T) {
		err := ValidateLoopTriggers("My Prompt", &PromptLoop{
			Trigger: []string{"onChild"},
			OnChild: &PromptLoopOnChild{When: []string{"anyEndResponse"}},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "onChild cannot be the only trigger") {
			t.Errorf("error = %q, want it to mention onChild cannot be the only trigger", err.Error())
		}
	})

	t.Run("inert onChild block not listed in Trigger is tolerated", func(t *testing.T) {
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"schedule"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "hours"},
			OnChild:  &PromptLoopOnChild{When: []string{"anyEndResponse"}},
		})
		if err != nil {
			t.Errorf("unexpected error for inert onChild block: %v", err)
		}
	})

	t.Run("inert onChild block still validates its when entries", func(t *testing.T) {
		// Even though the block is inert (onChild not armed), a malformed
		// when entry is still a hard error — validated unconditionally.
		err := ValidateLoopTriggers("p", &PromptLoop{
			Trigger:  []string{"schedule"},
			Schedule: &PromptLoopSchedule{Value: 1, Unit: "hours"},
			OnChild:  &PromptLoopOnChild{When: []string{"bogus"}},
		})
		if err == nil {
			t.Fatal("expected error for invalid when entry even in an inert block, got nil")
		}
		if !strings.Contains(err.Error(), "bogus") {
			t.Errorf("error = %q, want it to mention the invalid value 'bogus'", err.Error())
		}
	})
}

func TestValidatePromptLoop(t *testing.T) {
	t.Run("nil loop is OK", func(t *testing.T) {
		if err := ValidatePromptLoop("p", nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty mode is OK (treated as always)", func(t *testing.T) {
		if err := ValidatePromptLoop("p", &PromptLoop{}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("mode=always is OK", func(t *testing.T) {
		if err := ValidatePromptLoop("p", &PromptLoop{Mode: "always"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("mode=optional is OK", func(t *testing.T) {
		if err := ValidatePromptLoop("p", &PromptLoop{Mode: "optional"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown mode returns error mentioning prompt name and value", func(t *testing.T) {
		err := ValidatePromptLoop("My Prompt", &PromptLoop{Mode: "bogus"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "My Prompt") {
			t.Errorf("error = %q, want it to mention prompt name 'My Prompt'", err.Error())
		}
		if !strings.Contains(err.Error(), "bogus") {
			t.Errorf("error = %q, want it to mention the invalid value 'bogus'", err.Error())
		}
	})

	t.Run("default set with mode=always does not error (warning only)", func(t *testing.T) {
		f := false
		if err := ValidatePromptLoop("p", &PromptLoop{Mode: "always", Default: &f}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("default set with mode absent does not error (warning only)", func(t *testing.T) {
		tr := true
		if err := ValidatePromptLoop("p", &PromptLoop{Default: &tr}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("default set with mode=optional does not error and does not warn", func(t *testing.T) {
		f := false
		if err := ValidatePromptLoop("p", &PromptLoop{Mode: "optional", Default: &f}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// ---- ValidatePromptTarget ----

func TestValidatePromptTarget(t *testing.T) {
	t.Run("nil target is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", nil, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty target is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("title without reuseTitle is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{Title: "Weekly triage"}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.title with title is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{Title: "Weekly triage", Reuse: &PromptTargetReuse{Title: true}}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.title without title errors", func(t *testing.T) {
		err := ValidatePromptTarget("My Prompt", &PromptTarget{Reuse: &PromptTargetReuse{Title: true}}, false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "My Prompt") || !strings.Contains(msg, "reuse.title") {
			t.Errorf("error should mention prompt name and reuse.title: %v", err)
		}
	})

	t.Run("reuse.title with whitespace-only title errors", func(t *testing.T) {
		err := ValidatePromptTarget("p", &PromptTarget{Title: "   ", Reuse: &PromptTargetReuse{Title: true}}, false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("reuse.issue with title and no reuse.title is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{Title: "x", Reuse: &PromptTargetReuse{Issue: true}}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// --- Reuse.Coalesce validation (mitto-djs1; nested under target.reuse in
	// mitto-6b3). Coalesce is a modifier that requires at least one reuse
	// mode (Reuse.Issue, Reuse.Title, or the containing prompt's top-level
	// singleton flag). Nil / *false is always accepted since coalescing is
	// opt-in and off by default. ---

	t.Run("reuse.coalesce nil is valid without any reuse mode", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.coalesce false is valid without any reuse mode", func(t *testing.T) {
		off := false
		if err := ValidatePromptTarget("p", &PromptTarget{Reuse: &PromptTargetReuse{Coalesce: &off}}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.coalesce true without any reuse mode errors", func(t *testing.T) {
		on := true
		err := ValidatePromptTarget("My Prompt", &PromptTarget{Reuse: &PromptTargetReuse{Coalesce: &on}}, false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "My Prompt") || !strings.Contains(msg, "reuse.coalesce") {
			t.Errorf("error should mention prompt name and reuse.coalesce: %v", err)
		}
	})

	t.Run("reuse.coalesce true with reuse.title+title is valid", func(t *testing.T) {
		on := true
		tgt := &PromptTarget{Title: "Weekly triage", Reuse: &PromptTargetReuse{Title: true, Coalesce: &on}}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.coalesce true with reuse.issue is valid", func(t *testing.T) {
		on := true
		tgt := &PromptTarget{Reuse: &PromptTargetReuse{Issue: true, Coalesce: &on}}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reuse.coalesce true with promptSingleton is valid", func(t *testing.T) {
		on := true
		tgt := &PromptTarget{Reuse: &PromptTargetReuse{Coalesce: &on}}
		if err := ValidatePromptTarget("p", tgt, true); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// mitto-5qbo: target.title supports Go text/template syntax.
	// ValidatePromptTarget must parse-check templated titles at load time so
	// broken frontmatter is rejected up-front (mirrors the body precompile).
	t.Run("title with valid template syntax is accepted", func(t *testing.T) {
		tgt := &PromptTarget{Title: "{{ .Args.IssueID }}: work", Reuse: &PromptTargetReuse{Title: true}}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("title with invalid template syntax is rejected", func(t *testing.T) {
		tgt := &PromptTarget{Title: "{{ .Args.X ", Reuse: &PromptTargetReuse{Title: true}}
		err := ValidatePromptTarget("p", tgt, false)
		if err == nil {
			t.Fatal("expected parse error for unbalanced action, got nil")
		}
		if !strings.Contains(err.Error(), "p.target.title") {
			t.Errorf("error should reference \"p.target.title\"; got %q", err.Error())
		}
	})

	t.Run("literal title (no template syntax) skips template validation", func(t *testing.T) {
		// A literal title that contains characters that would break as a
		// template is fine because the fast-path skips parsing.
		tgt := &PromptTarget{Title: "Weekly triage — Q3", Reuse: &PromptTargetReuse{Title: true}}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// --- BackgroundColor validation (mitto-8sk). Must be a `#RGB` or
	// `#RRGGBB` hex color, case-insensitive; empty/absent is always valid
	// (unset). Runs from ParsePromptFile so a typo is caught at load time. ---

	t.Run("backgroundColor empty is valid", func(t *testing.T) {
		if err := ValidatePromptTarget("p", &PromptTarget{BackgroundColor: ""}, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	validColors := []string{
		"#fff", "#FFF", "#123", "#abc",
		"#ffffff", "#FFFFFF", "#E1BEE7", "#e1bee7", "#123456", "#AbC123",
	}
	for _, c := range validColors {
		c := c
		t.Run("backgroundColor "+c+" is valid", func(t *testing.T) {
			if err := ValidatePromptTarget("p", &PromptTarget{BackgroundColor: c}, false); err != nil {
				t.Errorf("unexpected error for %q: %v", c, err)
			}
		})
	}

	invalidColors := []string{
		"red", "#12", "#GGGGGG", "#12345", "#1234567", "fff", "#",
	}
	for _, c := range invalidColors {
		c := c
		t.Run("backgroundColor "+c+" is rejected", func(t *testing.T) {
			err := ValidatePromptTarget("My Prompt", &PromptTarget{BackgroundColor: c}, false)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", c)
			}
			if !strings.Contains(err.Error(), "My Prompt") || !strings.Contains(err.Error(), "backgroundColor") {
				t.Errorf("error should mention prompt name and backgroundColor: %v", err)
			}
		})
	}

	// --- SuppressAutoChildren validation (mitto-nlx). The flag is a
	// create-time hint orthogonal to the reuse modes and has no cross-field
	// requirements, so every combination — alone, with title, with reuse
	// modes, alongside singleton — must be accepted. ---

	t.Run("suppressAutoChildren alone is valid", func(t *testing.T) {
		tgt := &PromptTarget{SuppressAutoChildren: true}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("suppressAutoChildren with title is valid", func(t *testing.T) {
		tgt := &PromptTarget{Title: "Cleanup", SuppressAutoChildren: true}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("suppressAutoChildren with reuse.issue is valid", func(t *testing.T) {
		tgt := &PromptTarget{
			Reuse:                &PromptTargetReuse{Issue: true},
			SuppressAutoChildren: true,
		}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("suppressAutoChildren with reuse.title+title is valid", func(t *testing.T) {
		tgt := &PromptTarget{
			Title:                "Weekly triage",
			Reuse:                &PromptTargetReuse{Title: true},
			SuppressAutoChildren: true,
		}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("suppressAutoChildren with promptSingleton is valid", func(t *testing.T) {
		tgt := &PromptTarget{SuppressAutoChildren: true}
		if err := ValidatePromptTarget("p", tgt, true); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// --- NoArchive validation (mitto-yvel.1). Like SuppressAutoChildren, the
	// flag is a create-time hint orthogonal to the reuse modes and to
	// SuppressAutoChildren itself, with no cross-field requirements, so every
	// combination must be accepted. ---

	t.Run("noArchive alone is valid", func(t *testing.T) {
		tgt := &PromptTarget{NoArchive: true}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive with title is valid", func(t *testing.T) {
		tgt := &PromptTarget{Title: "Cleanup", NoArchive: true}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive with reuse.issue is valid", func(t *testing.T) {
		tgt := &PromptTarget{
			Reuse:     &PromptTargetReuse{Issue: true},
			NoArchive: true,
		}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive with reuse.title+title is valid", func(t *testing.T) {
		tgt := &PromptTarget{
			Title:     "Weekly triage",
			Reuse:     &PromptTargetReuse{Title: true},
			NoArchive: true,
		}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive with suppressAutoChildren is valid", func(t *testing.T) {
		tgt := &PromptTarget{SuppressAutoChildren: true, NoArchive: true}
		if err := ValidatePromptTarget("p", tgt, false); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive with promptSingleton is valid", func(t *testing.T) {
		tgt := &PromptTarget{NoArchive: true}
		if err := ValidatePromptTarget("p", tgt, true); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("noArchive does not satisfy reuse.title's title requirement", func(t *testing.T) {
		tgt := &PromptTarget{
			Reuse:     &PromptTargetReuse{Title: true},
			NoArchive: true,
		}
		if err := ValidatePromptTarget("p", tgt, false); err == nil {
			t.Error("expected error for reuse.title with empty title")
		}
	})
}

// ---- PromptParameter / Parameters field tests ----

func TestIsKnownPromptParameterType(t *testing.T) {
	for _, known := range KnownPromptParameterTypes {
		if !IsKnownPromptParameterType(known) {
			t.Errorf("IsKnownPromptParameterType(%q) = false, want true", known)
		}
	}
	if IsKnownPromptParameterType("unknown") {
		t.Error("IsKnownPromptParameterType(\"unknown\") = true, want false")
	}
	if IsKnownPromptParameterType("") {
		t.Error("IsKnownPromptParameterType(\"\") = true, want false")
	}
	// boolean is a recognised type (rendered as a checkbox in the UI).
	if !IsKnownPromptParameterType("boolean") {
		t.Error("IsKnownPromptParameterType(\"boolean\") = false, want true")
	}
	// prompts is a recognised type (rendered as a picker in the parameter dialog).
	if !IsKnownPromptParameterType("prompts") {
		t.Error("IsKnownPromptParameterType(\"prompts\") = false, want true")
	}
}

func TestParsePromptFile_WithParameters(t *testing.T) {
	reqTrue := true
	data := []byte(`name: "Task Prompt"
parameters:
  - name: id
    type: beadsId
    description: the task ID
    required: true
  - name: folder
    type: workspaceFolder
prompt: |
  Work on ${id} in ${folder}.
`)

	prompt, err := ParsePromptFile("task.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}

	p0 := prompt.Parameters[0]
	if p0.Name != "id" {
		t.Errorf("Parameters[0].Name = %q, want %q", p0.Name, "id")
	}
	if p0.Type != "beadsId" {
		t.Errorf("Parameters[0].Type = %q, want %q", p0.Type, "beadsId")
	}
	if p0.Description != "the task ID" {
		t.Errorf("Parameters[0].Description = %q, want %q", p0.Description, "the task ID")
	}
	if p0.Required == nil || *p0.Required != reqTrue {
		t.Errorf("Parameters[0].Required = %v, want *true", p0.Required)
	}

	p1 := prompt.Parameters[1]
	if p1.Name != "folder" {
		t.Errorf("Parameters[1].Name = %q, want %q", p1.Name, "folder")
	}
	if p1.Type != "workspaceFolder" {
		t.Errorf("Parameters[1].Type = %q, want %q", p1.Type, "workspaceFolder")
	}
	if p1.Required != nil {
		t.Errorf("Parameters[1].Required = %v, want nil (absent)", p1.Required)
	}
}

// TestParsePromptFile_ShowField pins the `show:` YAML key onto
// PromptParameter.Show end-to-end (mitto-9rff) — a plain struct-field-copy
// unit test (TestParsePromptFile_WithParameters) doesn't exercise this field
// at all, and it is the headline schema change of this bead (`ask:` renamed
// and re-scoped to `show:`).
func TestParsePromptFile_ShowField(t *testing.T) {
	data := []byte(`name: "Show Prompt"
parameters:
  - name: Secret
    type: text
    show: never
  - name: Instructions
    type: text
    show: always
  - name: Note
    type: text
    show: auto
  - name: Default
    type: text
prompt: |
  body
`)

	prompt, err := ParsePromptFile("show.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 4 {
		t.Fatalf("len(Parameters) = %d, want 4", len(prompt.Parameters))
	}
	wantShow := map[string]string{
		"Secret":       ShowNever,
		"Instructions": ShowAlways,
		"Note":         ShowAuto,
		"Default":      "", // absent show: key parses as the empty string, treated as ShowAuto.
	}
	for _, p := range prompt.Parameters {
		want, ok := wantShow[p.Name]
		if !ok {
			t.Fatalf("unexpected parameter %q in parsed prompt", p.Name)
		}
		if p.Show != want {
			t.Errorf("Parameters[%q].Show = %q, want %q", p.Name, p.Show, want)
		}
	}

	// The field also survives the ToWebPrompt DTO conversion (a direct slice
	// copy today, but this pins the contract so a future refactor that adds
	// per-field copying can't silently drop Show).
	wp := prompt.ToWebPrompt()
	if len(wp.Parameters) != 4 || wp.Parameters[0].Show != ShowNever || wp.Parameters[1].Show != ShowAlways {
		t.Fatalf("ToWebPrompt did not preserve Show: %+v", wp.Parameters)
	}
}

func TestParsePromptFile_InvalidShowValue(t *testing.T) {
	data := []byte(`name: "Bad Show Prompt"
parameters:
  - name: foo
    type: text
    show: sometimes
prompt: |
  body
`)

	_, err := ParsePromptFile("bad-show.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for unknown show value, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown show value") {
		t.Errorf("error = %q, want it to mention 'unknown show value'", err.Error())
	}
}

func TestToWebPrompt_RoundTripsParameters(t *testing.T) {
	req := true
	pf := &PromptFile{
		Name:    "Param Prompt",
		Content: "body",
		Parameters: []PromptParameter{
			{Name: "id", Type: "beadsId", Description: "task id", Required: &req},
			{Name: "note", Type: "text"},
		},
	}

	wp := pf.ToWebPrompt()

	if len(wp.Parameters) != 2 {
		t.Fatalf("WebPrompt.Parameters len = %d, want 2", len(wp.Parameters))
	}
	if wp.Parameters[0].Name != "id" || wp.Parameters[0].Type != "beadsId" {
		t.Errorf("WebPrompt.Parameters[0] = %+v, want {id beadsId}", wp.Parameters[0])
	}
	if wp.Parameters[0].Required == nil || !*wp.Parameters[0].Required {
		t.Errorf("WebPrompt.Parameters[0].Required = %v, want *true", wp.Parameters[0].Required)
	}
	if wp.Parameters[1].Name != "note" || wp.Parameters[1].Type != "text" {
		t.Errorf("WebPrompt.Parameters[1] = %+v, want {note text}", wp.Parameters[1])
	}
}

func TestToWebPrompt_RoundTripsTags(t *testing.T) {
	pf := &PromptFile{
		Name:    "Tagged Prompt",
		Content: "body",
		Tags:    []string{"coding", "fast"},
	}

	wp := pf.ToWebPrompt()

	if len(wp.Tags) != 2 {
		t.Fatalf("WebPrompt.Tags len = %d, want 2", len(wp.Tags))
	}
	if wp.Tags[0] != "coding" || wp.Tags[1] != "fast" {
		t.Errorf("WebPrompt.Tags = %+v, want [coding fast]", wp.Tags)
	}

	pfNoTags := &PromptFile{
		Name:    "Untagged Prompt",
		Content: "body",
	}
	wpNoTags := pfNoTags.ToWebPrompt()
	if len(wpNoTags.Tags) != 0 {
		t.Errorf("WebPrompt.Tags = %+v, want empty/nil for PromptFile with no tags", wpNoTags.Tags)
	}
}

func TestParsePromptFile_UnknownParameterType(t *testing.T) {
	data := []byte(`name: "Bad Prompt"
parameters:
  - name: foo
    type: notAType
prompt: |
  body
`)

	_, err := ParsePromptFile("bad.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for unknown parameter type, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want it to mention 'unknown type'", err.Error())
	}
}

func TestParsePromptFile_EmptyParameterName(t *testing.T) {
	data := []byte(`name: "Bad Prompt"
parameters:
  - name: ""
    type: text
prompt: |
  body
`)

	_, err := ParsePromptFile("bad.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for empty parameter name, got nil error")
	}
	if !strings.Contains(err.Error(), "name must not be empty") {
		t.Errorf("error = %q, want it to mention 'name must not be empty'", err.Error())
	}
}

func TestParsePromptFile_NoParameters(t *testing.T) {
	data := []byte(`name: "Simple Prompt"
prompt: |
  No params here.
`)

	prompt, err := ParsePromptFile("simple.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 0 {
		t.Errorf("Parameters = %v, want empty", prompt.Parameters)
	}
}

func TestValidatePromptParameters(t *testing.T) {
	t.Run("empty name returns error containing 'name must not be empty'", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "", Type: "text"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "name must not be empty") {
			t.Errorf("error = %q, want it to contain 'name must not be empty'", err.Error())
		}
	})

	t.Run("unknown type returns error containing 'unknown type'", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "x", Type: "notAType"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown type") {
			t.Errorf("error = %q, want it to contain 'unknown type'", err.Error())
		}
	})

	t.Run("childSessionId with empty menus is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=prompts is OK", func(t *testing.T) {
		err := ValidatePromptParameters("prompts", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=conversation is OK", func(t *testing.T) {
		err := ValidatePromptParameters("conversation", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=prompts,conversation is OK", func(t *testing.T) {
		err := ValidatePromptParameters("prompts, conversation", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=beadsList returns error mentioning childSessionId and beadsList", func(t *testing.T) {
		err := ValidatePromptParameters("beadsList", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "childSessionId") {
			t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
		}
		if !strings.Contains(err.Error(), "beadsList") {
			t.Errorf("error = %q, want it to contain 'beadsList'", err.Error())
		}
	})

	t.Run("childSessionId with menus=conversation,beadsList returns error", func(t *testing.T) {
		err := ValidatePromptParameters("conversation, beadsList", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "childSessionId") {
			t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
		}
	})

	t.Run("non-childSessionId param with beadsList menus is OK", func(t *testing.T) {
		err := ValidatePromptParameters("beadsList", []PromptParameter{{Name: "x", Type: "text"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("show accepts empty, auto, always and never", func(t *testing.T) {
		for _, show := range []string{"", ShowAuto, ShowAlways, ShowNever} {
			err := ValidatePromptParameters("", []PromptParameter{{Name: "x", Type: "text", Show: show}})
			if err != nil {
				t.Errorf("show=%q: unexpected error: %v", show, err)
			}
		}
	})

	t.Run("show rejects an unknown value", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "x", Type: "text", Show: "sometimes"}})
		if err == nil {
			t.Fatal("expected error for unknown show value")
		}
		if !strings.Contains(err.Error(), "unknown show value") {
			t.Errorf("unexpected error text: %v", err)
		}
	})

	t.Run("boolean param is OK in any menu", func(t *testing.T) {
		for _, menus := range []string{"", "prompts", "conversation", "beadsIssues"} {
			err := ValidatePromptParameters(menus, []PromptParameter{{Name: "Commit", Type: "boolean"}})
			if err != nil {
				t.Errorf("menus=%q: unexpected error: %v", menus, err)
			}
		}
	})

	t.Run("prompts param is OK in any menu", func(t *testing.T) {
		for _, menus := range []string{"", "prompts", "conversation", "beadsIssues"} {
			err := ValidatePromptParameters(menus, []PromptParameter{{Name: "Target", Type: "prompts"}})
			if err != nil {
				t.Errorf("menus=%q: unexpected error: %v", menus, err)
			}
		}
	})

	t.Run("collectInnerArgs: false on a prompts param is OK", func(t *testing.T) {
		no := false
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Target", Type: "prompts", CollectInnerArgs: &no}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("collectInnerArgs: true on a prompts param is OK", func(t *testing.T) {
		yes := true
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Target", Type: "prompts", CollectInnerArgs: &yes}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("collectInnerArgs on a non-prompts param returns error mentioning collectInnerArgs and the type", func(t *testing.T) {
		no := false
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Notes", Type: "text", CollectInnerArgs: &no}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "collectInnerArgs") {
			t.Errorf("error = %q, want it to contain 'collectInnerArgs'", err.Error())
		}
		if !strings.Contains(err.Error(), "prompts") {
			t.Errorf("error = %q, want it to contain 'prompts'", err.Error())
		}
	})

	t.Run("multiLine on a text param is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Instructions", Type: "text", MultiLine: true}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("multiLine on a non-text param returns error mentioning multiLine and text", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Issue", Type: "beadsId", MultiLine: true}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "multiLine") {
			t.Errorf("error = %q, want it to contain 'multiLine'", err.Error())
		}
		if !strings.Contains(err.Error(), "text") {
			t.Errorf("error = %q, want it to contain 'text'", err.Error())
		}
	})

	t.Run("options on a text param is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"Simplification", "Cleanup"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("options with a matching default is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"a", "b"}, Default: "a"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("options on a non-text param returns error mentioning options and text", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Issue", Type: "beadsId", Options: []string{"a", "b"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "options") {
			t.Errorf("error = %q, want it to contain 'options'", err.Error())
		}
		if !strings.Contains(err.Error(), "text") {
			t.Errorf("error = %q, want it to contain 'text'", err.Error())
		}
	})

	t.Run("options combined with multiLine returns error mentioning both", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"a", "b"}, MultiLine: true}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "options") {
			t.Errorf("error = %q, want it to contain 'options'", err.Error())
		}
		if !strings.Contains(err.Error(), "multiLine") {
			t.Errorf("error = %q, want it to contain 'multiLine'", err.Error())
		}
	})

	t.Run("options containing an empty string returns error naming the parameter", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"a", ""}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "Kind") {
			t.Errorf("error = %q, want it to contain 'Kind'", err.Error())
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error = %q, want it to contain 'empty'", err.Error())
		}
	})

	t.Run("options containing duplicates returns error naming the duplicate", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"a", "b", "a"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("error = %q, want it to contain 'duplicate'", err.Error())
		}
		if !strings.Contains(err.Error(), `"a"`) {
			t.Errorf("error = %q, want it to contain the duplicate value \"a\"", err.Error())
		}
	})

	t.Run("default outside options returns error mentioning default and the parameter", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{"a", "b"}, Default: "c"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "default") {
			t.Errorf("error = %q, want it to contain 'default'", err.Error())
		}
		if !strings.Contains(err.Error(), "Kind") {
			t.Errorf("error = %q, want it to contain 'Kind'", err.Error())
		}
	})

	t.Run("empty options behaves as if absent (regression guard)", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "Kind", Type: "text", Options: []string{}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// Remember field — mitto-x8v.
	t.Run("remember field: accepts empty/never/folder/global; rejects unknown", func(t *testing.T) {
		cases := []struct {
			remember string
			wantErr  bool
		}{
			{"", false},
			{RememberNever, false},
			{RememberFolder, false},
			{RememberGlobal, false},
			{"sometimes", true},
			{"Folder", true}, // case-sensitive
		}
		for _, tc := range cases {
			err := ValidatePromptParameters("", []PromptParameter{{Name: "X", Type: "text", Remember: tc.remember}})
			if tc.wantErr && err == nil {
				t.Errorf("remember=%q: expected error, got nil", tc.remember)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("remember=%q: unexpected error: %v", tc.remember, err)
			}
		}
	})

	// filename param type — mitto-vlg.
	t.Run("filename param with no dir/glob is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("filename param with valid dir and glob is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Dir: "docs/instructions", Glob: []string{"*.md"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("filename param is OK in any menu (never gates)", func(t *testing.T) {
		for _, menus := range []string{"", "prompts", "conversation", "beadsIssues"} {
			err := ValidatePromptParameters(menus, []PromptParameter{{Name: "F", Type: "filename"}})
			if err != nil {
				t.Errorf("menus=%q: unexpected error: %v", menus, err)
			}
		}
	})

	t.Run("dir on non-filename type returns error mentioning dir and filename", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "X", Type: "text", Dir: "docs"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "dir") {
			t.Errorf("error = %q, want it to contain 'dir'", err.Error())
		}
		if !strings.Contains(err.Error(), "filename") {
			t.Errorf("error = %q, want it to contain 'filename'", err.Error())
		}
	})

	t.Run("glob on non-filename type returns error mentioning glob and filename", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "X", Type: "text", Glob: []string{"*.md"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") {
			t.Errorf("error = %q, want it to contain 'glob'", err.Error())
		}
		if !strings.Contains(err.Error(), "filename") {
			t.Errorf("error = %q, want it to contain 'filename'", err.Error())
		}
	})

	t.Run("absolute dir returns error mentioning absolute", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Dir: "/etc"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("error = %q, want it to contain 'absolute'", err.Error())
		}
	})

	t.Run("dir with .. segment returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Dir: "docs/../etc"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("dir equal to .. returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Dir: ".."}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid glob returns error mentioning glob", func(t *testing.T) {
		// doublestar.ValidatePattern rejects an unterminated character class like "[abc".
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"[abc"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") {
			t.Errorf("error = %q, want it to contain 'glob'", err.Error())
		}
	})

	t.Run("recursive glob **/*.md accepted for filename", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"**/*.md"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("anchored recursive glob docs/**/*.md accepted for filename", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"docs/**/*.md"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("multiLine on filename type returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", MultiLine: true}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("options on filename type returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Options: []string{"a.md", "b.md"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	// dirname param type — mitto-2hw.
	t.Run("dirname param with no dir/glob is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dirname param with valid dir and glob is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Dir: "docs", Glob: []string{"20*"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dirname param is OK in any menu (never gates)", func(t *testing.T) {
		for _, menus := range []string{"", "prompts", "conversation", "beadsIssues"} {
			err := ValidatePromptParameters(menus, []PromptParameter{{Name: "D", Type: "dirname"}})
			if err != nil {
				t.Errorf("menus=%q: unexpected error: %v", menus, err)
			}
		}
	})

	t.Run("dir on dirname type is accepted (widened gate)", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Dir: "docs/plans"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("glob on dirname type is accepted (widened gate)", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Glob: []string{"*"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dirname with absolute dir returns error mentioning absolute", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Dir: "/etc"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("error = %q, want it to contain 'absolute'", err.Error())
		}
	})

	t.Run("dirname with .. segment returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Dir: "docs/../etc"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("dirname with dir equal to .. returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Dir: ".."}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("recursive glob **/env-* accepted for dirname", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Glob: []string{"**/env-*"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dirname with invalid glob returns error mentioning glob", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Glob: []string{"[abc"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") {
			t.Errorf("error = %q, want it to contain 'glob'", err.Error())
		}
	})

	t.Run("multiLine on dirname type returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", MultiLine: true}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("options on dirname type returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Options: []string{"a", "b"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	// mitto-ebb: multi-entry glob lists on filename/dirname.
	t.Run("filename multi-entry glob list accepted", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"*.md", "*.rst", "**/*.txt"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("dirname multi-entry glob list accepted", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Glob: []string{"prod-*", "stage-*", "**/env-*"}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("filename glob list with empty entry rejected", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"*.md", ""}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") || !strings.Contains(err.Error(), "empty") {
			t.Errorf("error = %q, want it to mention glob/empty", err.Error())
		}
	})

	t.Run("dirname glob list with empty entry rejected", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "D", Type: "dirname", Glob: []string{"", "prod-*"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") || !strings.Contains(err.Error(), "empty") {
			t.Errorf("error = %q, want it to mention glob/empty", err.Error())
		}
	})

	t.Run("filename glob list with mid-list invalid entry rejected", func(t *testing.T) {
		// The first entry is valid; validation must still surface the second
		// entry's failure (loop over every entry, not just the first).
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{"*.md", "[abc"}}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "glob") {
			t.Errorf("error = %q, want it to contain 'glob'", err.Error())
		}
	})

	t.Run("filename empty glob list is OK (no filter)", func(t *testing.T) {
		// A zero-length list means "no filter" — must be accepted (len == 0
		// is the same shape as an omitted field for downstream code).
		err := ValidatePromptParameters("", []PromptParameter{{Name: "F", Type: "filename", Glob: []string{}}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestParsePromptFile_GlobRejectsScalarYAMLForm pins mitto-ebb: after the
// PromptParameter.Glob switch from string to []string, YAML files that still
// use the old scalar form (`glob: "*.md"`) MUST fail at unmarshal time. A
// silent accept would leave external workspaces on the old shape after upgrade
// with no visible error.
func TestParsePromptFile_GlobRejectsScalarYAMLForm(t *testing.T) {
	data := []byte(`name: "Scalar glob"
parameters:
  - name: Instructions
    type: filename
    glob: "*.md"
prompt: |
  Body
`)
	_, err := ParsePromptFile("scalar-glob.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("expected error for scalar glob form, got nil")
	}
	// The error should surface from the YAML decode layer, not from
	// ValidatePromptParameters, because the shape mismatch is caught there.
	if !strings.Contains(err.Error(), "scalar-glob.prompt.yaml") {
		t.Errorf("error = %q, want it to mention the file path", err.Error())
	}
}

// TestParsePromptFile_GlobAcceptsSingleEntryListForm covers the base case: a
// one-element list must parse and behave identically to the previous single
// string.
func TestParsePromptFile_GlobAcceptsSingleEntryListForm(t *testing.T) {
	data := []byte(`name: "Single glob"
parameters:
  - name: Instructions
    type: filename
    glob:
      - "*.md"
prompt: |
  Body
`)
	p, err := ParsePromptFile("single-glob.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Parameters) != 1 {
		t.Fatalf("len(Parameters) = %d, want 1", len(p.Parameters))
	}
	got := p.Parameters[0].Glob
	if len(got) != 1 || got[0] != "*.md" {
		t.Errorf("Glob = %v, want [\"*.md\"]", got)
	}
}

// TestParsePromptFile_GlobAcceptsMultiEntryListForm covers the mitto-ebb
// primary target: a prompt can accept multiple extensions in one dropdown.
func TestParsePromptFile_GlobAcceptsMultiEntryListForm(t *testing.T) {
	data := []byte(`name: "Multi glob"
parameters:
  - name: Doc
    type: filename
    glob:
      - "**/*.md"
      - "**/*.rst"
prompt: |
  Body
`)
	p, err := ParsePromptFile("multi-glob.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Parameters) != 1 {
		t.Fatalf("len(Parameters) = %d, want 1", len(p.Parameters))
	}
	got := p.Parameters[0].Glob
	want := []string{"**/*.md", "**/*.rst"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Glob = %v, want %v", got, want)
	}
}

// TestParsePromptFile_GlobRejectsEmptyEntryInList pins the load-time reject
// for an empty pattern hidden inside an otherwise-valid list (an empty
// pattern would silently match nothing at runtime).
func TestParsePromptFile_GlobRejectsEmptyEntryInList(t *testing.T) {
	data := []byte(`name: "Empty entry"
parameters:
  - name: Doc
    type: filename
    glob:
      - "*.md"
      - ""
prompt: |
  Body
`)
	_, err := ParsePromptFile("empty-entry.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("expected error for empty glob entry, got nil")
	}
	if !strings.Contains(err.Error(), "glob") || !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want it to mention glob/empty", err.Error())
	}
}

func TestParsePromptFile_WithMultiLineParameter(t *testing.T) {
	data := []byte(`name: "MultiLine Prompt"
parameters:
  - name: Instructions
    type: text
    multiLine: true
  - name: Path
    type: text
prompt: |
  ${Instructions} for ${Path}.
`)

	prompt, err := ParsePromptFile("ml.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}
	if !prompt.Parameters[0].MultiLine {
		t.Errorf("Parameters[0].MultiLine = false, want true")
	}
	if prompt.Parameters[1].MultiLine {
		t.Errorf("Parameters[1].MultiLine = true, want false (absent)")
	}
}

func TestParsePromptFile_WithOptionsParameter(t *testing.T) {
	data := []byte(`name: "Options Prompt"
parameters:
  - name: Kind
    type: text
    options:
      - Simplification
      - Cleanup
  - name: Free
    type: text
prompt: |
  {{ .Args.Kind }} for {{ .Args.Free }}.
`)

	prompt, err := ParsePromptFile("opt.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}
	got := prompt.Parameters[0].Options
	want := []string{"Simplification", "Cleanup"}
	if len(got) != len(want) {
		t.Fatalf("Parameters[0].Options = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Parameters[0].Options[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(prompt.Parameters[1].Options) != 0 {
		t.Errorf("Parameters[1].Options = %v, want empty (absent)", prompt.Parameters[1].Options)
	}
}

func TestPromptParameter_OptionsJSONRoundTrip(t *testing.T) {
	// Guards the wire path: Options must survive JSON marshaling through
	// WebPrompt / MCP DTOs. `omitempty` must drop the field when empty.
	p := PromptParameter{Name: "Kind", Type: "text", Options: []string{"a", "b"}}
	buf, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"options":["a","b"]`) {
		t.Errorf("json = %s, want it to contain the options array", string(buf))
	}
	var round PromptParameter
	if err := json.Unmarshal(buf, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(round.Options) != 2 || round.Options[0] != "a" || round.Options[1] != "b" {
		t.Errorf("round-tripped Options = %v, want [a b]", round.Options)
	}

	empty := PromptParameter{Name: "Free", Type: "text"}
	buf, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(buf), "options") {
		t.Errorf("json = %s, want no 'options' key when Options is empty", string(buf))
	}
}

func TestParsePromptFile_ChildSessionId(t *testing.T) {
	tests := []struct {
		name    string
		menus   string
		wantErr bool
	}{
		{"no menus line is OK", "", false},
		{"menus=prompts is OK", "prompts", false},
		{"menus=conversation is OK", "conversation", false},
		{"menus=beadsList errors with childSessionId mention", "beadsList", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			menusLine := ""
			if tc.menus != "" {
				menusLine = "menus: " + tc.menus + "\n"
			}
			data := []byte("name: \"Test\"\n" + menusLine + "parameters:\n  - name: child\n    type: childSessionId\nprompt: |\n  body\n")
			_, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "childSessionId") {
					t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestMigrateMarkdownPromptsInDir(t *testing.T) {
	dir := t.TempDir()

	// Legacy .md prompt with YAML front-matter + markdown body.
	legacy := `---
name: "CSO: latest report"
description: "Generate a report"
backgroundColor: "#C8E6C9"
group: "CSOs"
---

# CSO Latest Report

Read the latest messages and generate a report.
`
	mdPath := filepath.Join(dir, "cso-latest.md")
	if err := os.WriteFile(mdPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	// A legacy file without front-matter: whole body is the content.
	plainPath := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(plainPath, []byte("Just the body.\n"), 0644); err != nil {
		t.Fatalf("write plain file: %v", err)
	}

	migrated, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(migrated) != 2 {
		t.Fatalf("migrated count = %d, want 2", len(migrated))
	}

	// Original .md files are kept.
	if _, err := os.Stat(mdPath); err != nil {
		t.Errorf("legacy .md should be kept: %v", err)
	}

	// New .prompt.yaml written and parses back into the expected prompt.
	p, err := LoadPromptFile(dir, "cso-latest.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated file: %v", err)
	}
	if p.Name != "CSO: latest report" {
		t.Errorf("Name = %q, want %q", p.Name, "CSO: latest report")
	}
	if p.Group != "CSOs" {
		t.Errorf("Group = %q, want %q", p.Group, "CSOs")
	}
	if p.BackgroundColor != "#C8E6C9" {
		t.Errorf("BackgroundColor = %q, want %q", p.BackgroundColor, "#C8E6C9")
	}
	wantBody := "# CSO Latest Report\n\nRead the latest messages and generate a report."
	if p.Content != wantBody {
		t.Errorf("Content = %q, want %q", p.Content, wantBody)
	}

	// Plain file: name derived from filename, whole file is content.
	plain, err := LoadPromptFile(dir, "plain.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated plain file: %v", err)
	}
	if plain.Name != "plain" {
		t.Errorf("plain Name = %q, want %q", plain.Name, "plain")
	}
	if plain.Content != "Just the body." {
		t.Errorf("plain Content = %q, want %q", plain.Content, "Just the body.")
	}

	// Idempotency: a second run migrates nothing (targets already exist).
	again, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("second MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second run migrated %d, want 0", len(again))
	}

	// Non-existent directory is treated as empty (no error).
	none, err := MigrateMarkdownPromptsInDir(filepath.Join(dir, "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if none != nil {
		t.Errorf("missing dir migrated = %v, want nil", none)
	}
}

// TestMigrateMarkdownPromptsInDir_LiteralBlock verifies that a multi-line body
// containing emoji (4-byte UTF-8 runes) is written as a readable literal block
// scalar with the emoji preserved, rather than the escaped double-quoted scalar
// that yaml.v3 emits by default, and that it round-trips back to the original.
func TestMigrateMarkdownPromptsInDir_LiteralBlock(t *testing.T) {
	dir := t.TempDir()

	body := "# Title\n\nLine with emoji 🔴 and a ▶️ button.\n\n- item one\n- item two"
	legacy := "---\nname: \"Emoji Prompt\"\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "emoji.md"), []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	migrated, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(migrated) != 1 {
		t.Fatalf("migrated count = %d, want 1", len(migrated))
	}

	raw, err := os.ReadFile(filepath.Join(dir, "emoji.prompt.yaml"))
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "prompt: |-") {
		t.Errorf("expected literal block scalar, got:\n%s", text)
	}
	if strings.Contains(text, "\\U0001F534") || strings.Contains(text, `prompt: "`) {
		t.Errorf("body should not be an escaped double-quoted scalar, got:\n%s", text)
	}
	if !strings.Contains(text, "🔴") {
		t.Errorf("emoji should be preserved verbatim, got:\n%s", text)
	}

	p, err := LoadPromptFile(dir, "emoji.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated file: %v", err)
	}
	if p.Content != body {
		t.Errorf("Content round-trip = %q, want %q", p.Content, body)
	}
}

// TestPrecompileTemplateConds_SavePathGuard proves that the validation function
// used by the MCP handlePromptUpdate and REST POST /api/workspace-prompts save
// paths rejects invalid prompt bodies (mitto-m7sb.6). No mcpserver harness
// exists for handlePromptUpdate so we verify the guard directly.
func TestPrecompileTemplateConds_SavePathGuard(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"non-template body accepted", "plain ${VAR} text", false},
		{"valid template accepted", "{{ .Session.ID }}", false},
		{"invalid template syntax rejected", "{{ .Session.ID ", true},
		{"invalid cond CEL rejected", "{{ if cond \"@@@ bad\" }}x{{ end }}", true},
		{"struct-field typo rejected", "{{ .Session.Nope }}", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := PrecompileTemplateConds("save-test", tc.body)
			if tc.wantErr && err == nil {
				t.Fatal("expected error (save-path guard should reject), got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestParsePromptFile_TemplateValidation verifies that ParsePromptFile accepts
// valid templates and rejects invalid ones (mitto-m7sb.6).
func TestParsePromptFile_TemplateValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "non-template body — fast path, no error",
			yaml: "name: \"p\"\nprompt: \"plain ${VAR} @mitto:session_id text\"\n",
		},
		{
			name: "valid template body — accepted",
			yaml: "name: \"p\"\nprompt: \"Hello {{ .Session.ID }}\"\n",
		},
		{
			name:    "invalid template syntax — unclosed action",
			yaml:    "name: \"p\"\nprompt: \"Hello {{ .Session.ID \"\n",
			wantErr: true,
		},
		{
			name:    "invalid cond CEL literal — rejected",
			yaml:    "name: \"p\"\nprompt: \"{{ if cond \\\"@@@ not valid\\\" }}x{{ end }}\"\n",
			wantErr: true,
		},
		{
			name:    "struct-field typo — rejected",
			yaml:    "name: \"p\"\nprompt: \"{{ .Session.Nope }}\"\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePromptFile("test.prompt.yaml", []byte(tc.yaml), time.Now())
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ---- PromptParameterCache tests ----

func TestParsePromptFile_CacheWithTTL(t *testing.T) {
	data := []byte(`name: "Cached Prompt"
parameters:
  - name: SlackChannel
    type: text
    description: Slack channel name
    cache:
      destination: memory
      ttl: 1h
prompt: |
  Post to ${SlackChannel}.
`)
	prompt, err := ParsePromptFile("cached.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 1 {
		t.Fatalf("len(Parameters) = %d, want 1", len(prompt.Parameters))
	}
	p := prompt.Parameters[0]
	if p.Name != "SlackChannel" {
		t.Errorf("Parameters[0].Name = %q, want %q", p.Name, "SlackChannel")
	}
	if p.Cache == nil {
		t.Fatal("Parameters[0].Cache = nil, want non-nil")
	}
	if p.Cache.Destination != "memory" {
		t.Errorf("Cache.Destination = %q, want %q", p.Cache.Destination, "memory")
	}
	if p.Cache.TTL != "1h" {
		t.Errorf("Cache.TTL = %q, want %q", p.Cache.TTL, "1h")
	}
}

func TestParsePromptFile_CacheWithoutTTL(t *testing.T) {
	data := []byte(`name: "Cached No TTL"
parameters:
  - name: Channel
    type: text
    cache:
      destination: memory
prompt: |
  Use ${Channel}.
`)
	prompt, err := ParsePromptFile("cached-nottl.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 1 {
		t.Fatalf("len(Parameters) = %d, want 1", len(prompt.Parameters))
	}
	p := prompt.Parameters[0]
	if p.Cache == nil {
		t.Fatal("Parameters[0].Cache = nil, want non-nil")
	}
	if p.Cache.Destination != "memory" {
		t.Errorf("Cache.Destination = %q, want %q", p.Cache.Destination, "memory")
	}
	if p.Cache.TTL != "" {
		t.Errorf("Cache.TTL = %q, want empty (conversation lifetime)", p.Cache.TTL)
	}
}

func TestParsePromptFile_CacheInvalidDestination(t *testing.T) {
	data := []byte(`name: "Bad Cache"
parameters:
  - name: Chan
    type: text
    cache:
      destination: disk
prompt: |
  body
`)
	_, err := ParsePromptFile("bad-cache.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for invalid cache destination, got nil error")
	}
	if !strings.Contains(err.Error(), "destination") {
		t.Errorf("error = %q, want it to mention 'destination'", err.Error())
	}
}

func TestParsePromptFile_CacheInvalidTTL_Unparseable(t *testing.T) {
	data := []byte(`name: "Bad TTL"
parameters:
  - name: Chan
    type: text
    cache:
      destination: memory
      ttl: not-a-duration
prompt: |
  body
`)
	_, err := ParsePromptFile("bad-ttl.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for unparseable TTL, got nil error")
	}
	if !strings.Contains(err.Error(), "ttl") {
		t.Errorf("error = %q, want it to mention 'ttl'", err.Error())
	}
}

func TestParsePromptFile_CacheInvalidTTL_Zero(t *testing.T) {
	data := []byte(`name: "Zero TTL"
parameters:
  - name: Chan
    type: text
    cache:
      destination: memory
      ttl: 0s
prompt: |
  body
`)
	_, err := ParsePromptFile("zero-ttl.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for zero TTL, got nil error")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("error = %q, want it to mention 'positive'", err.Error())
	}
}

func TestParsePromptFile_CacheInvalidTTL_Negative(t *testing.T) {
	data := []byte(`name: "Negative TTL"
parameters:
  - name: Chan
    type: text
    cache:
      destination: memory
      ttl: -1h
prompt: |
  body
`)
	_, err := ParsePromptFile("neg-ttl.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for negative TTL, got nil error")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("error = %q, want it to mention 'positive'", err.Error())
	}
}

// TestBuiltinPrompts_MenusTokensRecognized walks every .prompt.yaml in
// config/prompts/builtin/ and asserts that each comma-separated token in its
// `menus:` field is a recognised menu name (see knownMenuTokens above). A
// token may be prefixed with "!" (exclusion) — the prefix is stripped before
// comparison.
//
// This is a regression guard for mitto-kazd: `menus: prompts, conversations`
// (plural, unrecognised) in babysit-this-pr.prompt.yaml silently dropped the
// prompt from the conversation context menu with no error anywhere in the
// pipeline. ParsePromptFile now warns on unknown tokens via WarnUnknownMenus
// (mitto-rjg6), but this test still guards the builtin tree specifically so a
// future typo cannot ship even if warnings go unnoticed. Uses the production
// KnownMenuTokens registry (menus.go) so the guard and the runtime warning
// can never disagree.
func TestBuiltinPrompts_MenusTokensRecognized(t *testing.T) {
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skipf("builtin prompts dir not found at %s: %v", builtinDir, err)
	}

	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "name" . }}` refs at parse-time precompile (mirrors
	// TestBuiltinPromptsParseClean). Without this, most builtin prompts fail
	// to parse and get silently skipped below, which is exactly how this
	// test almost missed the mitto-kazd regression during development.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, loadErrs, ferr := LoadFragmentsFromDir(builtinDir)
	if ferr != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", ferr)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	checked := 0
	walkErr := filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", d.Name(), err)
			return nil
		}
		prompt, err := ParsePromptFile(d.Name(), data, time.Now())
		if err != nil {
			// Parse errors are reported by TestBuiltinPromptsParseClean.
			return nil
		}
		if prompt.Menus == "" {
			return nil
		}
		for _, raw := range strings.Split(prompt.Menus, ",") {
			token := strings.TrimSpace(raw)
			token = strings.TrimPrefix(token, "!")
			if token == "" {
				continue
			}
			if !KnownMenuTokens[token] {
				t.Errorf("%s: menus %q contains unrecognised token %q (known: prompts, promptsLoop, conversation, beadsIssues, beadsList, internal)",
					d.Name(), prompt.Menus, token)
			}
		}
		checked++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir(%s): %v", builtinDir, walkErr)
	}
	if checked == 0 {
		t.Error("no builtin prompt files found — something is wrong with the path")
	}
	t.Logf("validated menus tokens on %d builtin prompt files", checked)
}

func TestToWebPrompt_RoundTripsCacheBlock(t *testing.T) {
	pf := &PromptFile{
		Name:    "Cached Param Prompt",
		Content: "body",
		Parameters: []PromptParameter{
			{Name: "SlackChannel", Type: "text", Cache: &PromptParameterCache{Destination: "memory", TTL: "1h"}},
			{Name: "Note", Type: "text"},
		},
	}

	wp := pf.ToWebPrompt()

	if len(wp.Parameters) != 2 {
		t.Fatalf("WebPrompt.Parameters len = %d, want 2", len(wp.Parameters))
	}
	c := wp.Parameters[0].Cache
	if c == nil {
		t.Fatal("WebPrompt.Parameters[0].Cache = nil, want non-nil")
	}
	if c.Destination != "memory" {
		t.Errorf("Cache.Destination = %q, want %q", c.Destination, "memory")
	}
	if c.TTL != "1h" {
		t.Errorf("Cache.TTL = %q, want %q", c.TTL, "1h")
	}
	if wp.Parameters[1].Cache != nil {
		t.Errorf("WebPrompt.Parameters[1].Cache = %+v, want nil", wp.Parameters[1].Cache)
	}

	// Verify JSON round-trip.
	raw, err := json.Marshal(wp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"destination":"memory"`) {
		t.Errorf("JSON missing cache destination; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"ttl":"1h"`) {
		t.Errorf("JSON missing cache ttl; got: %s", jsonStr)
	}
}

func TestPromptParameterCache_ParsedTTL(t *testing.T) {
	t.Run("empty TTL returns (0, nil)", func(t *testing.T) {
		c := &PromptParameterCache{Destination: "memory", TTL: ""}
		d, err := c.ParsedTTL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 0 {
			t.Errorf("ParsedTTL() = %v, want 0", d)
		}
	})

	t.Run("1h returns time.Hour", func(t *testing.T) {
		c := &PromptParameterCache{Destination: "memory", TTL: "1h"}
		d, err := c.ParsedTTL()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != time.Hour {
			t.Errorf("ParsedTTL() = %v, want %v", d, time.Hour)
		}
	})

	t.Run("invalid TTL returns error", func(t *testing.T) {
		c := &PromptParameterCache{Destination: "memory", TTL: "not-valid"}
		_, err := c.ParsedTTL()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("zero TTL returns error", func(t *testing.T) {
		c := &PromptParameterCache{Destination: "memory", TTL: "0s"}
		_, err := c.ParsedTTL()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("negative TTL returns error", func(t *testing.T) {
		c := &PromptParameterCache{Destination: "memory", TTL: "-30m"}
		_, err := c.ParsedTTL()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestValidatePromptParameters_Cache(t *testing.T) {
	t.Run("valid memory destination with TTL", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Chan", Type: "text", Cache: &PromptParameterCache{Destination: "memory", TTL: "30m"}},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid memory destination without TTL", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Chan", Type: "text", Cache: &PromptParameterCache{Destination: "memory"}},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown destination returns error naming parameter and destination", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Chan", Type: "text", Cache: &PromptParameterCache{Destination: "disk"}},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "Chan") {
			t.Errorf("error = %q, want it to mention parameter name 'Chan'", err.Error())
		}
		if !strings.Contains(err.Error(), "disk") {
			t.Errorf("error = %q, want it to mention bad destination 'disk'", err.Error())
		}
	})

	t.Run("invalid TTL returns error", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Chan", Type: "text", Cache: &PromptParameterCache{Destination: "memory", TTL: "bad"}},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("nil cache is accepted (cache is optional)", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Chan", Type: "text"},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestValidatePromptParameters_Group covers mitto-boio: `group` is valid on
// every parameter type (no type gate, unlike multiLine/options/dir/glob/
// collectInnerArgs), a non-empty-but-whitespace-only value is rejected, an
// absent/empty group is a no-op, and "General" is not a reserved name.
func TestValidatePromptParameters_Group(t *testing.T) {
	t.Run("absent group is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "x", Type: "text"},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("non-empty group is OK on a non-text type", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "Commit", Type: "boolean", Group: "Changes Submission"},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("group: General is OK (not a reserved name)", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "x", Type: "text", Group: "General"},
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("whitespace-only group is rejected", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{
			{Name: "x", Type: "text", Group: "   "},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "group must not be empty or whitespace-only") {
			t.Errorf("error = %q, want it to contain 'group must not be empty or whitespace-only'", err.Error())
		}
		if !strings.Contains(err.Error(), "\"x\"") {
			t.Errorf("error = %q, want it to mention parameter name \"x\"", err.Error())
		}
	})
}

// TestBuiltinPromptsParseClean ensures every .prompt.yaml in config/prompts/builtin/
// passes ParsePromptFile without error. This exercises load-time template validation
// (added in mitto-m7sb.6) on the migrated builtin prompt set (mitto-m7sb.7/8).
//
// jira-sync-tasks.prompt.yaml previously contained literal {{...}} sequences (a JIRA
// wiki-markup example and a Python regex comment) that are NOT template directives;
// they are now escaped via {{ "{{" }} (design §10.3) so the whole set validates.
func TestBuiltinPromptsParseClean(t *testing.T) {
	// Relative to internal/config/ (the package directory during go test)
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skipf("builtin prompts dir not found at %s: %v", builtinDir, err)
	}
	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "name" . }}` refs at parse-time precompile (mitto-g61.4).
	// Restored on cleanup so nil-baseline tests remain unaffected.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, loadErrs, ferr := LoadFragmentsFromDir(builtinDir)
	if ferr != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", ferr)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)
	loaded := 0
	walkErr := filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", d.Name(), err)
			return nil
		}
		if _, err := ParsePromptFile(d.Name(), data, time.Now()); err != nil {
			t.Errorf("ParsePromptFile(%s): %v", d.Name(), err)
		}
		loaded++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir(%s): %v", builtinDir, walkErr)
	}
	if loaded == 0 {
		t.Error("no builtin prompt files found — something is wrong with the path")
	}
	t.Logf("validated %d builtin prompt files", loaded)

	// Verify no builtin prompt declares a SCREAMING_SNAKE_CASE argument name.
	// All parameter names must use PascalCase (no underscores, not all-uppercase).
	screamingSnake := regexp.MustCompile(`^[A-Z][A-Z0-9_]*_[A-Z0-9_]*$|^[A-Z0-9_]+$`)
	_ = filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		prompt, err := ParsePromptFile(d.Name(), data, time.Now())
		if err != nil {
			return nil // parse errors already reported above
		}
		for _, p := range prompt.Parameters {
			if strings.Contains(p.Name, "_") || screamingSnake.MatchString(p.Name) {
				t.Errorf("%s: parameter name %q is SCREAMING_SNAKE_CASE — use PascalCase instead", d.Name(), p.Name)
			}
		}
		return nil
	})
}

// TestBuiltinPrompt_GithubReviewPR_ParameterIsPascalCase pins the mitto-maxn
// regression: commit 5b92b4cf added a `PR` parameter to github-review-pr.prompt.yaml,
// which trips the SCREAMING_SNAKE_CASE guard in TestBuiltinPromptsParseClean and
// blocks `make check`. The fix is a targeted rename PR → Pr (parameter name and its
// three .Args.PR template call-sites). This test asserts the renamed shape directly,
// so an accidental revert on this specific file is caught even if the generic linter
// is later relaxed.
func TestBuiltinPrompt_GithubReviewPR_ParameterIsPascalCase(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("..", "..", "config", "prompts", "builtin", "github/review-pr.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	prompt, err := ParsePromptFile("github/review-pr.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	var names []string
	for _, p := range prompt.Parameters {
		names = append(names, p.Name)
		if p.Name == "PR" {
			t.Errorf("github-review-pr.prompt.yaml: parameter is still declared as %q; expected PascalCase %q (mitto-maxn)", p.Name, "Pr")
		}
	}
	hasPr := false
	for _, n := range names {
		if n == "Pr" {
			hasPr = true
			break
		}
	}
	if !hasPr {
		t.Errorf("github-review-pr.prompt.yaml: expected a parameter named %q; got %v (mitto-maxn)", "Pr", names)
	}
	if bytes.Contains(data, []byte(".Args.PR")) {
		t.Errorf("github-review-pr.prompt.yaml: template still references .Args.PR; expected .Args.Pr (mitto-maxn)")
	}
}

// TestBuiltinPrompts_SingletonMigratedToTargetReuseTitle pins mitto-y0l2:
// the six builtin beads-* prompts that historically declared `singleton: true`
// must now declare `target: { title: "<name>", reuse: { title: true } }` and
// must NOT declare `singleton: true` any longer. The target.title must equal
// the prompt's current `name:` verbatim so any pre-existing singleton
// conversation (whose title is the prompt name today, per
// FindSingletonCandidate) continues to be funneled by FindConversationByTitle
// after the migration — no duplicate spawn.
//
// Guards against silent regressions like: (a) somebody re-adding singleton to
// one of these files, (b) target.title drifting away from name, (c) reuse.title
// being flipped off. Machinery for the target block itself is exercised by
// TestValidatePromptTarget and the reuse tests under internal/session and
// internal/web/handlers.
func TestBuiltinPrompts_SingletonMigratedToTargetReuseTitle(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	migrated := []string{
		"beads/cleanup-stale.prompt.yaml",
		"beads/group-epics.prompt.yaml",
		"beads/overview.prompt.yaml",
		"beads/reevaluate.prompt.yaml",
		"beads/investigate-all-more.prompt.yaml",
		"beads/status-all-inprogress.prompt.yaml",
	}
	for _, file := range migrated {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(builtinDir, file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			// Raw-bytes check: the legacy `singleton: true` line must be gone.
			// Match `\nsingleton:` (with newline) to avoid catching the string
			// mentioned in a comment or prose block.
			if bytes.Contains(data, []byte("\nsingleton:")) {
				t.Errorf("%s: still declares `singleton:` at top-level; mitto-y0l2 requires migration to target.reuseTitle", file)
			}
			prompt, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", file, err)
			}
			if prompt.Singleton {
				t.Errorf("%s: Singleton = true, want false after mitto-y0l2 migration", file)
			}
			if prompt.Target == nil {
				t.Fatalf("%s: Target = nil, want non-nil {Title, ReuseTitle: true}", file)
			}
			if !promptTargetReuseTitle(prompt) {
				t.Errorf("%s: Target.ReuseTitle = false, want true", file)
			}
			if prompt.Target.Title == "" {
				t.Fatalf("%s: Target.Title = empty, want prompt name %q", file, prompt.Name)
			}
			if prompt.Target.Title != prompt.Name {
				t.Errorf("%s: Target.Title = %q, want %q (must equal name so pre-existing singleton conversations continue to match)", file, prompt.Target.Title, prompt.Name)
			}
		})
	}
}

// TestBuiltinPrompts_NoSingletonRemains complements the mitto-y0l2 migration
// pin above: since all shipped builtins are expected to have moved off the
// legacy `singleton: true` field, no builtin under config/prompts/builtin/
// should declare it. If a new builtin needs single-conversation reuse it must
// use `target: { title, reuse: { title: true } }` instead.
func TestBuiltinPrompts_NoSingletonRemains(t *testing.T) {
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skipf("builtin prompts dir not found at %s: %v", builtinDir, err)
	}
	walkErr := filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", d.Name(), err)
			return nil
		}
		if bytes.Contains(data, []byte("\nsingleton:")) {
			t.Errorf("%s: declares legacy `singleton:` — use target.reuse.title instead (mitto-y0l2)", d.Name())
		}
		prompt, err := ParsePromptFile(d.Name(), data, time.Now())
		if err != nil {
			return nil // parse errors already reported by TestBuiltinPromptsParseClean
		}
		if prompt.Singleton {
			t.Errorf("%s: parsed Singleton = true — use target.reuse.title instead (mitto-y0l2)", d.Name())
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir(%s): %v", builtinDir, walkErr)
	}
}

// TestBuiltinPrompts_SupportRoutingAdoption pins mitto-5x21.1: the five
// today-tier support-* builtin prompts must declare target/reuse routing so
// repeat dispatches funnel back into the right existing conversation instead
// of spawning parallels. Four per-ticket prompts route by beads issue
// (target.reuse.issue: true), and the workspace-wide housekeeping prompt
// routes by literal title (target.title: "Support: housekeeping" +
// target.reuse.title: true). All five must set target.reuse.coalesce: true so
// concurrent dispatches join instead of racing.
//
// Guards against silent regressions like: (a) somebody removing a target
// block from one of these files, (b) reuse.coalesce being flipped off, (c)
// the housekeeping literal title drifting away from the fixed workspace-wide
// value. Machinery for the target block itself is exercised by
// TestValidatePromptTarget and the reuse tests under internal/session and
// internal/web/handlers.
func TestBuiltinPrompts_SupportRoutingAdoption(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	type spec struct {
		file          string
		wantReuseByID bool   // target.reuse.issue: true
		wantTitle     string // literal target.title (only when reuseByID is false)
	}
	specs := []spec{
		{file: "support/check-status.prompt.yaml", wantReuseByID: true},
		{file: "support/gather-info.prompt.yaml", wantReuseByID: true},
		{file: "support/investigate.prompt.yaml", wantReuseByID: true},
		{file: "support/reply-to-user.prompt.yaml", wantReuseByID: true},
		{file: "support/housekeeping.prompt.yaml", wantTitle: "Support: housekeeping"},
	}
	for _, s := range specs {
		t.Run(s.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, s.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(s.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", s.file, err)
			}
			if prompt.Target == nil {
				t.Fatalf("%s: Target = nil, want non-nil target/reuse routing (mitto-5x21.1)", s.file)
			}
			if promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
				t.Errorf("%s: Target.ReuseCoalesce = %v, want true (concurrent dispatches must join, not race)", s.file, promptTargetReuseCoalesce(prompt))
			}
			if s.wantReuseByID {
				if !promptTargetReuseIssue(prompt) {
					t.Errorf("%s: Target.ReuseIssue = false, want true (per-ticket support prompt)", s.file)
				}
				if promptTargetReuseTitle(prompt) {
					t.Errorf("%s: Target.ReuseTitle = true, want false (per-ticket routes by beads issue, not title)", s.file)
				}
			} else {
				if !promptTargetReuseTitle(prompt) {
					t.Errorf("%s: Target.ReuseTitle = false, want true (workspace-wide singleton routes by title)", s.file)
				}
				if promptTargetReuseIssue(prompt) {
					t.Errorf("%s: Target.ReuseIssue = true, want false (workspace-wide singleton routes by title, not per-issue)", s.file)
				}
				if prompt.Target.Title != s.wantTitle {
					t.Errorf("%s: Target.Title = %q, want %q (literal workspace-wide title)", s.file, prompt.Target.Title, s.wantTitle)
				}
			}
		})
	}
}

// TestBuiltinPrompts_TodayTierRoutingAdoption pins mitto-5x21.2: the 51
// today-tier beads-* / workspace-wide builtin prompts must declare target/reuse
// routing so repeat dispatches funnel back into the right existing conversation
// instead of spawning parallels. Modelled on TestBuiltinPrompts_SupportRoutingAdoption
// (mitto-5x21.1) for the parallel support-* sweep.
//
// Four buckets:
//   - 2a — per-bead work prompts: reuseIssue + reuseCoalesce.
//   - 2b — per-bead phase prompts (feature/fix/mention): reuseIssue + reuseCoalesce.
//   - 2c — per-bead loop prompts: reuseIssue only (concurrent dispatch must NOT
//     merge into a live loop turn — no reuseCoalesce).
//   - 2d — workspace-wide singleton loops/sweeps: literal title + reuseTitle +
//     reuseCoalesce.
//
// Guards against silent regressions like: (a) somebody removing a target block
// from one of these files, (b) reuseCoalesce being flipped off on 2a/2b/2d, (c)
// reuseCoalesce being flipped ON for 2c, (d) a 2d literal title drifting.
func TestBuiltinPrompts_TodayTierRoutingAdoption(t *testing.T) {
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "github/shared/pr-comments" . }}` at parse-time precompile
	// (mitto-g61.4). Restored on cleanup so parallel/subsequent tests that
	// expect the nil-registry baseline are unaffected.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)
	type bucket int
	const (
		perBeadWithCoalesce   bucket = iota // 2a + 2b
		perBeadLoopNoCoalesce               // 2c
		workspaceTitle                      // 2d
	)
	type spec struct {
		file      string
		bucket    bucket
		wantTitle string // only for workspaceTitle
	}
	specs := []spec{
		// 2a — per-bead work prompts.
		{file: "beads-issues/work.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/assess.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/decompose.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/dependencies.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/discuss.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/investigate.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/status.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/resolved.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/publish.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/add-references.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/content-review.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/fact-check.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/linkedin-post.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "blog/polish.prompt.yaml", bucket: perBeadWithCoalesce},
		// 2b — per-bead phase prompts.
		{file: "beads-issues/feature-phase-plan.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/feature-phase-implement.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/feature-phase-test.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/feature-phase-review.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/fix-phase-investigate.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/fix-phase-reproduce.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/fix-phase-fix.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/mention-phase-answer.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/mention-phase-implement.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/mention-phase-investigate.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/mention-phase-plan.prompt.yaml", bucket: perBeadWithCoalesce},
		{file: "beads-issues/mention-driver.prompt.yaml", bucket: perBeadWithCoalesce},
		// 2c — per-bead loop prompts (no coalesce).
		{file: "beads-issues/loop-fixing-bug.prompt.yaml", bucket: perBeadLoopNoCoalesce},
		{file: "beads-issues/loop-implementing-feature.prompt.yaml", bucket: perBeadLoopNoCoalesce},
		{file: "beads-issues/loop-until-complete.prompt.yaml", bucket: perBeadLoopNoCoalesce},
		// 2d — workspace-wide singleton loops/sweeps (title + reuseTitle).
		{file: "beads-issues/loop-processing.prompt.yaml", bucket: workspaceTitle, wantTitle: "Loop processing tasks"},
		{file: "beads/work.prompt.yaml", bucket: workspaceTitle, wantTitle: "Start working on ready"},
		{file: "beads/followup-work.prompt.yaml", bucket: workspaceTitle, wantTitle: "Identify follow-up issues"},
		{file: "beads/triage-bugs.prompt.yaml", bucket: workspaceTitle, wantTitle: "Triage untriaged bugs"},
		{file: "beads/status-one-inprogress.prompt.yaml", bucket: workspaceTitle, wantTitle: "Status ONE in-progress"},
		{file: "github/babysit-my-prs.prompt.yaml", bucket: workspaceTitle, wantTitle: "GitHub: babysit my PRs"},
		{file: "github/babysit-contributions.prompt.yaml", bucket: workspaceTitle, wantTitle: "GitHub: babysit contributions"},
		{file: "github/post-merge-cleanup.prompt.yaml", bucket: workspaceTitle, wantTitle: "GitHub: post-merge cleanup"},
		{file: "github/review-slack-prs.prompt.yaml", bucket: workspaceTitle, wantTitle: "GitHub: review PRs requests in slack"},
		{file: "github/sync-tasks.prompt.yaml", bucket: workspaceTitle, wantTitle: "GitHub: sync tasks"},
		{file: "jira/status-all-inprogress.prompt.yaml", bucket: workspaceTitle, wantTitle: "JIRA: status ALL in-progress"},
		{file: "jira/status-one-inprogress.prompt.yaml", bucket: workspaceTitle, wantTitle: "JIRA: status ONE in-progress"},
		{file: "jira/sync-tasks.prompt.yaml", bucket: workspaceTitle, wantTitle: "JIRA: sync tasks"},
		{file: "jira/decompose.prompt.yaml", bucket: workspaceTitle, wantTitle: "JIRA: decompose"},
		{file: "jira/work.prompt.yaml", bucket: workspaceTitle, wantTitle: "JIRA: start work"},
		{file: "ci/check-ci.prompt.yaml", bucket: workspaceTitle, wantTitle: "Check CI"},
		{file: "ci/fix-ci.prompt.yaml", bucket: workspaceTitle, wantTitle: "Fix CI"},
		// The four git prompts share one routing title so they serialise on the
		// single working tree they all mutate.
		{file: "git/create-commits.prompt.yaml", bucket: workspaceTitle, wantTitle: "Submission of changes"},
		{file: "git/rebase-changes.prompt.yaml", bucket: workspaceTitle, wantTitle: "Submission of changes"},
		{file: "git/submit-changes.prompt.yaml", bucket: workspaceTitle, wantTitle: "Submission of changes"},
		{file: "git/commit-and-submit.prompt.yaml", bucket: workspaceTitle, wantTitle: "Submission of changes"},
		{file: "testing/run-tests.prompt.yaml", bucket: workspaceTitle, wantTitle: "Run tests"},
		{file: "ci/analyze-logs.prompt.yaml", bucket: workspaceTitle, wantTitle: "Analyze logs"},
		{file: "docs/architectural-analysis.prompt.yaml", bucket: workspaceTitle, wantTitle: "Architectural Analysis"},
		{file: "docs/document-arch.prompt.yaml", bucket: workspaceTitle, wantTitle: "Document Architecture"},
		{file: "docs/document-code.prompt.yaml", bucket: workspaceTitle, wantTitle: "Document Code"},
		{file: "docs/generate-agents-md.prompt.yaml", bucket: workspaceTitle, wantTitle: "Generate AGENTS.md"},
		{file: "blog/ideation.prompt.yaml", bucket: workspaceTitle, wantTitle: "Blog: ideation"},
	}
	for _, s := range specs {
		t.Run(s.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, s.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(s.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", s.file, err)
			}
			if prompt.Target == nil {
				t.Fatalf("%s: Target = nil, want non-nil target/reuse routing (mitto-5x21.2)", s.file)
			}
			switch s.bucket {
			case perBeadWithCoalesce:
				if !promptTargetReuseIssue(prompt) {
					t.Errorf("%s: Target.ReuseIssue = false, want true (per-bead prompt)", s.file)
				}
				if promptTargetReuseTitle(prompt) {
					t.Errorf("%s: Target.ReuseTitle = true, want false (per-bead routes by issue, not title)", s.file)
				}
				if promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
					t.Errorf("%s: Target.ReuseCoalesce = %v, want true (concurrent dispatches must join, not race)", s.file, promptTargetReuseCoalesce(prompt))
				}
			case perBeadLoopNoCoalesce:
				if !promptTargetReuseIssue(prompt) {
					t.Errorf("%s: Target.ReuseIssue = false, want true (per-bead loop prompt)", s.file)
				}
				if promptTargetReuseTitle(prompt) {
					t.Errorf("%s: Target.ReuseTitle = true, want false (per-bead loop routes by issue, not title)", s.file)
				}
				if promptTargetReuseCoalesce(prompt) != nil && *promptTargetReuseCoalesce(prompt) {
					t.Errorf("%s: Target.ReuseCoalesce = true, want unset/false (a concurrent dispatch must not merge into a live loop turn)", s.file)
				}
			case workspaceTitle:
				if !promptTargetReuseTitle(prompt) {
					t.Errorf("%s: Target.ReuseTitle = false, want true (workspace-wide singleton routes by title)", s.file)
				}
				if promptTargetReuseIssue(prompt) {
					t.Errorf("%s: Target.ReuseIssue = true, want false (workspace-wide singleton routes by title, not per-issue)", s.file)
				}
				if prompt.Target.Title != s.wantTitle {
					t.Errorf("%s: Target.Title = %q, want %q (literal workspace-wide title)", s.file, prompt.Target.Title, s.wantTitle)
				}
				if promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
					t.Errorf("%s: Target.ReuseCoalesce = %v, want true (concurrent dispatches must join, not race)", s.file, promptTargetReuseCoalesce(prompt))
				}
			}
		})
	}
}

// TestBuiltinPrompts_NeedsTemplatedTitleAdoption pins mitto-5x21.3: the four
// needs-5qbo-tier per-external-ID builtin prompts must declare templated
// target.title routing (Go-template `{{ .Args.X }}` inside target.title, unlocked
// by mitto-5qbo) so repeat dispatches for the same external ID (Slack channel or
// PR) funnel back into the right existing conversation instead of spawning
// parallels, while different IDs still create distinct buckets. Modelled on
// TestBuiltinPrompts_TodayTierRoutingAdoption (mitto-5x21.2) and
// TestBuiltinPrompts_SupportRoutingAdoption (mitto-5x21.1).
//
// Single bucket (perExternalIDWithCoalesce): templated target.title +
// reuse.title: true + reuse.coalesce: true, reuse.issue: false. The literal
// title string carried in Target.Title is the un-rendered template (rendering
// happens at dispatch); asserting on the exact template literal pins both the
// field name (e.g. SlackChannelID vs slack_channel_id) and the surrounding
// text (e.g. "Support: continue " vs "Support: watch ").
//
// Guards against silent regressions like: (a) somebody removing a target block
// from one of these files, (b) reuse.coalesce being flipped off, (c) the
// templated title drifting to a different arg name or literal shape, (d)
// reuse.issue being flipped on (which would silently override the templated
// per-ID bucket with per-bead routing).
func TestBuiltinPrompts_NeedsTemplatedTitleAdoption(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	type spec struct {
		file      string
		wantTitle string // exact templated literal expected in Target.Title
	}
	specs := []spec{
		{file: "support/continue-conversation.prompt.yaml", wantTitle: "Support: continue {{ .Args.SlackChannelID }}"},
		{file: "support/watch-channel.prompt.yaml", wantTitle: "Support: watch {{ .Args.SlackChannelID }}"},
		// The four per-PR GitHub prompts deliberately share ONE templated
		// title so only a single conversation ever works on a given PR
		// (review + triage + address + babysit all funnel into "PR #<n>").
		// Two of them mutate the PR branch (address, babysit), so distinct
		// titles here would let two agents push to the same branch.
		{file: "github/review-pr.prompt.yaml", wantTitle: "PR #{{ .Args.Pr }}"},
		{file: "github/address-pr-comments.prompt.yaml", wantTitle: "PR #{{ .Args.Pr }}"},
		{file: "github/check-pr-comments.prompt.yaml", wantTitle: "PR #{{ .Args.Pr }}"},
	}
	for _, s := range specs {
		t.Run(s.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, s.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(s.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", s.file, err)
			}
			if prompt.Target == nil {
				t.Fatalf("%s: Target = nil, want non-nil target/reuse routing (mitto-5x21.3)", s.file)
			}
			if !promptTargetReuseTitle(prompt) {
				t.Errorf("%s: Target.ReuseTitle = false, want true (per-external-ID templated title)", s.file)
			}
			if promptTargetReuseIssue(prompt) {
				t.Errorf("%s: Target.ReuseIssue = true, want false (per-external-ID routes by templated title, not beads issue)", s.file)
			}
			if prompt.Target.Title != s.wantTitle {
				t.Errorf("%s: Target.Title = %q, want %q (templated per-external-ID title)", s.file, prompt.Target.Title, s.wantTitle)
			}
			if promptTargetReuseCoalesce(prompt) == nil || !*promptTargetReuseCoalesce(prompt) {
				t.Errorf("%s: Target.ReuseCoalesce = %v, want true (concurrent dispatches must join, not race)", s.file, promptTargetReuseCoalesce(prompt))
			}
		})
	}
}

// TestBuiltinPrompts_NeedsTemplatedTitleRenders_PerIDBuckets exercises the
// mitto-5x21.3 acceptance criterion "Two dispatches with the same SlackChannelID
// (or Pr) reuse a single conversation; different values create distinct ones" at
// the render layer — the exact helper (RenderPromptTargetTitle, mitto-5qbo) that
// the dispatch path invokes to produce the reuseTitle lookup key.
//
// TestBuiltinPrompts_NeedsTemplatedTitleAdoption (above) pins the un-rendered
// template literal in Target.Title so drift is caught structurally; this test
// pins the *rendered* behavior so a silently-broken template (e.g. accidentally
// switching to a fixed literal, or renaming the arg without updating the
// template) is caught even when the frontmatter still parses.
//
// For each of the four needs-5qbo prompts, render Target.Title twice with the
// same arg value (must match byte-for-byte → same reuseTitle bucket) and twice
// with different arg values (must differ → distinct buckets). Also renders the
// missing/empty-arg case for github-*/address-* (Pr is optional) to document
// the "single fallback bucket" behavior called out in the plan comment.
func TestBuiltinPrompts_NeedsTemplatedTitleRenders_PerIDBuckets(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	type spec struct {
		file         string
		argName      string // key inside PromptTargetContext.Args
		sameValue    string // rendered twice, must match
		otherValue   string // rendered once, must differ from sameValue's render
		wantSame     string // expected rendered title for sameValue
		wantOther    string // expected rendered title for otherValue
		wantMissing  string // expected rendered title when arg is absent ("" = expect error)
		missingIsErr bool   // when true, missing-arg render must fail (SlackChannelID: whole title collapses to empty)
	}
	specs := []spec{
		{
			file: "support/continue-conversation.prompt.yaml", argName: "SlackChannelID",
			sameValue: "C0AAA", otherValue: "C0BBB",
			wantSame: "Support: continue C0AAA", wantOther: "Support: continue C0BBB",
			missingIsErr: false, // literal "Support: continue " is non-empty → allowed but coalesces
			wantMissing:  "Support: continue ",
		},
		{
			file: "support/watch-channel.prompt.yaml", argName: "SlackChannelID",
			sameValue: "C0AAA", otherValue: "C0BBB",
			wantSame: "Support: watch C0AAA", wantOther: "Support: watch C0BBB",
			missingIsErr: false,
			wantMissing:  "Support: watch ",
		},
		// The three per-PR GitHub prompts below render to the SAME title for
		// the same Pr, which is the point: one conversation per PR. Distinct
		// Pr values still yield distinct buckets.
		{
			file: "github/review-pr.prompt.yaml", argName: "Pr",
			sameValue: "123", otherValue: "456",
			wantSame: "PR #123", wantOther: "PR #456",
			missingIsErr: false,
			wantMissing:  "PR #",
		},
		{
			file: "github/address-pr-comments.prompt.yaml", argName: "Pr",
			sameValue: "123", otherValue: "456",
			wantSame: "PR #123", wantOther: "PR #456",
			missingIsErr: false,
			wantMissing:  "PR #",
		},
		{
			file: "github/check-pr-comments.prompt.yaml", argName: "Pr",
			sameValue: "123", otherValue: "456",
			wantSame: "PR #123", wantOther: "PR #456",
			missingIsErr: false,
			wantMissing:  "PR #",
		},
		// babysit-this-pr guards the empty-Pr case explicitly: with no Pr it
		// falls back to a literal title (the PR is auto-detected from the
		// conversation at run time), instead of collapsing every babysat PR
		// into a shared "PR #" bucket.
		{
			file: "github/babysit-this-pr.prompt.yaml", argName: "Pr",
			sameValue: "123", otherValue: "456",
			wantSame: "PR #123", wantOther: "PR #456",
			missingIsErr: false,
			wantMissing:  "GitHub: babysit this PR",
		},
	}
	for _, s := range specs {
		t.Run(s.file, func(t *testing.T) {
			path := filepath.Join(builtinDir, s.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			prompt, err := ParsePromptFile(s.file, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", s.file, err)
			}
			if prompt.Target == nil || prompt.Target.Title == "" {
				t.Fatalf("%s: Target.Title missing, cannot exercise render", s.file)
			}
			tpl := prompt.Target.Title

			// Two dispatches with the SAME arg value must render to the SAME title.
			ctxA := PromptTargetContext{Args: map[string]string{s.argName: s.sameValue}}
			gotA1, err := RenderPromptTargetTitle(prompt.Name, tpl, ctxA)
			if err != nil {
				t.Fatalf("%s: render #1 with %s=%q failed: %v", s.file, s.argName, s.sameValue, err)
			}
			gotA2, err := RenderPromptTargetTitle(prompt.Name, tpl, ctxA)
			if err != nil {
				t.Fatalf("%s: render #2 with %s=%q failed: %v", s.file, s.argName, s.sameValue, err)
			}
			if gotA1 != gotA2 {
				t.Errorf("%s: same %s=%q rendered differently: %q vs %q (would break reuseTitle bucketing)", s.file, s.argName, s.sameValue, gotA1, gotA2)
			}
			if gotA1 != s.wantSame {
				t.Errorf("%s: render(%s=%q) = %q, want %q", s.file, s.argName, s.sameValue, gotA1, s.wantSame)
			}

			// A dispatch with a DIFFERENT arg value must render to a DIFFERENT title.
			ctxB := PromptTargetContext{Args: map[string]string{s.argName: s.otherValue}}
			gotB, err := RenderPromptTargetTitle(prompt.Name, tpl, ctxB)
			if err != nil {
				t.Fatalf("%s: render with %s=%q failed: %v", s.file, s.argName, s.otherValue, err)
			}
			if gotB == gotA1 {
				t.Errorf("%s: different %s values (%q vs %q) collapsed to the same rendered title %q (would silently share reuseTitle bucket)", s.file, s.argName, s.sameValue, s.otherValue, gotB)
			}
			if gotB != s.wantOther {
				t.Errorf("%s: render(%s=%q) = %q, want %q", s.file, s.argName, s.otherValue, gotB, s.wantOther)
			}

			// Missing-arg render: documents the "single fallback bucket" behavior
			// (Pr optional on github-*/address-*) or fail-closed (SlackChannelID
			// required — but literal prefix keeps whole render non-empty). Either
			// way, pin the observed behavior so a future template change does not
			// silently flip it.
			ctxMissing := PromptTargetContext{Args: map[string]string{}}
			gotMissing, err := RenderPromptTargetTitle(prompt.Name, tpl, ctxMissing)
			if s.missingIsErr {
				if err == nil {
					t.Errorf("%s: expected empty-render error with %s absent, got %q", s.file, s.argName, gotMissing)
				}
			} else {
				if err != nil {
					t.Errorf("%s: expected non-error fallback render with %s absent, got err %v", s.file, s.argName, err)
				}
				if gotMissing != s.wantMissing {
					t.Errorf("%s: render(%s absent) = %q, want %q (documented single-fallback-bucket behavior)", s.file, s.argName, gotMissing, s.wantMissing)
				}
			}
		})
	}
}

// TestBuiltinPrompts_EnabledWhenCompiles CEL-compiles every non-empty enabledWhen
// on the shipped builtin prompts. TestBuiltinPromptsParseClean only YAML-parses
// them, so an undeclared identifier/function in a builtin's enabledWhen would slip
// through CI and only surface as a runtime WARN (see mitto-w7h: 3 Support prompts
// referenced the then-unregistered BeadIsOpen macro and were silently dropped).
func TestBuiltinPrompts_EnabledWhenCompiles(t *testing.T) {
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skipf("builtin prompts dir not found at %s: %v", builtinDir, err)
	}
	e, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	n := 0
	walkErr := filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", d.Name(), err)
			return nil
		}
		prompt, err := ParsePromptFile(d.Name(), data, time.Now())
		if err != nil {
			return nil // parse errors reported by TestBuiltinPromptsParseClean
		}
		if prompt.EnabledWhen == "" {
			return nil
		}
		if _, err := e.Compile(prompt.EnabledWhen); err != nil {
			t.Errorf("%s: enabledWhen %q failed to compile: %v", d.Name(), prompt.EnabledWhen, err)
			return nil
		}
		n++
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir(%s): %v", builtinDir, walkErr)
	}
	t.Logf("compiled enabledWhen for %d builtin prompts", n)
}

// TestBuiltinPrompts_NoInertLoopTriggerBlocks pins mitto-7hh0: no builtin
// prompt may declare a per-trigger loop block (loop.schedule, loop.onCompletion,
// loop.onTasks, loop.onChild) for a trigger that isn't listed in loop.trigger.
// Such a block is inert — ValidateLoopTriggers only WARNs on it (fields are
// tolerated so hand-authored/migrated prompts don't hard-fail), but a
// well-maintained builtin fixture should never ship dead configuration. This
// is the Go-test equivalent of `mitto prompts verify`'s "this block is inert"
// warning and is what mitto-7hh0's acceptance criterion ("mitto prompts
// verify emits zero inert-block warnings") reduces to at the unit level.
//
// The list of affected prompts is intentionally not enumerated: the test
// walks every builtin file so a future prompt that reintroduces an inert
// block (e.g. a copy-paste of an old loop: stanza, or a migration that nests
// a field under the wrong trigger block) is caught automatically.
func TestBuiltinPrompts_NoInertLoopTriggerBlocks(t *testing.T) {
	builtinDir := filepath.Join("..", "..", "config", "prompts", "builtin")
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skipf("builtin prompts dir not found at %s: %v", builtinDir, err)
	}
	// Install the on-disk fragment registry so ParsePromptFile can resolve
	// `{{ template "name" . }}` refs at parse-time precompile (mitto-g61.4);
	// otherwise most builtin prompts fail to parse and are silently skipped
	// below, undercounting this sweep. Mirrors TestBuiltinPromptsParseClean.
	prevFrags := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prevFrags) })
	reg, loadErrs, ferr := LoadFragmentsFromDir(builtinDir)
	if ferr != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", ferr)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)
	checkedWithLoop := 0
	walkErr := filepath.WalkDir(builtinDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".prompt.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", d.Name(), err)
			return nil
		}
		prompt, err := ParsePromptFile(d.Name(), data, time.Now())
		if err != nil {
			t.Errorf("ParsePromptFile(%s): %v", d.Name(), err) // should never fail here; TestBuiltinPromptsParseClean also pins this
			return nil
		}
		if prompt.Loop == nil {
			return nil
		}
		checkedWithLoop++
		loop := prompt.Loop
		if loop.Schedule != nil && !loop.hasTrigger("schedule") {
			t.Errorf("%s: loop.schedule is set but %q is not in loop.trigger %v — this block is inert", d.Name(), "schedule", loop.Trigger)
		}
		if loop.OnCompletion != nil && !loop.hasTrigger("onCompletion") {
			t.Errorf("%s: loop.onCompletion is set but %q is not in loop.trigger %v — this block is inert", d.Name(), "onCompletion", loop.Trigger)
		}
		if loop.OnTasks != nil && !loop.hasTrigger("onTasks") {
			t.Errorf("%s: loop.onTasks is set but %q is not in loop.trigger %v — this block is inert", d.Name(), "onTasks", loop.Trigger)
		}
		if loop.OnChild != nil && !loop.hasTrigger("onChild") {
			t.Errorf("%s: loop.onChild is set but %q is not in loop.trigger %v — this block is inert", d.Name(), "onChild", loop.Trigger)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir(%s): %v", builtinDir, walkErr)
	}
	if checkedWithLoop == 0 {
		t.Error("no builtin prompts with a loop: block were checked — the walk found none, which is unexpected")
	}
	t.Logf("checked %d builtin prompts with a loop: block for inert trigger sub-blocks", checkedWithLoop)
}

// TestParsePromptFile_UnknownFragmentFails pins mitto-g61.4: a prompt whose
// body references an unknown fragment must fail at ParsePromptFile (load
// time), same class of failure as an unbalanced `{{ ... }}` — not silently
// deferred to render time. The error is expected to be wrapped by the
// "prompt file %s: ..." envelope added by ParsePromptFile around
// PrecompileTemplateConds.
func TestParsePromptFile_UnknownFragmentFails(t *testing.T) {
	SetCurrentFragments(&FragmentRegistry{entries: map[string]string{
		"test/known": "hello",
	}})
	t.Cleanup(func() { SetCurrentFragments(nil) })

	data := []byte(`name: "Broken"
prompt: |
  intro {{ template "test/unknown" . }} outro
`)

	_, err := ParsePromptFile("broken.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatalf("expected ParsePromptFile to fail for unknown fragment reference, got nil")
	}
	// AC1 (mitto-g61.4): the wrapped error must mention both the prompt name
	// and the missing fragment name so an operator can locate both sides of
	// the break from the log line alone. ParsePromptFile wraps with the file
	// path and the derived prompt name ("Broken" from the name: field); the
	// inner error must name the missing fragment ("test/unknown").
	msg := err.Error()
	if !strings.Contains(msg, "broken.prompt.yaml") {
		t.Errorf("error %q should mention the prompt file path", msg)
	}
	if !strings.Contains(msg, "Broken") {
		t.Errorf("error %q should mention the prompt name %q", msg, "Broken")
	}
	if !strings.Contains(msg, "test/unknown") {
		t.Errorf("error %q should name the missing fragment %q", msg, "test/unknown")
	}
}

// TestParsePromptFile_KnownFragmentLoads verifies the positive path: when the
// installed registry contains the referenced fragment, ParsePromptFile
// succeeds at load time (the reference is resolved by text/template's parser
// against the attached sub-template).
func TestParsePromptFile_KnownFragmentLoads(t *testing.T) {
	SetCurrentFragments(&FragmentRegistry{entries: map[string]string{
		"test/known": "hello",
	}})
	t.Cleanup(func() { SetCurrentFragments(nil) })

	data := []byte(`name: "OK"
prompt: |
  intro {{ template "test/known" . }} outro
`)

	prompt, err := ParsePromptFile("ok.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Name != "OK" {
		t.Errorf("Name = %q, want %q", prompt.Name, "OK")
	}
}
