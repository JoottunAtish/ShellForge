package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Where a learner starts, and why one test guards it across two files.
//
// The interactive shell's starting directory is set in cmd_run.go, in the
// AttachOpts handed to Session.Attach. The directory the golden harness runs a
// level's own solution from is set in golden.go, in the ExecOpts handed to
// Session.Exec. Those are different files, different call sites, and different
// authors' business, and they MUST agree: the harness's whole claim is that it
// plays the level the way a learner does, and it stops being true the moment
// one of them moves.
//
// This is not a hypothetical. The first CI run of the golden test failed nav-01
// because both were the level root rather than the learner's home, so `pwd`
// answered /home/learner/quest while nav-01 asks the learner which directory
// they started in and expects /home/learner. Because BOTH were wrong in the
// same direction, the harness happily verified a solution against a directory
// no learner is ever in, and reported the level as broken instead. Two wrong
// copies of one value agree with each other, which is exactly why the check
// cannot be "do these two match" and has to be "are both this specific value".

// TestEverySandboxWorkDirIsTheLearnersHome asserts that every WorkDir this
// package sets on a sandbox call is sandboxHome.
//
// It reads production files only. Tests legitimately set other working
// directories: the near-miss suite in golden_test.go writes deliberately wrong
// answers into a level's world and does so from the level root, which is
// scaffolding rather than anything a learner experiences.
//
// The rule is deliberately blunt. There is no sandbox call in this package that
// wants a working directory other than the learner's home, so anything else is
// either the level-root bug returning or a new requirement that should arrive
// with a reason and an edit to this test.
func TestEverySandboxWorkDirIsTheLearnersHome(t *testing.T) {
	const want = "sandboxHome"

	found := 0
	for name, file := range parseProductionFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "WorkDir" {
				return true
			}

			found++
			ident, ok := kv.Value.(*ast.Ident)
			if ok && ident.Name == want {
				return true
			}

			t.Errorf("%s:%d sets WorkDir to %s, want %s.\n"+
				"A learner's shell and the golden harness both start in the learner's home "+
				"directory. Setting the level root here is the bug that made nav-01 fail its "+
				"own golden test: the level asks which directory you started in, and the "+
				"answer has to be the one a real shell gives.",
				name, position(t, kv.Pos()), render(kv.Value), want)
			return true
		})
	}

	// Without this the test passes trivially the day somebody renames the field
	// or moves the call, which is the day it is most needed.
	if found < 2 {
		t.Fatalf("found %d WorkDir assignments in this package's production files, want at least 2 "+
			"(the interactive Attach in cmd_run.go and the solution Exec in golden.go). "+
			"The AST walk is looking in the wrong place, or a call site has moved out of this package.", found)
	}
}

// parseProductionFiles parses every non-test Go file in this package, keyed by
// file name.
func parseProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	files := make(map[string]*ast.File, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(workDirFset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}
	if len(files) == 0 {
		t.Fatal("no production Go files found in this package")
	}
	return files
}

// workDirFset is package level so positions stay resolvable after the parse
// helper returns.
var workDirFset = token.NewFileSet()

// position returns the line a node sits on.
func position(t *testing.T, pos token.Pos) int {
	t.Helper()
	return workDirFset.Position(pos).Line
}

// render describes an expression well enough to name it in a failure, without
// pulling in go/printer for what is always a field access or an identifier.
func render(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return render(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		return render(e.Fun) + "()"
	case *ast.BasicLit:
		return e.Value
	default:
		return "an expression this test cannot render"
	}
}
