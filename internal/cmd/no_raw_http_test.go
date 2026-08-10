package cmd

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// netHTTPImportPath is the standard library HTTP client package. Constructing
// a client/request from it is exactly what pkg/api (the Go SDK, mitto-rwxq.2)
// exists to replace for anything that talks to a running Mitto server.
const netHTTPImportPath = "net/http"

// forbiddenHTTPSymbols are the net/http selectors that construct or perform
// client-side network egress. Constants (StatusX, MethodX) and server-side
// helpers (DetectContentType, CanonicalHeaderKey, ResponseWriter, Handler...)
// are deliberately NOT listed: they carry no network call of their own, and
// flagging them would force meaningless allowlist entries that erode the
// signal an entry is supposed to carry (see mitto-pscc.10 plan comment).
var forbiddenHTTPSymbols = map[string]bool{
	"Client":                true,
	"DefaultClient":         true,
	"DefaultTransport":      true,
	"Transport":             true,
	"RoundTripper":          true,
	"NewRequest":            true,
	"NewRequestWithContext": true,
	"Get":                   true,
	"Head":                  true,
	"Post":                  true,
	"PostForm":              true,
	"ReadResponse":          true,
}

// httpOffense is one flagged net/http client-construction call site.
type httpOffense struct {
	File   string
	Symbol string
	Line   int
}

func (o httpOffense) key() string { return o.File + "#" + o.Symbol }

// httpAllowEntry documents one deliberate exception to the no-raw-HTTP rule.
// Reason is mandatory: adding an entry must be a visible decision in review,
// not an accident (mitto-pscc.10 acceptance criteria).
type httpAllowEntry struct {
	File   string
	Symbol string
	Reason string
}

func (e httpAllowEntry) key() string { return e.File + "#" + e.Symbol }

// httpAllowlist is the complete set of allowed net/http client-construction
// sites in this package. Every entry must be justified and must still exist
// (see TestHTTPAllowlistIsCurrent) so the list cannot silently rot into
// permission-by-accident.
var httpAllowlist = []httpAllowEntry{
	{
		File:   "mcp.go",
		Symbol: "Client",
		Reason: "mitto mcp --proxy-to speaks MCP Streamable-HTTP JSON-RPC to an MCP endpoint, not the Mitto REST API; the SDK does not and should not model this transport.",
	},
	{
		File:   "mcp.go",
		Symbol: "NewRequestWithContext",
		Reason: "mitto mcp --proxy-to speaks MCP Streamable-HTTP JSON-RPC to an MCP endpoint, not the Mitto REST API; the SDK does not and should not model this transport.",
	},
}

// scanNetHTTPUsage walks the non-test .go files directly in this directory
// (internal/cmd) and returns every forbiddenHTTPSymbols call site found via
// net/http. Uses go/parser + go/ast (the pkg/api/legacy_import_test.go
// precedent) rather than grep, so matches inside comments or string literals
// are never mistaken for real usage.
func scanNetHTTPUsage(t *testing.T) []httpOffense {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read internal/cmd: %v", err)
	}
	fset := token.NewFileSet()
	var offenses []httpOffense
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		alias := netHTTPLocalName(f)
		if alias == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != alias || !forbiddenHTTPSymbols[sel.Sel.Name] {
				return true
			}
			offenses = append(offenses, httpOffense{
				File:   name,
				Symbol: sel.Sel.Name,
				Line:   fset.Position(sel.Pos()).Line,
			})
			return true
		})
	}
	return offenses
}

// netHTTPLocalName returns the local identifier the file uses to refer to
// net/http (honouring an import alias), or "" if the file does not import
// it or imports it blank ("_", which cannot be selector-referenced).
func netHTTPLocalName(f *ast.File) string {
	for _, imp := range f.Imports {
		if imp.Path == nil || strings.Trim(imp.Path.Value, `"`) != netHTTPImportPath {
			continue
		}
		if imp.Name == nil {
			return "http"
		}
		if imp.Name.Name == "_" {
			return ""
		}
		return imp.Name.Name
	}
	return ""
}

// TestNoRawHTTPClientsOutsideSDK pins mitto-pscc.10: no file in internal/cmd
// may construct a net/http client or request against a running Mitto server
// outside the Go SDK (pkg/api). A hit here means either a new command was
// written against net/http instead of pkg/api, or a genuinely new exception
// exists and needs a reasoned httpAllowlist entry (visible in review).
func TestNoRawHTTPClientsOutsideSDK(t *testing.T) {
	allowed := make(map[string]bool, len(httpAllowlist))
	for _, e := range httpAllowlist {
		allowed[e.key()] = true
	}
	var unlisted []string
	for _, o := range scanNetHTTPUsage(t) {
		if allowed[o.key()] {
			continue
		}
		unlisted = append(unlisted, fmt.Sprintf("%s:%d: http.%s", o.File, o.Line, o.Symbol))
	}
	if len(unlisted) > 0 {
		t.Errorf("internal/cmd must talk to a running Mitto server via the SDK (pkg/api), not net/http directly; found %d unlisted call site(s):\n%s\n"+
			"If this is a deliberate exception (e.g. a non-Mitto-API transport), add a justified entry to httpAllowlist in this file.",
			len(unlisted), strings.Join(unlisted, "\n"))
	}
}

// TestHTTPAllowlistIsCurrent pins the other half of mitto-pscc.10's
// acceptance criteria: every httpAllowlist entry must carry a non-empty
// Reason (so adding one is a visible decision, not an accident) and must
// still correspond to a real call site (so the list cannot rot into
// permission-by-accident once the code it excuses is refactored away).
func TestHTTPAllowlistIsCurrent(t *testing.T) {
	present := make(map[string]bool)
	for _, o := range scanNetHTTPUsage(t) {
		present[o.key()] = true
	}
	for _, e := range httpAllowlist {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("httpAllowlist entry %s/%s has no Reason", e.File, e.Symbol)
		}
		if !present[e.key()] {
			t.Errorf("httpAllowlist entry %s/%s no longer matches any net/http call site in internal/cmd; remove the stale entry", e.File, e.Symbol)
		}
	}
}
