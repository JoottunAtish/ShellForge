package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestThisPackageNeverCallsOsRemoveAll parses every non-test .go file in
// this package and fails on any call to the selector os.RemoveAll.
//
// Nothing in cmd/shellforge deletes anything on the host: `sandbox destroy`
// and `sandbox rebuild` call rt.Destroy(ctx), which the runtime backends
// implement, and this layer never resolves or removes a filesystem path
// itself. See the shape internal/doctor/no_removal_test.go already uses.
//
// Verified against a deliberate violation before it was trusted: a
// temporary os.RemoveAll call added to cmd_sandbox.go made this test fail
// naming the file and line, then was removed.
func TestThisPackageNeverCallsOsRemoveAll(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	selectorCalls := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			selectorCalls++
			if pkgIdent.Name == "os" && sel.Sel.Name == "RemoveAll" {
				t.Errorf("%s:%d: os.RemoveAll is forbidden in this package: nothing in cmd/shellforge deletes a host path itself",
					name, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}

	if selectorCalls == 0 {
		t.Fatal("found no selector calls at all in this package; the AST walk is looking in the wrong place")
	}
}
