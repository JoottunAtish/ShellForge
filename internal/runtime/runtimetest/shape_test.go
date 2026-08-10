package runtimetest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

// packageDir returns the absolute directory of this package, so the
// source-parsing guards below can find contract.go and its siblings without
// depending on the working directory the test binary happens to run from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to report this file's path")
	}
	return filepath.Dir(thisFile)
}

// parseContractFile parses contract.go with go/ast.
func parseContractFile(t *testing.T) *ast.File {
	t.Helper()
	path := filepath.Join(packageDir(t), "contract.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return f
}

// typeString renders a type expression the way it would be written in
// source, for the narrow set of forms an assertion signature can use.
func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}

// isAssertionSignature reports whether ft matches func(t *testing.T, f Factory).
func isAssertionSignature(ft *ast.FuncType) bool {
	if ft.Results != nil && len(ft.Results.List) > 0 {
		return false
	}
	if ft.Params == nil || len(ft.Params.List) != 2 {
		return false
	}
	first, second := ft.Params.List[0], ft.Params.List[1]
	if len(first.Names) != 1 || typeString(first.Type) != "*testing.T" {
		return false
	}
	if len(second.Names) != 1 || typeString(second.Type) != "Factory" {
		return false
	}
	return true
}

// topLevelFuncs returns every top level (non-method) function declaration in
// file, keyed by name.
func topLevelFuncs(file *ast.File) map[string]*ast.FuncDecl {
	decls := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		decls[fn.Name.Name] = fn
	}
	return decls
}

// sourceAssertionNames returns the name of every exported function declared
// in contract.go whose signature matches an assertion: func(t *testing.T, f Factory).
func sourceAssertionNames(t *testing.T) []string {
	t.Helper()
	file := parseContractFile(t)
	var names []string
	for name, fn := range topLevelFuncs(file) {
		if !fn.Name.IsExported() || !strings.HasPrefix(name, "Test") {
			continue
		}
		if isAssertionSignature(fn.Type) {
			names = append(names, name)
		}
	}
	return names
}

// TestContractTableIsComplete proves that the unexported dispatch table in
// contract.go lists every exported assertion function exactly once, with no
// omission and no duplicate. This is the regression this guard exists for:
// somebody adds a thirteenth assertion and forgets to register it, or
// registers one twice under two names.
func TestContractTableIsComplete(t *testing.T) {
	if len(contract) != 12 {
		t.Fatalf("dispatch table length: want 12, got %d", len(contract))
	}

	seen := make(map[string]bool, len(contract))
	tableNames := make(map[string]bool, len(contract))
	for _, a := range contract {
		if seen[a.name] {
			t.Errorf("dispatch table has a duplicate subtest name %q", a.name)
		}
		seen[a.name] = true

		fn := goruntime.FuncForPC(reflect.ValueOf(a.run).Pointer())
		if fn == nil {
			t.Errorf("dispatch table entry %q: could not resolve its function pointer", a.name)
			continue
		}
		full := fn.Name()
		short := full[strings.LastIndex(full, ".")+1:]
		want := "Test" + a.name
		if short != want {
			t.Errorf("dispatch table entry %q: run resolves to %s, want %s", a.name, full, want)
		}
		tableNames[want] = true
	}

	sourceNames := make(map[string]bool)
	for _, n := range sourceAssertionNames(t) {
		sourceNames[n] = true
	}

	for n := range sourceNames {
		if !tableNames[n] {
			t.Errorf("contract.go declares %s but the dispatch table does not register it", n)
		}
	}
	for n := range tableNames {
		if !sourceNames[n] {
			t.Errorf("dispatch table registers %s but contract.go does not declare it as an assertion", n)
		}
	}
}

// TestAssertionSignatures pins the exact signature of every assertion the
// ticket names: an exported func Test<Name>(t *testing.T, f Factory) with no
// results, declared in contract.go.
func TestAssertionSignatures(t *testing.T) {
	decls := topLevelFuncs(parseContractFile(t))

	names := []string{
		"ProvisionIsIdempotent",
		"StatusReportsProvisioned",
		"ExecCapturesStreamsSeparately",
		"ExecReportsExitCode",
		"ExecHonoursContextCancellation",
		"ExecRunsAsRequestedUser",
		"PushFilesMaterializesContentModeOwner",
		"PushFilesStripsCarriageReturns",
		"PullFileRoundTrips",
		"DestroyThenStatusReportsMissing",
		"CapabilitiesAreSelfConsistent",
		"NoNetworkByDefault",
	}

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			fn, ok := decls["Test"+name]
			if !ok {
				t.Fatalf("contract.go does not declare an exported func Test%s", name)
			}
			if !fn.Name.IsExported() {
				t.Fatalf("Test%s is declared but is not exported", name)
			}
			if !isAssertionSignature(fn.Type) {
				t.Fatalf("Test%s does not have the signature (t *testing.T, f Factory) with no results", name)
			}
		})
	}
}

// TestPackageImportsNoBackend parses every .go file in this package,
// including _test.go files, and asserts none of them imports a backend
// implementation or a package above L1. internal/archtest already enforces
// the non-test half of this; this guard additionally covers _test.go files,
// which archtest deliberately skips and which is exactly where a fake
// backend import would most plausibly land.
func TestPackageImportsNoBackend(t *testing.T) {
	dir := packageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	forbidden := []string{
		"/internal/runtime/docker",
		"/internal/runtime/wsl",
		"/internal/pty",
		"/internal/journal",
		"/internal/content",
		"/internal/verify",
		"/internal/game",
		"/cmd/shellforge",
		"/internal/platform",
		"net/http",
		"os/exec",
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if strings.Contains(p, bad) {
						t.Errorf("%s imports %q, which contains the forbidden substring %q", name, p, bad)
					}
				}
			}
		})
	}
}
