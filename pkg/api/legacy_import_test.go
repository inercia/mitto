package client_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// legacyClientImportPath is the pre-move import path. The package lived here
// before mitto-rwxq.2 relocated it wholesale to pkg/api (this package) with
// no re-export shim.
const legacyClientImportPath = "github.com/inercia/mitto/internal/client"

// skipDirNames are directories pruned from the repo-wide walk below: either
// huge/irrelevant (node_modules, .git, vendor) or holders of stale worktree
// checkouts (.mitto) that must not be touched or scanned (see the mitto-rwxq.2
// plan comment: .mitto/worktrees contains ~175 files on the old import path
// that are intentionally out of scope).
var skipDirNames = map[string]bool{
	".git":         true,
	".mitto":       true,
	"node_modules": true,
	"vendor":       true,
}

// TestNoLegacyInternalClientPackage pins the mitto-rwxq.2 relocation's
// acceptance criteria: internal/client must not exist. A future bad
// merge/rebase (or a stray revert) that resurrects the pre-move directory
// would silently reintroduce a duplicate, divergent copy of this package;
// this guard fails loudly instead.
func TestNoLegacyInternalClientPackage(t *testing.T) {
	root := repoRoot(t)
	legacyDir := filepath.Join(root, "internal", "client")
	if _, err := os.Stat(legacyDir); err == nil {
		t.Fatalf("internal/client still exists at %s; it was relocated to pkg/api in mitto-rwxq.2 and must not be reintroduced", legacyDir)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", legacyDir, err)
	}
}

// TestNoLegacyInternalClientImport pins the mitto-rwxq.2 relocation's other
// acceptance criterion: no .go file anywhere in the tracked tree imports the
// old github.com/inercia/mitto/internal/client path. All 50 importers were
// rewritten to github.com/inercia/mitto/pkg/api in the same commit as the
// git mv; a regression here means some file was missed or reverted.
func TestNoLegacyInternalClientImport(t *testing.T) {
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
			if imp.Path != nil && strings.Trim(imp.Path.Value, `"`) == legacyClientImportPath {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				offenders = append(offenders, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo for legacy import check: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("%d file(s) still import %s (relocated to github.com/inercia/mitto/pkg/api in mitto-rwxq.2): %v", len(offenders), legacyClientImportPath, offenders)
	}
}

// repoRoot locates the module root via `go list -m`, mirroring the
// findProjectRoot helper already used by client_test.go and the integration
// test suites (tests/integration/integration_test.go, tests/mocks/testutil).
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("find repo root via go list -m: %v", err)
	}
	return strings.TrimSpace(string(out))
}
