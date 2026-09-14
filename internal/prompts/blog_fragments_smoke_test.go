package prompts

import (
	"strings"
	"testing"
	"text/template"

	"github.com/inercia/mitto/internal/cel"
)

// TestBlogSharedFragmentsExistAndParse is a presence-and-parseability smoke
// test for the blog-suite shared fragments. It asserts:
//
//   - The fragment files load into FragmentRegistry under their expected
//     slash-namespaced names (blog/shared/<stem>).
//   - Each fragment parses under LoadFragmentsFromDir without per-file errors.
//   - Each fragment renders to a non-empty body when invoked with a dot value
//     shaped like its documented usage — this catches template-syntax breakage
//     that the load-time dry-run tolerates for parameterised fragments (see
//     validateFragmentBody's parameterised-fragment fallback).
//
// The blog suite stores post content in the bead (title + description) and
// agent notes in comments; there is no post file on disk during drafting.
func TestBlogSharedFragmentsExistAndParse(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	wantNames := []string{
		"blog/shared/load-post-from-bead",
		"blog/shared/update-post-in-bead",
		"blog/shared/blog-config-fragment",
		"blog/shared/audience-and-style",
		"blog/shared/read-or-ask-persist",
	}
	for _, name := range wantNames {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("fragment %q not found in registry (loaded %d fragments: %v)",
				name, reg.Len(), reg.Names())
		}
	}

	// Render each fragment with a dot value shaped like the documented usage,
	// to catch syntax breakage the load-time dry-run tolerates. The whole
	// registry is exposed via the same {{ template "..." }} lookup path
	// RenderPromptTemplate uses at runtime, so we build a root template that
	// pre-declares every registered fragment and then Execute the wrapper.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"IssueID": "mitto-abc.1"},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	// Rows: fragment name → wrapper body that invokes it with a compatible
	// dot value, and a hallmark substring proving the fragment's own body
	// (not the wrapper's) rendered.
	type row struct {
		fragment string
		wrapper  string
		hallmark string
	}
	rows := []row{
		{
			fragment: "blog/shared/load-post-from-bead",
			wrapper: `{{ template "blog/shared/load-post-from-bead" ` +
				`(dict "Target" "mitto-abc.1") }}`,
			hallmark: "post_file=$post_file",
		},
		{
			fragment: "blog/shared/update-post-in-bead",
			wrapper: `{{ template "blog/shared/update-post-in-bead" ` +
				`(dict "Target" "mitto-abc.1") }}`,
			hallmark: "bd update mitto-abc.1 --body-file",
		},
		{
			fragment: "blog/shared/blog-config-fragment",
			wrapper: `{{ template "blog/shared/blog-config-fragment" ` +
				`(dict "Name" "audience" "DefaultText" "SMOKE_DEFAULT_AUDIENCE") }}`,
			hallmark: "SMOKE_DEFAULT_AUDIENCE",
		},
		{
			fragment: "blog/shared/audience-and-style",
			wrapper:  `{{ template "blog/shared/audience-and-style" . }}`,
			hallmark: "Expert practitioners",
		},
		{
			fragment: "blog/shared/read-or-ask-persist",
			wrapper: `{{ template "blog/shared/read-or-ask-persist" ` +
				`(dict "Name" "publish" "SessionID" "s-test" ` +
				`"Purpose" "SMOKE_PURPOSE" "AskFields" "  - SMOKE_ASK") }}`,
			hallmark: "SMOKE_PURPOSE",
		},
	}

	for _, r := range rows {
		// Build a root template that pre-declares every registered fragment,
		// so `{{ template "blog/shared/..." . }}` in the wrapper resolves.
		root := template.New("smoke").Funcs(funcs)
		for name, body := range reg.All() {
			if _, err := root.New(name).Parse(body); err != nil {
				t.Errorf("fragment %q: parse error: %v", name, err)
				continue
			}
		}
		wrap, err := root.New("wrapper").Parse(r.wrapper)
		if err != nil {
			t.Errorf("fragment %q: wrapper parse error: %v", r.fragment, err)
			continue
		}
		var buf strings.Builder
		if err := wrap.ExecuteTemplate(&buf, "wrapper", ctx); err != nil {
			t.Errorf("fragment %q: render error: %v", r.fragment, err)
			continue
		}
		out := buf.String()
		if !strings.Contains(out, r.hallmark) {
			t.Errorf("fragment %q: rendered output missing hallmark %q; got:\n%s",
				r.fragment, r.hallmark, out)
		}
	}
}

// TestBlogIdeationPromptFragmentHallmarks is the consumer-hallmark smoke test
// for mitto-98l.2: it renders `blog/ideation.prompt.yaml` and asserts that each
// `{{ template "..." . }}` call it makes actually inlined its fragment's body.
// Modelled on TestJiraFragmentsRenderCorrectly.
//
// Ideation is dual-mode (mitto-98l idea-capture): with no target bead it
// renders the entry-point mode (capture-a-quick-idea / draft-a-full-post); with
// a target bead (from .Args.IssueID or .Session.BeadsIssue) it renders the
// promotion mode that develops a blog/idea bead into a full draft. Both modes
// are rendered here so a broken {{ template }} call or {{ if $target }} branch
// in either arm is caught.
func TestBlogIdeationPromptFragmentHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}
	body, ok := byName["Blog: ideation"]
	if !ok {
		t.Fatalf("prompt %q not found in builtin corpus", "Blog: ideation")
	}

	// Each mode is driven by a distinct render context and asserts hallmarks
	// unique to the fragments/branch that mode renders.
	cases := []struct {
		name      string
		ctx       *cel.PromptEnabledContext
		hallmarks []string
	}{
		{
			// Entry-point mode: no target bead → the {{ else }} branch renders
			// the bootstrap + capture/draft flow.
			name: "entry-point",
			ctx: &cel.PromptEnabledContext{
				Session: cel.SessionContext{ID: "s-test", Name: "Test"},
				Args:    map[string]string{},
			},
			hallmarks: []string{
				"bd init --non-interactive",    // from beads-issues/shared/bootstrap
				"Capture a quick idea",         // entry-point capture branch
				"bd create -l blog,blog:idea",  // Step 2A idea-capture create
				"Expert practitioners",         // from blog/shared/audience-and-style (audience default)
				"Slightly informal",            // from blog/shared/audience-and-style (style default)
				"No topics.md file configured", // from blog/shared/blog-config-fragment (topics default)
			},
		},
		{
			// Promotion mode: a target bead supplied via IssueID → the
			// {{ if $target }} branch renders the promotion flow.
			name: "promotion",
			ctx: &cel.PromptEnabledContext{
				Session: cel.SessionContext{ID: "s-test", Name: "Test"},
				Args:    map[string]string{"IssueID": "mitto-abc.1"},
			},
			hallmarks: []string{
				"target idea bead",                  // from beads-issues/shared/target-bead-header-strict (Noun)
				"--include-comments",                // Step P1 loads the idea seed from the first comment
				"Expert practitioners",              // from blog/shared/audience-and-style (audience default)
				"No topics.md file configured",      // from blog/shared/blog-config-fragment (topics default)
				"bd update mitto-abc.1 --body-file", // Step P4 seeds the description
				"--remove-label blog:idea",          // Step P5 advances blog:idea→blog:draft
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			funcs := cel.BuildTemplateFuncMap(tc.ctx)
			out, err := RenderPromptTemplate("Blog: ideation", body, tc.ctx, funcs)
			if err != nil {
				t.Fatalf("render %q (%s): %v", "Blog: ideation", tc.name, err)
			}
			for _, needle := range tc.hallmarks {
				if !strings.Contains(out, needle) {
					t.Errorf("prompt %q (%s mode): rendered output missing hallmark %q — fragment/branch did not inline correctly", "Blog: ideation", tc.name, needle)
				}
			}
		})
	}
}

// TestBlogReviewFamilyPromptFragmentHallmarks is the consumer-hallmark smoke
// test for mitto-98l.3: it renders each of the three review-family blog
// prompts (content-review, fact-check, add-references) and asserts hallmarks
// from every `{{ template "..." . }}` call they make appear in the rendered
// body. Modelled on TestBlogIdeationPromptFragmentHallmarks.
func TestBlogReviewFamilyPromptFragmentHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"IssueID": "mitto-abc.1"},
	}

	// Hallmarks: substrings unique to each fragment's own body that must
	// appear in the rendered prompt if the {{ template "..." }} call inlined
	// correctly. content-review and fact-check pull in the audience fragment;
	// add-references does not.
	wantHallmarks := map[string][]string{
		"Blog: content review": {
			"post_file=$post_file",   // from blog/shared/load-post-from-bead
			"post_title=$post_title", // from blog/shared/load-post-from-bead
			"Expert practitioners",   // from blog/shared/blog-config-fragment (audience default)
		},
		"Blog: fact-check": {
			"post_file=$post_file",   // from blog/shared/load-post-from-bead
			"post_title=$post_title", // from blog/shared/load-post-from-bead
			"Expert practitioners",   // from blog/shared/blog-config-fragment (audience default)
		},
		"Blog: add references": {
			"post_file=$post_file",   // from blog/shared/load-post-from-bead
			"post_title=$post_title", // from blog/shared/load-post-from-bead
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for promptName, hallmarks := range wantHallmarks {
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			continue
		}
		for _, needle := range hallmarks {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline correctly", promptName, needle)
			}
		}
	}
}

// TestBlogPolishPromptFragmentHallmarks is the consumer-hallmark smoke test
// for mitto-98l.4: it renders `blog/polish.prompt.yaml` with the default
// Mode ("General") and asserts that hallmarks from every
// `{{ template "..." . }}` call it makes (load-post-from-bead + audience via
// blog-config-fragment + update-post-in-bead) appear in the rendered body.
// Modelled on TestBlogReviewFamilyPromptFragmentHallmarks.
func TestBlogPolishPromptFragmentHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"IssueID": "mitto-abc.1", "Mode": "General"},
	}

	// Hallmarks from each fragment the polish prompt calls. load-post-from-bead
	// contributes the post_file/post_title echoes; blog-config-fragment
	// contributes the audience default text; update-post-in-bead contributes
	// the bd update --body-file line.
	wantHallmarks := map[string][]string{
		"Blog: polish": {
			"post_file=$post_file",              // from blog/shared/load-post-from-bead
			"Expert practitioners",              // from blog/shared/blog-config-fragment (audience default)
			"bd update mitto-abc.1 --body-file", // from blog/shared/update-post-in-bead
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for promptName, hallmarks := range wantHallmarks {
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			continue
		}
		for _, needle := range hallmarks {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline correctly", promptName, needle)
			}
		}
	}
}

// TestBlogPublishPromptFragmentHallmarks is the consumer-hallmark smoke test
// for mitto-98l.5: it renders `blog/publish.prompt.yaml` and asserts that
// hallmarks from every `{{ template "..." . }}` call it makes
// (load-post-from-bead + blog-config-fragment audience/style +
// read-or-ask-persist) appear in the rendered body. Modelled on
// TestBlogPolishPromptFragmentHallmarks.
func TestBlogPublishPromptFragmentHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"IssueID": "mitto-abc.1"},
	}

	// Hallmarks from every fragment the publish prompt calls: load-post-from-bead
	// contributes the post_file echo; blog-config-fragment (audience default)
	// contributes the expert-practitioners text; read-or-ask-persist (ask
	// branch, since .mitto/blog/publish.md is absent in the test CWD)
	// contributes the Purpose phrase.
	wantHallmarks := map[string][]string{
		"Blog: publish": {
			"post_file=$post_file",                    // from blog/shared/load-post-from-bead
			"Expert practitioners",                    // from blog/shared/blog-config-fragment (audience default)
			"how and where to publish this blog post", // from blog/shared/read-or-ask-persist (Purpose)
			"What format does the destination expect", // format field in the read-or-ask-persist AskFields
			"pandoc -f markdown -t",                   // Step 5 best-effort Markdown->destination-format conversion
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for promptName, hallmarks := range wantHallmarks {
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			continue
		}
		for _, needle := range hallmarks {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline correctly", promptName, needle)
			}
		}
	}
}

// TestBlogLinkedinPostPromptFragmentHallmarks is the consumer-hallmark smoke
// test for mitto-98l.6: it renders `blog/linkedin-post.prompt.yaml` and
// asserts that hallmarks from every `{{ template "..." . }}` call it makes
// (load-post-from-bead + audience-and-style -> blog-config-fragment twice for
// audience+style + blog-config-fragment for linkedin-template) appear in the
// rendered body. Modelled on TestBlogPublishPromptFragmentHallmarks.
func TestBlogLinkedinPostPromptFragmentHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"IssueID": "mitto-abc.1"},
	}

	// Hallmarks from every fragment the linkedin-post prompt calls:
	// load-post-from-bead contributes the post_file echo; audience-and-style
	// forwards the caller's dot to blog-config-fragment twice, which in turn
	// contributes the audience "Expert practitioners" default and the style
	// "Slightly informal" default; blog-config-fragment (linkedin-template
	// default) contributes the no-template-configured line.
	wantHallmarks := map[string][]string{
		"Blog: linkedin-post": {
			"post_file=$post_file",                    // from blog/shared/load-post-from-bead
			"Expert practitioners",                    // from blog/shared/audience-and-style -> blog-config-fragment (audience default)
			"Slightly informal",                       // from blog/shared/audience-and-style -> blog-config-fragment (style default)
			"No linkedin-template.md file configured", // from blog/shared/blog-config-fragment (linkedin-template default)
		},
	}

	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	for promptName, hallmarks := range wantHallmarks {
		body, ok := byName[promptName]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", promptName)
			continue
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q: %v", promptName, err)
			continue
		}
		for _, needle := range hallmarks {
			if !strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — fragment did not inline correctly", promptName, needle)
			}
		}
	}
}
