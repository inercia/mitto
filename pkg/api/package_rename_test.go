package api_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiImportPath is the current pkg/api import path (relocated from
// internal/client in mitto-rwxq.2; package clause renamed from "client" to
// "api" in mitto-rwxq.10 to match the import path).
const apiImportPath = "github.com/inercia/mitto/pkg/api"

// TestNoLegacyClientPackageClause pins the mitto-rwxq.10 acceptance criteria:
// every pkg/api/*.go file's package clause must be "api" (regular files) or
// "api_test" (external test files) — never the pre-rename "client" /
// "client_test". A future bad merge/rebase/revert that resurrects the old
// clause would silently re-widen the exported package identifier back to
// "client" (mismatched with the pkg/api directory and import path); this
// guard fails loudly instead.
func TestNoLegacyClientPackageClause(t *testing.T) {
	root := repoRoot(t)
	apiDir := filepath.Join(root, "pkg", "api")
	fset := token.NewFileSet()

	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read %s: %v", apiDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(apiDir, e.Name())
		f, perr := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly)
		if perr != nil {
			t.Fatalf("parse package clause of %s: %v", path, perr)
		}
		if name := f.Name.Name; name != "api" && name != "api_test" {
			t.Errorf("%s declares package %q; want \"api\" or \"api_test\" (pre-mitto-rwxq.10 clause was \"client\"/\"client_test\")", e.Name(), name)
		}
	}
}

// TestNoLegacyClientImportAlias pins the other half of mitto-rwxq.10: no .go
// file anywhere in the tracked tree re-introduces a "client" alias for the
// pkg/api import. Before this bead the package's Go identifier WAS "client"
// (mismatched with its "api" directory/import path), so importers either
// wrote client.Foo() directly or aliased the import as
// `client "github.com/inercia/mitto/pkg/api"` for clarity. After the rename
// the package identifier is "api" itself, so a bare import suffices and any
// explicit "client" alias would be a regression pointing back at the old
// naming.
func TestNoLegacyClientImportAlias(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirNames[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			if imp.Path == nil || strings.Trim(imp.Path.Value, `"`) != apiImportPath {
				continue
			}
			if imp.Name != nil && imp.Name.Name == "client" {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo for legacy import-alias check: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("%d file(s) alias %s as \"client\" (pre-mitto-rwxq.10 naming); the package identifier is now \"api\" so a bare import suffices: %v", len(offenders), apiImportPath, offenders)
	}
}
