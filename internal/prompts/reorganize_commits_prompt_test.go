package prompts

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestReorganizeCommitsPrompt(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	rel := "git/reorganize-commits.prompt.yaml"
	data, err := os.ReadFile("../../config/prompts/builtin/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePromptFile(rel, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if p.Name != "Reorganize commits" || p.Group != "Submission of changes" {
		t.Fatalf("name/group = %q/%q", p.Name, p.Group)
	}
	frag, err := os.ReadFile("../../config/prompts/builtin/git/shared/reorganize-commits.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `template "git/shared/reorganize-commits"`) {
		t.Error("prompt must include the shared fragment")
	}
	all := string(data) + string(frag)
	for _, want := range []string{
		"Approve and rewrite",
		"git reset --hard <backup>",
		"git diff <backup> HEAD",
		"--force-with-lease",
		"Timeout, decline, cancel",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(all, "timeout-default-continue") {
		t.Error("approval must never default-continue on timeout")
	}
}

func TestReorganizeCommitsParameterWiring(t *testing.T) {
	for _, f := range []string{
		"github/address-pr-comments", "github/check-pr-comments",
		"github/babysit-this-pr", "github/babysit-my-prs",
	} {
		b, err := os.ReadFile("../../config/prompts/builtin/" + f + ".prompt.yaml")
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, "name: ReorganizeCommits") {
			t.Errorf("%s missing ReorganizeCommits parameter", f)
		}
	}
	for f, want := range map[string]string{
		"github/address-pr-comments.prompt.yaml": `template "git/shared/reorganize-commits"`,
		"github/check-pr-comments.prompt.yaml":   `"ReorganizeCommits": "{{ .Args.ReorganizeCommits }}"`,
		"github/shared/pr-comments.tmpl":         `eq .Args.ReorganizeCommits "true"`,
	} {
		b, err := os.ReadFile("../../config/prompts/builtin/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s missing %q", f, want)
		}
	}
}
