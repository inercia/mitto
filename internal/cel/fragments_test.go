package cel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFragmentProviderForTest installs a provider returning entries as the
// package-wide fragmentProvider hook for the duration of the test, restoring
// nil (no provider) via t.Cleanup. Mirrors internal/prompts' own
// installFragments test helper — kept local here since internal/cel must not
// import internal/prompts (mitto-b8k.3, pinned by
// TestReadTemplate_NoPromptsImport).
func installFragmentProviderForTest(t *testing.T, entries map[string]string) {
	t.Helper()
	SetFragmentProvider(func() map[string]string { return entries })
	t.Cleanup(func() { SetFragmentProvider(nil) })
}

// TestFragmentsForNestedRender_NilSafe covers the raw accessor: no provider
// installed, a provider returning nil, and a provider returning entries.
func TestFragmentsForNestedRender_NilSafe(t *testing.T) {
	t.Cleanup(func() { SetFragmentProvider(nil) })

	t.Run("no provider installed", func(t *testing.T) {
		SetFragmentProvider(nil)
		if got := fragmentsForNestedRender(); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("provider returns nil", func(t *testing.T) {
		SetFragmentProvider(func() map[string]string { return nil })
		if got := fragmentsForNestedRender(); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("provider returns entries", func(t *testing.T) {
		want := map[string]string{"a": "b"}
		SetFragmentProvider(func() map[string]string { return want })
		got := fragmentsForNestedRender()
		if len(got) != 1 || got["a"] != "b" {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestReadTemplate_FragmentsAttached verifies (mitto-twa) that a file rendered
// via ReadTemplate can resolve {{ template "_shared/..." . }} against the
// installed fragment provider, the same way a top-level prompt render would.
func TestReadTemplate_FragmentsAttached(t *testing.T) {
	installFragmentProviderForTest(t, map[string]string{
		"_shared/greet": "Hello {{ .Args.Name }}!",
	})

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "frag.md"), []byte(`{{ template "_shared/greet" . }}`), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &PromptEnabledContext{
		Workspace: WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"Name": "alice"},
	}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("t", `{{ ReadTemplate "frag.md" . }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Hello alice!" {
		t.Errorf("got %q, want %q", got, "Hello alice!")
	}
}

// TestPromptTextWithArgs_FragmentsAttached verifies (mitto-twa) that a prompt
// body fetched and sub-rendered via PromptTextWithArgs can resolve
// {{ template "_shared/..." . }} against the installed fragment provider.
func TestPromptTextWithArgs_FragmentsAttached(t *testing.T) {
	installFragmentProviderForTest(t, map[string]string{
		"_shared/greet": "Hello {{ .Args.Name }}!",
	})

	resolver := func(name string) (string, error) {
		if name == "greeter" {
			return `{{ template "_shared/greet" . }}`, nil
		}
		return "", fmt.Errorf("resolver: unknown %q", name)
	}
	ctx := &PromptEnabledContext{PromptTextResolver: resolver}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("t", `{{ PromptTextWithArgs "greeter" (dict "Name" "bob") }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "Hello bob!" {
		t.Errorf("got %q, want %q", got, "Hello bob!")
	}
}

// TestNestedRender_FragmentsDataNarrowing verifies a fragment invoked with a
// narrowed dot (e.g. `.Args` instead of `.`) sees only that narrowed value —
// covered through both sub-render paths (ReadTemplate, PromptTextWithArgs).
func TestNestedRender_FragmentsDataNarrowing(t *testing.T) {
	installFragmentProviderForTest(t, map[string]string{
		"_shared/args-only": "foo={{ .Foo }}",
	})

	t.Run("via ReadTemplate", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "narrow.md"), []byte(`{{ template "_shared/args-only" .Args }}`), 0644); err != nil {
			t.Fatal(err)
		}
		ctx := &PromptEnabledContext{
			Workspace: WorkspaceContext{Folder: tmpDir},
			Args:      map[string]string{"Foo": "bar"},
		}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("t", `{{ ReadTemplate "narrow.md" . }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "foo=bar" {
			t.Errorf("got %q, want %q", got, "foo=bar")
		}
	})

	t.Run("via PromptTextWithArgs", func(t *testing.T) {
		resolver := func(name string) (string, error) {
			if name == "narrower" {
				return `{{ template "_shared/args-only" .Args }}`, nil
			}
			return "", fmt.Errorf("resolver: unknown %q", name)
		}
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("t", `{{ PromptTextWithArgs "narrower" (dict "Foo" "baz") }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "foo=baz" {
			t.Errorf("got %q, want %q", got, "foo=baz")
		}
	})
}

// TestNestedRender_FragmentFuncMapInheritance verifies a fragment attached to
// a sub-render inherits the same FuncMap as the sub-render itself (e.g. the
// Arg builtin), and that Arg resolves against the sub-render's OWN .Args
// scope (the PromptTextWithArgs inner args), not the outer render's.
func TestNestedRender_FragmentFuncMapInheritance(t *testing.T) {
	installFragmentProviderForTest(t, map[string]string{
		"_shared/arg-helper": `x={{ Arg "X" "def" }}`,
	})
	resolver := func(name string) (string, error) {
		if name == "user-of-arg" {
			return `{{ template "_shared/arg-helper" . }}`, nil
		}
		return "", fmt.Errorf("resolver: unknown %q", name)
	}

	t.Run("inner Arg set", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: resolver, Args: map[string]string{"X": "outer"}}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("t", `{{ PromptTextWithArgs "user-of-arg" (dict "X" "inner") }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "x=inner" {
			t.Errorf("got %q, want %q (Arg must read the sub-render's own .Args, not the outer scope)", got, "x=inner")
		}
	})

	t.Run("inner Arg absent falls back to fragment default", func(t *testing.T) {
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		got, err := RenderPromptTemplate("t", `{{ PromptTextWithArgs "user-of-arg" (dict) }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "x=def" {
			t.Errorf("got %q, want %q", got, "x=def")
		}
	})
}

// TestNestedRender_FragmentParseErrorFailsClosed verifies a malformed
// fragment returned by the provider fails the sub-render closed (not
// silently truncated), through both sub-render paths, with an error naming
// the offending fragment — mirrors internal/prompts'
// TestRenderPromptTemplate_Fragments "fragment-parse-error-fails-closed".
func TestNestedRender_FragmentParseErrorFailsClosed(t *testing.T) {
	installFragmentProviderForTest(t, map[string]string{
		"_shared/broken": "{{ if .Flag }}oops", // missing {{ end }}
	})

	t.Run("via ReadTemplate", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "usesbroken.md"), []byte(`{{ template "_shared/broken" . }}`), 0644); err != nil {
			t.Fatal(err)
		}
		ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "usesbroken.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsAll(err.Error(), "fragment", "_shared/broken") {
			t.Errorf("error %q should mention 'fragment' and name '_shared/broken'", err.Error())
		}
	})

	t.Run("via PromptTextWithArgs", func(t *testing.T) {
		resolver := func(name string) (string, error) {
			return `{{ template "_shared/broken" . }}`, nil
		}
		ctx := &PromptEnabledContext{PromptTextResolver: resolver}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("t", `{{ PromptTextWithArgs "any" (dict) }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !containsAll(err.Error(), "fragment", "_shared/broken") {
			t.Errorf("error %q should mention 'fragment' and name '_shared/broken'", err.Error())
		}
	})
}

// containsAll reports whether s contains every substring in subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
