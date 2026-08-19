package doctor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// forbiddenSelectors names every selector call this package must never
// make. Nothing in internal/doctor deletes anything: --fix's only action is
// platform.EnsureDir(platform.DataDir()), which creates a directory and
// never removes one.
var forbiddenSelectors = map[string]map[string]bool{
	"os":      {"Remove": true, "RemoveAll": true, "Truncate": true, "Rename": true, "Chmod": true, "Chown": true},
	"syscall": {"Unlink": true, "Rmdir": true},
	"unix":    {"Unlink": true, "Rmdir": true},
}

// forbiddenSubstrings names string literal fragments that would signal a
// destructive path even without a matching selector call: a shelled-out
// wsl.exe unregister, a raw rm -rf, or a write to the global .wslconfig.
var forbiddenSubstrings = []string{"--unregister", "rm -rf", ".wslconfig"}

// TestPackageMakesNoRemovalCall parses every non-test .go file in this
// package directory and fails on any of the selectors or string fragments
// above. Verified against a deliberate violation before this test was
// trusted: a temporary os.Remove call added to probes_disk.go failed this
// test with the expected message, then was removed. See PROGRESS.md.
func TestPackageMakesNoRemovalCall(t *testing.T) {
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
			switch node := n.(type) {
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				selectorCalls++
				if forbiddenSelectors[pkgIdent.Name][sel.Sel.Name] {
					t.Errorf("%s:%d: %s.%s is forbidden in this package: nothing in internal/doctor deletes anything",
						name, fset.Position(node.Pos()).Line, pkgIdent.Name, sel.Sel.Name)
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(node.Value)
				if err != nil {
					return true
				}
				for _, bad := range forbiddenSubstrings {
					if strings.Contains(value, bad) {
						t.Errorf("%s:%d: string literal contains %q, which names a destructive operation this package must never perform",
							name, fset.Position(node.Pos()).Line, bad)
					}
				}
			}
			return true
		})
	}

	if selectorCalls == 0 {
		t.Fatal("found no selector calls at all in this package; the AST walk is looking in the wrong place")
	}
}
