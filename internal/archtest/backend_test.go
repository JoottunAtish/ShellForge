package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoCodeBranchesOnStatusBackend enforces runtime.Status.Backend's own
// doc comment: it is for display only, and no decision anywhere may branch
// on it. Caps is what a real decision asks instead.
//
// Two shapes are flagged, anywhere in the module's non-test .go files:
//
//   - a binary == or != where one operand is a selector expression ending
//     in ".Backend" and the other is a non-empty string literal
//   - a switch statement whose tag expression is a selector ending in
//     ".Backend"
//
// There is no exemption list. A composite literal such as
// runtime.Status{Backend: "docker"} is never inspected here, because it
// constructs a value rather than branching on one, and a rendering path
// that merely prints status.Backend is likewise invisible to this walk:
// neither is a decision.
//
// The non-empty-literal condition is what keeps
// internal/runtime/runtimetest/contract.go's `after.Backend == ""` green:
// asserting the field was populated is verification, not selection, and no
// behaviour can be chosen from an emptiness check.
//
// Verified against a deliberate violation before being trusted: a temporary
// `if st.Backend == "docker"` added to cmd/shellforge/cmd_sandbox.go made
// this test fail naming the file and line, then was removed.
func TestNoCodeBranchesOnStatusBackend(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	filesSeen := 0

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "dist", "node_modules", "testdata", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		filesSeen++

		file, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Errorf("parse %s: %v", p, perr)
			return nil
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				if isBackendSelector(node.X) && isNonEmptyStringLit(node.Y) ||
					isBackendSelector(node.Y) && isNonEmptyStringLit(node.X) {
					t.Errorf("%s:%d: comparing a .Backend selector against a non-empty string literal; "+
						"nothing may branch on Status.Backend, ask Runtime.Capabilities() instead",
						rel, fset.Position(node.Pos()).Line)
				}
			case *ast.SwitchStmt:
				if node.Tag != nil && isBackendSelector(node.Tag) {
					t.Errorf("%s:%d: switching on a .Backend selector; "+
						"nothing may branch on Status.Backend, ask Runtime.Capabilities() instead",
						rel, fset.Position(node.Pos()).Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if filesSeen == 0 {
		t.Fatal("found no non-test .go files at all; the walk is looking in the wrong place")
	}
}

// isBackendSelector reports whether expr is a selector expression ending in
// ".Backend", such as st.Backend or someStruct.Status.Backend.
func isBackendSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Backend"
}

// isNonEmptyStringLit reports whether expr is a string literal whose value
// is not the empty string.
func isNonEmptyStringLit(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	return value != ""
}
