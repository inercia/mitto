package prompts

import (
	"strings"
	"testing"
	"text/template"

	"github.com/inercia/mitto/internal/cel"
)

// TestBlogSharedFragmentsExistAndParse is a presence-and-parseability smoke
// test for the mitto-98l.1 blog-suite shared fragments. It asserts:
//
//   - The four fragment files load into FragmentRegistry under their expected
//     slash-namespaced names (blog/shared/<stem>).
//   - Each fragment parses under LoadFragmentsFromDir without per-file errors.
//   - Each fragment renders to a non-empty body when invoked with a dot value
//     shaped like its documented usage — this catches template-syntax breakage
//     that the load-time dry-run tolerates for parameterised fragments (see
//     validateFragmentBody's parameterised-fragment fallback).
//
// Consumer-hallmark assertions (fragment X inlines into prompt Y with substring
// Z) are intentionally left out: no consumer prompts exist yet. Per-prompt
// child issues under mitto-98l (ideation, content-review, fact-check,
// add-references, polish, publish, linkedin-post) will each extend this test
// with a hallmark row when their consumer lands, matching the jira/git
// fragment-smoke-test pattern.
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
		"blog/shared/locate-post-file",
		"blog/shared/blog-config-fragment",
		"blog/shared/audience-and-tone",
		"blog/shared/attach-file-to-bead",
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
		Args:    map[string]string{"IssueID": "mitto-abc.1", "Folder": "blog/posts"},
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
			fragment: "blog/shared/locate-post-file",
			wrapper:  `{{ template "blog/shared/locate-post-file" . }}`,
			hallmark: "File: [path](path)",
		},
		{
			fragment: "blog/shared/blog-config-fragment",
			wrapper: `{{ template "blog/shared/blog-config-fragment" ` +
				`(dict "Name" "audience" "DefaultText" "SMOKE_DEFAULT_AUDIENCE") }}`,
			hallmark: "SMOKE_DEFAULT_AUDIENCE",
		},
		{
			fragment: "blog/shared/audience-and-tone",
			wrapper:  `{{ template "blog/shared/audience-and-tone" . }}`,
			hallmark: "Expert practitioners",
		},
		{
			fragment: "blog/shared/attach-file-to-bead",
			wrapper: `{{ template "blog/shared/attach-file-to-bead" ` +
				`(dict "IssueID" "mitto-abc.1" "Path" "blog/posts/draft-slug.md") }}`,
			hallmark: "scripts/bd-attach.sh add mitto-abc.1 blog/posts/draft-slug.md",
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

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s-test", Name: "Test"},
		Args:    map[string]string{"Folder": "blog/posts"},
	}

	// Hallmarks: substrings unique to each fragment's own body that must appear
	// in the rendered prompt if the {{ template "..." }} call inlined correctly.
	wantHallmarks := map[string][]string{
		"Blog: ideation": {
			"bd init --non-interactive",                   // from beads-issues/shared/bootstrap
			"Expert practitioners",                        // from blog/shared/audience-and-tone (default)
			"No topics.md file configured",                // from blog/shared/blog-config-fragment (topics default)
			"scripts/bd-attach.sh add $new_id $post_path", // from blog/shared/attach-file-to-bead
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
		Args:    map[string]string{"IssueID": "mitto-abc.1", "Folder": "blog/posts"},
	}

	// Hallmarks: substrings unique to each fragment's own body that must
	// appear in the rendered prompt if the {{ template "..." }} call inlined
	// correctly. content-review and fact-check pull in the audience fragment;
	// add-references does not.
	wantHallmarks := map[string][]string{
		"Blog: content review": {
			"File: [path](path)",   // from blog/shared/locate-post-file
			"post_abs=$post_abs",   // from blog/shared/locate-post-file (bash resolve)
			"Expert practitioners", // from blog/shared/blog-config-fragment (audience default)
		},
		"Blog: fact-check": {
			"File: [path](path)",   // from blog/shared/locate-post-file
			"post_abs=$post_abs",   // from blog/shared/locate-post-file
			"Expert practitioners", // from blog/shared/blog-config-fragment (audience default)
		},
		"Blog: add references": {
			"File: [path](path)", // from blog/shared/locate-post-file
			"post_abs=$post_abs", // from blog/shared/locate-post-file
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
// `{{ template "..." . }}` call it makes (locate-post-file + audience via
// blog-config-fragment) appear in the rendered body. Modelled on
// TestBlogReviewFamilyPromptFragmentHallmarks.
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
		Args:    map[string]string{"IssueID": "mitto-abc.1", "Folder": "blog/posts", "Mode": "General"},
	}

	// Hallmarks from each fragment the polish prompt calls. locate-post-file
	// contributes the File-line convention prose and the bash resolve echo;
	// blog-config-fragment contributes the audience default text.
	wantHallmarks := map[string][]string{
		"Blog: polish": {
			"File: [path](path)",   // from blog/shared/locate-post-file
			"post_abs=$post_abs",   // from blog/shared/locate-post-file (bash resolve)
			"Expert practitioners", // from blog/shared/blog-config-fragment (audience default)
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
