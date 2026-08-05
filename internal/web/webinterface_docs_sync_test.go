package web

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/mcpserver"
)

// TestDocsGoroutineTriageSectionExists pins the "Triaging goroutine counts
// (mitto-am0)" documentation increment added to docs/devel/web-interface.md
// against the actual source symbols and terms it describes, so a future
// rename/removal of any of them breaks this test instead of the docs
// silently drifting.
//
// Same docs↔code sync convention as
// internal/prompts/docs_fragments_sync_test.go, internal/prompts/docs_sync_test.go,
// and internal/cel/docs_sync_test.go.
func TestDocsGoroutineTriageSectionExists(t *testing.T) {
	root := webInterfaceDocsRepoRoot(t)
	doc := webInterfaceDocsReadFile(t, filepath.Join(root, "docs", "devel", "web-interface.md"))

	const wantHeading = "### Triaging goroutine counts (mitto-am0)"
	if !strings.Contains(doc, wantHeading) {
		t.Errorf("docs/devel/web-interface.md: missing heading %q", wantHeading)
	}

	for _, marker := range []string{
		"runtime.NumGoroutine()",
		"mitto_get_runtime_info",
		"num_goroutine=",
		"internal/coldstart/contention.go",
		"handleSessionWS",
		"readPump",
		"writePump",
		"~18 goroutines",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("docs/devel/web-interface.md §Triaging goroutine counts: missing marker %q", marker)
		}
	}

	// The two live-count exposure points named in the doc must still be real
	// exported fields carrying the json tag the doc's grep instructions rely
	// on (mitto_get_runtime_info's num_goroutine, and the cold-start log's
	// num_goroutine= key).
	rtInfoField, ok := reflect.TypeOf(mcpserver.RuntimeInfo{}).FieldByName("NumGoroutine")
	if !ok {
		t.Fatal("mcpserver.RuntimeInfo: missing NumGoroutine field (docs reference mitto_get_runtime_info's num_goroutine)")
	}
	if tag := rtInfoField.Tag.Get("json"); tag != "num_goroutine" {
		t.Errorf("mcpserver.RuntimeInfo.NumGoroutine json tag = %q, want %q", tag, "num_goroutine")
	}

	snapField, ok := reflect.TypeOf(coldstart.ContentionSnapshot{}).FieldByName("NumGoroutine")
	if !ok {
		t.Fatal("coldstart.ContentionSnapshot: missing NumGoroutine field (docs reference the cold-start num_goroutine= log line)")
	}
	if tag := snapField.Tag.Get("json"); tag != "num_goroutine" {
		t.Errorf("coldstart.ContentionSnapshot.NumGoroutine json tag = %q, want %q", tag, "num_goroutine")
	}
}

// TestDocsGoroutineTriageWSPumpMethodsExist compile-links the readPump/writePump
// method names quoted in the doc's per-WS-client marginal-cost table to the
// actual unexported methods on SessionWSClient via method expressions (reflect
// can't invoke unexported methods from outside the defining package, but this
// test lives in package web, so a direct method-expression reference is both
// simpler and a stronger pin: it fails the build, not just this test, if
// either method is renamed without updating the docs).
func TestDocsGoroutineTriageWSPumpMethodsExist(t *testing.T) {
	// The map's explicit value type pins both signatures at compile time.
	pumps := map[string]func(*SessionWSClient){
		"readPump":  (*SessionWSClient).readPump,
		"writePump": (*SessionWSClient).writePump,
	}
	for name, fn := range pumps {
		if fn == nil {
			t.Errorf("SessionWSClient.%s method expression is nil", name)
		}
	}
}

// webInterfaceDocsRepoRoot returns the absolute path to the repo root,
// resolved from the test source file location. Mirrors the runtime.Caller
// idiom used in internal/prompts/docs_fragments_sync_test.go.
func webInterfaceDocsRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <repo>/internal/web/webinterface_docs_sync_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// webInterfaceDocsReadFile is a t.Helper-wrapped os.ReadFile that fails the
// test on error and returns the file contents as a string.
func webInterfaceDocsReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
