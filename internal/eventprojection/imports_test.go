package eventprojection

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestPackageDependencies_StaysPureProjectionLeaf enforces the mitto-lrt.8
// design decision that internal/eventprojection stays a protocol-neutral,
// non-process, non-persistence-backed package: it must not transitively
// depend on a protocol SDK (github.com/coder/acp-go-sdk), the ACP-specific
// client/process-management layers (internal/acp, internal/acpproc), the
// web or conversation layers, internal/session (the durable
// CheckpointStore adapter lives in the sibling package
// eventprojectionsession instead, see doc.go), or os/exec. Mirrors
// internal/agentbackend/imports_test.go's `go list -deps -json` pattern so
// the check catches transitive imports, not just direct ones.
//
// Skipped under -short (shells out to the Go toolchain).
func TestPackageDependencies_StaysPureProjectionLeaf(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dependency scan in short mode")
	}

	cmd := exec.Command("go", "list", "-deps", "-json", ".")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}

	// `go list -deps -json` emits a stream of JSON objects (not a JSON
	// array), so decode iteratively.
	dec := json.NewDecoder(strings.NewReader(string(out)))
	forbidden := []string{
		"github.com/coder/acp-go-sdk",
		"github.com/inercia/mitto/internal/acp",
		"github.com/inercia/mitto/internal/acpproc",
		"github.com/inercia/mitto/internal/web",
		"github.com/inercia/mitto/internal/conversation",
		"github.com/inercia/mitto/internal/session",
		"os/exec",
	}
	sawSelf := false
	for dec.More() {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&pkg); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		if pkg.ImportPath == "github.com/inercia/mitto/internal/eventprojection" {
			sawSelf = true
		}
		for _, bad := range forbidden {
			if pkg.ImportPath == bad || strings.HasPrefix(pkg.ImportPath, bad+"/") {
				t.Errorf("internal/eventprojection transitively depends on %s (via %s); the package must stay a pure projection leaf", bad, pkg.ImportPath)
			}
			for _, imp := range pkg.Imports {
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("package %s imports %s; internal/eventprojection must not reach it transitively", pkg.ImportPath, imp)
				}
			}
		}
	}
	if !sawSelf {
		t.Fatalf("go list -deps did not report internal/eventprojection itself; test setup is broken")
	}
}
