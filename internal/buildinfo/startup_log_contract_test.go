package buildinfo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// TestMittoAppStartupLogIncludesBuildIdentity reproduces mitto-a1ca: without
// these fields, logs cannot distinguish a stale running app from a newer binary.
func TestMittoAppStartupLogIncludesBuildIdentity(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	mainPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "cmd", "mitto-app", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainPath, err)
	}

	var startupKeys map[string]bool
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Info" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "slog" {
			return true
		}
		message, ok := stringLiteral(call.Args[0])
		if !ok || message != "Mitto starting" {
			return true
		}
		startupKeys = make(map[string]bool)
		for i := 1; i < len(call.Args); i += 2 {
			if key, ok := stringLiteral(call.Args[i]); ok {
				startupKeys[key] = true
			}
		}
		return false
	})

	if startupKeys == nil {
		t.Fatal("Mitto starting log call not found")
	}
	for _, key := range []string{"executable", "revision", "build_time", "modified"} {
		if !startupKeys[key] {
			t.Errorf("Mitto starting log missing build identity field %q", key)
		}
	}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
