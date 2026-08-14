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

// exportedWrapper embeds an Entry in an exported field, the shape a caller
// outside this package is likeliest to reach for. wrapperWithUnexported
// embeds one in an unexported field instead: see TestEntryRedactionHoleIsExactlyTheUnexportedFieldCase.
type exportedWrapper struct {
	E Entry
}

type wrapperWithUnexported struct {
	e Entry
}

// TestEntryRedactionMatrix is TestEntryStringRedactsRaw and
// TestEntryGoStringRedactsRaw's replacement: the same claim, verified over
// every shape fmt can reach a *Entry or Entry through, crossed with every
// verb that formats a struct. Every case here must redact; the one shape
// that cannot is pinned separately, deliberately unredacted, by
// TestEntryRedactionHoleIsExactlyTheUnexportedFieldCase below, so this table
// stays a guarantee rather than papering over the gap.
func TestEntryRedactionMatrix(t *testing.T) {
	raw := "curl -u admin:hunter2 https://example.com"
	e := Entry{Seq: 1, LevelID: "pipes-03", Cwd: "/home/learner", Raw: raw}

	values := []struct {
		name string
		v    any
	}{
		{"Entry", e},
		{"*Entry", &e},
		{"[]Entry", []Entry{e}},
		{"[]*Entry", []*Entry{&e}},
		{"map[string]Entry", map[string]Entry{"k": e}},
		{"exported-field wrapper", exportedWrapper{E: e}},
		{"fmt.Errorf(%v)", fmt.Errorf("attempt failed: %v", e)},
	}
	verbs := []string{"%v", "%s", "%q", "%#v", "%+v"}

	for _, val := range values {
		for _, verb := range verbs {
			t.Run(val.name+"/"+verb, func(t *testing.T) {
				// %s and %q on a bare error value from fmt.Errorf call
				// Error(), not a formatter on the Entry inside it: this is
				// still a legitimate case for the matrix, since Error()
				// itself is built from the already-redacted %v of e.
				out := fmt.Sprintf(verb, val.v)
				if strings.Contains(out, raw) {
					t.Errorf("%s leaked Raw in full: %s", verb, out)
				}
				if strings.Contains(out, "hunter2") {
					t.Errorf("%s leaked a substring of Raw: %s", verb, out)
				}
			})
		}
	}
}

// TestEntryRedactionHoleIsExactlyTheUnexportedFieldCase pins the one shape
// where Entry's redaction cannot run, documented on Entry and in doc.go: an
// Entry (or *Entry) reached only through an unexported field of another
// type. reflect marks a value obtained through an unexported field as
// read-only, so fmt.CanInterface returns false and fmt never calls String
// or GoString here, falling back to printing every field, Raw included.
//
// This test asserts the LEAK, not a redaction: if it starts failing because
// Raw is no longer visible in the output, either Go's reflect semantics
// changed underneath this package or the wrapper below stopped being an
// unexported-field case, and either way the doc comments this test is
// pinned against need a second look before declaring the gap closed.
func TestEntryRedactionHoleIsExactlyTheUnexportedFieldCase(t *testing.T) {
	raw := "curl -u admin:hunter2 https://example.com"
	w := wrapperWithUnexported{e: Entry{Seq: 1, LevelID: "pipes-03", Cwd: "/home/learner", Raw: raw}}

	out := fmt.Sprintf("%+v", w)
	if !strings.Contains(out, raw) {
		t.Fatalf("expected the known unexported-field gap to leak Raw in %%+v, but it did not: %s\n"+
			"if this is a genuine fix, update Entry's doc comment, journal.go's String doc comment, and doc.go, which all currently describe this as an open gap", out)
	}
}
