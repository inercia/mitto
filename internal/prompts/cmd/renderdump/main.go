//go:build renderdump

// renderdump is a small dev-only helper (build-tag gated so it does not
// pollute the normal build/test) that renders a builtin prompt against
// the loaded fragment registry and prints the output to stdout.
//
// Usage from the repo root:
//
//	go run -tags renderdump ./internal/prompts/cmd/renderdump "Assess issue readiness" linked
//	go run -tags renderdump ./internal/prompts/cmd/renderdump "Assess issue readiness"
//
// Only used interactively when verifying that a fragment refactor
// preserves visible prompt output; NOT a shipped tool.
package main

import (
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/cel"
	"github.com/inercia/mitto/internal/prompts"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: renderdump <prompt-name> [linked]")
		os.Exit(2)
	}
	target := os.Args[1]
	haveLink := len(os.Args) > 2 && os.Args[2] == "linked"

	reg, _, err := prompts.LoadFragmentsFromDir("config/prompts/builtin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load fragments:", err)
		os.Exit(1)
	}
	prompts.SetCurrentFragments(reg)

	list, err := prompts.LoadPromptsFromDir("config/prompts/builtin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load prompts:", err)
		os.Exit(1)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s1", Name: "test", HasMessages: true},
		Args:    map[string]string{},
	}
	if haveLink {
		ctx.Session.BeadsIssue = "mitto-42"
		ctx.Session.HasBeadsIssue = true
	}
	// Extra positional args of the form K=V populate .Args (any position
	// after the mandatory prompt-name / mode). Useful for prompts that
	// branch on parameters (e.g. MentionTS, Commit, IssueID).
	for _, arg := range os.Args[2:] {
		if arg == "linked" {
			continue
		}
		for i := 0; i < len(arg); i++ {
			if arg[i] == '=' {
				ctx.Args[arg[:i]] = arg[i+1:]
				break
			}
		}
	}

	for _, p := range list {
		if p.Name == target {
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, err := prompts.RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
			if err != nil {
				fmt.Fprintln(os.Stderr, "render:", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "prompt not found:", target)
	os.Exit(1)
}
