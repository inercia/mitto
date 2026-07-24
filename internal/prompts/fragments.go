package prompts

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/inercia/mitto/internal/cel"
)

// fragmentExt is the file extension used for prompt fragments. It is matched
// case-insensitively; regular .prompt.yaml prompt files are ignored by the
// fragment loader (and, conversely, LoadPromptsFromDir ignores .tmpl files —
// see internal/prompts/prompts.go:637). Co-location isolation is structural:
// the two loaders share directories but never share the file set.
const fragmentExt = ".tmpl"

// FragmentLoadError describes a single fragment file that failed to load/parse
// or violated a naming rule (duplicate, illegal path segment). The loader
// accumulates these instead of aborting so a broken fragment does not prevent
// the rest of the tree from loading.
type FragmentLoadError struct {
	Path string // path (relative to the scanned dir, forward-slash normalised)
	Err  error  // underlying error (parse failure, duplicate name, illegal segment, …)
}

// Error satisfies the error interface.
func (e FragmentLoadError) Error() string {
	return fmt.Sprintf("fragment %s: %v", e.Path, e.Err)
}

// Unwrap exposes the underlying error for errors.Is / errors.As.
func (e FragmentLoadError) Unwrap() error {
	return e.Err
}

// FragmentRegistry holds a set of prompt fragments keyed by their
// slash-namespaced name (relative path from the origin root with the .tmpl
// suffix stripped). A registry is safe for concurrent read/merge.
//
// This is child #1 of the mitto-g61 epic (prompt template fragments via native
// {{ template "name" . }}). It delivers the type and on-disk loader only —
// wiring into RenderPromptTemplate, PrecompileTemplateConds, and the fs-watcher
// are separate children.
type FragmentRegistry struct {
	mu      sync.RWMutex
	entries map[string]string
}

// NewFragmentRegistry returns an empty registry.
func NewFragmentRegistry() *FragmentRegistry {
	return &FragmentRegistry{entries: map[string]string{}}
}

// LoadFragmentsFromDir loads all *.tmpl fragments from a directory recursively.
// The loader walks the same directory tree as LoadPromptsFromDir but filters
// strictly on the .tmpl extension (case-insensitive), so it never picks up
// .prompt.yaml files.
//
// Returns an empty registry (never nil) if the directory does not exist —
// matching LoadPromptsFromDirWithErrors' tolerance for absent workspace prompt
// directories. Per-file failures (parse errors, duplicate names, illegal path
// segments) are accumulated into the returned []FragmentLoadError; the walker
// only returns a top-level error for filesystem walk failures.
func LoadFragmentsFromDir(dir string) (*FragmentRegistry, []FragmentLoadError, error) {
	reg := NewFragmentRegistry()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return reg, nil, nil
	}

	var loadErrors []FragmentLoadError

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process *.tmpl files (case-insensitive)
		if !strings.HasSuffix(strings.ToLower(d.Name()), fragmentExt) {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Derive fragment name: forward-slash normalise, strip .tmpl suffix
		// (case-insensitive; preserve the original casing of the name).
		slashPath := filepath.ToSlash(relPath)
		name := slashPath[:len(slashPath)-len(fragmentExt)]

		// Validate name segments: reject "..", empty segments, absolute paths.
		if err := validateFragmentName(name); err != nil {
			loadErrors = append(loadErrors, FragmentLoadError{Path: slashPath, Err: err})
			slog.Warn("skipping fragment with invalid name",
				"path", filepath.Join(dir, relPath),
				"error", err)
			return nil
		}

		// Read the fragment body.
		body, err := os.ReadFile(path)
		if err != nil {
			loadErrors = append(loadErrors, FragmentLoadError{Path: slashPath, Err: err})
			slog.Warn("failed to read fragment file",
				"path", filepath.Join(dir, relPath),
				"error", err)
			return nil
		}

		// Parse-time syntax check: reuse the condStub pattern from
		// PrecompileTemplateConds so CEL literals in Cond/When inside a
		// fragment are validated at load, not at first render.
		if perr := validateFragmentBody(name, string(body)); perr != nil {
			loadErrors = append(loadErrors, FragmentLoadError{Path: slashPath, Err: perr})
			slog.Warn("failed to parse fragment file",
				"path", filepath.Join(dir, relPath),
				"error", perr)
			return nil
		}

		// Duplicate name (case-insensitive filesystem collision): first-wins,
		// record an error for the second occurrence.
		if _, dup := reg.entries[name]; dup {
			loadErrors = append(loadErrors, FragmentLoadError{
				Path: slashPath,
				Err:  fmt.Errorf("duplicate fragment name %q", name),
			})
			slog.Warn("duplicate fragment name",
				"path", filepath.Join(dir, relPath),
				"name", name)
			return nil
		}

		reg.entries[name] = string(body)
		return nil
	})

	if err != nil {
		return reg, loadErrors, fmt.Errorf("failed to walk fragments directory %s: %w", dir, err)
	}

	return reg, loadErrors, nil
}

// validateFragmentName rejects names with empty segments, ".." segments, or
// an absolute-path prefix. WalkDir under a real dir should not surface these,
// but the guard is defense-in-depth (symlinks, oddly-cased platforms).
func validateFragmentName(name string) error {
	if name == "" {
		return fmt.Errorf("empty fragment name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("absolute path not allowed: %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return fmt.Errorf("empty segment in fragment name %q", name)
		}
		if seg == ".." {
			return fmt.Errorf("%q segment not allowed in fragment name %q", "..", name)
		}
	}
	return nil
}

// validateFragmentBody parse-checks a fragment body for valid Go text/template
// syntax AND compiles any Cond/When CEL literals. Mirrors the
// PrecompileTemplateConds pattern: install a condStub that compiles (but does
// not evaluate) each literal, then Parse + Execute against an empty
// PromptEnabledContext with missingkey=zero so the stub is actually invoked
// for every Cond/When call site.
func validateFragmentBody(name, body string) error {
	if !HasTemplateSyntax(body) {
		return nil
	}
	condStub := func(expr string) (bool, error) {
		ev := cel.GetCELEvaluator()
		if ev == nil {
			return false, nil
		}
		if _, err := ev.Compile(expr); err != nil {
			return false, err
		}
		return false, nil
	}
	fm := cel.BuildTemplateFuncMap(&cel.PromptEnabledContext{})
	fm["Cond"] = condStub
	fm["When"] = condStub

	t, err := template.New(name).Option("missingkey=zero").Funcs(fm).Parse(body)
	if err != nil {
		return fmt.Errorf("fragment %q: parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, &cel.PromptEnabledContext{}); err != nil {
		return fmt.Errorf("fragment %q: cond precompile: %w", name, err)
	}
	return nil
}

// Get returns the body of the named fragment and whether it was found.
func (r *FragmentRegistry) Get(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	body, ok := r.entries[name]
	return body, ok
}

// All returns a defensive copy of the name → body map. Mutating the returned
// map does not affect the registry.
func (r *FragmentRegistry) All() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.entries))
	for k, v := range r.entries {
		out[k] = v
	}
	return out
}

// Names returns the fragment names in ascending order.
func (r *FragmentRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Len returns the number of fragments in the registry.
func (r *FragmentRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Merge copies entries from other into r, with other winning by name. This
// implements the builtin < settings < workspace priority chain described in
// the mitto-g61 epic: callers invoke merged.Merge(next) so a later origin
// overrides an earlier one. Nil other is a no-op.
func (r *FragmentRegistry) Merge(other *FragmentRegistry) {
	if other == nil {
		return
	}
	other.mu.RLock()
	defer other.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range other.entries {
		r.entries[k] = v
	}
}
