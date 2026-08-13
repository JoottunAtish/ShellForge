package journal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// journalBannedFilesystemRemoval lists the os functions this package's own
// production source must never call. Unlike internal/store, this package
// has no exception: it has no file of its own to restrict permissions on.
var journalBannedFilesystemRemoval = map[string]bool{
	"Remove":    true,
	"RemoveAll": true,
	"Truncate":  true,
	"Rename":    true,
	"WriteFile": true,
	"Create":    true,
	"Chmod":     true,
}

// TestJournalPackageCallsNoFilesystemRemoval is a source level tripwire, not
// a proof: see internal/store/guards_test.go's twin for the same limits
// stated in full. It exists so a future removal added to this package fails
// a test instead of shipping quietly.
func TestJournalPackageCallsNoFilesystemRemoval(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range journalProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "os/exec" {
				t.Errorf("%s imports os/exec; internal/journal must never spawn a subprocess", name)
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch ident.Name {
			case "os":
				if journalBannedFilesystemRemoval[sel.Sel.Name] {
					t.Errorf("%s calls os.%s, which this package must never call", name, sel.Sel.Name)
				}
			case "exec":
				if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" {
					t.Errorf("%s calls exec.%s; internal/journal must never spawn a subprocess", name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}
