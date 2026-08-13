package journal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// journalProductionFiles lists this package's own non-test .go files.
func journalProductionFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package directory: %v", err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// TestJournalPackageDoesNotPrintOrLog is an AST scan over this package's own
// production source. It asserts the package cannot print or log at all,
// which is stronger than merely never printing Raw: see doc.go. fmt.Errorf
// is the only fmt function this package may call; Entry.String and
// Entry.GoString are built with strings.Builder and strconv precisely so
// that this holds even for the one place this package formats an Entry.
func TestJournalPackageDoesNotPrintOrLog(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range journalProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "log" || path == "log/slog" {
				t.Errorf("%s imports %q; internal/journal must never log", name, path)
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "fmt" {
						callName := sel.Sel.Name
						if callName == "Errorf" {
							return true
						}
						if strings.HasPrefix(callName, "Print") || strings.HasPrefix(callName, "Fprint") || strings.HasPrefix(callName, "Sprint") {
							t.Errorf("%s calls fmt.%s; internal/journal may only call fmt.Errorf", name, callName)
						}
					}
				}
				if ident, ok := v.Fun.(*ast.Ident); ok && (ident.Name == "print" || ident.Name == "println") {
					t.Errorf("%s calls the builtin %s", name, ident.Name)
				}
			case *ast.SelectorExpr:
				if ident, ok := v.X.(*ast.Ident); ok && ident.Name == "os" && (v.Sel.Name == "Stdout" || v.Sel.Name == "Stderr") {
					t.Errorf("%s references os.%s", name, v.Sel.Name)
				}
			}
			return true
		})
	}
}

// rawSafeCalls names the calls that may legitimately take .Raw as a direct
// argument: the parameterized-query path (ExecContext, QueryContext,
// QueryRowContext), the read-back path (Scan), and len, which reveals only
// a byte count, never content, which is exactly what Entry.String and
// Entry.GoString need to redact safely.
var rawSafeCalls = map[string]bool{
	"ExecContext":     true,
	"QueryContext":    true,
	"QueryRowContext": true,
	"Scan":            true,
	"len":             true,
}

// TestJournalPackageKeepsRawOutOfErrors is an AST scan asserting that no
// argument to any call in this package's production source directly names
// the Raw field, except inside one of rawSafeCalls. This is a stronger
// statement than "fmt.Errorf never sees Raw": it also blocks a future call
// that would pass Raw somewhere else this package should not send it.
func TestJournalPackageKeepsRawOutOfErrors(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range journalProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callName := callFuncName(call)
			if rawSafeCalls[callName] {
				return true
			}
			for _, arg := range call.Args {
				if referencesRawDirectly(arg) {
					t.Errorf("%s: call to %s passes .Raw directly, outside the parameterized-query, read-back, and len paths", name, callName)
				}
			}
			return true
		})
	}
}

func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// referencesRawDirectly reports whether arg is, after unwrapping & and (),
// a selector expression naming the Raw field. It deliberately does not
// recurse into a nested call's own arguments: a nested call such as
// len(e.Raw) is itself visited separately by the ast.Inspect walk above and
// judged against rawSafeCalls on its own terms, so recursing here would
// wrongly blame the outer call for what an inner, permitted call does.
func referencesRawDirectly(arg ast.Expr) bool {
	e := arg
	for {
		switch v := e.(type) {
		case *ast.ParenExpr:
			e = v.X
			continue
		case *ast.UnaryExpr:
			e = v.X
			continue
		}
		break
	}
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Raw"
}

func TestEntryStringRedactsRaw(t *testing.T) {
	e := Entry{
		Seq:     1,
		LevelID: "pipes-03",
		Cwd:     "/home/learner",
		Raw:     "curl -u admin:hunter2 https://example.com",
	}

	for _, format := range []string{"%v", "%s"} {
		out := fmt.Sprintf(format, e)
		if strings.Contains(out, e.Raw) {
			t.Errorf("format %s leaked Raw: %s", format, out)
		}
		if strings.Contains(out, "hunter2") {
			t.Errorf("format %s leaked a substring of Raw: %s", format, out)
		}
		if !strings.Contains(out, "redacted") {
			t.Errorf("format %s does not say redacted: %s", format, out)
		}
		if !strings.Contains(out, fmt.Sprintf("%d", len(e.Raw))) {
			t.Errorf("format %s does not report the byte count: %s", format, out)
		}
	}
}

func TestEntryGoStringRedactsRaw(t *testing.T) {
	e := Entry{
		Seq:     1,
		LevelID: "pipes-03",
		Cwd:     "/home/learner",
		Raw:     "curl -u admin:hunter2 https://example.com",
	}

	out := fmt.Sprintf("%#v", e)
	if strings.Contains(out, e.Raw) {
		t.Errorf("%%#v leaked Raw: %s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("%%#v leaked a substring of Raw: %s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("%%#v does not say redacted: %s", out)
	}
}
