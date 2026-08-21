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
		offenses = append(offenses, scanFileForHTTPOffenses(fset, name, f)...)
	}
	return offenses
}

// scanFileForHTTPOffenses inspects a single parsed file's AST for forbidden
// net/http selector usage, honouring the file's own import alias (or "" if
// the file does not import net/http, or imports it blank). Split out from
// scanNetHTTPUsage so the detection logic is unit-testable against synthetic
// in-memory source (see TestScanFileForHTTPOffenses_*) without needing a
// real file on disk.
func scanFileForHTTPOffenses(fset *token.FileSet, name string, f *ast.File) []httpOffense {
	alias := netHTTPLocalName(f)
	if alias == "" {
		return nil
	}
	var offenses []httpOffense
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

// unlistedOffenses returns the offenses not covered by any allowlist entry,
// formatted as "file:line: http.Symbol". Pure/deterministic so it can be
// exercised directly with synthetic data (see TestUnlistedOffenses_*),
// independent of what internal/cmd's real files currently contain.
func unlistedOffenses(offenses []httpOffense, allowlist []httpAllowEntry) []string {
	allowed := make(map[string]bool, len(allowlist))
	for _, e := range allowlist {
		allowed[e.key()] = true
	}
	var unlisted []string
	for _, o := range offenses {
		if allowed[o.key()] {
			continue
		}
		unlisted = append(unlisted, fmt.Sprintf("%s:%d: http.%s", o.File, o.Line, o.Symbol))
	}
	return unlisted
}

// validateAllowlist checks the two acceptance criteria for every allowlist
// entry: a non-empty Reason, and a match against a currently-observed
// offense (offenses is typically the live scanNetHTTPUsage result). Returns
// one error string per violation. Pure/deterministic, see
// TestValidateAllowlist_*.
func validateAllowlist(allowlist []httpAllowEntry, offenses []httpOffense) []string {
	present := make(map[string]bool, len(offenses))
	for _, o := range offenses {
		present[o.key()] = true
	}
	var errs []string
	for _, e := range allowlist {
		if strings.TrimSpace(e.Reason) == "" {
			errs = append(errs, fmt.Sprintf("httpAllowlist entry %s/%s has no Reason", e.File, e.Symbol))
		}
		if !present[e.key()] {
			errs = append(errs, fmt.Sprintf("httpAllowlist entry %s/%s no longer matches any net/http call site in internal/cmd; remove the stale entry", e.File, e.Symbol))
		}
	}
	return errs
}

// TestNoRawHTTPClientsOutsideSDK pins mitto-pscc.10: no file in internal/cmd
// may construct a net/http client or request against a running Mitto server
// outside the Go SDK (pkg/api). A hit here means either a new command was
// written against net/http instead of pkg/api, or a genuinely new exception
// exists and needs a reasoned httpAllowlist entry (visible in review).
func TestNoRawHTTPClientsOutsideSDK(t *testing.T) {
	unlisted := unlistedOffenses(scanNetHTTPUsage(t), httpAllowlist)
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
	for _, msg := range validateAllowlist(httpAllowlist, scanNetHTTPUsage(t)) {
		t.Error(msg)
	}
}

// TestUnlistedOffenses_FlagsUnknownCallSite pins the "stray http.Get" failure
// mode promised in the mitto-pscc.10 plan: a call site with no matching
// allowlist entry is reported, regardless of what the real repo tree
// currently contains.
func TestUnlistedOffenses_FlagsUnknownCallSite(t *testing.T) {
	offenses := []httpOffense{{File: "stray.go", Symbol: "Get", Line: 42}}
	got := unlistedOffenses(offenses, httpAllowlist)
	if len(got) != 1 || got[0] != "stray.go:42: http.Get" {
		t.Fatalf("unlistedOffenses() = %v, want exactly one entry for stray.go:42: http.Get", got)
	}
}

// TestUnlistedOffenses_AllowlistedCallSiteIsSilent confirms an offense whose
// (file, symbol) matches an allowlist entry is not reported.
func TestUnlistedOffenses_AllowlistedCallSiteIsSilent(t *testing.T) {
	allowlist := []httpAllowEntry{{File: "mcp.go", Symbol: "Client", Reason: "test fixture"}}
	offenses := []httpOffense{{File: "mcp.go", Symbol: "Client", Line: 10}}
	if got := unlistedOffenses(offenses, allowlist); len(got) != 0 {
		t.Fatalf("unlistedOffenses() = %v, want none (entry is allowlisted)", got)
	}
}

// TestValidateAllowlist_EmptyReasonFails pins the "empty Reason" failure mode
// from the plan's acceptance criteria.
func TestValidateAllowlist_EmptyReasonFails(t *testing.T) {
	allowlist := []httpAllowEntry{{File: "mcp.go", Symbol: "Client", Reason: "   "}}
	offenses := []httpOffense{{File: "mcp.go", Symbol: "Client", Line: 10}}
	errs := validateAllowlist(allowlist, offenses)
	if len(errs) != 1 || !strings.Contains(errs[0], "no Reason") {
		t.Fatalf("validateAllowlist() = %v, want exactly one 'no Reason' error", errs)
	}
}

// TestValidateAllowlist_StaleEntryFails pins the "deleting mcp.go's
// forwarding code" failure mode: an allowlist entry with no matching offense
// anymore must fail, so the list cannot rot into permission-by-accident.
func TestValidateAllowlist_StaleEntryFails(t *testing.T) {
	allowlist := []httpAllowEntry{{File: "mcp.go", Symbol: "Client", Reason: "no longer applies"}}
	errs := validateAllowlist(allowlist, nil) // no offenses observed at all
	if len(errs) != 1 || !strings.Contains(errs[0], "no longer matches") {
		t.Fatalf("validateAllowlist() = %v, want exactly one 'no longer matches' error", errs)
	}
}

// TestValidateAllowlist_ValidEntryPasses is the negative control: a
// well-formed entry matching a real offense produces no errors.
func TestValidateAllowlist_ValidEntryPasses(t *testing.T) {
	allowlist := []httpAllowEntry{{File: "mcp.go", Symbol: "Client", Reason: "MCP proxy, not the Mitto REST API"}}
	offenses := []httpOffense{{File: "mcp.go", Symbol: "Client", Line: 140}}
	if errs := validateAllowlist(allowlist, offenses); len(errs) != 0 {
		t.Fatalf("validateAllowlist() = %v, want none", errs)
	}
}

// TestScanFileForHTTPOffenses_DetectsForbiddenSymbolsOnly parses synthetic
// source directly (no dependency on the real internal/cmd tree) to confirm
// the scanner (a) flags client-egress symbols, (b) ignores constants/helpers
// that carry no network call, and (c) honours an import alias.
func TestScanFileForHTTPOffenses_DetectsForbiddenSymbolsOnly(t *testing.T) {
	const src = `package cmd

import althttp "net/http"

func f() {
	_ = &althttp.Client{}
	_ = althttp.StatusAccepted
	_ = althttp.DetectContentType(nil)
	req, _ := althttp.NewRequestWithContext(nil, "GET", "", nil)
	_ = req
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	got := scanFileForHTTPOffenses(fset, "synthetic.go", f)
	var symbols []string
	for _, o := range got {
		symbols = append(symbols, o.Symbol)
	}
	if len(symbols) != 2 || symbols[0] != "Client" || symbols[1] != "NewRequestWithContext" {
		t.Fatalf("scanFileForHTTPOffenses() symbols = %v, want exactly [Client NewRequestWithContext] (StatusAccepted/DetectContentType must not be flagged)", symbols)
	}
}

// TestScanFileForHTTPOffenses_NoImportIsSilent confirms a file that never
// imports net/http produces no offenses even if it happens to reference an
// identifier named "http" (e.g. a local variable), since netHTTPLocalName
// requires an actual import to establish the alias.
func TestScanFileForHTTPOffenses_NoImportIsSilent(t *testing.T) {
	const src = `package cmd

type http struct{}

func f() {
	h := http{}
	_ = h
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	if got := scanFileForHTTPOffenses(fset, "synthetic.go", f); len(got) != 0 {
		t.Fatalf("scanFileForHTTPOffenses() = %v, want none (no net/http import present)", got)
	}
}

// TestScanFileForHTTPOffenses_BlankImportIsSilent confirms a blank
// net/http import (side-effect only, cannot be selector-referenced) yields
// no local alias and therefore no offenses.
func TestScanFileForHTTPOffenses_BlankImportIsSilent(t *testing.T) {
	const src = `package cmd

import _ "net/http"

func f() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	if got := scanFileForHTTPOffenses(fset, "synthetic.go", f); len(got) != 0 {
		t.Fatalf("scanFileForHTTPOffenses() = %v, want none (blank import has no selectable alias)", got)
	}
}
