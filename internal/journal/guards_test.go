package journal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// journalBannedFilesystemRemoval lists the os functions this package's own
// production source must never even name, whether called directly or taken
// as a function value (`var rm = os.Remove` is caught exactly like
// `os.Remove(p)`, since both are the same *ast.SelectorExpr). Unlike
// internal/store, this package has no exception: it has no file of its own
// to restrict permissions on, so Chmod stays fully banned here. os.OpenFile
// is not in this map: like internal/store, it gets the narrower, flag-aware
// check in journalOpenFileRequestsTruncateOrCreate instead of a flat ban,
// even though this package has no legitimate call to it today, so that a
// future read-only use is not blocked by a rule that exists to catch
// truncation and creation.
var journalBannedFilesystemRemoval = map[string]bool{
	"Remove":    true,
	"RemoveAll": true,
	"Truncate":  true,
	"Rename":    true,
	"WriteFile": true,
	"Create":    true,
	"Chmod":     true,
}

// journalBannedSyscall lists syscall functions this package's own
// production source must never name, for the same reason and by the same
// rule as journalBannedFilesystemRemoval.
var journalBannedSyscall = map[string]bool{
	"Unlink":    true,
	"Ftruncate": true,
}

// journalImportLocalName is internal/store/guards_test.go's importLocalName,
// duplicated rather than shared: each package's guard test is intentionally
// self-contained, the same way journalProductionFiles duplicates
// storeProductionFiles. See that function's doc comment for the full
// rationale, including why a dot import is out of scope.
func journalImportLocalName(f *ast.File, importPath string) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
			return importPath[idx+1:]
		}
		return importPath
	}
	return ""
}

// journalOpenFileRequestsTruncateOrCreate is
// internal/store/guards_test.go's openFileRequestsTruncateOrCreate,
// duplicated for the same reason as journalImportLocalName.
func journalOpenFileRequestsTruncateOrCreate(expr ast.Expr, osAlias string) bool {
	switch v := expr.(type) {
	case *ast.ParenExpr:
		return journalOpenFileRequestsTruncateOrCreate(v.X, osAlias)
	case *ast.BinaryExpr:
		return journalOpenFileRequestsTruncateOrCreate(v.X, osAlias) || journalOpenFileRequestsTruncateOrCreate(v.Y, osAlias)
	case *ast.SelectorExpr:
		ident, ok := v.X.(*ast.Ident)
		return ok && ident.Name == osAlias && (v.Sel.Name == "O_TRUNC" || v.Sel.Name == "O_CREATE")
	default:
		return false
	}
}

// TestJournalPackageCallsNoFilesystemRemoval is a source level tripwire, not
// a proof: see internal/store/guards_test.go's twin for the same limits
// stated in full. It resolves each file's own import aliases rather than
// matching the literal identifiers "os", "exec", and "syscall", and it
// matches on bare *ast.SelectorExpr nodes rather than only a CallExpr's Fun,
// so a banned function taken as a value is caught the same way a direct
// call is. It exists so a future removal added to this package fails a test
// instead of shipping quietly.
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

		osAlias := journalImportLocalName(f, "os")
		execAlias := journalImportLocalName(f, "os/exec")
		syscallAlias := journalImportLocalName(f, "syscall")

		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == osAlias && sel.Sel.Name == "OpenFile" && len(v.Args) >= 2 {
					if journalOpenFileRequestsTruncateOrCreate(v.Args[1], osAlias) {
						t.Errorf("%s calls os.OpenFile with O_TRUNC or O_CREATE in its flags, which this package must never do", name)
					}
				}
				return true

			case *ast.SelectorExpr:
				ident, ok := v.X.(*ast.Ident)
				if !ok {
					return true
				}
				switch ident.Name {
				case osAlias:
					if journalBannedFilesystemRemoval[v.Sel.Name] {
						t.Errorf("%s references os.%s, which this package must never call, whether called directly or taken as a function value", name, v.Sel.Name)
					}
				case execAlias:
					if v.Sel.Name == "Command" || v.Sel.Name == "CommandContext" {
						t.Errorf("%s references exec.%s; internal/journal must never spawn a subprocess", name, v.Sel.Name)
					}
				case syscallAlias:
					if journalBannedSyscall[v.Sel.Name] {
						t.Errorf("%s references syscall.%s, which this package must never call", name, v.Sel.Name)
					}
				}
				return true
			}
			return true
		})
	}
}
