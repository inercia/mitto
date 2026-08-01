package cel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadTemplate_HappyPath covers .Args expansion, fast-path bit-identity
// for files without `{{`, and empty-file returning "".
func TestReadTemplate_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	// File that uses .Args and a FuncMap helper.
	if err := os.WriteFile(filepath.Join(tmpDir, "greet.md"), []byte(`Hello {{ Upper .Args.Name }}!`), 0644); err != nil {
		t.Fatal(err)
	}
	// File with no `{{` — must be returned verbatim by the fast path
	// (no parse cost, no missingkey substitution).
	verbatim := "line 1\nno template metacharacters here\nline 3\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "verbatim.md"), []byte(verbatim), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty file — readFile returns "" (fail-open), readTemplate short-circuits.
	if err := os.WriteFile(filepath.Join(tmpDir, "empty.md"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := &PromptEnabledContext{
		Workspace: WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"Name": "alice"},
	}
	fm := BuildTemplateFuncMap(ctx)

	t.Run("expands .Args and FuncMap helpers", func(t *testing.T) {
		got, err := RenderPromptTemplate("t", `{{ ReadTemplate "greet.md" . }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "Hello ALICE!" {
			t.Errorf("got %q, want %q", got, "Hello ALICE!")
		}
	})

	t.Run("verbatim body returned unchanged (fast-path skips parse)", func(t *testing.T) {
		got, err := RenderPromptTemplate("t", `{{ ReadTemplate "verbatim.md" . }}`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != verbatim {
			t.Errorf("got %q, want %q", got, verbatim)
		}
	})

	t.Run("empty file returns empty string", func(t *testing.T) {
		got, err := RenderPromptTemplate("t", `[{{ ReadTemplate "empty.md" . }}]`, ctx, fm)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "[]" {
			t.Errorf("got %q, want %q", got, "[]")
		}
	})
}

// TestReadTemplate_ReadStepSafetyParity verifies the read step inherits
// ReadFile's fail-open path safety and size cap: absolute path, "..", is-dir,
// missing, and empty-path all render as empty (no error).
func TestReadTemplate_ReadStepSafetyParity(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
	fm := BuildTemplateFuncMap(ctx)

	cases := []struct {
		name string
		body string
	}{
		{"missing", `[{{ ReadTemplate "absent.md" . }}]`},
		{"empty_path", `[{{ ReadTemplate "" . }}]`},
		{"is_dir", `[{{ ReadTemplate "sub" . }}]`},
		{"path_escape_dotdot", `[{{ ReadTemplate "../etc/passwd" . }}]`},
		{"absolute_path_rejected", `[{{ ReadTemplate "/etc/passwd" . }}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPromptTemplate("t", tc.body, ctx, fm)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != "[]" {
				t.Errorf("got %q, want %q", got, "[]")
			}
		})
	}
}

// TestReadTemplate_RenderFailClosed verifies the render step is fail-closed
// on parse errors and unknown-func references.
func TestReadTemplate_RenderFailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	// Parse error: unterminated action.
	if err := os.WriteFile(filepath.Join(tmpDir, "broken.md"), []byte(`{{ .Args.X `), 0644); err != nil {
		t.Fatal(err)
	}
	// Unknown func reference — parse-time failure ("function not defined").
	if err := os.WriteFile(filepath.Join(tmpDir, "unknown.md"), []byte(`{{ NoSuchFunction "x" }}`), 0644); err != nil {
		t.Fatal(err)
	}
	// Fragment reference — Phase-A limitation: fragments are NOT attached in
	// the sub-render, so this parse-fails with a "template ... not defined"
	// exec error.
	if err := os.WriteFile(filepath.Join(tmpDir, "frag.md"), []byte(`{{ template "_shared/foo" . }}`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
	fm := BuildTemplateFuncMap(ctx)

	t.Run("parse error propagates", func(t *testing.T) {
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "broken.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
		if !strings.Contains(err.Error(), "ReadTemplate") {
			t.Errorf("error should mention ReadTemplate; got %v", err)
		}
	})

	t.Run("unknown func propagates", func(t *testing.T) {
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "unknown.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected unknown-func error, got nil")
		}
	})

	t.Run("fragment reference fails (no fragments attached)", func(t *testing.T) {
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "frag.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected fragment-not-defined error, got nil")
		}
	})
}

// TestReadTemplate_MissingKeyZero verifies that referencing a missing .Args
// entry from an included template body renders as empty (matches the outer
// renderer's Option("missingkey=zero")), NOT as "<no value>".
func TestReadTemplate_MissingKeyZero(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "opt.md"), []byte(`[{{ .Args.NotSet }}]`), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
	fm := BuildTemplateFuncMap(ctx)
	got, err := RenderPromptTemplate("t", `{{ ReadTemplate "opt.md" . }}`, ctx, fm)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "[]" {
		t.Errorf("got %q, want %q (missingkey=zero must render absent Args as empty)", got, "[]")
	}
}

// TestReadTemplate_DepthGuard verifies the recursion cap: a file that reads
// itself hits promptTextMaxDepth and errors out (fail-closed) rather than
// blowing the stack. Also verifies a two-file cycle (a -> b -> a) is capped
// the same way.
func TestReadTemplate_DepthGuard(t *testing.T) {
	t.Run("self-cycle exceeds depth", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "self.md"), []byte(`{{ ReadTemplate "self.md" . }}`), 0644); err != nil {
			t.Fatal(err)
		}
		ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "self.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected recursion-depth error, got nil")
		}
		if !strings.Contains(err.Error(), "recursion depth exceeded") {
			t.Errorf("error should mention 'recursion depth exceeded'; got %v", err)
		}
	})

	t.Run("two-file cycle exceeds depth", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "a.md"), []byte(`{{ ReadTemplate "b.md" . }}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "b.md"), []byte(`{{ ReadTemplate "a.md" . }}`), 0644); err != nil {
			t.Fatal(err)
		}
		ctx := &PromptEnabledContext{Workspace: WorkspaceContext{Folder: tmpDir}}
		fm := BuildTemplateFuncMap(ctx)
		_, err := RenderPromptTemplate("t", `{{ ReadTemplate "a.md" . }}`, ctx, fm)
		if err == nil {
			t.Fatal("expected recursion-depth error, got nil")
		}
		if !strings.Contains(err.Error(), "recursion depth exceeded") {
			t.Errorf("error should mention 'recursion depth exceeded'; got %v", err)
		}
	})

	t.Run("depth cap constant unchanged", func(t *testing.T) {
		// Pin the constant so a future change that raises it forces a
		// deliberate test update rather than silently loosening the guard.
		if promptTextMaxDepth != 3 {
			t.Errorf("promptTextMaxDepth = %d, want 3 (if the cap is intentionally raised, update this test and audit ReadTemplate/PromptTextWithArgs docs)", promptTextMaxDepth)
		}
	})
}

// TestReadTemplate_NilCtxSafe verifies ReadTemplate is safe when
// BuildTemplateFuncMap is called with a nil ctx (folder == "" via readFile).
// The read short-circuits so the render step never runs; result is empty.
func TestReadTemplate_NilCtxSafe(t *testing.T) {
	fm := BuildTemplateFuncMap(nil)
	got, err := RenderPromptTemplate("t", `[{{ ReadTemplate "anything.md" . }}]`, nil, fm)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "[]" {
		t.Errorf("nil-ctx ReadTemplate got %q, want %q", got, "[]")
	}
}
