package agentbackend

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestPackageDependencies_NoProtocolSDKOrProcessLeakage enforces the
// mitto-lrt.4 acceptance criterion that internal/agentbackend stays a
// protocol-neutral, non-process package: it must not transitively depend on
// a protocol SDK (github.com/coder/acp-go-sdk), the ACP-specific client/
// process-management layers (internal/acp, internal/acpproc), the web or
// conversation layers, or os/exec. Reuses the internal/stats `go list -deps
// -json` pattern (stats_test.go) so the check catches transitive imports,
// not just direct ones.
//
// Skipped under -short (shells out to the Go toolchain).
func TestPackageDependencies_NoProtocolSDKOrProcessLeakage(t *testing.T) {
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
		if pkg.ImportPath == "github.com/inercia/mitto/internal/agentbackend" {
			sawSelf = true
		}
		for _, bad := range forbidden {
			if pkg.ImportPath == bad || strings.HasPrefix(pkg.ImportPath, bad+"/") {
				t.Errorf("internal/agentbackend transitively depends on %s (via %s); the package must stay protocol-neutral and non-process", bad, pkg.ImportPath)
			}
			for _, imp := range pkg.Imports {
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("package %s imports %s; internal/agentbackend must not reach it transitively", pkg.ImportPath, imp)
				}
			}
		}
	}
	if !sawSelf {
		t.Fatalf("go list -deps did not report internal/agentbackend itself; test setup is broken")
	}
}
